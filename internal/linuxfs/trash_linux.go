//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

const (
	trashInfoFilenameSuffix  = ".trashinfo"
	trashTokenRequiredPrefix = "ldc-"
	minTrashTokenHexBytes    = 16
	maxTrashTokenBytes       = 245
)

// trashDirectorySource is the only authority from which this package obtains
// the Freedesktop Trash files/info descriptor pair. The production source is
// a mounts.TrashLease. Keeping the interface private permits focused tests of
// descriptor cleanup and point-of-use requalification without making another
// public authority constructor.
type trashDirectorySource interface {
	RootID() domain.TrustedRootID
	DuplicateTrashDescriptorPair() (mounts.TrashDescriptorPair, error)
}

var _ trashDirectorySource = (*mounts.TrashLease)(nil)

// trashTopologyDirectorySource supplies the additional descriptor evidence
// needed to prove a literal Freedesktop layout. It remains private so no
// caller can invent a raw-fd authority outside mounts and this safety layer.
type trashTopologyDirectorySource interface {
	trashDirectorySource
	Placement() mounts.TrashPlacement
	AnchorKind() mounts.TrashAnchorKind
	OwnerUID() uint32
	DuplicateTrashTopologyDescriptorSet() (mounts.TrashTopologyDescriptorSet, error)
}

var _ trashTopologyDirectorySource = (*mounts.TrashLease)(nil)

// TrashDirectories is an opaque, descriptor-rooted qualification of one
// engine/helper-selected Freedesktop Trash files/info pair. It retains only
// stable identity evidence and the source authority, never a pathname or a
// raw descriptor. Every publication obtains a newly requalified pair.
type TrashDirectories struct {
	source   trashDirectorySource
	rootID   domain.TrustedRootID
	files    domain.FilesystemSnapshot
	info     domain.FilesystemSnapshot
	topology *trashTopologyQualification

	mu     sync.Mutex
	closed bool
}

// trashTopologyQualification records the descriptor identities that were
// proven to form one literal FDO layout. It never stores a path; every future
// operation repeats the literal-child proof from newly duplicated descriptors.
type trashTopologyQualification struct {
	source     trashTopologyDirectorySource
	placement  mounts.TrashPlacement
	anchorKind mounts.TrashAnchorKind
	ownerUID   uint32
	anchor     domain.FilesystemSnapshot
	trashRoot  domain.FilesystemSnapshot
	sharedTop  domain.FilesystemSnapshot
}

type trashTopologySnapshots struct {
	anchor    domain.FilesystemSnapshot
	trashRoot domain.FilesystemSnapshot
	files     domain.FilesystemSnapshot
	info      domain.FilesystemSnapshot
	sharedTop domain.FilesystemSnapshot
}

// TrashInfoPublication is an opaque receipt for a durably verified owned
// .trashinfo record. It deliberately retains no descriptor or path. The
// private identity facts let a later no-replace move bind itself to this exact
// verified publication rather than accepting a token alone.
type TrashInfoPublication struct {
	rootID        domain.TrustedRootID
	token         string
	files         domain.FilesystemSnapshot
	infoDirectory domain.FilesystemSnapshot
	infoRecord    domain.FilesystemSnapshot
	contents      []byte
}

// OpenTrashDirectories qualifies the engine/helper-selected files/info pair
// for metadata publication. It is a legacy pre-selector boundary, not proof
// of a complete Freedesktop layout. It accepts no path, UID, HOME, XDG
// variable, or raw descriptor. The first pair establishes stable baseline
// identities; later calls must duplicate and match that baseline before any
// publication occurs.
func OpenTrashDirectories(lease *mounts.TrashLease) (*TrashDirectories, error) {
	if lease == nil {
		return nil, fmt.Errorf("%w: trusted Trash lease is required", ErrUnsupported)
	}
	return openTrashDirectoriesWithSource(lease)
}

// OpenTopologyQualifiedTrashDirectories converts an engine/helper-attested
// Trash lease into a descriptor-rooted layout that has proven its literal FDO
// child relationships. It accepts no path, UID, environment, or topology name
// from a caller. A legacy pre-selector lease is unsupported here rather than
// being upgraded through inferred discovery.
func OpenTopologyQualifiedTrashDirectories(lease *mounts.TrashLease) (*TrashDirectories, error) {
	if lease == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash lease is required", ErrUnsupported)
	}
	return openTopologyQualifiedTrashDirectoriesWithSource(lease)
}

func openTrashDirectoriesWithSource(source trashDirectorySource) (*TrashDirectories, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: trusted Trash lease is required", ErrUnsupported)
	}
	rootID := source.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: Trash root identity: %v", ErrUnsupported, err)
	}
	pair, err := source.DuplicateTrashDescriptorPair()
	if err != nil {
		_ = pair.Close()
		return nil, classifyTrashDirectorySourceFailure("duplicate trusted Trash directories", err)
	}
	defer pair.Close()
	if source.RootID() != rootID {
		return nil, fmt.Errorf("%w: trusted Trash root identity changed while qualifying directories", ErrDrifted)
	}
	files, info, err := inspectTrashDescriptorPair(pair)
	if err != nil {
		return nil, err
	}
	return &TrashDirectories{
		source: source,
		rootID: rootID,
		files:  files,
		info:   info,
	}, nil
}

func openTopologyQualifiedTrashDirectoriesWithSource(source trashTopologyDirectorySource) (*TrashDirectories, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash lease is required", ErrUnsupported)
	}
	rootID := source.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: Trash root identity: %v", ErrUnsupported, err)
	}
	placement := source.Placement()
	anchorKind := source.AnchorKind()
	ownerUID := source.OwnerUID()
	if err := validateTrashTopologyAuthority(placement, anchorKind); err != nil {
		return nil, err
	}

	set, err := source.DuplicateTrashTopologyDescriptorSet()
	if err != nil {
		_ = set.Close()
		return nil, classifyTrashDirectorySourceFailure("duplicate trusted Trash topology", err)
	}
	defer set.Close()
	if source.RootID() != rootID {
		return nil, fmt.Errorf("%w: trusted Trash root identity changed while qualifying topology", ErrDrifted)
	}
	if source.Placement() != placement || source.AnchorKind() != anchorKind || source.OwnerUID() != ownerUID {
		return nil, fmt.Errorf("%w: trusted Trash topology authority changed while qualifying directories", ErrDrifted)
	}
	snapshots, err := inspectTrashTopologyDescriptorSet(set, placement, anchorKind, ownerUID)
	if err != nil {
		return nil, err
	}
	return &TrashDirectories{
		source: source,
		rootID: rootID,
		files:  snapshots.files,
		info:   snapshots.info,
		topology: &trashTopologyQualification{
			source:     source,
			placement:  placement,
			anchorKind: anchorKind,
			ownerUID:   ownerUID,
			anchor:     snapshots.anchor,
			trashRoot:  snapshots.trashRoot,
			sharedTop:  snapshots.sharedTop,
		},
	}, nil
}

// Close prevents later publication through this qualification. It never
// closes the caller-owned mounts lease and never deletes an existing record.
func (directories *TrashDirectories) Close() error {
	if directories == nil {
		return nil
	}
	directories.mu.Lock()
	defer directories.mu.Unlock()
	if directories.closed {
		return nil
	}
	directories.closed = true
	return nil
}

// RootID reports the trusted-root identity that selected these directories.
// It is data-only and cannot be used to reconstruct a filesystem path.
func (directories *TrashDirectories) RootID() domain.TrustedRootID {
	if directories == nil {
		return ""
	}
	return directories.rootID
}

func (directories *TrashDirectories) topologyQualified() bool {
	return directories != nil && directories.topology != nil
}

// PublishTrashInfoDurable creates exactly info/<token>.trashinfo beneath a
// freshly requalified held Trash info descriptor. It publishes metadata only:
// this function does not move a source into files, restore an entry, remove
// content, or reconcile interrupted pairs.
func PublishTrashInfoDurable(ctx context.Context, directories *TrashDirectories, token string, contents []byte) (*TrashInfoPublication, error) {
	return publishTrashInfoDurableWith(ctx, directories, token, contents, defaultTrashInfoPublishHooks())
}

type trashInfoPublishHooks struct {
	open          func(int, string, int, uint32) (int, error)
	write         func(int, []byte) (int, error)
	syncFile      func(int) error
	syncDirectory func(int) error
	close         func(int) error
}

func defaultTrashInfoPublishHooks() trashInfoPublishHooks {
	return trashInfoPublishHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	}
}

func (hooks trashInfoPublishHooks) validate() error {
	if hooks.open == nil || hooks.write == nil || hooks.syncFile == nil || hooks.syncDirectory == nil || hooks.close == nil {
		return fmt.Errorf("%w: Trash-info durability implementation is incomplete", ErrUnsupported)
	}
	return nil
}

func publishTrashInfoDurableWith(
	ctx context.Context,
	directories *TrashDirectories,
	token string,
	contents []byte,
	hooks trashInfoPublishHooks,
) (*TrashInfoPublication, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if directories == nil {
		return nil, fmt.Errorf("%w: qualified Trash directories are required", ErrUnsupported)
	}
	basename, err := trashInfoBasename(token)
	if err != nil {
		return nil, err
	}
	if len(contents) > maximumDurableFileBytes {
		return nil, fmt.Errorf("%w: Trash info content exceeds %d-byte limit", ErrUnsupported, maximumDurableFileBytes)
	}
	// The caller retains its slice. Copy before creation so publication and its
	// later receipt describe one immutable byte sequence even if the caller
	// reuses its buffer after this call starts.
	recordContents := append([]byte(nil), contents...)
	if err := hooks.validate(); err != nil {
		return nil, err
	}

	directories.mu.Lock()
	defer directories.mu.Unlock()
	pair, err := directories.duplicateCheckedLocked()
	if err != nil {
		return nil, err
	}
	defer pair.Close()

	// Prove that the held info directory can be synced before openat is allowed
	// to create a record. A failed preflight leaves no entry to reconcile.
	if err := hooks.syncDirectory(pair.InfoFD); err != nil {
		return nil, classifyTrashInfoPreflightFailure(err)
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	fd, err := hooks.open(pair.InfoFD, basename, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
	if err != nil {
		return nil, classifyTrashInfoOpenFailure(err)
	}
	openFD := fd
	closed := false
	defer func() {
		// hooks.open has transferred ownership on every successful return. If a
		// process started with a closed standard stream, openat may legitimately
		// reuse descriptor 0, 1, or 2; it still must be released on this retained
		// post-create path.
		if !closed && openFD >= 0 {
			_ = hooks.close(openFD)
		}
	}()

	// A successful no-replace open may have linked the metadata entry. From
	// this point on every failure is an interrupted retained record, never a
	// cleanup attempt or a clean retry signal.
	if openFD < 3 {
		return nil, postCreateDurabilityError("open Trash info record returned a reserved descriptor", fmt.Errorf("%w: descriptor %d", ErrDrifted, openFD))
	}
	if err := contextError(ctx); err != nil {
		return nil, postCreateDurabilityError("context changed after Trash info creation", err)
	}
	identity, err := snapshotPublishedRegularFile(openFD)
	if err != nil {
		return nil, postCreateIdentityError("new Trash info identity", err)
	}
	if err := writeDurableFileContents(ctx, openFD, recordContents, hooks.write); err != nil {
		return nil, postCreateDurabilityError("write Trash info record", err)
	}
	if err := hooks.syncFile(openFD); err != nil {
		return nil, postCreateDurabilityError("sync Trash info record", err)
	}
	fdToClose := openFD
	openFD = -1
	closed = true
	if err := hooks.close(fdToClose); err != nil {
		return nil, postCreateDurabilityError("close Trash info record", err)
	}
	if err := contextError(ctx); err != nil {
		return nil, postCreateDurabilityError("context changed before Trash info directory sync", err)
	}
	if err := hooks.syncDirectory(pair.InfoFD); err != nil {
		return nil, postCreateDurabilityError("sync Trash info directory", err)
	}
	if err := verifyPublishedName(ctx, pair.InfoFD, basename, identity, recordContents); err != nil {
		return nil, postCreateIdentityError("Trash info name changed after durability sync", err)
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return nil, postCreateIdentityError("Trash directories changed after info publication", err)
	}
	return &TrashInfoPublication{
		rootID:        directories.rootID,
		token:         token,
		files:         directories.files,
		infoDirectory: directories.info,
		infoRecord:    identity,
		contents:      append([]byte(nil), recordContents...),
	}, nil
}

func trashInfoBasename(token string) (string, error) {
	if err := validateOwnedTrashToken(token); err != nil {
		return "", err
	}
	if err := validateBasename(token); err != nil {
		return "", err
	}
	basename := token + trashInfoFilenameSuffix
	if err := validateBasename(basename); err != nil {
		return "", err
	}
	return basename, nil
}

// validateOwnedTrashToken admits only the bounded opaque LDC token profile.
// It prevents the descriptor-rooted writer from publishing metadata under a
// foreign or caller-chosen name even if a higher-level metadata serializer was
// bypassed.
func validateOwnedTrashToken(token string) error {
	if len(token) == 0 || len(token) > maxTrashTokenBytes {
		return fmt.Errorf("%w: Trash token must contain 1 through %d bytes", ErrUnsupported, maxTrashTokenBytes)
	}
	if !strings.HasPrefix(token, trashTokenRequiredPrefix) || len(token)-len(trashTokenRequiredPrefix) < minTrashTokenHexBytes {
		return fmt.Errorf("%w: Trash token must begin with %q and contain at least %d hexadecimal bytes", ErrUnsupported, trashTokenRequiredPrefix, minTrashTokenHexBytes)
	}
	for _, value := range []byte(token[len(trashTokenRequiredPrefix):]) {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return fmt.Errorf("%w: Trash token contains an unsupported byte %q", ErrUnsupported, value)
		}
	}
	return nil
}

func (directories *TrashDirectories) duplicateCheckedLocked() (mounts.TrashDescriptorPair, error) {
	pair := mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}
	if directories.closed {
		return pair, fmt.Errorf("%w: Trash directories are closed", ErrDrifted)
	}
	if directories.source == nil {
		return pair, fmt.Errorf("%w: Trash directory authority is missing", ErrUnsupported)
	}
	if err := directories.rootID.Validate(); err != nil {
		return pair, fmt.Errorf("%w: Trash root identity: %v", ErrUnsupported, err)
	}
	if directories.source.RootID() != directories.rootID {
		return pair, fmt.Errorf("%w: Trash root identity changed", ErrDrifted)
	}
	if directories.topology != nil {
		return directories.duplicateTopologyCheckedLocked()
	}
	pair, err := directories.source.DuplicateTrashDescriptorPair()
	if err != nil {
		_ = pair.Close()
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, classifyTrashDirectorySourceFailure("requalify trusted Trash directories", err)
	}
	if directories.source.RootID() != directories.rootID {
		_ = pair.Close()
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, fmt.Errorf("%w: Trash root identity changed during requalification", ErrDrifted)
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		_ = pair.Close()
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, err
	}
	return pair, nil
}

func (directories *TrashDirectories) duplicateTopologyCheckedLocked() (mounts.TrashDescriptorPair, error) {
	pair := mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}
	topology := directories.topology
	if topology == nil || topology.source == nil {
		return pair, fmt.Errorf("%w: Trash topology authority is missing", ErrUnsupported)
	}
	if topology.source.RootID() != directories.rootID {
		return pair, fmt.Errorf("%w: Trash topology root identity changed", ErrDrifted)
	}
	if topology.source.Placement() != topology.placement || topology.source.AnchorKind() != topology.anchorKind || topology.source.OwnerUID() != topology.ownerUID {
		return pair, fmt.Errorf("%w: Trash topology authority changed", ErrDrifted)
	}
	set, err := topology.source.DuplicateTrashTopologyDescriptorSet()
	if err != nil {
		_ = set.Close()
		return pair, classifyTrashDirectorySourceFailure("requalify trusted Trash topology", err)
	}
	keepPair := false
	defer func() {
		_ = set.Close()
		if !keepPair {
			_ = pair.Close()
		}
	}()
	if topology.source.RootID() != directories.rootID {
		return pair, fmt.Errorf("%w: Trash topology root identity changed during requalification", ErrDrifted)
	}
	snapshots, err := inspectTrashTopologyDescriptorSet(set, topology.placement, topology.anchorKind, topology.ownerUID)
	if err != nil {
		return pair, err
	}
	if err := directories.compareTopologySnapshotsLocked(snapshots); err != nil {
		return pair, err
	}
	pair.FilesFD = set.FilesFD
	pair.InfoFD = set.InfoFD
	set.FilesFD = -1
	set.InfoFD = -1
	keepPair = true
	return pair, nil
}

func (directories *TrashDirectories) verifyPairLocked(pair mounts.TrashDescriptorPair) error {
	files, info, err := inspectTrashDescriptorPair(pair)
	if err != nil {
		return err
	}
	if err := compareTrashDirectoryIdentity("Trash files directory", directories.files, files); err != nil {
		return err
	}
	if err := compareTrashDirectoryIdentity("Trash info directory", directories.info, info); err != nil {
		return err
	}
	if directories.topology != nil {
		return directories.verifyTopologyLocked()
	}
	return nil
}

func (directories *TrashDirectories) verifyTopologyLocked() error {
	topology := directories.topology
	if topology == nil || topology.source == nil {
		return fmt.Errorf("%w: Trash topology authority is missing", ErrUnsupported)
	}
	set, err := topology.source.DuplicateTrashTopologyDescriptorSet()
	if err != nil {
		_ = set.Close()
		return classifyTrashDirectorySourceFailure("reverify trusted Trash topology", err)
	}
	defer set.Close()
	if topology.source.RootID() != directories.rootID ||
		topology.source.Placement() != topology.placement ||
		topology.source.AnchorKind() != topology.anchorKind ||
		topology.source.OwnerUID() != topology.ownerUID {
		return fmt.Errorf("%w: Trash topology authority changed during verification", ErrDrifted)
	}
	snapshots, err := inspectTrashTopologyDescriptorSet(set, topology.placement, topology.anchorKind, topology.ownerUID)
	if err != nil {
		return err
	}
	return directories.compareTopologySnapshotsLocked(snapshots)
}

func (directories *TrashDirectories) compareTopologySnapshotsLocked(observed trashTopologySnapshots) error {
	if directories.topology == nil {
		return fmt.Errorf("%w: Trash topology authority is missing", ErrUnsupported)
	}
	if err := compareTrashDirectoryIdentity("Trash topology anchor", directories.topology.anchor, observed.anchor); err != nil {
		return err
	}
	if err := compareTrashDirectoryIdentity("Trash root", directories.topology.trashRoot, observed.trashRoot); err != nil {
		return err
	}
	if directories.topology.placement == mounts.TrashPlacementTopShared {
		if err := compareTrashDirectoryIdentity("shared Trash parent", directories.topology.sharedTop, observed.sharedTop); err != nil {
			return err
		}
	}
	if err := compareTrashDirectoryIdentity("Trash files directory", directories.files, observed.files); err != nil {
		return err
	}
	return compareTrashDirectoryIdentity("Trash info directory", directories.info, observed.info)
}

func inspectTrashDescriptorPair(pair mounts.TrashDescriptorPair) (domain.FilesystemSnapshot, domain.FilesystemSnapshot, error) {
	if pair.FilesFD < 3 || pair.InfoFD < 3 {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, fmt.Errorf("%w: trusted Trash pair returned reserved or missing descriptors", ErrDrifted)
	}
	if pair.FilesFD == pair.InfoFD {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, fmt.Errorf("%w: trusted Trash files and info descriptors alias", ErrDrifted)
	}
	files, err := snapshotTrashDirectory(pair.FilesFD, "Trash files directory")
	if err != nil {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, err
	}
	info, err := snapshotTrashDirectory(pair.InfoFD, "Trash info directory")
	if err != nil {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, err
	}
	if files.DeviceMajor != info.DeviceMajor || files.DeviceMinor != info.DeviceMinor || files.MountID != info.MountID {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, fmt.Errorf("%w: Trash files and info directories are not on the same mount", ErrDrifted)
	}
	if files.Inode == info.Inode {
		return domain.FilesystemSnapshot{}, domain.FilesystemSnapshot{}, fmt.Errorf("%w: Trash files and info directories alias the same directory", ErrDrifted)
	}
	return files, info, nil
}

func validateTrashTopologyAuthority(placement mounts.TrashPlacement, anchorKind mounts.TrashAnchorKind) error {
	switch placement {
	case mounts.TrashPlacementHome:
		if anchorKind != mounts.TrashAnchorHomeData {
			return fmt.Errorf("%w: home Trash requires a data-home topology anchor", ErrUnsupported)
		}
	case mounts.TrashPlacementTopUser, mounts.TrashPlacementTopShared:
		if anchorKind != mounts.TrashAnchorFilesystemTop {
			return fmt.Errorf("%w: top-directory Trash requires a filesystem-top topology anchor", ErrUnsupported)
		}
	default:
		return fmt.Errorf("%w: unknown Trash placement %q", ErrUnsupported, placement)
	}
	return nil
}

func inspectTrashTopologyDescriptorSet(
	set mounts.TrashTopologyDescriptorSet,
	placement mounts.TrashPlacement,
	anchorKind mounts.TrashAnchorKind,
	ownerUID uint32,
) (trashTopologySnapshots, error) {
	if err := validateTrashTopologyAuthority(placement, anchorKind); err != nil {
		return trashTopologySnapshots{}, err
	}
	if set.AnchorFD < 3 || set.TrashRootFD < 3 || set.FilesFD < 3 || set.InfoFD < 3 {
		return trashTopologySnapshots{}, fmt.Errorf("%w: topology-qualified Trash descriptor set is incomplete", ErrDrifted)
	}
	if placement == mounts.TrashPlacementTopShared {
		if set.SharedTopFD < 3 {
			return trashTopologySnapshots{}, fmt.Errorf("%w: shared Trash layout has no parent descriptor", ErrDrifted)
		}
	} else if set.SharedTopFD != -1 {
		return trashTopologySnapshots{}, fmt.Errorf("%w: non-shared Trash layout has an unexpected shared parent descriptor", ErrDrifted)
	}

	roles := []struct {
		name string
		fd   int
	}{
		{name: "Trash topology anchor", fd: set.AnchorFD},
		{name: "Trash root", fd: set.TrashRootFD},
		{name: "Trash files directory", fd: set.FilesFD},
		{name: "Trash info directory", fd: set.InfoFD},
	}
	if placement == mounts.TrashPlacementTopShared {
		roles = append(roles, struct {
			name string
			fd   int
		}{name: "shared Trash parent", fd: set.SharedTopFD})
	}
	seenFDs := make(map[int]string, len(roles))
	for _, role := range roles {
		if previous, found := seenFDs[role.fd]; found {
			return trashTopologySnapshots{}, fmt.Errorf("%w: %s descriptor aliases %s", ErrDrifted, role.name, previous)
		}
		seenFDs[role.fd] = role.name
	}

	var snapshots trashTopologySnapshots
	var err error
	if snapshots.anchor, err = snapshotTrashDirectory(set.AnchorFD, "Trash topology anchor"); err != nil {
		return trashTopologySnapshots{}, err
	}
	if snapshots.trashRoot, err = snapshotTrashDirectory(set.TrashRootFD, "Trash root"); err != nil {
		return trashTopologySnapshots{}, err
	}
	if snapshots.files, err = snapshotTrashDirectory(set.FilesFD, "Trash files directory"); err != nil {
		return trashTopologySnapshots{}, err
	}
	if snapshots.info, err = snapshotTrashDirectory(set.InfoFD, "Trash info directory"); err != nil {
		return trashTopologySnapshots{}, err
	}
	if placement == mounts.TrashPlacementTopShared {
		if snapshots.sharedTop, err = snapshotTrashDirectory(set.SharedTopFD, "shared Trash parent"); err != nil {
			return trashTopologySnapshots{}, err
		}
	}
	if err := requireSameTrashMount("Trash topology anchor", snapshots.anchor, snapshots.trashRoot); err != nil {
		return trashTopologySnapshots{}, err
	}
	if err := requireSameTrashMount("Trash files directory", snapshots.trashRoot, snapshots.files); err != nil {
		return trashTopologySnapshots{}, err
	}
	if err := requireSameTrashMount("Trash info directory", snapshots.trashRoot, snapshots.info); err != nil {
		return trashTopologySnapshots{}, err
	}
	if placement == mounts.TrashPlacementTopShared {
		if err := requireSameTrashMount("shared Trash parent", snapshots.anchor, snapshots.sharedTop); err != nil {
			return trashTopologySnapshots{}, err
		}
		if err := requireSameTrashMount("shared Trash user root", snapshots.sharedTop, snapshots.trashRoot); err != nil {
			return trashTopologySnapshots{}, err
		}
	}
	if err := requireDistinctTrashTopologyRoles(snapshots, placement); err != nil {
		return trashTopologySnapshots{}, err
	}
	if err := requirePrivateTrashDirectory("Trash root", snapshots.trashRoot, ownerUID); err != nil {
		return trashTopologySnapshots{}, err
	}
	if err := requirePrivateTrashDirectory("Trash files directory", snapshots.files, ownerUID); err != nil {
		return trashTopologySnapshots{}, err
	}
	if err := requirePrivateTrashDirectory("Trash info directory", snapshots.info, ownerUID); err != nil {
		return trashTopologySnapshots{}, err
	}
	if placement == mounts.TrashPlacementTopShared && snapshots.sharedTop.Mode.Value&unix.S_ISVTX == 0 {
		return trashTopologySnapshots{}, fmt.Errorf("%w: shared Trash parent is not sticky", ErrDrifted)
	}

	if err := verifyTrashTopologyLiteralChildren(set, placement, ownerUID, snapshots); err != nil {
		return trashTopologySnapshots{}, err
	}
	return snapshots, nil
}

func requireSameTrashMount(role string, first, second domain.FilesystemSnapshot) error {
	if first.DeviceMajor != second.DeviceMajor || first.DeviceMinor != second.DeviceMinor || first.MountID != second.MountID {
		return fmt.Errorf("%w: %s is not on the expected Trash mount", ErrDrifted, role)
	}
	return nil
}

func requireDistinctTrashTopologyRoles(snapshots trashTopologySnapshots, placement mounts.TrashPlacement) error {
	roles := []struct {
		name     string
		snapshot domain.FilesystemSnapshot
	}{
		{name: "Trash topology anchor", snapshot: snapshots.anchor},
		{name: "Trash root", snapshot: snapshots.trashRoot},
		{name: "Trash files directory", snapshot: snapshots.files},
		{name: "Trash info directory", snapshot: snapshots.info},
	}
	if placement == mounts.TrashPlacementTopShared {
		roles = append(roles, struct {
			name     string
			snapshot domain.FilesystemSnapshot
		}{name: "shared Trash parent", snapshot: snapshots.sharedTop})
	}
	for index, role := range roles {
		for previous := 0; previous < index; previous++ {
			if role.snapshot.Inode == roles[previous].snapshot.Inode {
				return fmt.Errorf("%w: %s aliases %s", ErrDrifted, role.name, roles[previous].name)
			}
		}
	}
	return nil
}

func requirePrivateTrashDirectory(role string, snapshot domain.FilesystemSnapshot, ownerUID uint32) error {
	if snapshot.UID.Value != ownerUID {
		return fmt.Errorf("%w: %s is not owned by the configured Trash owner", ErrDrifted, role)
	}
	if snapshot.Mode.Value&0o7777 != 0o700 {
		return fmt.Errorf("%w: %s must have exact mode 0700", ErrDrifted, role)
	}
	return nil
}

func verifyTrashTopologyLiteralChildren(
	set mounts.TrashTopologyDescriptorSet,
	placement mounts.TrashPlacement,
	ownerUID uint32,
	snapshots trashTopologySnapshots,
) error {
	if placement == mounts.TrashPlacementTopShared {
		if err := verifyLiteralTrashChild(set.AnchorFD, ".Trash", "shared Trash parent", snapshots.sharedTop); err != nil {
			return err
		}
		if err := verifyLiteralTrashChild(set.SharedTopFD, strconv.FormatUint(uint64(ownerUID), 10), "shared Trash user root", snapshots.trashRoot); err != nil {
			return err
		}
	} else {
		name := "Trash"
		if placement == mounts.TrashPlacementTopUser {
			name = ".Trash-" + strconv.FormatUint(uint64(ownerUID), 10)
		}
		if err := verifyLiteralTrashChild(set.AnchorFD, name, "Trash root", snapshots.trashRoot); err != nil {
			return err
		}
	}
	if err := verifyLiteralTrashChild(set.TrashRootFD, "files", "Trash files directory", snapshots.files); err != nil {
		return err
	}
	return verifyLiteralTrashChild(set.TrashRootFD, "info", "Trash info directory", snapshots.info)
}

func verifyLiteralTrashChild(parentFD int, name, role string, expected domain.FilesystemSnapshot) error {
	fd, err := openDirectoryBeneath(context.Background(), unix.Openat2, parentFD, name)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return fmt.Errorf("%w: reopen literal %s child %q: %v", ErrUnsupported, role, name, err)
		}
		return fmt.Errorf("%w: reopen literal %s child %q: %v", ErrDrifted, role, name, err)
	}
	defer unix.Close(fd)
	observed, err := snapshotTrashDirectory(fd, role)
	if err != nil {
		return err
	}
	return compareTrashDirectoryIdentity(role, expected, observed)
}

func snapshotTrashDirectory(fd int, role string) (domain.FilesystemSnapshot, error) {
	if fd < 3 {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: %s descriptor is reserved or missing", ErrDrifted, role)
	}
	snapshot, err := SnapshotFD(fd, baselineFilesystemMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: %s is not a directory", ErrDrifted, role)
	}
	return snapshot, nil
}

func compareTrashDirectoryIdentity(role string, expected, observed domain.FilesystemSnapshot) error {
	if expected.DeviceMajor != observed.DeviceMajor || expected.DeviceMinor != observed.DeviceMinor {
		return driftedField(role + " device")
	}
	if expected.Inode != observed.Inode {
		return driftedField(role + " inode")
	}
	if expected.Type != observed.Type {
		return driftedField(role + " type")
	}
	if expected.UID != observed.UID || expected.GID != observed.GID {
		return driftedField(role + " ownership")
	}
	if expected.Mode != observed.Mode {
		return driftedField(role + " mode")
	}
	if expected.MountID != observed.MountID {
		return driftedField(role + " mount ID")
	}
	return nil
}

func classifyTrashDirectorySourceFailure(operation string, err error) error {
	switch {
	case errors.Is(err, mounts.ErrUnsupported),
		errors.Is(err, mounts.ErrInvalidAuthority),
		errors.Is(err, mounts.ErrUnknownTrash),
		errors.Is(err, mounts.ErrUnknownTrustedRoot):
		return fmt.Errorf("%w: %s: %v", ErrUnsupported, operation, err)
	case errors.Is(err, mounts.ErrLeaseClosed), errors.Is(err, mounts.ErrDrifted):
		return fmt.Errorf("%w: %s: %v", ErrDrifted, operation, err)
	default:
		return fmt.Errorf("%w: %s: %v", ErrDrifted, operation, err)
	}
}

func classifyTrashInfoPreflightFailure(err error) error {
	switch {
	case errors.Is(err, unix.EBADF):
		return fmt.Errorf("%w: Trash info directory is no longer syncable: %v", ErrDrifted, err)
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: Trash info directory cannot provide durable publication: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: Trash info durability preflight did not complete: %v", ErrInterrupted, err)
	}
}

func classifyTrashInfoOpenFailure(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.ELOOP):
		return fmt.Errorf("%w: Trash info basename is occupied or hostile: %v", ErrDrifted, err)
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: no-replace Trash info publication is unavailable: %v", ErrUnsupported, err)
	case errors.Is(err, unix.EINTR), errors.Is(err, unix.EAGAIN):
		return fmt.Errorf("%w: Trash info creation may require reconciliation: %v", ErrInterrupted, err)
	default:
		return fmt.Errorf("%w: Trash info creation failed: %v", ErrDrifted, err)
	}
}
