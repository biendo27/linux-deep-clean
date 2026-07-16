//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRemoveStagedTreeRetainsWithoutExclusiveRemovalAuthority(t *testing.T) {
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

	if err := RemoveStagedTree(context.Background(), staged); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RemoveStagedTree() without exclusive removal authority error = %v, want ErrUnsupported", err)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("unqualified removal deleted retained staged content")
	}
}

func TestRemoveStagedTreeRemovesVerifiedDirectoryOnly(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestDirectory(t, source, "item")
	itemFD := openStageTestDirectory(t, source, "item")
	createRawTestFile(t, itemFD, "child")
	if err := unix.Mkdirat(itemFD, "nested", 0o700); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	nestedFD, err := unix.Openat(itemFD, "nested", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open nested directory: %v", err)
	}
	createRawTestFile(t, nestedFD, "grandchild")
	if err := unix.Close(nestedFD); err != nil {
		t.Fatalf("close nested directory: %v", err)
	}
	if err := unix.Close(itemFD); err != nil {
		t.Fatalf("close staged source directory: %v", err)
	}

	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()
	authorizeStagedRemovalForTest(t, staged)

	if err := RemoveStagedTree(context.Background(), staged); err != nil {
		t.Fatalf("RemoveStagedTree() error = %v", err)
	}
	if testEntryExists(t, source, "item") {
		t.Fatal("source entry reappeared after staged removal")
	}
	if testEntryExists(t, staging, "stage-token") {
		t.Fatal("verified staged directory remains after removal")
	}
}

func TestRemoveStagedTreeRejectsSpecialBeforeDeletingSiblings(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestDirectory(t, source, "item")
	itemFD := openStageTestDirectory(t, source, "item")
	createRawTestFile(t, itemFD, "safe")
	if err := unix.Mkfifoat(itemFD, "fifo", 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	if err := unix.Close(itemFD); err != nil {
		t.Fatalf("close staged source directory: %v", err)
	}

	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()
	authorizeStagedRemovalForTest(t, staged)

	if err := RemoveStagedTree(context.Background(), staged); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RemoveStagedTree() special-file error = %v, want ErrUnsupported", err)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("special-file rejection deleted the staged root")
	}
	if !testChildEntryExists(t, staging, "stage-token", "safe") {
		t.Fatal("special-file preflight deleted an otherwise-safe sibling")
	}
}

func TestRemoveStagedTreeRejectsRetainedAndCanceledObjectsWithoutDeletion(t *testing.T) {
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

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RemoveStagedTree(canceledContext, staged); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("RemoveStagedTree() canceled error = %v, want ErrInterrupted", err)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("canceled removal deleted the staged entry")
	}

	if err := RestoreNoReplace(context.Background(), staged); err != nil {
		t.Fatalf("RestoreNoReplace() after canceled pre-effect removal error = %v", err)
	}
	if testEntryExists(t, staging, "stage-token") {
		t.Fatal("verified staged entry remains after explicit restore")
	}
}

func TestRemoveStagedTreeLeavesOtherHardLinkUntouched(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	if err := source.withFD(func(parentFD int) error {
		return unix.Linkat(parentFD, "item", parentFD, "other-link", 0)
	}); err != nil {
		t.Fatalf("create hard link: %v", err)
	}
	otherBefore := stageTestSnapshot(t, source, "other-link")
	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() error = %v", err)
	}
	defer staged.Close()
	authorizeStagedRemovalForTest(t, staged)

	if err := RemoveStagedTree(context.Background(), staged); err != nil {
		t.Fatalf("RemoveStagedTree() hard-link error = %v", err)
	}
	otherAfter := stageTestSnapshot(t, source, "other-link")
	if otherAfter.Inode != otherBefore.Inode {
		t.Fatalf("staged removal changed other hard-link inode = %#v, want %#v", otherAfter.Inode, otherBefore.Inode)
	}
	if otherAfter.LinkCount.Value != 1 {
		t.Fatalf("other hard-link count after staged removal = %d, want 1", otherAfter.LinkCount.Value)
	}
}

func createStageTestDirectory(t *testing.T, parent *ParentLease, name string) {
	t.Helper()
	if err := parent.withFD(func(parentFD int) error {
		return unix.Mkdirat(parentFD, name, 0o700)
	}); err != nil {
		t.Fatalf("create test directory %q: %v", name, err)
	}
}

func openStageTestDirectory(t *testing.T, parent *ParentLease, name string) int {
	t.Helper()
	var directoryFD int
	if err := parent.withFD(func(parentFD int) error {
		fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		directoryFD = fd
		return nil
	}); err != nil {
		t.Fatalf("open test directory %q: %v", name, err)
	}
	return directoryFD
}

func createRawTestFile(t *testing.T, parentFD int, name string) {
	t.Helper()
	fd, err := unix.Openat(parentFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create raw test file %q: %v", name, err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close raw test file %q: %v", name, err)
	}
}

func testChildEntryExists(t *testing.T, parent *ParentLease, directory string, basename string) bool {
	t.Helper()
	directoryFD := openStageTestDirectory(t, parent, directory)
	defer unix.Close(directoryFD)
	var stat unix.Stat_t
	err := unix.Fstatat(directoryFD, basename, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return true
	}
	if errors.Is(err, unix.ENOENT) {
		return false
	}
	t.Fatalf("stat test child %q/%q: %v", directory, basename, err)
	return false
}
