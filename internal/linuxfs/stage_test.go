//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

const stageTestAction = domain.ActionDeleteRecreatablePath

func TestStageNoReplaceMovesOnlyVerifiedIdentity(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()

	if err := VerifyStagedIdentity(context.Background(), staged); err != nil {
		t.Fatalf("VerifyStagedIdentity() error = %v", err)
	}
	if testEntryExists(t, source, "item") {
		t.Fatal("source entry remains after verified no-replace stage")
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("staged entry is missing after verified no-replace stage")
	}
}

func TestStageNoReplaceRequiresExactActionMask(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   domain.ActionKind
		mutate func(*domain.FilesystemPrecondition)
	}{
		{
			name: "under-specified destructive mask",
			kind: stageTestAction,
			mutate: func(expected *domain.FilesystemPrecondition) {
				expected.Required &^= domain.FilesystemFieldChangedAt
			},
		},
		{
			name: "restoration action with destructive mask",
			kind: domain.ActionRestoreTrashPath,
			mutate: func(*domain.FilesystemPrecondition) {
				// The normal stage fixture carries a destructive mask. A restore
				// action requires the narrower baseline mask instead.
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, source, staging, rootID := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer source.Close()
			defer staging.Close()

			createStageTestFile(t, source, "item")
			expected := stageTestPrecondition(t, rootID, source, "item")
			test.mutate(&expected)

			staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", test.kind, expected)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("StageNoReplace() exact mask error = %v, want ErrUnsupported", err)
			}
			if staged != nil {
				defer staged.Close()
				t.Fatal("StageNoReplace() returned a staged object with an action-mask mismatch")
			}
			assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
		})
	}
}

func TestStageNoReplaceClonesCallerPrecondition(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()

	otherRoot, err := domain.NewTrustedRootID("linuxfs-mutated-caller-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	expected.Target.Filesystem.Root = otherRoot
	expected.Target.Filesystem.Path = testBytePath(t, "source", "replacement")
	expected.Snapshot.Inode.Value++

	if staged.RootID() != rootID {
		t.Fatalf("staged RootID() = %q, want original root %q after caller mutation", staged.RootID(), rootID)
	}
	if !sameBytePath(staged.expected.Target.Filesystem.Path, testBytePath(t, "source", "item")) {
		t.Fatalf("staged precondition path changed after caller mutation: %#v", staged.expected.Target.Filesystem.Path.Components())
	}
	if err := VerifyStagedIdentity(context.Background(), staged); err != nil {
		t.Fatalf("VerifyStagedIdentity() after caller mutation error = %v", err)
	}
}

func TestStageNoReplaceRequiresQualifiedPrivateStaging(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	staged, err := StageNoReplace(context.Background(), source, "item", &PrivateStagingLease{}, "stage-token", stageTestAction, expected)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("StageNoReplace() unqualified staging error = %v, want ErrUnsupported", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("StageNoReplace() accepted an unqualified private staging lease")
	}
	assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")

	qualified := stageTestPrivateStaging(t, staging)
	if err := staging.withFD(func(parentFD int) error {
		return unix.Fchmod(parentFD, 0o755)
	}); err != nil {
		t.Fatalf("make qualified staging directory non-private: %v", err)
	}
	staged, err = StageNoReplace(context.Background(), source, "item", qualified, "stage-token", stageTestAction, expected)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("StageNoReplace() requalified privacy error = %v, want ErrUnsupported", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("StageNoReplace() accepted a staging directory after its private mode changed")
	}
	assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
}

func TestStageMismatchNeverDeleted(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")

	staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
		renameNoReplace: func(oldParentFD int, oldName string, newParentFD int, newName string) error {
			if err := unix.Renameat2(oldParentFD, oldName, newParentFD, newName, unix.RENAME_NOREPLACE); err != nil {
				return err
			}
			if err := unix.Unlinkat(newParentFD, newName, 0); err != nil {
				return err
			}
			fd, err := unix.Openat(newParentFD, newName, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
			if err != nil {
				return err
			}
			return unix.Close(fd)
		},
	})
	if !errors.Is(err, ErrDrifted) || !errors.Is(err, ErrRetained) {
		t.Fatalf("StageNoReplace() post-stage mismatch error = %v, want ErrDrifted + ErrRetained", err)
	}
	if staged == nil {
		t.Fatal("StageNoReplace() returned no retained recovery object")
	}
	defer staged.Close()

	if testEntryExists(t, source, "item") {
		t.Fatal("source entry was unexpectedly restored or replaced after mismatch")
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("post-stage mismatch content was deleted instead of retained")
	}
	if err := VerifyStagedIdentity(context.Background(), staged); !errors.Is(err, ErrDrifted) {
		t.Fatalf("VerifyStagedIdentity() after mismatch error = %v, want ErrDrifted", err)
	}
}

func TestStageNoReplaceLeavesIndeterminateWhenMoveCannotBeSynced(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
		renameNoReplace: renameNoReplace,
		syncDirectory: func(int) error {
			return unix.EIO
		},
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("stageNoReplaceWith() durability error = %v, want ErrInterrupted", err)
	}
	if staged == nil {
		t.Fatal("stageNoReplaceWith() returned no recovery object after a moved but unsynced entry")
	}
	defer staged.Close()
	if staged.state != stagedStateIndeterminate {
		t.Fatalf("staged state after durability failure = %d, want indeterminate", staged.state)
	}
	if testEntryExists(t, source, "item") {
		t.Fatal("stage durability failure unexpectedly restored the source entry")
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("stage durability failure deleted the moved entry")
	}
}

func TestRestoreNoReplaceNeverOverwritesOccupiedSource(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()

	createStageTestFile(t, source, "item")
	replacement := stageTestSnapshot(t, source, "item")
	if err := RestoreNoReplace(context.Background(), staged); !errors.Is(err, ErrRetained) {
		t.Fatalf("RestoreNoReplace() collision error = %v, want ErrRetained", err)
	}
	if observed := stageTestSnapshot(t, source, "item"); observed.Inode != replacement.Inode {
		t.Fatalf("RestoreNoReplace() replaced occupied source inode = %#v, want %#v", observed.Inode, replacement.Inode)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("RestoreNoReplace() deleted the retained staged entry after collision")
	}
}

func TestStageNoReplaceFailsBeforeMoveWhenPreconditionIsStale(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	createStageTestFile(t, source, "replacement")
	if err := source.withFD(func(parentFD int) error {
		return unix.Renameat(parentFD, "replacement", parentFD, "item")
	}); err != nil {
		t.Fatalf("replace source before stage: %v", err)
	}

	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("StageNoReplace() stale precondition error = %v, want ErrDrifted", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("StageNoReplace() returned staged content before precondition mismatch")
	}
	if !testEntryExists(t, source, "item") {
		t.Fatal("stale precondition source disappeared before staging")
	}
	if testEntryExists(t, staging, "stage-token") {
		t.Fatal("stale precondition unexpectedly created a staged entry")
	}
}

func TestStageNoReplaceBindsResolvedPathToPreconditionTarget(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	wrongSource := stageTestParent(t, rootFD, rootID, "source", "different")
	defer wrongSource.Close()

	staged, err := StageNoReplace(context.Background(), wrongSource, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("StageNoReplace() mismatched resolved path error = %v, want ErrUnsupported", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("StageNoReplace() returned a staged object for a mismatched resolved path")
	}
	if !testEntryExists(t, source, "item") {
		t.Fatal("mismatched resolved path moved the planned source entry")
	}
}

func TestPostMovePreconditionExcludesChangedAtOnly(t *testing.T) {
	required, err := RequiredStatMask(domain.ActionDeleteRecreatablePath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	expected := testFilesystemPrecondition(t, required, testKnownSnapshot())
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		t.Fatalf("postMovePrecondition() error = %v", err)
	}
	if postMove.Required&domain.FilesystemFieldChangedAt != 0 {
		t.Fatalf("post-move required mask = %b, unexpectedly includes ChangedAt", postMove.Required)
	}
	if postMove.Required != required&^domain.FilesystemFieldChangedAt {
		t.Fatalf("post-move required mask = %b, want %b", postMove.Required, required&^domain.FilesystemFieldChangedAt)
	}

	observed := testKnownSnapshot()
	observed.ChangedAt.Value++
	if err := ComparePrecondition(postMove, observed); err != nil {
		t.Fatalf("post-move comparison with changed ctime error = %v", err)
	}
}

func stageTestDirectories(t *testing.T) (int, *ParentLease, *ParentLease, domain.TrustedRootID) {
	t.Helper()

	rootFD := openTestDirectory(t)
	makeTestDirectory(t, rootFD, "source")
	makeTestDirectory(t, rootFD, "staging")
	rootID, err := domain.NewTrustedRootID("linuxfs-stage-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source := stageTestParent(t, rootFD, rootID, "source", "item")
	staging := stageTestParent(t, rootFD, rootID, "staging", "stage-token")
	return rootFD, source, staging, rootID
}

func stageTestPrivateStaging(t *testing.T, staging *ParentLease) *PrivateStagingLease {
	t.Helper()
	lease, err := newPrivateStagingLease(staging)
	if err != nil {
		t.Fatalf("newPrivateStagingLease() error = %v", err)
	}
	return lease
}

func stageTestParent(t *testing.T, rootFD int, rootID domain.TrustedRootID, directory string, basename string) *ParentLease {
	t.Helper()
	parent, _, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, directory, basename), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve test parent %q: %v", directory, err)
	}
	parent.rootID = rootID
	return parent
}

func createStageTestFile(t *testing.T, parent *ParentLease, name string) {
	t.Helper()
	if err := parent.withFD(func(parentFD int) error {
		fd, err := unix.Openat(parentFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("create test file %q: %v", name, err)
	}
}

func stageTestPrecondition(t *testing.T, rootID domain.TrustedRootID, parent *ParentLease, basename string) domain.FilesystemPrecondition {
	t.Helper()
	path := testBytePath(t, "source", basename)
	target, err := domain.NewFilesystemTarget(rootID, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	required, err := RequiredStatMask(domain.ActionDeleteRecreatablePath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	return domain.FilesystemPrecondition{
		Target:   target,
		Required: required,
		Snapshot: stageTestSnapshot(t, parent, basename),
	}
}

func stageTestSnapshot(t *testing.T, parent *ParentLease, basename string) domain.FilesystemSnapshot {
	t.Helper()
	handle, err := OpenTargetHandle(context.Background(), parent, basename)
	if err != nil {
		t.Fatalf("OpenTargetHandle(%q) error = %v", basename, err)
	}
	defer handle.Close()
	required, err := RequiredStatMask(domain.ActionDeleteRecreatablePath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	snapshot, err := handle.Snapshot(required)
	if err != nil {
		t.Fatalf("snapshot %q: %v", basename, err)
	}
	return snapshot
}

func testEntryExists(t *testing.T, parent *ParentLease, basename string) bool {
	t.Helper()
	err := parent.withFD(func(parentFD int) error {
		var stat unix.Stat_t
		return unix.Fstatat(parentFD, basename, &stat, unix.AT_SYMLINK_NOFOLLOW)
	})
	if err == nil {
		return true
	}
	if errors.Is(err, unix.ENOENT) {
		return false
	}
	t.Fatalf("stat test entry %q: %v", basename, err)
	return false
}
