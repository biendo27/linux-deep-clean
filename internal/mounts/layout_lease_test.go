//go:build linux

package mounts

import (
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

func TestOpenTrustedLayoutBindsOneKindToTheHeldTrustedRoot(t *testing.T) {
	rootID := testRootID(t)
	rootDirectory := t.TempDir()
	root, err := openTrustedRootWith(
		StaticRegistry{rootID: testAuthority(t, rootID, rootDirectory, testExpectation())},
		rootID,
		testInspectionFor,
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open trusted root: %v", err)
	}
	defer root.Close()

	layoutDirectory := t.TempDir()
	registry := StaticLayoutRegistry{
		rootID: {
			LayoutPrivateState: testLayoutAuthority(t, rootID, LayoutPrivateState, layoutDirectory, testLayoutExpectation()),
		},
	}

	inspections := 0
	layout, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, func(int) (MountInspection, error) {
		inspections++
		if inspections == 1 {
			return testInspection(), nil
		}
		return testLayoutInspection(), nil
	}, func() error { return nil })
	if err != nil {
		t.Fatalf("openTrustedLayoutWith() error = %v", err)
	}
	defer layout.Close()

	if layout.RootID() != rootID {
		t.Fatalf("layout RootID() = %q, want %q", layout.RootID(), rootID)
	}
	if layout.Kind() != LayoutPrivateState {
		t.Fatalf("layout Kind() = %q, want %q", layout.Kind(), LayoutPrivateState)
	}
	duplicate, err := layout.duplicateWith(layoutInspectionSequence(testInspection(), testLayoutInspection()))
	if err != nil {
		t.Fatalf("layout.Duplicate() error = %v", err)
	}
	defer unix.Close(duplicate)
	if flags, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("layout duplicate flags = (%#x, %v), want FD_CLOEXEC", flags, err)
	}
}

func TestOpenTrustedLayoutFailsClosedBeforeOpeningUnboundOrDriftedLayout(t *testing.T) {
	rootID := testRootID(t)
	rootDirectory := t.TempDir()
	root, err := openTrustedRootWith(
		StaticRegistry{rootID: testAuthority(t, rootID, rootDirectory, testExpectation())},
		rootID,
		testInspectionFor,
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open trusted root: %v", err)
	}
	defer root.Close()

	layoutDirectory := t.TempDir()
	valid := testLayoutAuthority(t, rootID, LayoutPrivateState, layoutDirectory, testLayoutExpectation())
	otherRoot, err := domain.NewTrustedRootID("other-layout-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}

	tests := map[string]struct {
		registry LayoutRegistry
		kind     LayoutKind
		inspect  rootInspector
		want     error
	}{
		"unknown layout": {
			registry: StaticLayoutRegistry{}, kind: LayoutPrivateState, inspect: testInspectionFor, want: ErrUnknownLayout,
		},
		"unknown kind": {
			registry: StaticLayoutRegistry{rootID: {LayoutPrivateState: valid}}, kind: LayoutKind("future"), inspect: testInspectionFor, want: ErrInvalidAuthority,
		},
		"authority root mismatch": {
			registry: StaticLayoutRegistry{rootID: {LayoutPrivateState: func() LayoutAuthority { value := valid; value.Root = otherRoot; return value }()}}, kind: LayoutPrivateState, inspect: testInspectionFor, want: ErrInvalidAuthority,
		},
		"layout mount drifts": {
			registry: StaticLayoutRegistry{rootID: {LayoutPrivateState: valid}}, kind: LayoutPrivateState,
			inspect: func(int) (MountInspection, error) {
				return testInspection(), nil
			}, want: ErrDrifted,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			layout, err := openTrustedLayoutWith(test.registry, root, test.kind, test.inspect, func() error { return nil })
			if layout != nil {
				defer layout.Close()
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("openTrustedLayoutWith() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOpenTrustedLayoutRejectsASecondMountEvenWhenRootIDMatches(t *testing.T) {
	rootID := testRootID(t)
	rootDirectory := t.TempDir()
	root, err := openTrustedRootWith(
		StaticRegistry{rootID: testAuthority(t, rootID, rootDirectory, testExpectation())},
		rootID,
		testInspectionFor,
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open trusted root: %v", err)
	}
	defer root.Close()

	layoutDirectory := t.TempDir()
	expected := testLayoutExpectation()
	layoutInspection := testLayoutInspection()
	layoutInspection.Mount.ID++
	layoutInspection.Mount.MountPoint = "/other-mounted-layout"
	expected.Mount = layoutInspection.Mount

	registry := StaticLayoutRegistry{
		rootID: {
			LayoutPrivateState: testLayoutAuthority(t, rootID, LayoutPrivateState, layoutDirectory, expected),
		},
	}
	inspections := 0
	_, err = openTrustedLayoutWith(registry, root, LayoutPrivateState, func(int) (MountInspection, error) {
		inspections++
		if inspections == 1 {
			return testInspection(), nil
		}
		return layoutInspection, nil
	}, func() error { return nil })
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openTrustedLayoutWith() cross-mount error = %v, want ErrUnsupported", err)
	}
}

func TestCaptureLayoutExpectationRequiresSameCurrentlyQualifiedMount(t *testing.T) {
	root := &RootLease{rootID: testRootID(t), expected: testExpectation(), fd: -1}
	_, err := captureLayoutExpectationWith(root, 7, func(int) (MountInspection, error) {
		return testInspection(), nil
	})
	if !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("captureLayoutExpectationWith(closed root) error = %v, want ErrLeaseClosed", err)
	}
}

func TestCaptureLayoutExpectationRejectsUnsafeLayoutDescriptors(t *testing.T) {
	root := openLayoutTestRoot(t)
	directory := t.TempDir()
	file := t.TempDir() + "/not-a-directory"
	if err := unix.Close(mustOpenFile(t, file)); err != nil {
		t.Fatalf("close test file: %v", err)
	}

	tests := map[string]struct {
		open func() (int, error)
		want error
	}{
		"descriptor without close on exec": {
			open: func() (int, error) {
				return unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY, 0)
			},
			want: ErrInvalidAuthority,
		},
		"O_PATH descriptor": {
			open: func() (int, error) {
				return unix.Open(directory, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			},
			want: ErrInvalidAuthority,
		},
		"non-directory descriptor": {
			open: func() (int, error) {
				return unix.Open(file, unix.O_RDONLY|unix.O_CLOEXEC, 0)
			},
			want: ErrDrifted,
		},
		"read-write descriptor": {
			open: func() (int, error) {
				return unix.Open(file, unix.O_RDWR|unix.O_CLOEXEC, 0)
			},
			want: ErrInvalidAuthority,
		},
		"descriptor closed before capture": {
			open: func() (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err != nil {
					return -1, err
				}
				return fd, unix.Close(fd)
			},
			want: ErrDrifted,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fd, err := test.open()
			if err != nil {
				t.Fatalf("open test descriptor: %v", err)
			}
			defer unix.Close(fd)

			_, err = captureLayoutExpectationWith(root, fd, layoutInspectionSequence(testInspection(), testLayoutInspection()))
			if !errors.Is(err, test.want) {
				t.Fatalf("captureLayoutExpectationWith() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLayoutAuthorityValidation(t *testing.T) {
	rootID := testRootID(t)
	valid := LayoutAuthority{
		Root:     rootID,
		Kind:     LayoutPrivateState,
		Open:     func() (int, error) { return -1, nil },
		Expected: testLayoutExpectation(),
	}
	if err := valid.validate(rootID, LayoutPrivateState); err != nil {
		t.Fatalf("valid LayoutAuthority.validate() error = %v", err)
	}

	tests := map[string]LayoutAuthority{
		"missing opener":            {Root: rootID, Kind: LayoutPrivateState, Expected: testLayoutExpectation()},
		"invalid authority root":    {Kind: LayoutPrivateState, Open: valid.Open, Expected: testLayoutExpectation()},
		"wrong root":                {Root: domain.TrustedRootID("wrong-root"), Kind: LayoutPrivateState, Open: valid.Open, Expected: testLayoutExpectation()},
		"invalid authority kind":    {Root: rootID, Kind: LayoutKind("future"), Open: valid.Open, Expected: testLayoutExpectation()},
		"mismatched authority kind": {Root: rootID, Kind: LayoutTrash, Open: valid.Open, Expected: testLayoutExpectation()},
		"missing inode":             {Root: rootID, Kind: LayoutPrivateState, Open: valid.Open, Expected: LayoutExpectation{}},
	}
	for name, authority := range tests {
		t.Run(name, func(t *testing.T) {
			if err := authority.validate(rootID, LayoutPrivateState); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("LayoutAuthority.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
	if err := valid.validate("", LayoutPrivateState); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("LayoutAuthority.validate(invalid requested root) error = %v, want ErrInvalidAuthority", err)
	}
	if err := valid.validate(rootID, LayoutKind("future")); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("LayoutAuthority.validate(invalid requested kind) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestLayoutExpectationValidation(t *testing.T) {
	valid := testLayoutExpectation()
	if err := valid.validate(); err != nil {
		t.Fatalf("valid LayoutExpectation.validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*LayoutExpectation)
	}{
		{
			name: "missing namespace",
			mutate: func(expectation *LayoutExpectation) {
				expectation.Namespace = MountNamespace{}
			},
		},
		{
			name: "missing device",
			mutate: func(expectation *LayoutExpectation) {
				expectation.Device = DeviceIdentity{}
			},
		},
		{
			name: "missing inode",
			mutate: func(expectation *LayoutExpectation) {
				expectation.Inode = 0
			},
		},
		{
			name: "unsupported mount record",
			mutate: func(expectation *LayoutExpectation) {
				expectation.Mount.Root = "/subtree"
			},
		},
		{
			name: "conflicting mount device",
			mutate: func(expectation *LayoutExpectation) {
				expectation.Mount.Device.Minor++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectation := valid
			test.mutate(&expectation)
			if err := expectation.validate(); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("LayoutExpectation.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
}

func TestLayoutLeaseLifecycleKeepsExplicitDuplicateOwned(t *testing.T) {
	var nilLease *LayoutLease
	if nilLease.RootID() != "" {
		t.Fatalf("nil layout RootID() = %q, want empty", nilLease.RootID())
	}
	if nilLease.Kind() != "" {
		t.Fatalf("nil layout Kind() = %q, want empty", nilLease.Kind())
	}
	if err := nilLease.Close(); err != nil {
		t.Fatalf("nil layout Close() error = %v", err)
	}
	if _, err := nilLease.Duplicate(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil layout Duplicate() error = %v, want ErrLeaseClosed", err)
	}

	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	layoutDirectory := t.TempDir()
	openedFD := -1
	registry := StaticLayoutRegistry{
		rootID: {
			LayoutPrivateState: {
				Root: rootID,
				Kind: LayoutPrivateState,
				Open: func() (int, error) {
					fd, err := unix.Open(layoutDirectory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
					if err == nil {
						openedFD = fd
					}
					return fd, err
				},
				Expected: testLayoutExpectation(),
			},
		},
	}
	layout, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, layoutInspectionSequence(testInspection(), testLayoutInspection()), func() error { return nil })
	if err != nil {
		t.Fatalf("openTrustedLayoutWith() error = %v", err)
	}
	if layout.RootID() != rootID {
		t.Fatalf("layout RootID() = %q, want %q", layout.RootID(), rootID)
	}
	if layout.Kind() != LayoutPrivateState {
		t.Fatalf("layout Kind() = %q, want %q", layout.Kind(), LayoutPrivateState)
	}

	duplicate, err := layout.duplicateWith(layoutInspectionSequence(testInspection(), testLayoutInspection()))
	if err != nil {
		t.Fatalf("layout.Duplicate() error = %v", err)
	}
	defer unix.Close(duplicate)

	if err := layout.Close(); err != nil {
		t.Fatalf("layout.Close() error = %v", err)
	}
	if err := layout.Close(); err != nil {
		t.Fatalf("second layout.Close() error = %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(openedFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("opener FD after layout.Close() error = %v, want EBADF", err)
	}
	if _, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); err != nil {
		t.Fatalf("caller-owned duplicate after layout.Close() error = %v", err)
	}
	if _, err := layout.Duplicate(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("layout.Duplicate() after Close error = %v, want ErrLeaseClosed", err)
	}

	invalid := &LayoutLease{rootID: rootID, kind: LayoutPrivateState, fd: -1}
	if err := invalid.Close(); err != nil {
		t.Fatalf("invalid-descriptor layout Close() error = %v", err)
	}
	if _, err := invalid.Duplicate(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("invalid-descriptor layout Duplicate() error = %v, want ErrLeaseClosed", err)
	}
}

func TestLayoutLeaseDuplicateFailsAfterItsTrustedRootCloses(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	layoutDirectory := t.TempDir()
	layout, err := openTrustedLayoutWith(
		StaticLayoutRegistry{rootID: {
			LayoutPrivateState: testLayoutAuthority(t, rootID, LayoutPrivateState, layoutDirectory, testLayoutExpectation()),
		}},
		root,
		LayoutPrivateState,
		layoutInspectionSequence(testInspection(), testLayoutInspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedLayoutWith() error = %v", err)
	}
	defer layout.Close()

	if err := root.Close(); err != nil {
		t.Fatalf("close trusted root: %v", err)
	}
	if _, err := layout.Duplicate(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("layout.Duplicate() after its root closes error = %v, want ErrLeaseClosed", err)
	}
}

func TestLayoutLeaseDuplicateRequalifiesRootAndLayoutEvidenceAtPointOfUse(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	layoutDirectory := t.TempDir()
	layout, err := openTrustedLayoutWith(
		StaticLayoutRegistry{rootID: {
			LayoutPrivateState: testLayoutAuthority(t, rootID, LayoutPrivateState, layoutDirectory, testLayoutExpectation()),
		}},
		root,
		LayoutPrivateState,
		layoutInspectionSequence(testInspection(), testLayoutInspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedLayoutWith() error = %v", err)
	}
	defer layout.Close()

	tests := []struct {
		name    string
		inspect rootInspector
		want    error
	}{
		{
			name: "root drift",
			inspect: func(int) (MountInspection, error) {
				inspection := testInspection()
				inspection.Inode++
				return inspection, nil
			},
			want: ErrDrifted,
		},
		{
			name: "layout drift",
			inspect: func() rootInspector {
				layoutInspection := testLayoutInspection()
				layoutInspection.Inode++
				return layoutInspectionSequence(testInspection(), layoutInspection)
			}(),
			want: ErrDrifted,
		},
		{
			name: "layout becomes read only",
			inspect: func() rootInspector {
				layoutInspection := testLayoutInspection()
				layoutInspection.Filesystem.ReadOnly = true
				return layoutInspectionSequence(testInspection(), layoutInspection)
			}(),
			want: ErrUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := layout.duplicateWith(test.inspect); !errors.Is(err, test.want) {
				t.Fatalf("layout.duplicateWith() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := layout.duplicateWith(nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("layout.duplicateWith(nil) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestOpenTrustedLayoutRejectsInputsBeforeDerivingLayoutAuthority(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	layoutDirectory := t.TempDir()
	opens := 0
	registry := StaticLayoutRegistry{
		rootID: {
			LayoutPrivateState: {
				Root: rootID,
				Kind: LayoutPrivateState,
				Open: func() (int, error) {
					opens++
					return unix.Open(layoutDirectory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				},
				Expected: testLayoutExpectation(),
			},
		},
	}

	if _, err := OpenTrustedLayout(nil, nil, LayoutPrivateState); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("OpenTrustedLayout(nil, nil) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, nil, func() error { return nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedLayoutWith(nil inspector) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, testInspectionFor, nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedLayoutWith(nil kernel checker) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := openTrustedLayoutWith(registry, nil, LayoutPrivateState, testInspectionFor, func() error { return nil }); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("openTrustedLayoutWith(nil root) error = %v, want ErrLeaseClosed", err)
	}
	if _, err := openTrustedLayoutWith(registry, &RootLease{}, LayoutPrivateState, testInspectionFor, func() error { return nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedLayoutWith(invalid root ID) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := openTrustedLayoutWith(registry, root, LayoutKind("future"), testInspectionFor, func() error { return nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedLayoutWith(unknown kind) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, testInspectionFor, func() error { return ErrUnsupported }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openTrustedLayoutWith(unsupported kernel) error = %v, want ErrUnsupported", err)
	}
	if opens != 0 {
		t.Fatalf("layout opener calls after unsupported kernel = %d, want 0", opens)
	}

	driftedRoot := testInspection()
	driftedRoot.Inode++
	if _, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, func(int) (MountInspection, error) {
		return driftedRoot, nil
	}, func() error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("openTrustedLayoutWith(root drift) error = %v, want ErrDrifted", err)
	}
	if opens != 0 {
		t.Fatalf("layout opener calls after root drift = %d, want 0", opens)
	}

	failingOpen := StaticLayoutRegistry{rootID: {LayoutPrivateState: {
		Root:     rootID,
		Kind:     LayoutPrivateState,
		Open:     func() (int, error) { return -1, unix.EACCES },
		Expected: testLayoutExpectation(),
	}}}
	if _, err := openTrustedLayoutWith(failingOpen, root, LayoutPrivateState, layoutInspectionSequence(testInspection(), testLayoutInspection()), func() error { return nil }); !errors.Is(err, unix.EACCES) {
		t.Fatalf("openTrustedLayoutWith(opener error) error = %v, want EACCES", err)
	}

	negativeDescriptor := StaticLayoutRegistry{rootID: {LayoutPrivateState: {
		Root:     rootID,
		Kind:     LayoutPrivateState,
		Open:     func() (int, error) { return -1, nil },
		Expected: testLayoutExpectation(),
	}}}
	if _, err := openTrustedLayoutWith(negativeDescriptor, root, LayoutPrivateState, layoutInspectionSequence(testInspection(), testLayoutInspection()), func() error { return nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedLayoutWith(negative descriptor) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestOpenTrustedLayoutClosesRejectedDescriptors(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()

	layoutOnOtherMount := testLayoutInspection()
	layoutOnOtherMount.Mount.ID++
	layoutOnOtherMount.Mount.MountPoint = "/other-layout"
	expectedOnOtherMount := testLayoutExpectation()
	expectedOnOtherMount.Mount = layoutOnOtherMount.Mount

	tests := []struct {
		name     string
		expected LayoutExpectation
		open     func(string, *int) (int, error)
		inspect  rootInspector
		want     error
	}{
		{
			name:     "descriptor without close on exec",
			expected: testLayoutExpectation(),
			open: func(directory string, opened *int) (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY, 0)
				if err == nil {
					*opened = fd
				}
				return fd, err
			},
			inspect: layoutInspectionSequence(testInspection(), testLayoutInspection()),
			want:    ErrInvalidAuthority,
		},
		{
			name:     "O_PATH descriptor",
			expected: testLayoutExpectation(),
			open: func(directory string, opened *int) (int, error) {
				fd, err := unix.Open(directory, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					*opened = fd
				}
				return fd, err
			},
			inspect: layoutInspectionSequence(testInspection(), testLayoutInspection()),
			want:    ErrInvalidAuthority,
		},
		{
			name:     "layout inspection failure",
			expected: testLayoutExpectation(),
			open: func(directory string, opened *int) (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					*opened = fd
				}
				return fd, err
			},
			inspect: func() rootInspector {
				calls := 0
				return func(int) (MountInspection, error) {
					calls++
					if calls == 1 {
						return testInspection(), nil
					}
					return MountInspection{}, ErrDrifted
				}
			}(),
			want: ErrDrifted,
		},
		{
			name: "layout evidence drift",
			expected: func() LayoutExpectation {
				value := testLayoutExpectation()
				value.UID++
				return value
			}(),
			open: func(directory string, opened *int) (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					*opened = fd
				}
				return fd, err
			},
			inspect: layoutInspectionSequence(testInspection(), testLayoutInspection()),
			want:    ErrDrifted,
		},
		{
			name:     "layout outside trusted root mount",
			expected: expectedOnOtherMount,
			open: func(directory string, opened *int) (int, error) {
				fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err == nil {
					*opened = fd
				}
				return fd, err
			},
			inspect: layoutInspectionSequence(testInspection(), layoutOnOtherMount),
			want:    ErrUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			opened := -1
			registry := StaticLayoutRegistry{rootID: {LayoutPrivateState: {
				Root:     rootID,
				Kind:     LayoutPrivateState,
				Open:     func() (int, error) { return test.open(directory, &opened) },
				Expected: test.expected,
			}}}

			_, err := openTrustedLayoutWith(registry, root, LayoutPrivateState, test.inspect, func() error { return nil })
			if !errors.Is(err, test.want) {
				t.Fatalf("openTrustedLayoutWith() error = %v, want %v", err, test.want)
			}
			if opened < 0 {
				t.Fatal("layout opener did not return a descriptor")
			}
			if _, err := unix.FcntlInt(uintptr(opened), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatalf("rejected layout descriptor error = %v, want EBADF", err)
			}
		})
	}
}

func TestCaptureLayoutExpectationCapturesOnlyCurrentRootMount(t *testing.T) {
	root := openLayoutTestRoot(t)
	directory := t.TempDir()
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open layout descriptor: %v", err)
	}
	defer unix.Close(fd)

	got, err := captureLayoutExpectationWith(root, fd, layoutInspectionSequence(testInspection(), testLayoutInspection()))
	if err != nil {
		t.Fatalf("captureLayoutExpectationWith() error = %v", err)
	}
	if got != testLayoutExpectation() {
		t.Fatalf("captureLayoutExpectationWith() = %#v, want %#v", got, testLayoutExpectation())
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("capture leaves caller-owned descriptor open: %v", err)
	}

	if _, err := CaptureLayoutExpectation(nil, -1); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("CaptureLayoutExpectation(-1) error = %v, want ErrInvalidAuthority", err)
	}

	tests := []struct {
		name    string
		root    *RootLease
		fd      int
		inspect rootInspector
		want    error
	}{
		{
			name:    "negative descriptor",
			root:    root,
			fd:      -1,
			inspect: testInspectionFor,
			want:    ErrInvalidAuthority,
		},
		{
			name: "missing inspector",
			root: root,
			fd:   fd,
			want: ErrInvalidAuthority,
		},
		{
			name:    "nil root",
			fd:      fd,
			inspect: testInspectionFor,
			want:    ErrLeaseClosed,
		},
		{
			name: "root requalification drift",
			root: root,
			fd:   fd,
			inspect: func(int) (MountInspection, error) {
				inspection := testInspection()
				inspection.Inode++
				return inspection, nil
			},
			want: ErrDrifted,
		},
		{
			name: "layout crosses mount boundary",
			root: root,
			fd:   fd,
			inspect: func() rootInspector {
				layout := testLayoutInspection()
				layout.Mount.ID++
				return layoutInspectionSequence(testInspection(), layout)
			}(),
			want: ErrUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := captureLayoutExpectationWith(test.root, test.fd, test.inspect)
			if !errors.Is(err, test.want) {
				t.Fatalf("captureLayoutExpectationWith() error = %v, want %v", err, test.want)
			}
			if got != (LayoutExpectation{}) {
				t.Fatalf("captureLayoutExpectationWith() result = %#v, want zero value on failure", got)
			}
		})
	}
}

func TestLayoutEvidenceRejectsDriftAndUnsupportedFacts(t *testing.T) {
	tests := []struct {
		name           string
		mutateExpected func(*LayoutExpectation)
		mutateActual   func(*MountInspection)
		want           error
	}{
		{
			name: "invalid registered evidence",
			mutateExpected: func(expected *LayoutExpectation) {
				expected.Namespace = MountNamespace{}
			},
			want: ErrInvalidAuthority,
		},
		{
			name: "mount namespace changed",
			mutateActual: func(actual *MountInspection) {
				actual.Namespace.Inode++
			},
			want: ErrDrifted,
		},
		{
			name: "device changed",
			mutateActual: func(actual *MountInspection) {
				actual.Device.Minor++
			},
			want: ErrDrifted,
		},
		{
			name: "inode changed",
			mutateActual: func(actual *MountInspection) {
				actual.Inode++
			},
			want: ErrDrifted,
		},
		{
			name: "ownership changed",
			mutateActual: func(actual *MountInspection) {
				actual.UID++
			},
			want: ErrDrifted,
		},
		{
			name: "mode changed",
			mutateActual: func(actual *MountInspection) {
				actual.Mode = 0o755
			},
			want: ErrDrifted,
		},
		{
			name: "mount record changed",
			mutateActual: func(actual *MountInspection) {
				actual.Mount.ID++
			},
			want: ErrDrifted,
		},
		{
			name: "read-only layout filesystem",
			mutateActual: func(actual *MountInspection) {
				actual.Filesystem.ReadOnly = true
			},
			want: ErrUnsupported,
		},
		{
			name: "inconsistent live device facts",
			mutateActual: func(actual *MountInspection) {
				actual.Filesystem.RootDevice.Minor++
			},
			want: ErrDrifted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := testLayoutExpectation()
			actual := testLayoutInspection()
			if test.mutateExpected != nil {
				test.mutateExpected(&expected)
			}
			if test.mutateActual != nil {
				test.mutateActual(&actual)
			}
			if err := checkLayoutEvidence(expected, actual); !errors.Is(err, test.want) {
				t.Fatalf("checkLayoutEvidence() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLayoutRootBindingRejectsDifferentNamespaceMountAndFilesystem(t *testing.T) {
	root := testInspection()
	layout := testLayoutInspection()
	if err := checkLayoutRootBinding(root, layout); err != nil {
		t.Fatalf("checkLayoutRootBinding() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*MountInspection)
		want   error
	}{
		{
			name: "different namespace",
			mutate: func(layout *MountInspection) {
				layout.Namespace.Inode++
			},
			want: ErrDrifted,
		},
		{
			name: "different device",
			mutate: func(layout *MountInspection) {
				layout.Device.Minor++
			},
			want: ErrUnsupported,
		},
		{
			name: "different mount record",
			mutate: func(layout *MountInspection) {
				layout.Mount.ID++
			},
			want: ErrUnsupported,
		},
		{
			name: "different filesystem facts",
			mutate: func(layout *MountInspection) {
				layout.Filesystem.MountRoot = "/subtree"
			},
			want: ErrUnsupported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := layout
			test.mutate(&candidate)
			if err := checkLayoutRootBinding(root, candidate); !errors.Is(err, test.want) {
				t.Fatalf("checkLayoutRootBinding() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRootRequalificationForLayoutFailsClosed(t *testing.T) {
	var nilRoot *RootLease
	if _, err := nilRoot.requalifyWith(testInspectionFor); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil root requalifyWith() error = %v, want ErrLeaseClosed", err)
	}
	invalidRoot := &RootLease{rootID: testRootID(t), fd: -1}
	if _, err := invalidRoot.requalifyWith(testInspectionFor); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("invalid root requalifyWith() error = %v, want ErrInvalidAuthority", err)
	}

	root := openLayoutTestRoot(t)
	if _, err := root.requalifyWith(nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("requalifyWith(nil) error = %v, want ErrInvalidAuthority", err)
	}
	drifted := testInspection()
	drifted.Inode++
	if _, err := root.requalifyWith(func(int) (MountInspection, error) { return drifted, nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("requalifyWith(drifted root) error = %v, want ErrDrifted", err)
	}
	unsupported := testInspection()
	unsupported.Filesystem.ReadOnly = true
	if _, err := root.requalifyWith(func(int) (MountInspection, error) { return unsupported, nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("requalifyWith(read-only root) error = %v, want ErrUnsupported", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("close root: %v", err)
	}
	if _, err := root.requalifyWith(testInspectionFor); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("closed root requalifyWith() error = %v, want ErrLeaseClosed", err)
	}
}

func testLayoutAuthority(t *testing.T, rootID domain.TrustedRootID, kind LayoutKind, directory string, expected LayoutExpectation) LayoutAuthority {
	t.Helper()
	return LayoutAuthority{
		Root: rootID,
		Kind: kind,
		Open: func() (int, error) {
			return unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		},
		Expected: expected,
	}
}

func testLayoutExpectation() LayoutExpectation {
	inspection := testLayoutInspection()
	return LayoutExpectation{
		Namespace: inspection.Namespace,
		Device:    inspection.Device,
		Inode:     inspection.Inode,
		UID:       inspection.UID,
		GID:       inspection.GID,
		Mode:      inspection.Mode,
		Mount:     inspection.Mount,
	}
}

func testLayoutInspection() MountInspection {
	inspection := testInspection()
	inspection.Inode = 202
	inspection.Mode = 0o700
	return inspection
}

func openLayoutTestRoot(t *testing.T) *RootLease {
	t.Helper()
	rootID := testRootID(t)
	root, err := openTrustedRootWith(
		StaticRegistry{rootID: testAuthority(t, rootID, t.TempDir(), testExpectation())},
		rootID,
		testInspectionFor,
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open trusted root: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close trusted root: %v", err)
		}
	})
	return root
}

func layoutInspectionSequence(root, layout MountInspection) rootInspector {
	inspections := []MountInspection{root, layout}
	index := 0
	return func(int) (MountInspection, error) {
		if index >= len(inspections) {
			return layout, nil
		}
		inspection := inspections[index]
		index++
		return inspection, nil
	}
}
