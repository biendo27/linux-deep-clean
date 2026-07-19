//go:build linux

package mounts

import (
	"encoding/binary"
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

// TrashAnchorKind identifies the engine/helper-attested parent from which a
// Freedesktop Trash layout must be reopened by literal child name. It is
// topology evidence only: neither kind permits a caller to discover a HOME,
// XDG directory, or filesystem top from a string path.
type TrashAnchorKind string

const (
	// TrashAnchorHomeData is the trusted data-home directory whose literal
	// Trash child is the home-filesystem Trash root.
	TrashAnchorHomeData TrashAnchorKind = "home_data"
	// TrashAnchorFilesystemTop is the trusted filesystem-top directory whose
	// literal .Trash-$uid or .Trash child establishes a top-directory Trash.
	TrashAnchorFilesystemTop TrashAnchorKind = "filesystem_top"
)

// TrashTopology records optional engine/helper-owned evidence required to
// prove literal Freedesktop child relationships. A zero topology preserves
// the existing pre-selector lease boundary for metadata-only callers; any
// operation that needs a qualified layout must require a nonzero topology.
type TrashTopology struct {
	AnchorKind TrashAnchorKind
	Anchor     LayoutExpectation
}

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
// opener boundary. A legacy pre-selector TrashLease lends duplicated
// files/info descriptors to linuxfs through DuplicateTrashDescriptorPair; a
// topology-qualified lease can instead lend the complete fixed role set
// through DuplicateTrashTopologyDescriptorSet.
type TrashDescriptors struct {
	Anchor    int
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
	return newTrashDescriptors(-1, trashRoot, files, info, sharedTop)
}

// NewTrashTopologyDescriptors transfers an engine/helper-opened topology
// anchor together with the fixed Trash roles. Unlike NewTrashDescriptors, it
// requires a real anchor descriptor so linuxfs can later prove literal FDO
// child relationships without reconstructing any location from a path.
func NewTrashTopologyDescriptors(anchor, trashRoot, files, info, sharedTop int) (TrashDescriptors, error) {
	if anchor < 0 {
		closeOwnedTrashDescriptors([]int{anchor, trashRoot, files, info, sharedTop})
		return emptyTrashDescriptors(), fmt.Errorf("%w: Trash topology anchor is missing", ErrInvalidAuthority)
	}
	return newTrashDescriptors(anchor, trashRoot, files, info, sharedTop)
}

func newTrashDescriptors(anchor, trashRoot, files, info, sharedTop int) (TrashDescriptors, error) {
	raw := []int{anchor, trashRoot, files, info, sharedTop}
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

	normalized := []int{-1, -1, -1, -1, -1}
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
		Anchor:     normalized[0],
		TrashRoot:  normalized[1],
		Files:      normalized[2],
		Info:       normalized[3],
		SharedTop:  normalized[4],
		normalized: true,
	}, nil
}

func emptyTrashDescriptors() TrashDescriptors {
	return TrashDescriptors{Anchor: -1, TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}
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
		&descriptors.Anchor,
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
	if descriptors.Anchor != -1 {
		roles = append(roles, struct {
			name string
			fd   int
		}{name: "Trash topology anchor", fd: descriptors.Anchor})
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

func (descriptors TrashDescriptors) validateTopology(placement TrashPlacement) error {
	if err := descriptors.validate(placement); err != nil {
		return err
	}
	if descriptors.Anchor < 3 {
		return fmt.Errorf("%w: topology-qualified Trash bundle has no anchor descriptor", ErrInvalidAuthority)
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
	Topology  TrashTopology
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
	if err := authority.Topology.validate(authority.Placement); err != nil {
		return err
	}
	if authority.Topology.configured() {
		if err := validateTrashTopologyBinding(authority.Topology, authority.Expected); err != nil {
			return err
		}
	}
	return nil
}

func validateTrashTopologyBinding(topology TrashTopology, expected TrashLayoutExpectation) error {
	anchor := topology.Anchor
	if !sameMountRecord(anchor.Mount, expected.TrashRoot.Mount) ||
		anchor.Namespace != expected.TrashRoot.Namespace ||
		anchor.Device != expected.TrashRoot.Device {
		return fmt.Errorf("%w: Trash topology anchor is not on the Trash root mount", ErrInvalidAuthority)
	}
	if anchor.Inode == expected.TrashRoot.Inode ||
		anchor.Inode == expected.Files.Inode ||
		anchor.Inode == expected.Info.Inode ||
		(expected.HasSharedTop && anchor.Inode == expected.SharedTop.Inode) {
		return fmt.Errorf("%w: Trash topology anchor aliases a protected Trash role", ErrInvalidAuthority)
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

func (kind TrashAnchorKind) validate() error {
	switch kind {
	case TrashAnchorHomeData, TrashAnchorFilesystemTop:
		return nil
	default:
		return fmt.Errorf("%w: unknown Trash topology anchor kind %q", ErrInvalidAuthority, kind)
	}
}

func (topology TrashTopology) configured() bool {
	return topology.AnchorKind != ""
}

func (topology TrashTopology) validate(placement TrashPlacement) error {
	if !topology.configured() {
		if !isZeroTrashLayoutExpectation(topology.Anchor) {
			return fmt.Errorf("%w: Trash topology anchor evidence requires an anchor kind", ErrInvalidAuthority)
		}
		return nil
	}
	if err := topology.AnchorKind.validate(); err != nil {
		return err
	}
	if err := topology.Anchor.validate(); err != nil {
		return fmt.Errorf("Trash topology anchor: %w", err)
	}
	switch placement {
	case TrashPlacementHome:
		if topology.AnchorKind != TrashAnchorHomeData {
			return fmt.Errorf("%w: home Trash requires a data-home topology anchor", ErrInvalidAuthority)
		}
	case TrashPlacementTopUser, TrashPlacementTopShared:
		if topology.AnchorKind != TrashAnchorFilesystemTop {
			return fmt.Errorf("%w: top-directory Trash requires a filesystem-top topology anchor", ErrInvalidAuthority)
		}
	default:
		return fmt.Errorf("%w: unknown Trash placement %q", ErrInvalidAuthority, placement)
	}
	return nil
}

func isZeroTrashLayoutExpectation(expectation LayoutExpectation) bool {
	return expectation.Namespace == (MountNamespace{}) &&
		expectation.Device == (DeviceIdentity{}) &&
		expectation.Inode == 0 &&
		expectation.UID == 0 &&
		expectation.GID == 0 &&
		expectation.Mode == 0 &&
		expectation.Mount == (MountRecord{})
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
		if expectation.HasSharedTop || !isZeroTrashLayoutExpectation(expectation.SharedTop) {
			return fmt.Errorf("%w: only shared top-directory Trash may include shared parent evidence", ErrInvalidAuthority)
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

// TrashTopologyDescriptorSet is the only full topology handoff from a
// topology-qualified TrashLease to linuxfs. It carries CLOEXEC duplicates of
// engine/helper-selected descriptors, never pathnames. The normal pair handoff
// remains available for metadata-only pre-selector users.
type TrashTopologyDescriptorSet struct {
	AnchorFD    int
	TrashRootFD int
	FilesFD     int
	InfoFD      int
	SharedTopFD int
}

// EmptyTrashTopologyDescriptorSet returns a released descriptor set. It is
// useful to an opener that must return a descriptor-shaped value with an
// error, while preserving the invariant that no nonnegative descriptor is
// implicitly owned by the caller.
func EmptyTrashTopologyDescriptorSet() TrashTopologyDescriptorSet {
	return TrashTopologyDescriptorSet{AnchorFD: -1, TrashRootFD: -1, FilesFD: -1, InfoFD: -1, SharedTopFD: -1}
}

// Close releases every descriptor in the topology handoff. It is safe for a
// partially populated set and ignores reserved descriptors because production
// authority constructors normalize ownership above standard streams.
func (set *TrashTopologyDescriptorSet) Close() error {
	if set == nil {
		return nil
	}
	var closeErr error
	closed := make(map[int]struct{}, 5)
	for _, entry := range []*int{&set.AnchorFD, &set.TrashRootFD, &set.FilesFD, &set.InfoFD, &set.SharedTopFD} {
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
// all protected roles, and their exact mount binding before it lends either a
// legacy files/info pair or the full topology role set to linuxfs. Metadata
// context remains data-only.
type TrashLease struct {
	root      *RootLease
	rootID    domain.TrustedRootID
	placement TrashPlacement
	ownerUID  uint32
	metadata  TrashMetadataMapping
	topology  TrashTopology
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

// AnchorKind reports whether this lease contains engine/helper-attested FDO
// topology evidence. An empty value means the lease is intentionally limited
// to the legacy pre-selector metadata boundary.
func (lease *TrashLease) AnchorKind() TrashAnchorKind {
	if lease == nil {
		return ""
	}
	return lease.topology.AnchorKind
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

// MetadataReconciliationIdentity returns immutable correlation evidence for
// one complete topology-qualified Trash layout. It contains no path or
// descriptor and grants no authority to reopen, mutate, or probe the layout.
// Legacy pre-selector leases fail closed because they do not bind a literal
// Freedesktop topology suitable for metadata reconciliation.
func (lease *TrashLease) MetadataReconciliationIdentity() (domain.TrashLayoutBinding, error) {
	if lease == nil {
		return domain.TrashLayoutBinding{}, ErrLeaseClosed
	}

	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return domain.TrashLayoutBinding{}, ErrLeaseClosed
	}
	if !lease.topology.configured() {
		return domain.TrashLayoutBinding{}, fmt.Errorf("%w: metadata reconciliation requires a topology-qualified Trash lease", ErrUnsupported)
	}
	if err := lease.validateMetadataReconciliationIdentityLocked(); err != nil {
		return domain.TrashLayoutBinding{}, err
	}
	return domain.ComputeTrashLayoutBinding(encodeTrashLayoutBinding(lease)), nil
}

func (lease *TrashLease) validateMetadataReconciliationIdentityLocked() error {
	if lease.root == nil {
		return ErrLeaseClosed
	}
	if lease.root.RootID() != lease.rootID {
		return fmt.Errorf("%w: trusted Trash root identity changed", ErrDrifted)
	}
	if err := lease.rootID.Validate(); err != nil {
		return fmt.Errorf("%w: Trash layout binding root: %v", ErrInvalidAuthority, err)
	}
	if err := lease.placement.validate(); err != nil {
		return err
	}
	if err := lease.metadata.validate(lease.placement); err != nil {
		return err
	}
	if err := lease.expected.validate(lease.placement, lease.ownerUID); err != nil {
		return err
	}
	if err := lease.topology.validate(lease.placement); err != nil {
		return err
	}
	if err := validateTrashTopologyBinding(lease.topology, lease.expected); err != nil {
		return err
	}
	if err := lease.descriptors.validateTopology(lease.placement); err != nil {
		return err
	}
	return nil
}

// encodeTrashLayoutBinding is an explicit, tagged, length-prefixed encoding
// of static Trash authority facts. Fixed field order and length-delimited raw
// values make it unambiguous; only its digest leaves this package.
func encodeTrashLayoutBinding(lease *TrashLease) []byte {
	encoder := trashLayoutBindingEncoder{}
	encoder.text("version", "1")
	encoder.text("root", lease.rootID.String())
	encoder.text("placement", string(lease.placement))
	encoder.uint32("owner_uid", lease.ownerUID)
	encoder.text("metadata.basis", string(lease.metadata.Basis))
	encoder.bytePath("metadata.prefix", lease.metadata.Prefix)
	encoder.text("topology.anchor_kind", string(lease.topology.AnchorKind))
	encoder.expectation("topology.anchor", lease.topology.Anchor)
	encoder.expectation("roles.trash_root", lease.expected.TrashRoot)
	encoder.expectation("roles.files", lease.expected.Files)
	encoder.expectation("roles.info", lease.expected.Info)
	encoder.boolean("roles.has_shared_top", lease.expected.HasSharedTop)
	if lease.expected.HasSharedTop {
		encoder.expectation("roles.shared_top", lease.expected.SharedTop)
	}
	return encoder.bytes
}

type trashLayoutBindingEncoder struct {
	bytes []byte
}

func (encoder *trashLayoutBindingEncoder) field(name string, value []byte) {
	encoder.uint64Raw(uint64(len(name)))
	encoder.bytes = append(encoder.bytes, name...)
	encoder.uint64Raw(uint64(len(value)))
	encoder.bytes = append(encoder.bytes, value...)
}

func (encoder *trashLayoutBindingEncoder) text(name, value string) {
	encoder.field(name, []byte(value))
}

func (encoder *trashLayoutBindingEncoder) uint32(name string, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	encoder.field(name, encoded[:])
}

func (encoder *trashLayoutBindingEncoder) uint64(name string, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	encoder.field(name, encoded[:])
}

func (encoder *trashLayoutBindingEncoder) boolean(name string, value bool) {
	encoded := byte(0)
	if value {
		encoded = 1
	}
	encoder.field(name, []byte{encoded})
}

func (encoder *trashLayoutBindingEncoder) bytePath(name string, value pathbytes.BytePath) {
	components := value.Components()
	encoder.uint64(name+".component_count", uint64(len(components)))
	for _, component := range components {
		encoder.field(name+".component", component)
	}
}

func (encoder *trashLayoutBindingEncoder) expectation(name string, value LayoutExpectation) {
	encoder.uint64(name+".namespace.device", value.Namespace.Device)
	encoder.uint64(name+".namespace.inode", value.Namespace.Inode)
	encoder.uint32(name+".device.major", value.Device.Major)
	encoder.uint32(name+".device.minor", value.Device.Minor)
	encoder.uint64(name+".inode", value.Inode)
	encoder.uint32(name+".uid", value.UID)
	encoder.uint32(name+".gid", value.GID)
	encoder.uint32(name+".mode", value.Mode)
	encoder.uint64(name+".mount.id", value.Mount.ID)
	encoder.uint64(name+".mount.parent_id", value.Mount.ParentID)
	encoder.uint32(name+".mount.device.major", value.Mount.Device.Major)
	encoder.uint32(name+".mount.device.minor", value.Mount.Device.Minor)
	encoder.text(name+".mount.root", value.Mount.Root)
	encoder.text(name+".mount.point", value.Mount.MountPoint)
	encoder.text(name+".mount.filesystem", string(value.Mount.Filesystem))
	encoder.text(name+".mount.source", value.Mount.Source)
}

func (encoder *trashLayoutBindingEncoder) uint64Raw(value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	encoder.bytes = append(encoder.bytes, encoded[:]...)
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

// DuplicateTrashDescriptorPair gives linuxfs requalified CLOEXEC duplicates
// of the fixed files and info directories. Its capability-specific name lets
// architecture rules prohibit every other production package, including
// interface-mediated callers, from borrowing raw descriptors.
func (lease *TrashLease) DuplicateTrashDescriptorPair() (TrashDescriptorPair, error) {
	return lease.duplicateWith(InspectMount)
}

// DuplicateTrashTopologyDescriptorSet gives linuxfs requalified CLOEXEC
// duplicates of every descriptor needed to prove literal Freedesktop Trash
// topology. It is unavailable for a legacy pre-selector lease; callers must
// treat that as unsupported rather than attempting path-based discovery.
// Its capability-specific name also makes interface-mediated borrowing
// rejectable at the architecture boundary.
func (lease *TrashLease) DuplicateTrashTopologyDescriptorSet() (TrashTopologyDescriptorSet, error) {
	return lease.duplicateTopologyWith(InspectMount)
}

func (lease *TrashLease) duplicateTopologyWith(inspect rootInspector) (TrashTopologyDescriptorSet, error) {
	return lease.duplicateTopologyWithFcntl(inspect, func(fd int) (int, error) {
		return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
	})
}

func (lease *TrashLease) duplicateTopologyWithFcntl(inspect rootInspector, duplicate func(int) (int, error)) (TrashTopologyDescriptorSet, error) {
	set := EmptyTrashTopologyDescriptorSet()
	if lease == nil {
		return set, ErrLeaseClosed
	}
	if inspect == nil || duplicate == nil {
		return set, fmt.Errorf("%w: missing Trash topology requalification dependency", ErrInvalidAuthority)
	}

	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return set, ErrLeaseClosed
	}
	if !lease.topology.configured() {
		return set, fmt.Errorf("%w: trusted Trash lease has no topology anchor", ErrUnsupported)
	}
	if err := lease.requalifyLocked(inspect); err != nil {
		return set, err
	}

	roles := []struct {
		name string
		fd   int
		out  *int
	}{
		{name: "topology anchor", fd: lease.descriptors.Anchor, out: &set.AnchorFD},
		{name: "Trash root", fd: lease.descriptors.TrashRoot, out: &set.TrashRootFD},
		{name: "Trash files", fd: lease.descriptors.Files, out: &set.FilesFD},
		{name: "Trash info", fd: lease.descriptors.Info, out: &set.InfoFD},
	}
	if lease.placement == TrashPlacementTopShared {
		roles = append(roles, struct {
			name string
			fd   int
			out  *int
		}{name: "shared Trash parent", fd: lease.descriptors.SharedTop, out: &set.SharedTopFD})
	}
	for _, role := range roles {
		duplicated, err := duplicate(role.fd)
		if err != nil {
			lease.closeRejectedTopologyDuplicate(duplicated, set)
			_ = set.Close()
			return EmptyTrashTopologyDescriptorSet(), fmt.Errorf("duplicate trusted Trash %s directory: %w", role.name, err)
		}
		if err := lease.validateTopologyDuplicateDescriptor(duplicated, role.name, set); err != nil {
			lease.closeRejectedTopologyDuplicate(duplicated, set)
			_ = set.Close()
			return EmptyTrashTopologyDescriptorSet(), err
		}
		*role.out = duplicated
	}
	return set, nil
}

func (lease *TrashLease) validateTopologyDuplicateDescriptor(fd int, role string, set TrashTopologyDescriptorSet) error {
	if fd < 3 {
		return fmt.Errorf("%w: duplicate trusted Trash %s directory returned reserved descriptor %d", ErrInvalidAuthority, role, fd)
	}
	for _, held := range []int{lease.descriptors.Anchor, lease.descriptors.TrashRoot, lease.descriptors.Files, lease.descriptors.Info, lease.descriptors.SharedTop} {
		if fd == held {
			return fmt.Errorf("%w: duplicate trusted Trash %s directory aliases an authority descriptor", ErrInvalidAuthority, role)
		}
	}
	for _, duplicated := range []int{set.AnchorFD, set.TrashRootFD, set.FilesFD, set.InfoFD, set.SharedTopFD} {
		if fd == duplicated {
			return fmt.Errorf("%w: duplicate trusted Trash %s directory aliases a descriptor in the handoff", ErrInvalidAuthority, role)
		}
	}
	if err := validateLeasedDirectoryFD(fd, "duplicated Trash "+role); err != nil {
		return err
	}
	return nil
}

func (lease *TrashLease) closeRejectedTopologyDuplicate(fd int, set TrashTopologyDescriptorSet) {
	if fd < 3 {
		return
	}
	for _, held := range []int{lease.descriptors.Anchor, lease.descriptors.TrashRoot, lease.descriptors.Files, lease.descriptors.Info, lease.descriptors.SharedTop} {
		if fd == held {
			return
		}
	}
	for _, duplicated := range []int{set.AnchorFD, set.TrashRootFD, set.FilesFD, set.InfoFD, set.SharedTopFD} {
		if fd == duplicated {
			return
		}
	}
	_ = unix.Close(fd)
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
	if lease.topology.configured() {
		if err := lease.topology.validate(lease.placement); err != nil {
			return err
		}
		if err := validateTrashTopologyBinding(lease.topology, lease.expected); err != nil {
			return err
		}
		if err := lease.descriptors.validateTopology(lease.placement); err != nil {
			return err
		}
		if err := checkTrashDescriptor(lease.descriptors.Anchor, "Trash topology anchor", lease.topology.Anchor, rootInspection, inspect); err != nil {
			return err
		}
	} else if lease.descriptors.Anchor != -1 {
		return fmt.Errorf("%w: legacy Trash lease carries an unbound topology anchor", ErrInvalidAuthority)
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
	if authority.Topology.configured() {
		if err := descriptors.validateTopology(authority.Placement); err != nil {
			return nil, err
		}
		if err := checkTrashDescriptor(descriptors.Anchor, "Trash topology anchor", authority.Topology.Anchor, rootInspection, inspect); err != nil {
			return nil, err
		}
	} else if descriptors.Anchor != -1 {
		return nil, fmt.Errorf("%w: legacy Trash authority returned an unbound topology anchor", ErrInvalidAuthority)
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
		topology:    authority.Topology,
		expected:    authority.Expected,
		descriptors: descriptors,
	}
	keepDescriptors = true
	return lease, nil
}
