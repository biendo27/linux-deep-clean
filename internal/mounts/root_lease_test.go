//go:build linux

package mounts

import (
	"errors"
	"fmt"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

func TestOpenTrustedRootRejectsUnknownRegistryID(t *testing.T) {
	rootID := testRootID(t)
	_, err := openTrustedRootWith(StaticRegistry{}, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrUnknownTrustedRoot) {
		t.Fatalf("openTrustedRootWith() error = %v, want ErrUnknownTrustedRoot", err)
	}
}

func TestOpenTrustedRootPublicEntryPointRejectsUnknownRegistryID(t *testing.T) {
	rootID := testRootID(t)
	_, err := OpenTrustedRoot(StaticRegistry{}, rootID)
	if !errors.Is(err, ErrUnknownTrustedRoot) {
		t.Fatalf("OpenTrustedRoot() error = %v, want ErrUnknownTrustedRoot", err)
	}
}

func TestOpenTrustedRootRejectsInvalidAuthority(t *testing.T) {
	rootID := testRootID(t)
	registry := StaticRegistry{rootID: {ID: rootID, Expected: testExpectation()}}
	_, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedRootWith() error = %v, want ErrInvalidAuthority", err)
	}

	_, err = openTrustedRootWith(nil, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedRootWith(nil registry) error = %v, want ErrInvalidAuthority", err)
	}

	_, err = openTrustedRootWith(StaticRegistry{}, "", testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedRootWith(invalid root ID) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestOpenTrustedRootFailsClosedBeforeOrDuringQualification(t *testing.T) {
	rootID := testRootID(t)
	directory := t.TempDir()
	valid := testAuthority(t, rootID, directory, testExpectation())
	unattestedLocalDevice := testExpectation()
	unattestedLocalDevice.FixedLocalDevice = false
	unattestedBindFree := testExpectation()
	unattestedBindFree.BindFreeAttested = false

	tests := map[string]struct {
		registry    Registry
		inspect     rootInspector
		checkKernel kernelChecker
		want        error
	}{
		"missing inspector": {
			registry: StaticRegistry{rootID: valid},
			want:     ErrInvalidAuthority,
		},
		"kernel unsupported": {
			registry:    StaticRegistry{rootID: valid},
			inspect:     testInspectionFor,
			checkKernel: func() error { return ErrUnsupported },
			want:        ErrUnsupported,
		},
		"opener error": {
			registry: StaticRegistry{rootID: {
				ID:       rootID,
				Open:     func() (int, error) { return -1, unix.EACCES },
				Expected: testExpectation(),
			}},
			inspect:     testInspectionFor,
			checkKernel: func() error { return nil },
			want:        unix.EACCES,
		},
		"negative descriptor": {
			registry: StaticRegistry{rootID: {
				ID:       rootID,
				Open:     func() (int, error) { return -1, nil },
				Expected: testExpectation(),
			}},
			inspect:     testInspectionFor,
			checkKernel: func() error { return nil },
			want:        ErrInvalidAuthority,
		},
		"descriptor without close on exec": {
			registry: StaticRegistry{rootID: {
				ID: rootID,
				Open: func() (int, error) {
					return unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY, 0)
				},
				Expected: testExpectation(),
			}},
			inspect:     testInspectionFor,
			checkKernel: func() error { return nil },
			want:        ErrInvalidAuthority,
		},
		"inspector drift": {
			registry: StaticRegistry{rootID: valid},
			inspect: func(int) (MountInspection, error) {
				return MountInspection{}, ErrDrifted
			},
			checkKernel: func() error { return nil },
			want:        ErrDrifted,
		},
		"unattested local device": {
			registry:    StaticRegistry{rootID: testAuthority(t, rootID, directory, unattestedLocalDevice)},
			inspect:     testInspectionFor,
			checkKernel: func() error { return nil },
			want:        ErrUnsupported,
		},
		"unattested bind-free provenance": {
			registry:    StaticRegistry{rootID: testAuthority(t, rootID, directory, unattestedBindFree)},
			inspect:     testInspectionFor,
			checkKernel: func() error { return nil },
			want:        ErrUnsupported,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := openTrustedRootWith(test.registry, rootID, test.inspect, test.checkKernel)
			if !errors.Is(err, test.want) {
				t.Fatalf("openTrustedRootWith() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOpenTrustedRootRejectsRootMismatch(t *testing.T) {
	rootID := testRootID(t)
	directory := t.TempDir()
	expectation := testExpectation()
	expectation.Inode++

	registry := StaticRegistry{
		rootID: testAuthority(t, rootID, directory, expectation),
	}

	_, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openTrustedRootWith() error = %v, want ErrDrifted", err)
	}
}

func TestOpenTrustedRootClosesLeaseAndTracksExplicitDuplicateOwnership(t *testing.T) {
	rootID := testRootID(t)
	directory := t.TempDir()
	var openedFD int = -1

	registry := StaticRegistry{
		rootID: {
			ID: rootID,
			Open: func() (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					openedFD = fd
				}
				return fd, err
			},
			Expected: testExpectation(),
		},
	}

	lease, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if err != nil {
		t.Fatalf("openTrustedRootWith() error = %v", err)
	}

	if err := lease.withFD(func(fd int) error {
		if fd != openedFD {
			return fmt.Errorf("borrowed FD = %d, opener FD = %d", fd, openedFD)
		}
		return nil
	}); err != nil {
		t.Fatalf("lease.withFD() error = %v", err)
	}
	if lease.RootID() != rootID {
		t.Fatalf("lease.RootID() = %q, want %q", lease.RootID(), rootID)
	}
	if err := lease.withFD(nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.withFD(nil) error = %v, want ErrInvalidAuthority", err)
	}
	duplicateFD, err := lease.DuplicateRootDescriptor()
	if err != nil {
		t.Fatalf("lease.DuplicateRootDescriptor() error = %v", err)
	}
	defer unix.Close(duplicateFD)
	if flags, err := unix.FcntlInt(uintptr(duplicateFD), unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("duplicate FD flags = (%#x, %v), want FD_CLOEXEC", flags, err)
	}

	if err := lease.Close(); err != nil {
		t.Fatalf("lease.Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second lease.Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(openedFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("opener FD after Close error = %v, want EBADF", err)
	}
	if _, err := unix.FcntlInt(uintptr(duplicateFD), unix.F_GETFD, 0); err != nil {
		t.Fatalf("explicit caller-owned duplicate after lease Close error = %v", err)
	}
	if _, err := lease.DuplicateRootDescriptor(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("lease.DuplicateRootDescriptor() after Close error = %v, want ErrLeaseClosed", err)
	}
	if err := lease.withFD(func(int) error { return nil }); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("lease.withFD() after Close error = %v, want ErrLeaseClosed", err)
	}
}

func TestOpenTrustedRootClosesFDWhenQualificationFails(t *testing.T) {
	rootID := testRootID(t)
	directory := t.TempDir()
	var openedFD int = -1
	expectation := testExpectation()
	expectation.UID++

	registry := StaticRegistry{
		rootID: {
			ID: rootID,
			Open: func() (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					openedFD = fd
				}
				return fd, err
			},
			Expected: expectation,
		},
	}

	_, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openTrustedRootWith() error = %v, want ErrDrifted", err)
	}
	if _, err := unix.FcntlInt(uintptr(openedFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("opened FD after qualification failure error = %v, want EBADF", err)
	}
}

func TestOpenTrustedRootRejectsNonDirectory(t *testing.T) {
	rootID := testRootID(t)
	file := t.TempDir() + "/not-a-directory"
	if err := unix.Close(mustOpenFile(t, file)); err != nil {
		t.Fatalf("close test file: %v", err)
	}

	registry := StaticRegistry{
		rootID: {
			ID: rootID,
			Open: func() (int, error) {
				return unix.Open(file, unix.O_RDONLY|unix.O_CLOEXEC, 0)
			},
			Expected: testExpectation(),
		},
	}

	_, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openTrustedRootWith() error = %v, want ErrDrifted", err)
	}
}

func TestOpenTrustedRootRejectsOPathDescriptor(t *testing.T) {
	rootID := testRootID(t)
	directory := t.TempDir()
	registry := StaticRegistry{
		rootID: {
			ID: rootID,
			Open: func() (int, error) {
				return unix.Open(directory, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			},
			Expected: testExpectation(),
		},
	}

	lease, err := openTrustedRootWith(registry, rootID, testInspectionFor, func() error { return nil })
	if lease != nil {
		defer lease.Close()
	}
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedRootWith(O_PATH descriptor) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestRootLeaseNilAndInvalidFDBehavior(t *testing.T) {
	var nilLease *RootLease
	if nilLease.RootID() != "" {
		t.Fatalf("nil lease RootID() = %q, want empty", nilLease.RootID())
	}
	if err := nilLease.Close(); err != nil {
		t.Fatalf("nil lease Close() error = %v", err)
	}
	if err := nilLease.withFD(func(int) error { return nil }); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil lease withFD() error = %v, want ErrLeaseClosed", err)
	}
	if _, err := nilLease.DuplicateRootDescriptor(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil lease DuplicateRootDescriptor() error = %v, want ErrLeaseClosed", err)
	}

	lease := &RootLease{rootID: testRootID(t), fd: -1}
	if err := lease.Close(); err != nil {
		t.Fatalf("invalid FD lease Close() error = %v", err)
	}
}

func TestRootAuthorityValidation(t *testing.T) {
	rootID := testRootID(t)
	valid := RootAuthority{ID: rootID, Open: func() (int, error) { return -1, nil }, Expected: testExpectation()}
	if err := valid.validate(rootID); err != nil {
		t.Fatalf("valid RootAuthority.validate() error = %v", err)
	}

	badNamespace := testExpectation()
	badNamespace.Namespace = MountNamespace{}
	missingInode := testExpectation()
	missingInode.Inode = 0
	missingMountID := testExpectation()
	missingMountID.Mount.ID = 0
	missingParentMountID := testExpectation()
	missingParentMountID.Mount.ParentID = 0
	inconsistentMountDevice := testExpectation()
	inconsistentMountDevice.Mount.Device.Minor++
	missingMountDevice := testExpectation()
	missingMountDevice.Mount.Device = DeviceIdentity{}
	unsupportedFilesystem := testExpectation()
	unsupportedFilesystem.Mount.Filesystem = FilesystemUnknown
	bindExpectedRoot := testExpectation()
	bindExpectedRoot.Mount.Root = "/subtree"
	missingMountPoint := testExpectation()
	missingMountPoint.Mount.MountPoint = ""
	missingMountSource := testExpectation()
	missingMountSource.Mount.Source = ""

	tests := map[string]RootAuthority{
		"wrong ID":                {ID: domain.TrustedRootID("other-root"), Open: valid.Open, Expected: testExpectation()},
		"bad namespace":           {ID: rootID, Open: valid.Open, Expected: badNamespace},
		"missing inode":           {ID: rootID, Open: valid.Open, Expected: missingInode},
		"missing mount ID":        {ID: rootID, Open: valid.Open, Expected: missingMountID},
		"missing parent mount ID": {ID: rootID, Open: valid.Open, Expected: missingParentMountID},
		"inconsistent mount device": {
			ID: rootID, Open: valid.Open, Expected: inconsistentMountDevice,
		},
		"missing mount device":   {ID: rootID, Open: valid.Open, Expected: missingMountDevice},
		"unsupported filesystem": {ID: rootID, Open: valid.Open, Expected: unsupportedFilesystem},
		"bind expected root":     {ID: rootID, Open: valid.Open, Expected: bindExpectedRoot},
		"missing mount point":    {ID: rootID, Open: valid.Open, Expected: missingMountPoint},
		"missing mount source":   {ID: rootID, Open: valid.Open, Expected: missingMountSource},
	}
	for name, authority := range tests {
		t.Run(name, func(t *testing.T) {
			if err := authority.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("RootAuthority.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
	if err := valid.validate(""); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("RootAuthority.validate(invalid request) error = %v, want ErrInvalidAuthority", err)
	}
	invalidID := valid
	invalidID.ID = ""
	if err := invalidID.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("RootAuthority.validate(invalid authority ID) error = %v, want ErrInvalidAuthority", err)
	}
	for name, mutate := range map[string]func(*RootExpectation){
		"missing fixed-local-device attestation": func(expectation *RootExpectation) {
			expectation.FixedLocalDevice = false
		},
		"missing bind-free attestation": func(expectation *RootExpectation) {
			expectation.BindFreeAttested = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			authority := valid
			mutate(&authority.Expected)
			if err := authority.validate(rootID); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("RootAuthority.validate() error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func testRootID(t *testing.T) domain.TrustedRootID {
	t.Helper()
	rootID, err := domain.NewTrustedRootID("test-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	return rootID
}

func testAuthority(t *testing.T, rootID domain.TrustedRootID, directory string, expected RootExpectation) RootAuthority {
	t.Helper()
	return RootAuthority{
		ID: rootID,
		Open: func() (int, error) {
			return unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		},
		Expected: expected,
	}
}

func testExpectation() RootExpectation {
	return RootExpectation{
		Namespace: MountNamespace{Device: 19, Inode: 23},
		Device:    DeviceIdentity{Major: 8, Minor: 2},
		Inode:     101,
		UID:       1000,
		GID:       1000,
		Mode:      0o700,
		Mount: MountRecord{
			ID:         42,
			ParentID:   1,
			Device:     DeviceIdentity{Major: 8, Minor: 2},
			Root:       "/",
			MountPoint: "/owned-test-root",
			Filesystem: FilesystemExt4,
			Source:     "/dev/fixed-test",
		},
		FixedLocalDevice: true,
		BindFreeAttested: true,
	}
}

func testInspection() MountInspection {
	return MountInspection{
		Namespace: MountNamespace{Device: 19, Inode: 23},
		Device:    DeviceIdentity{Major: 8, Minor: 2},
		Inode:     101,
		UID:       1000,
		GID:       1000,
		Mode:      0o700,
		Mount: MountRecord{
			ID:         42,
			ParentID:   1,
			Device:     DeviceIdentity{Major: 8, Minor: 2},
			Root:       "/",
			MountPoint: "/owned-test-root",
			Filesystem: FilesystemExt4,
			Source:     "/dev/fixed-test",
		},
		Filesystem: FilesystemFacts{
			Filesystem:       FilesystemExt4,
			MountFilesystem:  FilesystemExt4,
			MountRoot:        "/",
			FixedLocalDevice: true,
			RootDevice:       DeviceIdentity{Major: 8, Minor: 2},
			MountDevice:      DeviceIdentity{Major: 8, Minor: 2},
		},
	}
}

func testInspectionFor(int) (MountInspection, error) {
	return testInspection(), nil
}

func mustOpenFile(t *testing.T, name string) int {
	t.Helper()
	fd, err := unix.Open(name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("open test file %q: %v", name, err)
	}
	return fd
}
