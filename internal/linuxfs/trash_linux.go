//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"
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
	Duplicate() (mounts.TrashDescriptorPair, error)
}

var _ trashDirectorySource = (*mounts.TrashLease)(nil)

// TrashDirectories is an opaque, descriptor-rooted qualification of one
// engine/helper-selected Freedesktop Trash files/info pair. It retains only
// stable identity evidence and the source authority, never a pathname or a
// raw descriptor. Every publication obtains a newly requalified pair.
type TrashDirectories struct {
	source trashDirectorySource
	rootID domain.TrustedRootID
	files  domain.FilesystemSnapshot
	info   domain.FilesystemSnapshot

	mu     sync.Mutex
	closed bool
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

// OpenTrashDirectories qualifies the one complete Trash layout selected by a
// trusted mounts lease. It accepts no path, UID, HOME, XDG variable, or raw
// descriptor. The first pair establishes stable baseline identities; later
// calls must duplicate and match that baseline before any publication occurs.
func OpenTrashDirectories(lease *mounts.TrashLease) (*TrashDirectories, error) {
	if lease == nil {
		return nil, fmt.Errorf("%w: trusted Trash lease is required", ErrUnsupported)
	}
	return openTrashDirectoriesWithSource(lease)
}

func openTrashDirectoriesWithSource(source trashDirectorySource) (*TrashDirectories, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: trusted Trash lease is required", ErrUnsupported)
	}
	rootID := source.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: Trash root identity: %v", ErrUnsupported, err)
	}
	pair, err := source.Duplicate()
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
	pair, err := directories.source.Duplicate()
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

func (directories *TrashDirectories) verifyPairLocked(pair mounts.TrashDescriptorPair) error {
	files, info, err := inspectTrashDescriptorPair(pair)
	if err != nil {
		return err
	}
	if err := compareTrashDirectoryIdentity("Trash files directory", directories.files, files); err != nil {
		return err
	}
	return compareTrashDirectoryIdentity("Trash info directory", directories.info, info)
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
