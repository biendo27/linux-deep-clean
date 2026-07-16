//go:build linux

package mounts

import (
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

// RootLease owns one qualified trusted-root descriptor. Downstream rooted
// safety leases can explicitly obtain a CLOEXEC duplicate with Duplicate; that
// duplicate is caller-owned and must be closed by the downstream lease. The
// root lease itself never makes an untracked duplicate.
type RootLease struct {
	rootID domain.TrustedRootID

	mu     sync.Mutex
	fd     int
	closed bool
}

// RootID returns the registry identity that authorized this lease. It is not a
// path and cannot be used to derive a new root without another registry lookup.
func (lease *RootLease) RootID() domain.TrustedRootID {
	if lease == nil {
		return ""
	}
	return lease.rootID
}

// withFD is package-private so no external caller can close or replace the
// authority-bearing root descriptor. Cross-package consumers receive only an
// owned duplicate through the narrowly audited rooted-safety handoff.
func (lease *RootLease) withFD(operation func(int) error) error {
	if lease == nil {
		return ErrLeaseClosed
	}
	if operation == nil {
		return fmt.Errorf("%w: nil root-descriptor operation", ErrInvalidAuthority)
	}

	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.fd < 0 {
		return ErrLeaseClosed
	}
	return operation(lease.fd)
}

// Duplicate returns a CLOEXEC duplicate of the held root descriptor for a
// downstream rooted safety lease such as linuxfs.ParentLease. The caller owns
// the returned descriptor and must close it on every path. Duplicate refuses
// a closed lease, preventing new authority from being derived after Close.
func (lease *RootLease) Duplicate() (int, error) {
	if lease == nil {
		return -1, ErrLeaseClosed
	}

	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.fd < 0 {
		return -1, ErrLeaseClosed
	}
	fd, err := unix.FcntlInt(uintptr(lease.fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return -1, fmt.Errorf("duplicate trusted root %q: %w", lease.rootID, err)
	}
	return fd, nil
}

// Close is idempotent and closes the sole descriptor owned by the lease. A
// close error is returned once; later calls observe the closed lease and are
// successful no-ops so cleanup paths remain deterministic.
func (lease *RootLease) Close() error {
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
		return fmt.Errorf("close trusted root %q: %w", lease.rootID, err)
	}
	return nil
}

type rootInspector func(int) (MountInspection, error)
type kernelChecker func() error

// OpenTrustedRoot reopens a root solely through its engine/helper registry
// authority, validates live descriptor/mount evidence, and returns the held FD
// lease. It accepts no filesystem path from an apply request.
func OpenTrustedRoot(registry Registry, rootID domain.TrustedRootID) (*RootLease, error) {
	return openTrustedRootWith(registry, rootID, InspectMount, checkMinimumKernel)
}

func openTrustedRootWith(registry Registry, rootID domain.TrustedRootID, inspect rootInspector, checkKernel kernelChecker) (*RootLease, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: nil trusted-root registry", ErrInvalidAuthority)
	}
	if inspect == nil || checkKernel == nil {
		return nil, fmt.Errorf("%w: missing root qualification dependency", ErrInvalidAuthority)
	}
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: requested root ID: %v", ErrInvalidAuthority, err)
	}
	authority, found := registry.Lookup(rootID)
	if !found {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTrustedRoot, rootID)
	}
	if err := authority.validate(rootID); err != nil {
		return nil, err
	}
	if err := checkKernel(); err != nil {
		return nil, err
	}

	fd, err := authority.Open()
	if err != nil {
		return nil, fmt.Errorf("open trusted root %q: %w", rootID, err)
	}
	if fd < 0 {
		return nil, fmt.Errorf("%w: trusted root %q opener returned negative descriptor", ErrInvalidAuthority, rootID)
	}
	keepFD := false
	defer func() {
		if !keepFD {
			_ = unix.Close(fd)
		}
	}()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect trusted root %q descriptor flags: %v", ErrDrifted, rootID, err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return nil, fmt.Errorf("%w: trusted root %q opener returned a descriptor without FD_CLOEXEC", ErrInvalidAuthority, rootID)
	}
	statusFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect trusted root %q status flags: %v", ErrDrifted, rootID, err)
	}
	if statusFlags&unix.O_PATH != 0 {
		return nil, fmt.Errorf("%w: trusted root %q opener returned an O_PATH descriptor", ErrInvalidAuthority, rootID)
	}
	if statusFlags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, fmt.Errorf("%w: trusted root %q opener must return an O_RDONLY descriptor", ErrInvalidAuthority, rootID)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("%w: stat trusted root %q: %v", ErrDrifted, rootID, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("%w: trusted root %q opener did not return a directory", ErrDrifted, rootID)
	}

	inspection, err := inspect(fd)
	if err != nil {
		return nil, err
	}
	if err := checkRootEvidence(authority.Expected, inspection); err != nil {
		return nil, err
	}
	filesystem := inspection.Filesystem
	filesystem.FixedLocalDevice = authority.Expected.FixedLocalDevice
	if err := CheckSupportedFilesystem(filesystem); err != nil {
		return nil, err
	}

	lease := &RootLease{rootID: rootID, fd: fd}
	keepFD = true
	return lease, nil
}
