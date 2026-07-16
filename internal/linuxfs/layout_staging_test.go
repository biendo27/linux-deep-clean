//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestPublicPrivateLayoutFactoriesRejectNilAuthority(t *testing.T) {
	if _, err := OpenPrivateDirectory(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenPrivateDirectory(nil) error = %v, want ErrUnsupported", err)
	}
	if _, err := OpenPrivateStaging(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenPrivateStaging(nil) error = %v, want ErrUnsupported", err)
	}
}

func TestOpenPrivateStagingRequiresPrivateStagingLayout(t *testing.T) {
	rootFD, _, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer staging.Close()

	for _, kind := range []mounts.LayoutKind{
		mounts.LayoutPrivateState,
		mounts.LayoutPrivateQuarantine,
		mounts.LayoutTrash,
	} {
		t.Run(string(kind), func(t *testing.T) {
			duplicateCalls := 0
			lease, err := openPrivateStagingWithSource(&testPrivateDirectorySource{
				rootID: rootID,
				kind:   kind,
				duplicate: func() (int, error) {
					duplicateCalls++
					return staging.duplicate()
				},
			})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("openPrivateStagingWithSource() error = %v, want ErrUnsupported", err)
			}
			if lease != nil {
				t.Fatal("openPrivateStagingWithSource() accepted a non-staging layout")
			}
			if duplicateCalls != 0 {
				t.Fatalf("source duplicate calls = %d, want 0 before the layout kind is accepted", duplicateCalls)
			}
		})
	}

	lease, err := openPrivateStagingWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateStaging,
		duplicate: func() (int, error) {
			return staging.duplicate()
		},
	})
	if err != nil {
		t.Fatalf("openPrivateStagingWithSource() valid layout error = %v", err)
	}
	fd, err := lease.duplicate()
	if err != nil {
		t.Fatalf("layout-backed staging duplicate() error = %v", err)
	}
	defer unix.Close(fd)
}

func TestOpenPrivateStagingFailsClosedForNilLayout(t *testing.T) {
	lease, err := OpenPrivateStaging(nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenPrivateStaging(nil) error = %v, want ErrUnsupported", err)
	}
	if lease != nil {
		t.Fatal("OpenPrivateStaging(nil) returned descriptor authority")
	}
}

func TestOpenPrivateDirectoryFailsClosedForNilLayout(t *testing.T) {
	lease, err := OpenPrivateDirectory(nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenPrivateDirectory(nil) error = %v, want ErrUnsupported", err)
	}
	if lease != nil {
		t.Fatal("OpenPrivateDirectory(nil) returned descriptor authority")
	}
}

func TestStageNoReplaceRequalifiesLayoutAtPointOfUse(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	duplicateCalls := 0
	layoutStaging, err := openPrivateStagingWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateStaging,
		duplicate: func() (int, error) {
			duplicateCalls++
			if duplicateCalls > 1 {
				return -1, mounts.ErrLeaseClosed
			}
			return staging.duplicate()
		},
	})
	if err != nil {
		t.Fatalf("openPrivateStagingWithSource() error = %v", err)
	}

	renameCalls := 0
	staged, err := stageNoReplaceWith(context.Background(), source, "item", layoutStaging, "stage-token", stageTestAction, expected, stageHooks{
		renameNoReplace: func(int, string, int, string) error {
			renameCalls++
			return nil
		},
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("stageNoReplaceWith() layout requalification error = %v, want ErrDrifted", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("stageNoReplaceWith() returned a staged object after layout requalification failed")
	}
	if duplicateCalls != 2 {
		t.Fatalf("layout duplicate calls = %d, want 2 (open and point-of-use requalification)", duplicateCalls)
	}
	if renameCalls != 0 {
		t.Fatalf("rename calls = %d, want 0 after layout requalification failed", renameCalls)
	}
	assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
}

func TestStageNoReplaceRejectsLayoutRootMismatchBeforeRename(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	duplicateCalls := 0
	layoutSource := &testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateStaging,
		duplicate: func() (int, error) {
			duplicateCalls++
			return staging.duplicate()
		},
	}
	layoutStaging, err := openPrivateStagingWithSource(layoutSource)
	if err != nil {
		t.Fatalf("openPrivateStagingWithSource() error = %v", err)
	}

	otherRoot, err := domain.NewTrustedRootID("linuxfs-layout-root-mismatch")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	layoutSource.rootID = otherRoot

	renameCalls := 0
	staged, err := stageNoReplaceWith(context.Background(), source, "item", layoutStaging, "stage-token", stageTestAction, expected, stageHooks{
		renameNoReplace: func(int, string, int, string) error {
			renameCalls++
			return nil
		},
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("stageNoReplaceWith() root mismatch error = %v, want ErrDrifted", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("stageNoReplaceWith() returned a staged object after layout root drift")
	}
	if duplicateCalls != 1 {
		t.Fatalf("layout duplicate calls = %d, want only the opening qualification", duplicateCalls)
	}
	if renameCalls != 0 {
		t.Fatalf("rename calls = %d, want 0 after layout root drift", renameCalls)
	}
	assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
}

func TestLayoutBackedStagingCanRetainOrRestoreButCannotAuthorizeRemoval(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	layoutStaging := testLayoutBackedStaging(t, staging, rootID)
	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	restored, err := StageNoReplace(context.Background(), source, "item", layoutStaging, "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer restored.Close()
	if err := RestoreNoReplace(context.Background(), restored); err != nil {
		t.Fatalf("RestoreNoReplace() error = %v", err)
	}
	if !testEntryExists(t, source, "item") {
		t.Fatal("RestoreNoReplace() did not restore the layout-backed staged item")
	}
	if testEntryExists(t, staging, "stage-token") {
		t.Fatal("RestoreNoReplace() retained the successfully restored staging token")
	}

	expected = stageTestPrecondition(t, rootID, source, "item")
	retained, err := StageNoReplace(context.Background(), source, "item", layoutStaging, "stage-token-retained", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() retained item error = %v", err)
	}
	defer retained.Close()
	if err := RemoveStagedTree(context.Background(), retained); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RemoveStagedTree() layout-backed staging error = %v, want ErrUnsupported", err)
	}
	if !testEntryExists(t, staging, "stage-token-retained") {
		t.Fatal("RemoveStagedTree() removed content without exclusive-removal authority")
	}
}

func TestLayoutBackedStagingDuplicateFailsClosedOnConflictsAndDrift(t *testing.T) {
	rootFD, _, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer staging.Close()

	layoutStaging := testLayoutBackedStaging(t, staging, rootID)
	otherRoot, err := domain.NewTrustedRootID("linuxfs-layout-staging-other-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func()
		want   error
	}{
		{
			name: "conflicting parent authority",
			mutate: func() {
				layoutStaging.parent = staging
			},
			want: ErrUnsupported,
		},
		{
			name: "root binding drift",
			mutate: func() {
				layoutStaging.parent = nil
				layoutStaging.rootID = otherRoot
			},
			want: ErrDrifted,
		},
		{
			name: "layout kind drift",
			mutate: func() {
				layoutStaging.rootID = rootID
				layoutStaging.directory.kind = mounts.LayoutPrivateState
			},
			want: ErrDrifted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.mutate()
			if _, err := layoutStaging.duplicate(); !errors.Is(err, test.want) {
				t.Fatalf("layout-backed staging duplicate() error = %v, want %v", err, test.want)
			}
		})
	}

	missingAuthority := &PrivateStagingLease{rootID: rootID}
	if _, err := missingAuthority.duplicate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PrivateStagingLease without authority duplicate() error = %v, want ErrUnsupported", err)
	}
}

func TestPrivateDirectoryDuplicateFailsClosedAfterRevocationOrBadDescriptor(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	var nilLease *PrivateDirectoryLease
	if _, err := nilLease.duplicate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("nil PrivateDirectoryLease duplicate() error = %v, want ErrUnsupported", err)
	}

	source := &testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			return directory.duplicate()
		},
	}
	lease, err := openPrivateDirectoryWithSource(source)
	if err != nil {
		t.Fatalf("openPrivateDirectoryWithSource() error = %v", err)
	}
	defer lease.Close()

	source.duplicate = func() (int, error) { return -1, nil }
	if _, err := lease.duplicate(); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease duplicate() invalid descriptor error = %v, want ErrDrifted", err)
	}

	if err := lease.Close(); err != nil {
		t.Fatalf("PrivateDirectoryLease.Close() error = %v", err)
	}
	if _, err := lease.duplicate(); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed PrivateDirectoryLease duplicate() error = %v, want ErrDrifted", err)
	}
}

func testLayoutBackedStaging(t *testing.T, staging *ParentLease, rootID domain.TrustedRootID) *PrivateStagingLease {
	t.Helper()
	lease, err := openPrivateStagingWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateStaging,
		duplicate: func() (int, error) {
			return staging.duplicate()
		},
	})
	if err != nil {
		t.Fatalf("openPrivateStagingWithSource() error = %v", err)
	}
	return lease
}
