//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

func TestRemoveStagedTreeLeavesVerifiedObjectRestorableWhenPreEffectSyncFails(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
	defer staged.Close()

	hooks := defaultRemovalHooks()
	hooks.syncDirectory = func(int) error { return unix.EIO }
	if err := removeStagedTreeWith(context.Background(), staged, hooks); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("removeStagedTreeWith() pre-effect sync error = %v, want ErrInterrupted", err)
	}
	if staged.state != stagedStateVerified {
		t.Fatalf("state after pre-effect sync failure = %d, want verified", staged.state)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("pre-effect sync failure removed staged content")
	}
	if err := RestoreNoReplace(context.Background(), staged); err != nil {
		t.Fatalf("RestoreNoReplace() after pre-effect sync failure = %v", err)
	}
	if !testEntryExists(t, source, "item") {
		t.Fatal("restore after pre-effect sync failure did not return the source entry")
	}
}

func TestRemoveStagedTreeMarksUnlinkFailureIndeterminate(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
	defer staged.Close()

	hooks := defaultRemovalHooks()
	hooks.unlink = func(int, string, int) error { return unix.EIO }
	if err := removeStagedTreeWith(context.Background(), staged, hooks); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("removeStagedTreeWith() uncertain unlink error = %v, want ErrInterrupted", err)
	}
	if staged.state != stagedStateIndeterminate {
		t.Fatalf("state after uncertain unlink = %d, want indeterminate", staged.state)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("simulated uncertain unlink removed staged content")
	}
	if err := RestoreNoReplace(context.Background(), staged); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("RestoreNoReplace() after uncertain unlink = %v, want ErrInterrupted", err)
	}
}

func TestRemoveStagedTreeMarksPostEffectSyncFailureIndeterminate(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
	defer staged.Close()

	hooks := defaultRemovalHooks()
	syncCalls := 0
	hooks.syncDirectory = func(int) error {
		syncCalls++
		if syncCalls == 3 {
			return unix.EIO
		}
		return nil
	}
	if err := removeStagedTreeWith(context.Background(), staged, hooks); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("removeStagedTreeWith() post-effect sync error = %v, want ErrInterrupted", err)
	}
	if staged.state != stagedStateIndeterminate {
		t.Fatalf("state after post-effect sync failure = %d, want indeterminate", staged.state)
	}
	if testEntryExists(t, staging, "stage-token") {
		t.Fatal("successful unlink remained visible after post-effect sync failure")
	}
}

func TestRemoveStagedTreeReclassifiesEntriesAfterPreflight(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestDirectory(t, source, "item")
	itemFD := openStageTestDirectory(t, source, "item")
	createRawTestFile(t, itemFD, "safe")
	if err := unix.Close(itemFD); err != nil {
		t.Fatalf("close source item: %v", err)
	}
	staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
	defer staged.Close()

	hooks := defaultRemovalHooks()
	readCalls := 0
	originalReadNames := hooks.readNames
	hooks.readNames = func(directoryFD int) ([]string, error) {
		readCalls++
		names, err := originalReadNames(directoryFD)
		if err != nil || readCalls != 2 {
			return names, err
		}
		if err := unix.Unlinkat(directoryFD, "safe", 0); err != nil {
			return nil, err
		}
		if err := unix.Mkfifoat(directoryFD, "safe", 0o600); err != nil {
			return nil, err
		}
		return names, nil
	}
	if err := removeStagedTreeWith(context.Background(), staged, hooks); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("removeStagedTreeWith() reclassified FIFO error = %v, want ErrUnsupported", err)
	}
	if staged.state != stagedStateVerified {
		t.Fatalf("state after pre-effect reclassification rejection = %d, want verified", staged.state)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("reclassification failure removed staged root")
	}
	if !testChildEntryExists(t, staging, "stage-token", "safe") {
		t.Fatal("reclassification failure removed replacement special entry")
	}
}

func TestRemovalSafetyHelpersFailClosed(t *testing.T) {
	if err := (removalHooks{}).validate(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("empty removal hooks error = %v, want ErrUnsupported", err)
	}
	if _, err := readDirectoryNames(-1); !errors.Is(err, ErrDrifted) {
		t.Fatalf("readDirectoryNames(-1) error = %v, want ErrDrifted", err)
	}
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "unsupported sync", err: unix.EOPNOTSUPP, want: ErrUnsupported},
		{name: "interrupted sync", err: unix.EIO, want: ErrInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			hooks := defaultRemovalHooks()
			hooks.syncDirectory = func(int) error { return test.err }
			if err := syncRemovalDirectory(7, hooks); !errors.Is(err, test.want) {
				t.Fatalf("syncRemovalDirectory() error = %v, want %v", err, test.want)
			}
		})
	}
	if err := indeterminateRemovalError(unix.EIO); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("indeterminateRemovalError() = %v, want ErrInterrupted", err)
	}
}

func stageVerifiedTestObject(t *testing.T, source, staging *ParentLease, rootID domain.TrustedRootID, basename string) *StagedObject {
	t.Helper()
	expected := stageTestPrecondition(t, rootID, source, basename)
	staged, err := StageNoReplace(context.Background(), source, basename, stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if err != nil {
		t.Fatalf("StageNoReplace() = %v", err)
	}
	authorizeStagedRemovalForTest(t, staged)
	return staged
}

// authorizeStagedRemovalForTest deliberately models a future exclusive
// engine/helper-owned deletion authority. It exists only in test code so the
// descriptor walker can be exercised without making the production 0700
// staging lease claim to exclude other processes with the same UID.
func authorizeStagedRemovalForTest(t *testing.T, staged *StagedObject) {
	t.Helper()
	if staged == nil {
		t.Fatal("cannot authorize a nil staged object")
	}
	staged.mu.Lock()
	defer staged.mu.Unlock()
	if staged.state != stagedStateVerified {
		t.Fatalf("cannot authorize staged object in state %d", staged.state)
	}
	staged.removalAuthorized = true
}
