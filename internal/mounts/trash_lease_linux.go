//go:build linux

package mounts

import (
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

// TrashPlacement identifies the fixed Freedesktop Trash layout that an
// engine/helper authority selected for a trusted root. It is configuration
// evidence, never a request-controlled discovery instruction.
type TrashPlacement string

const (
	// TrashPlacementHome is a home-filesystem Trash. Path metadata is absolute.
	TrashPlacementHome TrashPlacement = "home"
	// TrashPlacementTopUser is an owned top-directory Trash such as .Trash-$uid.
	TrashPlacementTopUser TrashPlacement = "top_user"
	// TrashPlacementTopShared is a user directory beneath an owned sticky .Trash.
	TrashPlacementTopShared TrashPlacement = "top_shared"
)

// TrashMetadataBasis controls only the serialized .trashinfo Path form. It
// cannot be used to resolve a filesystem path.
type TrashMetadataBasis string

const (
	TrashMetadataBasisHomeAbsolute TrashMetadataBasis = "home_absolute"
	TrashMetadataBasisTopRelative  TrashMetadataBasis = "top_relative"
)

// TrashMetadataMapping is trusted registry context for serializing the path
// of a descendant of the selected root. Prefix is a copied raw-byte prefix;
// it is metadata only and never a directory to open.
type TrashMetadataMapping struct {
	Basis  TrashMetadataBasis
	Prefix pathbytes.BytePath
}

// TrashDescriptors is the descriptor bundle an engine/helper opener returns
// for one exact trusted Trash-role bundle. The opener accepts no path, UID,
// environment, or request input. All descriptors must be
// O_RDONLY|O_DIRECTORY|O_CLOEXEC and use a non-standard descriptor number
// (>= 3). Construct a production bundle with NewTrashDescriptors so an owned
// descriptor returned as 0, 1, or 2 is normalized before the bundle crosses
// this authority boundary.
//
// The bundle is intentionally internal to mounts except for the trusted
// opener boundary. A TrashLease only lends duplicated files/info descriptors
// to linuxfs through Duplicate.
type TrashDescriptors struct {
	TrashRoot int
	Files     int
	Info      int
	SharedTop int

	normalized bool
}

// NewTrashDescriptors transfers ownership of newly opened Trash-role
// descriptors to a normalized bundle. It duplicates each present descriptor
// with a minimum number of 3 and closes the transferred original, including a
// descriptor that reused a closed standard stream. The caller must not close
// or reuse any supplied descriptor after this call, whether it succeeds or
// fails.
//
// A shared parent may be -1 for non-shared layouts; role selection is checked
// later by the fixed authority placement. Every other negative descriptor and
// any aliased input descriptor is rejected before it can become authority.
func NewTrashDescriptors(trashRoot, files, info, sharedTop int) (TrashDescriptors, error) {
	raw := []int{trashRoot, files, info, sharedTop}
	for index, fd := range raw {
		if fd < -1 {
			closeOwnedTrashDescriptors(raw)
			return emptyTrashDescriptors(), fmt.Errorf("%w: Trash descriptor %d is negative", ErrInvalidAuthority, index)
		}
		if fd < 0 {
			continue
		}
		for previous := 0; previous < index; previous++ {
			if raw[previous] == fd {
				closeOwnedTrashDescriptors(raw)
				return emptyTrashDescriptors(), fmt.Errorf("%w: Trash descriptor %d aliases descriptor %d", ErrInvalidAuthority, index, previous)
			}
		}
	}

	normalized := []int{-1, -1, -1, -1}
	for index, fd := range raw {
		if fd < 0 {
			continue
		}
		duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			closeOwnedTrashDescriptors(raw[index:])
			closeOwnedTrashDescriptors(normalized)
			return emptyTrashDescriptors(), fmt.Errorf("%w: normalize trusted Trash descriptor %d: %v", ErrInvalidAuthority, index, err)
		}
		normalized[index] = duplicate
		if err := unix.Close(fd); err != nil {
			// On Linux, retrying close after an error risks closing a descriptor
			// that another thread has already reused. Do not retry this FD.
			raw[index] = -1
			closeOwnedTrashDescriptors(raw[index+1:])
			closeOwnedTrashDescriptors(normalized)
			return emptyTrashDescriptors(), fmt.Errorf("%w: release transferred Trash descriptor %d: %v", ErrDrifted, index, err)
		}
		raw[index] = -1
	}

	return TrashDescriptors{
		TrashRoot:  normalized[0],
		Files:      normalized[1],
		Info:       normalized[2],
		SharedTop:  normalized[3],
		normalized: true,
	}, nil
}

func emptyTrashDescriptors() TrashDescriptors {
	return TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}
}

func closeOwnedTrashDescriptors(fds []int) {
	closed := make(map[int]struct{}, len(fds))
	for _, fd := range fds {
		if fd < 0 {
			continue
		}
		if _, alreadyClosed := closed[fd]; alreadyClosed {
			continue
		}
		closed[fd] = struct{}{}
		_ = unix.Close(fd)
	}
}

// Close closes every owned descriptor in the bundle. It is safe for partially
// populated bundles and reports only the first close error.
func (descriptors *TrashDescriptors) Close() error {
	if descriptors == nil {
		return nil
	}
	var closeErr error
	closed := make(map[int]struct{}, 4)
	for _, entry := range []*int{
		&descriptors.TrashRoot,
		&descriptors.Files,
		&descriptors.Info,
		&descriptors.SharedTop,
	} {
		fd := *entry
		*entry = -1
		if fd < 3 {
			continue
		}
		if _, alreadyClosed := closed[fd]; alreadyClosed {
			continue
		}
		closed[fd] = struct{}{}
		if err := unix.Close(fd); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	descriptors.normalized = false
	return closeErr
}

func (descriptors TrashDescriptors) validate(placement TrashPlacement) error {
	if err := placement.validate(); err != nil {
		return err
	}
	if !descriptors.normalized {
		return fmt.Errorf("%w: Trash descriptor bundle was not normalized by the ownership-transfer constructor", ErrInvalidAuthority)
	}
	roles := []struct {
		name string
		fd   int
	}{
		{name: "Trash root", fd: descriptors.TrashRoot},
		{name: "Trash files", fd: descriptors.Files},
		{name: "Trash info", fd: descriptors.Info},
	}
	if placement == TrashPlacementTopShared {
		roles = append(roles, struct {
			name string
			fd   int
		}{name: "shared Trash parent", fd: descriptors.SharedTop})
	} else if descriptors.SharedTop != -1 {
		return fmt.Errorf("%w: non-shared Trash bundle returned a shared parent descriptor", ErrInvalidAuthority)
	}

	seen := make(map[int]string, len(roles))
	for _, role := range roles {
		if role.fd < 3 {
			return fmt.Errorf("%w: %s descriptor is missing or reserved", ErrInvalidAuthority, role.name)
		}
		if previous, found := seen[role.fd]; found {
			return fmt.Errorf("%w: %s descriptor aliases %s", ErrInvalidAuthority, role.name, previous)
		}
		seen[role.fd] = role.name
	}
	return nil
}

// TrashOpener transfers one newly opened descriptor bundle from trusted
// engine/helper composition. It deliberately has no parameters so callers
// cannot redirect it to an arbitrary Trash directory.
type TrashOpener func() (TrashDescriptors, error)

// TrashLayoutExpectation records the exact expected descriptors for a Trash
// root and its literal files/info roles. SharedTop is present only for the
// sticky top-directory placement.
type TrashLayoutExpectation struct {
	TrashRoot    LayoutExpectation
	Files        LayoutExpectation
	Info         LayoutExpectation
	HasSharedTop bool
	SharedTop    LayoutExpectation
}

// TrashAuthority binds all FDO layout roles and metadata context to a single
// trusted root. Only engine/helper composition may supply this registry data;
// no caller path, caller UID, or runtime environment participates.
type TrashAuthority struct {
	Root      domain.TrustedRootID
	Placement TrashPlacement
	OwnerUID  uint32
	Metadata  TrashMetadataMapping
	Open      TrashOpener
	Expected  TrashLayoutExpectation
}

func (authority TrashAuthority) validate(requestedRoot domain.TrustedRootID) error {
	if err := requestedRoot.Validate(); err != nil {
		return fmt.Errorf("%w: requested Trash root: %v", ErrInvalidAuthority, err)
	}
	if err := authority.Root.Validate(); err != nil {
		return fmt.Errorf("%w: Trash authority root: %v", ErrInvalidAuthority, err)
	}
	if authority.Root != requestedRoot {
		return fmt.Errorf("%w: Trash authority root %q does not match requested root %q", ErrInvalidAuthority, authority.Root, requestedRoot)
	}
	if authority.Open == nil {
		return fmt.Errorf("%w: Trash authority has no descriptor opener", ErrInvalidAuthority)
	}
	if err := authority.Placement.validate(); err != nil {
		return err
	}
	if err := authority.Metadata.validate(authority.Placement); err != nil {
		return err
	}
	if err := authority.Expected.validate(authority.Placement, authority.OwnerUID); err != nil {
		return err
	}
	return nil
}

func (placement TrashPlacement) validate() error {
	switch placement {
	case TrashPlacementHome, TrashPlacementTopUser, TrashPlacementTopShared:
		return nil
	default:
		return fmt.Errorf("%w: unknown Trash placement %q", ErrInvalidAuthority, placement)
	}
}

func (basis TrashMetadataBasis) validate() error {
	switch basis {
	case TrashMetadataBasisHomeAbsolute, TrashMetadataBasisTopRelative:
		return nil
	default:
		return fmt.Errorf("%w: unknown Trash metadata basis %q", ErrInvalidAuthority, basis)
	}
}

func (mapping TrashMetadataMapping) validate(placement TrashPlacement) error {
	if err := mapping.Basis.validate(); err != nil {
		return err
	}
	switch placement {
	case TrashPlacementHome:
		if mapping.Basis != TrashMetadataBasisHomeAbsolute {
			return fmt.Errorf("%w: home Trash requires absolute metadata", ErrInvalidAuthority)
		}
	case TrashPlacementTopUser, TrashPlacementTopShared:
		if mapping.Basis != TrashMetadataBasisTopRelative {
			return fmt.Errorf("%w: top-directory Trash requires relative metadata", ErrInvalidAuthority)
		}
	}
	if components := mapping.Prefix.Components(); len(components) > 0 {
		if _, err := pathbytes.New(components); err != nil {
			return fmt.Errorf("%w: Trash metadata prefix: %v", ErrInvalidAuthority, err)
		}
	}
	return nil
}

func (expectation TrashLayoutExpectation) validate(placement TrashPlacement, ownerUID uint32) error {
	for _, role := range []struct {
		name        string
		expectation LayoutExpectation
	}{
		{name: "Trash root", expectation: expectation.TrashRoot},
		{name: "Trash files", expectation: expectation.Files},
		{name: "Trash info", expectation: expectation.Info},
	} {
		if err := role.expectation.validate(); err != nil {
			return fmt.Errorf("%s: %w", role.name, err)
		}
		if role.expectation.UID != ownerUID {
			return fmt.Errorf("%w: %s is not owned by the configured Trash owner", ErrInvalidAuthority, role.name)
		}
		if role.expectation.Mode&0o7777 != 0o700 {
			return fmt.Errorf("%w: %s must have exact mode 0700", ErrInvalidAuthority, role.name)
		}
		if !sameMountRecord(expectation.TrashRoot.Mount, role.expectation.Mount) || expectation.TrashRoot.Namespace != role.expectation.Namespace || expectation.TrashRoot.Device != role.expectation.Device {
			return fmt.Errorf("%w: %s is not on the Trash root mount", ErrInvalidAuthority, role.name)
		}
	}
	if expectation.TrashRoot.Inode == expectation.Files.Inode ||
		expectation.TrashRoot.Inode == expectation.Info.Inode ||
		expectation.Files.Inode == expectation.Info.Inode {
		return fmt.Errorf("%w: Trash root, files, and info must be distinct directories", ErrInvalidAuthority)
	}

	if placement != TrashPlacementTopShared {
		if expectation.HasSharedTop {
			return fmt.Errorf("%w: only shared top-directory Trash may include a shared parent", ErrInvalidAuthority)
		}
		return nil
	}
	if !expectation.HasSharedTop {
		return fmt.Errorf("%w: shared top-directory Trash requires sticky parent evidence", ErrInvalidAuthority)
	}
	if err := expectation.SharedTop.validate(); err != nil {
		return fmt.Errorf("shared Trash parent: %w", err)
	}
	if expectation.SharedTop.Mode&unix.S_ISVTX == 0 {
		return fmt.Errorf("%w: shared Trash parent must be sticky", ErrInvalidAuthority)
	}
	if !sameMountRecord(expectation.TrashRoot.Mount, expectation.SharedTop.Mount) || expectation.TrashRoot.Namespace != expectation.SharedTop.Namespace || expectation.TrashRoot.Device != expectation.SharedTop.Device {
		return fmt.Errorf("%w: shared Trash parent is not on the Trash root mount", ErrInvalidAuthority)
	}
	if expectation.SharedTop.Inode == expectation.TrashRoot.Inode || expectation.SharedTop.Inode == expectation.Files.Inode || expectation.SharedTop.Inode == expectation.Info.Inode {
		return fmt.Errorf("%w: shared Trash parent aliases a protected Trash directory", ErrInvalidAuthority)
	}
	return nil
}

// TrashRegistry resolves one engine/helper-owned Trash bundle by trusted root
// ID. It cannot be queried with a path, UID, environment value, or FDO name.
type TrashRegistry interface {
	LookupTrash(domain.TrustedRootID) (TrashAuthority, bool)
}

// StaticTrashRegistry is a small engine/helper-friendly registry. It returns
// a value copy so callers cannot mutate a selected mapping in place.
type StaticTrashRegistry map[domain.TrustedRootID]TrashAuthority

// LookupTrash implements TrashRegistry.
func (registry StaticTrashRegistry) LookupTrash(root domain.TrustedRootID) (TrashAuthority, bool) {
	authority, found := registry[root]
	return authority, found
}

// TrashDescriptorPair is the only descriptor handoff from TrashLease to the
// rooted linuxfs package. It contains duplicate CLOEXEC FDs for the fixed
// files and info directories; no Trash pathname is exposed.
type TrashDescriptorPair struct {
	FilesFD int
	InfoFD  int
}

// Close releases the duplicated directory descriptors.
func (pair *TrashDescriptorPair) Close() error {
	if pair == nil {
		return nil
	}
	var closeErr error
	closed := make(map[int]struct{}, 2)
	for _, entry := range []*int{&pair.FilesFD, &pair.InfoFD} {
		fd := *entry
		*entry = -1
		if fd < 3 {
			continue
		}
		if _, alreadyClosed := closed[fd]; alreadyClosed {
			continue
		}
		closed[fd] = struct{}{}
		if err := unix.Close(fd); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

// TrashLease owns a qualified Trash descriptor bundle. It rechecks the root,
// all protected roles, and their exact mount binding before it lends files/info
// duplicates to linuxfs. Metadata context remains data-only.
type TrashLease struct {
	root      *RootLease
	rootID    domain.TrustedRootID
	placement TrashPlacement
	ownerUID  uint32
	metadata  TrashMetadataMapping
	expected  TrashLayoutExpectation

	mu          sync.Mutex
	descriptors TrashDescriptors
	closed      bool
}

// RootID reports the configured trusted root binding.
func (lease *TrashLease) RootID() domain.TrustedRootID {
	if lease == nil {
		return ""
	}
	return lease.rootID
}

// Placement reports the fixed FDO layout class selected by trusted authority.
func (lease *TrashLease) Placement() TrashPlacement {
	if lease == nil {
		return ""
	}
	return lease.placement
}

// MetadataBasis reports the serialization-only Path basis.
func (lease *TrashLease) MetadataBasis() TrashMetadataBasis {
	if lease == nil {
		return ""
	}
	return lease.metadata.Basis
}

// OwnerUID reports the registry-configured owner for the fixed Trash
// directory. It is layout evidence only; callers cannot supply or alter it.
func (lease *TrashLease) OwnerUID() uint32 {
	if lease == nil {
		return 0
	}
	return lease.ownerUID
}

// MetadataPathFor maps a trusted-root-relative raw-byte path to metadata
// bytes. It never opens, resolves, or returns an absolute filesystem path.
func (lease *TrashLease) MetadataPathFor(sourceRelative pathbytes.BytePath) (pathbytes.BytePath, error) {
	if lease == nil {
		return pathbytes.BytePath{}, ErrLeaseClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return pathbytes.BytePath{}, ErrLeaseClosed
	}
	if _, err := pathbytes.New(sourceRelative.Components()); err != nil {
		return pathbytes.BytePath{}, fmt.Errorf("%w: source-relative Trash metadata path: %v", ErrInvalidAuthority, err)
	}
	components := append(lease.metadata.Prefix.Components(), sourceRelative.Components()...)
	mapped, err := pathbytes.New(components)
	if err != nil {
		return pathbytes.BytePath{}, fmt.Errorf("%w: combined Trash metadata path: %v", ErrInvalidAuthority, err)
	}
	encoded := pathbytes.PercentEncodeTrashPath(mapped)
	if _, err := pathbytes.PercentDecodeTrashPath(encoded); err != nil {
		return pathbytes.BytePath{}, fmt.Errorf("%w: combined Trash metadata path exceeds the decoder profile: %v", ErrInvalidAuthority, err)
	}
	return mapped, nil
}

// Duplicate gives linuxfs requalified CLOEXEC duplicates of the fixed files
// and info directories. Architecture rules prohibit every other production
// package from calling it.
func (lease *TrashLease) Duplicate() (TrashDescriptorPair, error) {
	return lease.duplicateWith(InspectMount)
}

func (lease *TrashLease) duplicateWith(inspect rootInspector) (TrashDescriptorPair, error) {
	return lease.duplicateWithFcntl(inspect, func(fd int) (int, error) {
		return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	})
}

func (lease *TrashLease) duplicateWithFcntl(inspect rootInspector, duplicate func(int) (int, error)) (TrashDescriptorPair, error) {
	pair := TrashDescriptorPair{FilesFD: -1, InfoFD: -1}
	if lease == nil {
		return pair, ErrLeaseClosed
	}
	if inspect == nil || duplicate == nil {
		return pair, fmt.Errorf("%w: missing Trash requalification dependency", ErrInvalidAuthority)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return pair, ErrLeaseClosed
	}
	if err := lease.requalifyLocked(inspect); err != nil {
		return pair, err
	}
	keepPair := false
	defer func() {
		if !keepPair {
			_ = pair.Close()
		}
	}()
	files, err := duplicate(lease.descriptors.Files)
	if err != nil {
		lease.closeRejectedDuplicate(files, -1)
		return pair, fmt.Errorf("duplicate trusted Trash files directory: %w", err)
	}
	if err := lease.validateDuplicateDescriptor(files, "files", -1); err != nil {
		lease.closeRejectedDuplicate(files, -1)
		return pair, err
	}
	pair.FilesFD = files
	info, err := duplicate(lease.descriptors.Info)
	if err != nil {
		lease.closeRejectedDuplicate(info, pair.FilesFD)
		return pair, fmt.Errorf("duplicate trusted Trash info directory: %w", err)
	}
	if err := lease.validateDuplicateDescriptor(info, "info", pair.FilesFD); err != nil {
		lease.closeRejectedDuplicate(info, pair.FilesFD)
		return pair, err
	}
	pair.InfoFD = info
	keepPair = true
	return pair, nil
}

func (lease *TrashLease) requalifyLocked(inspect rootInspector) error {
	if lease.root == nil || lease.root.RootID() != lease.rootID {
		return fmt.Errorf("%w: trusted Trash root identity changed", ErrDrifted)
	}
	if err := lease.expected.validate(lease.placement, lease.ownerUID); err != nil {
		return err
	}
	if err := lease.descriptors.validate(lease.placement); err != nil {
		return err
	}
	rootInspection, err := lease.root.requalifyWith(inspect)
	if err != nil {
		return err
	}
	if err := checkTrashDescriptor(lease.descriptors.TrashRoot, "Trash root", lease.expected.TrashRoot, rootInspection, inspect); err != nil {
		return err
	}
	if err := checkTrashDescriptor(lease.descriptors.Files, "Trash files", lease.expected.Files, rootInspection, inspect); err != nil {
		return err
	}
	if err := checkTrashDescriptor(lease.descriptors.Info, "Trash info", lease.expected.Info, rootInspection, inspect); err != nil {
		return err
	}
	if lease.placement == TrashPlacementTopShared {
		if err := checkTrashDescriptor(lease.descriptors.SharedTop, "shared Trash parent", lease.expected.SharedTop, rootInspection, inspect); err != nil {
			return err
		}
	}
	return nil
}

func (lease *TrashLease) validateDuplicateDescriptor(fd int, role string, pairedFD int) error {
	if fd < 3 {
		return fmt.Errorf("%w: duplicate trusted Trash %s directory returned reserved descriptor %d", ErrInvalidAuthority, role, fd)
	}
	if fd == pairedFD {
		return fmt.Errorf("%w: duplicate trusted Trash %s directory aliases a descriptor in the handoff", ErrInvalidAuthority, role)
	}
	if fd == lease.descriptors.TrashRoot || fd == lease.descriptors.Files || fd == lease.descriptors.Info || fd == lease.descriptors.SharedTop {
		return fmt.Errorf("%w: duplicate trusted Trash %s directory aliases an authority descriptor", ErrInvalidAuthority, role)
	}
	if err := validateLeasedDirectoryFD(fd, "duplicated Trash "+role); err != nil {
		return err
	}
	return nil
}

func (lease *TrashLease) closeRejectedDuplicate(fd, pairedFD int) {
	if fd < 3 || fd == lease.descriptors.TrashRoot || fd == lease.descriptors.Files || fd == lease.descriptors.Info || fd == lease.descriptors.SharedTop {
		return
	}
	if fd == pairedFD {
		return
	}
	_ = unix.Close(fd)
}

func checkTrashDescriptor(fd int, label string, expected LayoutExpectation, rootInspection MountInspection, inspect rootInspector) error {
	if fd < 3 {
		return fmt.Errorf("%w: %s descriptor is missing or reserved", ErrInvalidAuthority, label)
	}
	if err := validateLeasedDirectoryFD(fd, label); err != nil {
		return err
	}
	inspection, err := inspect(fd)
	if err != nil {
		return err
	}
	if err := checkLayoutEvidence(expected, inspection); err != nil {
		return err
	}
	return checkLayoutRootBinding(rootInspection, inspection)
}

// Close releases the authority-owned Trash descriptors. It does not close the
// separately held source RootLease and is idempotent.
func (lease *TrashLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	return lease.descriptors.Close()
}

// OpenTrustedTrash opens the complete engine/helper-selected Trash bundle for
// a held root. It never discovers FDO directories from a caller path, UID,
// HOME, XDG variable, or mount traversal.
func OpenTrustedTrash(registry TrashRegistry, root *RootLease) (*TrashLease, error) {
	return openTrustedTrashWith(registry, root, InspectMount, checkMinimumKernel)
}

func openTrustedTrashWith(registry TrashRegistry, root *RootLease, inspect rootInspector, checkKernel kernelChecker) (*TrashLease, error) {
	if registry == nil || inspect == nil || checkKernel == nil {
		return nil, fmt.Errorf("%w: missing trusted Trash dependency", ErrInvalidAuthority)
	}
	if root == nil {
		return nil, ErrLeaseClosed
	}
	rootID := root.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: Trash root identity: %v", ErrInvalidAuthority, err)
	}
	authority, found := registry.LookupTrash(rootID)
	if !found {
		return nil, fmt.Errorf("%w: root %q", ErrUnknownTrash, rootID)
	}
	if err := authority.validate(rootID); err != nil {
		return nil, err
	}
	if err := checkKernel(); err != nil {
		return nil, err
	}
	rootInspection, err := root.requalifyWith(inspect)
	if err != nil {
		return nil, err
	}
	descriptors, err := authority.Open()
	if err != nil {
		_ = descriptors.Close()
		return nil, fmt.Errorf("open trusted Trash bundle for root %q: %w", rootID, err)
	}
	keepDescriptors := false
	defer func() {
		if !keepDescriptors {
			_ = descriptors.Close()
		}
	}()
	if err := descriptors.validate(authority.Placement); err != nil {
		return nil, err
	}
	if err := checkTrashDescriptor(descriptors.TrashRoot, "Trash root", authority.Expected.TrashRoot, rootInspection, inspect); err != nil {
		return nil, err
	}
	if err := checkTrashDescriptor(descriptors.Files, "Trash files", authority.Expected.Files, rootInspection, inspect); err != nil {
		return nil, err
	}
	if err := checkTrashDescriptor(descriptors.Info, "Trash info", authority.Expected.Info, rootInspection, inspect); err != nil {
		return nil, err
	}
	if authority.Placement == TrashPlacementTopShared {
		if err := checkTrashDescriptor(descriptors.SharedTop, "shared Trash parent", authority.Expected.SharedTop, rootInspection, inspect); err != nil {
			return nil, err
		}
	}
	lease := &TrashLease{
		root:        root,
		rootID:      rootID,
		placement:   authority.Placement,
		ownerUID:    authority.OwnerUID,
		metadata:    authority.Metadata,
		expected:    authority.Expected,
		descriptors: descriptors,
	}
	keepDescriptors = true
	return lease, nil
}
