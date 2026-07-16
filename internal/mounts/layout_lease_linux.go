//go:build linux

package mounts

import (
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

// LayoutKind is a closed set of engine/helper-owned directory roles. The
// source root plus this kind is the stable, non-path mapping used to find a
// recovery layout again; callers cannot provide a layout path or UID.
type LayoutKind string

const (
	LayoutPrivateState      LayoutKind = "private_state"
	LayoutPrivateStaging    LayoutKind = "private_staging"
	LayoutPrivateQuarantine LayoutKind = "private_quarantine"
	LayoutTrash             LayoutKind = "trash"
)

func (kind LayoutKind) validate() error {
	switch kind {
	case LayoutPrivateState, LayoutPrivateStaging, LayoutPrivateQuarantine, LayoutTrash:
		return nil
	default:
		return fmt.Errorf("%w: unknown layout kind %q", ErrInvalidAuthority, kind)
	}
}

// LayoutExpectation is exact descriptor and mount evidence for one
// engine/helper-owned directory. Mount-point and source values in its mount
// record are drift evidence only; neither is reopened or used as apply-time
// path authority. Its mount record must be the same full-root mount record as
// the trusted source root.
type LayoutExpectation struct {
	Namespace MountNamespace
	Device    DeviceIdentity
	Inode     uint64
	UID       uint32
	GID       uint32
	Mode      uint32
	Mount     MountRecord
}

func (expectation LayoutExpectation) validate() error {
	if err := expectation.Namespace.validate(); err != nil {
		return err
	}
	if expectation.Device == (DeviceIdentity{}) {
		return fmt.Errorf("%w: layout device is required", ErrInvalidAuthority)
	}
	if expectation.Inode == 0 {
		return fmt.Errorf("%w: layout inode is required", ErrInvalidAuthority)
	}
	if err := validateTrustedRootMountRecord(expectation.Mount); err != nil {
		return err
	}
	if expectation.Device != expectation.Mount.Device {
		return fmt.Errorf("%w: layout statx device %d:%d conflicts with mountinfo device %d:%d", ErrInvalidAuthority, expectation.Device.Major, expectation.Device.Minor, expectation.Mount.Device.Major, expectation.Mount.Device.Minor)
	}
	return nil
}

// LayoutAuthority is configuration owned by the engine/helper. Its opener is
// the sole source of a descriptor for the fixed layout directory; it accepts
// no caller path, caller UID, or environment-derived location.
type LayoutAuthority struct {
	Root     domain.TrustedRootID
	Kind     LayoutKind
	Open     RootOpener
	Expected LayoutExpectation
}

func (authority LayoutAuthority) validate(requestedRoot domain.TrustedRootID, requestedKind LayoutKind) error {
	if err := requestedRoot.Validate(); err != nil {
		return fmt.Errorf("%w: requested layout root: %v", ErrInvalidAuthority, err)
	}
	if err := requestedKind.validate(); err != nil {
		return err
	}
	if err := authority.Root.Validate(); err != nil {
		return fmt.Errorf("%w: layout authority root: %v", ErrInvalidAuthority, err)
	}
	if authority.Root != requestedRoot {
		return fmt.Errorf("%w: layout root %q does not match requested root %q", ErrInvalidAuthority, authority.Root, requestedRoot)
	}
	if err := authority.Kind.validate(); err != nil {
		return err
	}
	if authority.Kind != requestedKind {
		return fmt.Errorf("%w: layout kind %q does not match requested kind %q", ErrInvalidAuthority, authority.Kind, requestedKind)
	}
	if authority.Open == nil {
		return fmt.Errorf("%w: layout %q has no opener", ErrInvalidAuthority, authority.Kind)
	}
	return authority.Expected.validate()
}

// LayoutRegistry resolves exactly one layout kind for a trusted source root.
// The association is deliberately external to plan/request data so a recovery
// handle's root and kind cannot be redirected to a caller-selected directory.
type LayoutRegistry interface {
	LookupLayout(domain.TrustedRootID, LayoutKind) (LayoutAuthority, bool)
}

// StaticLayoutRegistry is an engine/helper-friendly layout registry. Lookup
// returns a value copy; callers cannot mutate its selected entry in place.
type StaticLayoutRegistry map[domain.TrustedRootID]map[LayoutKind]LayoutAuthority

// LookupLayout implements LayoutRegistry.
func (registry StaticLayoutRegistry) LookupLayout(root domain.TrustedRootID, kind LayoutKind) (LayoutAuthority, bool) {
	byKind, found := registry[root]
	if !found {
		return LayoutAuthority{}, false
	}
	authority, found := byKind[kind]
	return authority, found
}

// LayoutLease owns a qualified layout directory. It only lends a CLOEXEC
// duplicate to the rooted linuxfs safety package; no layout pathname or raw
// descriptor is exposed to providers, plans, or presenters.
type LayoutLease struct {
	root     *RootLease
	rootID   domain.TrustedRootID
	kind     LayoutKind
	expected LayoutExpectation

	mu     sync.Mutex
	fd     int
	closed bool
}

// RootID returns the trusted source-root mapping that selected this layout.
func (lease *LayoutLease) RootID() domain.TrustedRootID {
	if lease == nil {
		return ""
	}
	return lease.rootID
}

// Kind returns the fixed engine/helper-owned role of this layout.
func (lease *LayoutLease) Kind() LayoutKind {
	if lease == nil {
		return ""
	}
	return lease.kind
}

// Duplicate gives the descriptor-rooted linuxfs layer one owned CLOEXEC
// duplicate after the trusted root and layout both requalify. Architecture
// rules prohibit other packages from calling it.
func (lease *LayoutLease) Duplicate() (int, error) {
	return lease.duplicateWith(InspectMount)
}

// duplicateWith keeps root/layout requalification testable without exposing a
// second production authority path. It is deliberately package-private: the
// only cross-package caller remains the audited linuxfs safety layer.
func (lease *LayoutLease) duplicateWith(inspect rootInspector) (int, error) {
	if lease == nil {
		return -1, ErrLeaseClosed
	}
	if inspect == nil {
		return -1, fmt.Errorf("%w: missing layout requalification inspector", ErrInvalidAuthority)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.fd < 0 {
		return -1, ErrLeaseClosed
	}
	if lease.root == nil {
		return -1, ErrLeaseClosed
	}
	if lease.root.RootID() != lease.rootID {
		return -1, fmt.Errorf("%w: trusted layout root identity changed", ErrDrifted)
	}
	if err := validateLeasedDirectoryFD(lease.fd, "trusted layout"); err != nil {
		return -1, err
	}
	rootInspection, err := lease.root.requalifyWith(inspect)
	if err != nil {
		return -1, err
	}
	layoutInspection, err := inspect(lease.fd)
	if err != nil {
		return -1, err
	}
	if err := checkLayoutEvidence(lease.expected, layoutInspection); err != nil {
		return -1, err
	}
	if err := checkLayoutRootBinding(rootInspection, layoutInspection); err != nil {
		return -1, err
	}
	fd, err := unix.FcntlInt(uintptr(lease.fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return -1, fmt.Errorf("duplicate trusted layout %q for root %q: %w", lease.kind, lease.rootID, err)
	}
	return fd, nil
}

// Close releases the one descriptor owned by this lease. It is idempotent so
// every error path can defer it safely.
func (lease *LayoutLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	fd := lease.fd
	lease.fd = -1
	if fd < 0 {
		return nil
	}
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("close trusted layout %q for root %q: %w", lease.kind, lease.rootID, err)
	}
	return nil
}

// OpenTrustedLayout opens the fixed layout selected by the engine/helper for
// the held trusted root and requalifies both descriptors before issuing a
// lease. It has no pathname fallback and rejects a layout on another mount.
func OpenTrustedLayout(registry LayoutRegistry, root *RootLease, kind LayoutKind) (*LayoutLease, error) {
	return openTrustedLayoutWith(registry, root, kind, InspectMount, checkMinimumKernel)
}

func openTrustedLayoutWith(registry LayoutRegistry, root *RootLease, kind LayoutKind, inspect rootInspector, checkKernel kernelChecker) (*LayoutLease, error) {
	if registry == nil || inspect == nil || checkKernel == nil {
		return nil, fmt.Errorf("%w: missing trusted layout dependency", ErrInvalidAuthority)
	}
	if root == nil {
		return nil, ErrLeaseClosed
	}
	rootID := root.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: layout root identity: %v", ErrInvalidAuthority, err)
	}
	if err := kind.validate(); err != nil {
		return nil, err
	}
	authority, found := registry.LookupLayout(rootID, kind)
	if !found {
		return nil, fmt.Errorf("%w: root %q layout %q", ErrUnknownLayout, rootID, kind)
	}
	if err := authority.validate(rootID, kind); err != nil {
		return nil, err
	}
	if err := checkKernel(); err != nil {
		return nil, err
	}

	rootInspection, err := root.requalifyWith(inspect)
	if err != nil {
		return nil, err
	}
	fd, err := authority.Open()
	if err != nil {
		return nil, fmt.Errorf("open trusted layout %q for root %q: %w", kind, rootID, err)
	}
	if fd < 0 {
		return nil, fmt.Errorf("%w: trusted layout %q for root %q opener returned negative descriptor", ErrInvalidAuthority, kind, rootID)
	}
	keepFD := false
	defer func() {
		if !keepFD {
			_ = unix.Close(fd)
		}
	}()
	if err := validateLeasedDirectoryFD(fd, "trusted layout"); err != nil {
		return nil, err
	}
	layoutInspection, err := inspect(fd)
	if err != nil {
		return nil, err
	}
	if err := checkLayoutEvidence(authority.Expected, layoutInspection); err != nil {
		return nil, err
	}
	if err := checkLayoutRootBinding(rootInspection, layoutInspection); err != nil {
		return nil, err
	}

	lease := &LayoutLease{root: root, rootID: rootID, kind: kind, expected: authority.Expected, fd: fd}
	keepFD = true
	return lease, nil
}

// CaptureLayoutExpectation captures engine/helper registration facts from a
// held layout descriptor only after it is proven to be on the root's currently
// qualified mount. It intentionally records no path and cannot discover one.
func CaptureLayoutExpectation(root *RootLease, fd int) (LayoutExpectation, error) {
	return captureLayoutExpectationWith(root, fd, InspectMount)
}

func captureLayoutExpectationWith(root *RootLease, fd int, inspect rootInspector) (LayoutExpectation, error) {
	if fd < 0 {
		return LayoutExpectation{}, fmt.Errorf("%w: negative layout descriptor", ErrInvalidAuthority)
	}
	if inspect == nil {
		return LayoutExpectation{}, fmt.Errorf("%w: missing layout inspector", ErrInvalidAuthority)
	}
	if root == nil {
		return LayoutExpectation{}, ErrLeaseClosed
	}
	rootInspection, err := root.requalifyWith(inspect)
	if err != nil {
		return LayoutExpectation{}, err
	}
	if err := validateLeasedDirectoryFD(fd, "trusted layout capture"); err != nil {
		return LayoutExpectation{}, err
	}
	layoutInspection, err := inspect(fd)
	if err != nil {
		return LayoutExpectation{}, err
	}
	if err := checkLayoutRootBinding(rootInspection, layoutInspection); err != nil {
		return LayoutExpectation{}, err
	}
	expectation := LayoutExpectation{
		Namespace: layoutInspection.Namespace,
		Device:    layoutInspection.Device,
		Inode:     layoutInspection.Inode,
		UID:       layoutInspection.UID,
		GID:       layoutInspection.GID,
		Mode:      layoutInspection.Mode,
		Mount:     layoutInspection.Mount,
	}
	if err := expectation.validate(); err != nil {
		return LayoutExpectation{}, err
	}
	return expectation, nil
}

func (lease *RootLease) requalifyWith(inspect rootInspector) (MountInspection, error) {
	if lease == nil {
		return MountInspection{}, ErrLeaseClosed
	}
	if inspect == nil {
		return MountInspection{}, fmt.Errorf("%w: missing root requalification inspector", ErrInvalidAuthority)
	}
	if err := lease.expected.validate(); err != nil {
		return MountInspection{}, err
	}
	var inspection MountInspection
	err := lease.withFD(func(fd int) error {
		current, err := inspect(fd)
		if err != nil {
			return err
		}
		if err := checkRootEvidence(lease.expected, current); err != nil {
			return err
		}
		facts := current.Filesystem
		facts.FixedLocalDevice = lease.expected.FixedLocalDevice
		if err := CheckSupportedFilesystem(facts); err != nil {
			return err
		}
		inspection = current
		return nil
	})
	if err != nil {
		return MountInspection{}, err
	}
	return inspection, nil
}

func validateLeasedDirectoryFD(fd int, label string) error {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return fmt.Errorf("%w: inspect %s descriptor flags: %v", ErrDrifted, label, err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return fmt.Errorf("%w: %s descriptor lacks FD_CLOEXEC", ErrInvalidAuthority, label)
	}
	statusFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return fmt.Errorf("%w: inspect %s descriptor status flags: %v", ErrDrifted, label, err)
	}
	if statusFlags&unix.O_PATH != 0 {
		return fmt.Errorf("%w: %s descriptor is O_PATH", ErrInvalidAuthority, label)
	}
	if statusFlags&unix.O_ACCMODE != unix.O_RDONLY {
		return fmt.Errorf("%w: %s descriptor must be O_RDONLY", ErrInvalidAuthority, label)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("%w: stat %s descriptor: %v", ErrDrifted, label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: %s descriptor is not a directory", ErrDrifted, label)
	}
	return nil
}

func checkLayoutEvidence(expected LayoutExpectation, actual MountInspection) error {
	if err := expected.validate(); err != nil {
		return err
	}
	if err := CheckMountNamespace(expected.Namespace, actual.Namespace); err != nil {
		return err
	}
	if expected.Device != actual.Device {
		return fmt.Errorf("%w: layout device changed from %d:%d to %d:%d", ErrDrifted, expected.Device.Major, expected.Device.Minor, actual.Device.Major, actual.Device.Minor)
	}
	if expected.Inode != actual.Inode {
		return fmt.Errorf("%w: layout inode changed from %d to %d", ErrDrifted, expected.Inode, actual.Inode)
	}
	if expected.UID != actual.UID || expected.GID != actual.GID {
		return fmt.Errorf("%w: layout ownership changed from %d:%d to %d:%d", ErrDrifted, expected.UID, expected.GID, actual.UID, actual.GID)
	}
	if expected.Mode != actual.Mode {
		return fmt.Errorf("%w: layout mode changed from %#o to %#o", ErrDrifted, expected.Mode, actual.Mode)
	}
	if !sameMountRecord(expected.Mount, actual.Mount) {
		return fmt.Errorf("%w: layout mount record changed", ErrDrifted)
	}
	if actual.Filesystem.ReadOnly {
		return fmt.Errorf("%w: layout filesystem is read-only", ErrUnsupported)
	}
	if actual.Device != actual.Mount.Device || actual.Device != actual.Filesystem.RootDevice || actual.Mount.Device != actual.Filesystem.MountDevice {
		return fmt.Errorf("%w: layout device evidence is internally inconsistent", ErrDrifted)
	}
	return nil
}

func checkLayoutRootBinding(root, layout MountInspection) error {
	if err := CheckMountNamespace(root.Namespace, layout.Namespace); err != nil {
		return fmt.Errorf("layout namespace does not match trusted root: %w", err)
	}
	if root.Device != layout.Device || !sameMountRecord(root.Mount, layout.Mount) {
		return fmt.Errorf("%w: layout is not on the trusted root's exact mount", ErrUnsupported)
	}
	if root.Filesystem.Filesystem != layout.Filesystem.Filesystem || root.Filesystem.MountFilesystem != layout.Filesystem.MountFilesystem || root.Filesystem.MountRoot != layout.Filesystem.MountRoot {
		return fmt.Errorf("%w: layout filesystem facts do not match the trusted root", ErrUnsupported)
	}
	return nil
}

func sameMountRecord(left, right MountRecord) bool {
	return left.ID == right.ID &&
		left.ParentID == right.ParentID &&
		left.Device == right.Device &&
		left.Root == right.Root &&
		left.MountPoint == right.MountPoint &&
		left.Filesystem == right.Filesystem &&
		left.Source == right.Source
}
