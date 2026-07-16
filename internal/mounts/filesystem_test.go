//go:build linux

package mounts

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCheckSupportedFilesystem(t *testing.T) {
	valid := FilesystemFacts{
		Filesystem:       FilesystemExt4,
		MountFilesystem:  FilesystemExt4,
		ReadOnly:         false,
		MountRoot:        "/",
		FixedLocalDevice: true,
		RootDevice:       DeviceIdentity{Major: 8, Minor: 2},
		MountDevice:      DeviceIdentity{Major: 8, Minor: 2},
	}

	if err := CheckSupportedFilesystem(valid); err != nil {
		t.Fatalf("CheckSupportedFilesystem(valid) error = %v", err)
	}

	tests := map[string]FilesystemFacts{
		"unknown filesystem": {
			Filesystem:       FilesystemUnknown,
			MountFilesystem:  FilesystemUnknown,
			MountRoot:        "/",
			FixedLocalDevice: true,
			RootDevice:       valid.RootDevice,
			MountDevice:      valid.MountDevice,
		},
		"mountinfo mismatch": {
			Filesystem:       FilesystemExt4,
			MountFilesystem:  FilesystemXFS,
			MountRoot:        "/",
			FixedLocalDevice: true,
			RootDevice:       valid.RootDevice,
			MountDevice:      valid.MountDevice,
		},
		"read only": {
			Filesystem:       FilesystemExt4,
			MountFilesystem:  FilesystemExt4,
			ReadOnly:         true,
			MountRoot:        "/",
			FixedLocalDevice: true,
			RootDevice:       valid.RootDevice,
			MountDevice:      valid.MountDevice,
		},
		"bind root": {
			Filesystem:       FilesystemExt4,
			MountFilesystem:  FilesystemExt4,
			MountRoot:        "/subtree",
			FixedLocalDevice: true,
			RootDevice:       valid.RootDevice,
			MountDevice:      valid.MountDevice,
		},
		"missing fixed local attestation": {
			Filesystem:      FilesystemExt4,
			MountFilesystem: FilesystemExt4,
			MountRoot:       "/",
			RootDevice:      valid.RootDevice,
			MountDevice:     valid.MountDevice,
		},
		"mount device mismatch": {
			Filesystem:       FilesystemExt4,
			MountFilesystem:  FilesystemExt4,
			MountRoot:        "/",
			FixedLocalDevice: true,
			RootDevice:       valid.RootDevice,
			MountDevice:      DeviceIdentity{Major: 8, Minor: 3},
		},
	}

	for name, facts := range tests {
		t.Run(name, func(t *testing.T) {
			err := CheckSupportedFilesystem(facts)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("CheckSupportedFilesystem() error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestCheckMountNamespace(t *testing.T) {
	expected := MountNamespace{Device: 19, Inode: 23}
	if err := CheckMountNamespace(expected, expected); err != nil {
		t.Fatalf("CheckMountNamespace(equal) error = %v", err)
	}

	if err := CheckMountNamespace(expected, MountNamespace{Device: 19, Inode: 24}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("CheckMountNamespace(different) error = %v, want ErrDrifted", err)
	}
}

func TestCheckMountNamespaceRejectsIncompleteEvidence(t *testing.T) {
	if err := CheckMountNamespace(MountNamespace{}, MountNamespace{}); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("CheckMountNamespace(empty expected) error = %v, want ErrInvalidAuthority", err)
	}
	if err := CheckMountNamespace(MountNamespace{Device: 1, Inode: 2}, MountNamespace{}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("CheckMountNamespace(empty observed) error = %v, want ErrDrifted", err)
	}
}

func TestMountIDAloneNeverAuthorizes(t *testing.T) {
	expected := testExpectation()
	actual := testInspection()
	actual.Inode++

	if err := checkRootEvidence(expected, actual); !errors.Is(err, ErrDrifted) {
		t.Fatalf("checkRootEvidence() error = %v, want ErrDrifted", err)
	}
}

func TestCheckRootEvidenceRejectsEveryNonMountIDMismatch(t *testing.T) {
	expected := testExpectation()

	tests := map[string]func(*MountInspection){
		"namespace":                func(actual *MountInspection) { actual.Namespace.Inode++ },
		"device":                   func(actual *MountInspection) { actual.Device.Minor++ },
		"ownership":                func(actual *MountInspection) { actual.GID++ },
		"mode":                     func(actual *MountInspection) { actual.Mode ^= 0o100 },
		"descriptor filesystem":    func(actual *MountInspection) { actual.Filesystem.Filesystem = FilesystemXFS },
		"mount ID":                 func(actual *MountInspection) { actual.Mount.ID++ },
		"parent mount ID":          func(actual *MountInspection) { actual.Mount.ParentID++ },
		"mount device":             func(actual *MountInspection) { actual.Mount.Device.Minor++ },
		"mount root":               func(actual *MountInspection) { actual.Mount.Root = "/subtree" },
		"mount point":              func(actual *MountInspection) { actual.Mount.MountPoint = "/moved-root" },
		"mount filesystem":         func(actual *MountInspection) { actual.Mount.Filesystem = FilesystemXFS },
		"mount source":             func(actual *MountInspection) { actual.Mount.Source = "/dev/other-root" },
		"inconsistent live device": func(actual *MountInspection) { actual.Mount.Device.Minor++ },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			actual := testInspection()
			mutate(&actual)
			if err := checkRootEvidence(expected, actual); !errors.Is(err, ErrDrifted) {
				t.Fatalf("checkRootEvidence() error = %v, want ErrDrifted", err)
			}
		})
	}
}

func TestCheckMountTopologyRejectsAmbiguousFullRootDuplicates(t *testing.T) {
	target := testInspection().Mount
	duplicate := target
	duplicate.ID++
	duplicate.MountPoint = "/another-view"
	if err := checkMountTopology([]MountRecord{target, duplicate}, target); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("checkMountTopology(duplicate full root) error = %v, want ErrUnsupported", err)
	}

	variants := map[string]MountRecord{}
	differentDevice := target
	differentDevice.ID++
	differentDevice.Device.Minor++
	variants["different device"] = differentDevice
	differentFilesystem := target
	differentFilesystem.ID++
	differentFilesystem.Filesystem = FilesystemXFS
	variants["different filesystem"] = differentFilesystem
	differentRoot := target
	differentRoot.ID++
	differentRoot.Root = "/subtree"
	variants["different root"] = differentRoot

	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			if err := checkMountTopology([]MountRecord{target, variant}, target); err != nil {
				t.Fatalf("checkMountTopology(similar topology) error = %v, want nil", err)
			}
		})
	}
}

func TestInspectMountWithRejectsUnstableMountSnapshot(t *testing.T) {
	stableRecord := testInspection().Mount
	stableNamespace := testExpectation().Namespace

	t.Run("descriptor changes", func(t *testing.T) {
		calls := 0
		_, err := inspectMountWith(42, func(int) (descriptorMountEvidence, error) {
			calls++
			evidence := testDescriptorEvidence()
			if calls%2 == 0 {
				evidence.Inode++
			}
			return evidence, nil
		}, func() ([]MountRecord, error) {
			return []MountRecord{stableRecord}, nil
		}, func() (MountNamespace, error) {
			return stableNamespace, nil
		})
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("inspectMountWith(unstable descriptor) error = %v, want ErrDrifted", err)
		}
		if calls != mountInspectionAttemptLimit*2 {
			t.Fatalf("descriptor inspections = %d, want %d bounded samples", calls, mountInspectionAttemptLimit*2)
		}
	})

	t.Run("mount namespace changes", func(t *testing.T) {
		calls := 0
		_, err := inspectMountWith(42, func(int) (descriptorMountEvidence, error) {
			return testDescriptorEvidence(), nil
		}, func() ([]MountRecord, error) {
			return []MountRecord{stableRecord}, nil
		}, func() (MountNamespace, error) {
			calls++
			namespace := stableNamespace
			if calls%2 == 0 {
				namespace.Inode++
			}
			return namespace, nil
		})
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("inspectMountWith(unstable namespace) error = %v, want ErrDrifted", err)
		}
		if calls != mountInspectionAttemptLimit*2 {
			t.Fatalf("namespace inspections = %d, want %d bounded samples", calls, mountInspectionAttemptLimit*2)
		}
	})
}

func TestFilesystemHelpers(t *testing.T) {
	tests := []struct {
		magic uint64
		want  FilesystemType
	}{
		{magic: unix.EXT4_SUPER_MAGIC, want: FilesystemExt4},
		{magic: unix.XFS_SUPER_MAGIC, want: FilesystemXFS},
		{magic: unix.BTRFS_SUPER_MAGIC, want: FilesystemBtrfs},
		{magic: 0xdecafbad, want: FilesystemUnknown},
	}
	for _, test := range tests {
		if actual := filesystemFromMagic(int64(test.magic)); actual != test.want {
			t.Fatalf("filesystemFromMagic(%#x) = %q, want %q", test.magic, actual, test.want)
		}
	}

	if err := syscallQualificationError("test", unix.ENOSYS); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("syscallQualificationError(ENOSYS) = %v, want ErrUnsupported", err)
	}
	if err := syscallQualificationError("test", unix.EBADF); !errors.Is(err, ErrDrifted) {
		t.Fatalf("syscallQualificationError(EBADF) = %v, want ErrDrifted", err)
	}
}

func TestKernelVersionPolicy(t *testing.T) {
	tests := []struct {
		release string
		wantOK  bool
	}{
		{release: "5.15.0", wantOK: true},
		{release: "6.12.3-custom", wantOK: true},
		{release: "5.14.99", wantOK: false},
		{release: "not-a-version", wantOK: false},
		{release: "5", wantOK: false},
		{release: "5.not-a-number", wantOK: false},
	}
	for _, test := range tests {
		err := checkMinimumKernelRelease(test.release)
		if test.wantOK && err != nil {
			t.Fatalf("checkMinimumKernelRelease(%q) error = %v", test.release, err)
		}
		if !test.wantOK && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("checkMinimumKernelRelease(%q) error = %v, want ErrUnsupported", test.release, err)
		}
	}
}

func TestLiveKernelAndNamespaceInspection(t *testing.T) {
	if err := checkMinimumKernel(); err != nil {
		t.Fatalf("checkMinimumKernel() error = %v", err)
	}
	namespace, err := currentMountNamespace()
	if err != nil {
		t.Fatalf("currentMountNamespace() error = %v", err)
	}
	if namespace.Device == 0 || namespace.Inode == 0 {
		t.Fatalf("currentMountNamespace() = %#v, want complete identity", namespace)
	}
}

func TestCaptureRootExpectationBuildsOnlyQualifiedEvidence(t *testing.T) {
	expectation, err := captureRootExpectationWith(42, true, true, testInspectionFor, func() error { return nil })
	if err != nil {
		t.Fatalf("captureRootExpectationWith() error = %v", err)
	}
	if expectation != testExpectation() {
		t.Fatalf("captureRootExpectationWith() = %#v, want %#v", expectation, testExpectation())
	}

	_, err = captureRootExpectationWith(42, false, true, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("captureRootExpectationWith(no fixed-local-device attestation) error = %v, want ErrUnsupported", err)
	}
	_, err = captureRootExpectationWith(42, true, false, testInspectionFor, func() error { return nil })
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("captureRootExpectationWith(no bind-free attestation) error = %v, want ErrUnsupported", err)
	}
	_, err = captureRootExpectationWith(42, true, true, nil, func() error { return nil })
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("captureRootExpectationWith(nil inspector) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestInspectMountUsesOnlyOwnedTemporaryRoot(t *testing.T) {
	directory := t.TempDir()
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open owned temporary root: %v", err)
	}
	defer unix.Close(fd)

	inspection, err := InspectMount(fd)
	if err != nil {
		// A temporary test directory may sit on an intentionally rejected
		// duplicate/bind-like mount topology in a container. That is a valid
		// fail-closed result, not a reason for the default test lane to require
		// an otherwise unsupported host mount to qualify.
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("InspectMount(owned temporary root) error = %v, want qualified inspection or ErrUnsupported", err)
		}
		return
	}
	if inspection.Inode == 0 || inspection.Mount.ID == 0 {
		t.Fatalf("InspectMount() returned incomplete identity: %#v", inspection)
	}
	if inspection.Namespace.Device == 0 || inspection.Namespace.Inode == 0 {
		t.Fatalf("InspectMount() returned incomplete namespace: %#v", inspection.Namespace)
	}
}

func TestInspectMountRejectsInvalidOrNonDirectoryOwnedDescriptors(t *testing.T) {
	if _, err := InspectMount(-1); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("InspectMount(-1) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := InspectMount(1 << 30); !errors.Is(err, ErrDrifted) {
		t.Fatalf("InspectMount(invalid descriptor) error = %v, want ErrDrifted", err)
	}

	file := t.TempDir() + "/regular-file"
	fd, err := unix.Open(file, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("open owned temporary file: %v", err)
	}
	defer unix.Close(fd)
	if _, err := InspectMount(fd); !errors.Is(err, ErrDrifted) {
		t.Fatalf("InspectMount(regular file) error = %v, want ErrDrifted", err)
	}
}

func TestCaptureRootExpectationPublicEntryPointRejectsInvalidFD(t *testing.T) {
	_, err := CaptureRootExpectation(-1, true, true)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("CaptureRootExpectation(-1) error = %v, want ErrInvalidAuthority", err)
	}
}

func testDescriptorEvidence() descriptorMountEvidence {
	inspection := testInspection()
	return descriptorMountEvidence{
		Device:     inspection.Device,
		Inode:      inspection.Inode,
		UID:        inspection.UID,
		GID:        inspection.GID,
		Mode:       inspection.Mode,
		MountID:    inspection.Mount.ID,
		Filesystem: inspection.Filesystem.Filesystem,
		ReadOnly:   inspection.Filesystem.ReadOnly,
	}
}
