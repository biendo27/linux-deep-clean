//go:build linux

package linuxfs

import (
	"errors"
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

const privateDirectoryMask = baselineFilesystemMask

// privateDirectorySource is the narrowly scoped authority from which a
// private directory operation can obtain a newly requalified descriptor. The
// production implementation is mounts.LayoutLease; keeping this interface
// private allows unit tests to exercise descriptor failure paths without
// creating a second exported authority constructor.
type privateDirectorySource interface {
	RootID() domain.TrustedRootID
	Kind() mounts.LayoutKind
	Duplicate() (int, error)
}

var _ privateDirectorySource = (*mounts.LayoutLease)(nil)

// PrivateDirectoryLease owns an opaque, engine/helper-attested private
// directory authority. It obtains a fresh descriptor for each scoped
// operation, so the underlying layout and trusted root must requalify at the
// point of use. It is suitable for no-replace durable publication and retained
// staging only; it never grants irreversible-removal authority.
type PrivateDirectoryLease struct {
	source   privateDirectorySource
	rootID   domain.TrustedRootID
	kind     mounts.LayoutKind
	expected domain.FilesystemSnapshot

	mu     sync.Mutex
	closed bool
}

// OpenPrivateDirectory converts a qualified engine/helper layout lease into a
// stricter executor-private directory lease. It accepts no path or raw file
// descriptor from callers. Trash layouts have distinct Freedesktop semantics
// and deliberately do not use this exact-0700 private-directory contract.
func OpenPrivateDirectory(layout *mounts.LayoutLease) (*PrivateDirectoryLease, error) {
	return openPrivateDirectoryWithSource(layout)
}

func openPrivateDirectoryWithSource(source privateDirectorySource) (*PrivateDirectoryLease, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: trusted private layout lease is required", ErrUnsupported)
	}
	rootID := source.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: private layout root identity: %v", ErrUnsupported, err)
	}
	kind := source.Kind()
	if !isPrivateLayoutKind(kind) {
		return nil, fmt.Errorf("%w: layout kind %q is not an executor-private directory", ErrUnsupported, kind)
	}
	fd, err := source.Duplicate()
	if err != nil {
		return nil, classifyPrivateDirectorySourceFailure("duplicate trusted private layout", err)
	}
	if fd >= 0 {
		defer unix.Close(fd)
	}
	expected, err := snapshotPrivateDirectory(fd)
	if err != nil {
		return nil, err
	}
	return &PrivateDirectoryLease{source: source, rootID: rootID, kind: kind, expected: expected}, nil
}

func isPrivateLayoutKind(kind mounts.LayoutKind) bool {
	switch kind {
	case mounts.LayoutPrivateState, mounts.LayoutPrivateStaging, mounts.LayoutPrivateQuarantine:
		return true
	default:
		return false
	}
}

// RootID returns the source-root identity that the layout authority attested.
func (lease *PrivateDirectoryLease) RootID() domain.TrustedRootID {
	if lease == nil {
		return ""
	}
	return lease.rootID
}

// Kind reports the non-path engine/helper layout role for this directory.
func (lease *PrivateDirectoryLease) Kind() mounts.LayoutKind {
	if lease == nil {
		return ""
	}
	return lease.kind
}

// withFD holds the private directory while the caller performs one narrow
// descriptor-relative operation. It rechecks the directory's identity,
// ownership, and permissions first; no raw descriptor leaves linuxfs.
func (lease *PrivateDirectoryLease) withFD(operation func(int) error) error {
	if lease == nil || operation == nil {
		return fmt.Errorf("%w: qualified private directory operation is required", ErrUnsupported)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	fd, err := lease.duplicateLocked()
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return operation(fd)
}

// duplicate lends a checked, caller-owned descriptor only to another
// linuxfs primitive. It is intentionally package-private so layout authority
// cannot escape to packages that do not own raw filesystem operations.
func (lease *PrivateDirectoryLease) duplicate() (int, error) {
	if lease == nil {
		return -1, fmt.Errorf("%w: qualified private directory lease is required", ErrUnsupported)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.duplicateLocked()
}

// duplicateLocked performs the same point-of-use requalification used by
// withFD. The returned descriptor remains valid if a concurrent close revokes
// future lease uses, while every failed recheck closes its newly duplicated
// descriptor before returning.
func (lease *PrivateDirectoryLease) duplicateLocked() (int, error) {
	if lease.closed {
		return -1, fmt.Errorf("%w: private directory lease is closed", ErrDrifted)
	}
	if err := lease.recheckLocked(); err != nil {
		return -1, err
	}
	fd, err := lease.source.Duplicate()
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return -1, classifyPrivateDirectorySourceFailure("requalify trusted private layout", err)
	}
	if fd < 0 {
		return -1, fmt.Errorf("%w: trusted private layout returned an invalid descriptor", ErrDrifted)
	}
	keepFD := false
	defer func() {
		if !keepFD {
			_ = unix.Close(fd)
		}
	}()
	if err := lease.recheckFDLocked(fd); err != nil {
		return -1, err
	}
	keepFD = true
	return fd, nil
}

func (lease *PrivateDirectoryLease) recheckLocked() error {
	if lease.source == nil {
		return fmt.Errorf("%w: private directory authority is missing", ErrUnsupported)
	}
	if err := lease.rootID.Validate(); err != nil {
		return fmt.Errorf("%w: private directory root identity: %v", ErrUnsupported, err)
	}
	if !isPrivateLayoutKind(lease.kind) {
		return fmt.Errorf("%w: private directory layout kind %q", ErrUnsupported, lease.kind)
	}
	if lease.source.RootID() != lease.rootID {
		return fmt.Errorf("%w: private directory root identity changed", ErrDrifted)
	}
	if lease.source.Kind() != lease.kind {
		return fmt.Errorf("%w: private directory layout kind changed", ErrDrifted)
	}
	return nil
}

func (lease *PrivateDirectoryLease) recheckFDLocked(fd int) error {
	observed, err := snapshotPrivateDirectory(fd)
	if err != nil {
		return err
	}
	return comparePrivateDirectoryIdentity(lease.expected, observed)
}

func classifyPrivateDirectorySourceFailure(operation string, err error) error {
	switch {
	case errors.Is(err, mounts.ErrUnsupported),
		errors.Is(err, mounts.ErrInvalidAuthority),
		errors.Is(err, mounts.ErrUnknownLayout),
		errors.Is(err, mounts.ErrUnknownTrustedRoot):
		return fmt.Errorf("%w: %s: %v", ErrUnsupported, operation, err)
	default:
		return fmt.Errorf("%w: %s: %v", ErrDrifted, operation, err)
	}
}

func snapshotPrivateDirectory(fd int) (domain.FilesystemSnapshot, error) {
	snapshot, err := SnapshotFD(fd, privateDirectoryMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private layout descriptor is not a directory", ErrUnsupported)
	}
	if snapshot.UID.Value != uint32(unix.Geteuid()) {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private layout directory is not owned by the executor UID", ErrUnsupported)
	}
	if snapshot.Mode.Value&0o7777 != 0o700 {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private layout directory must have exact mode 0700", ErrUnsupported)
	}
	return snapshot, nil
}

func comparePrivateDirectoryIdentity(expected, observed domain.FilesystemSnapshot) error {
	if expected.DeviceMajor != observed.DeviceMajor || expected.DeviceMinor != observed.DeviceMinor {
		return driftedField("private directory device")
	}
	if expected.Inode != observed.Inode {
		return driftedField("private directory inode")
	}
	if expected.Type != observed.Type {
		return driftedField("private directory type")
	}
	if expected.UID != observed.UID || expected.GID != observed.GID {
		return driftedField("private directory ownership")
	}
	if expected.Mode != observed.Mode {
		return driftedField("private directory mode")
	}
	if expected.MountID != observed.MountID {
		return driftedField("private directory mount ID")
	}
	return nil
}

// Close prevents later scoped descriptor operations without mutating any
// retained or published entry. It is safe to call repeatedly.
func (lease *PrivateDirectoryLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	return nil
}
