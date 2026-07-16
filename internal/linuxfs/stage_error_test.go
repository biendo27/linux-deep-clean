//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

func TestStageNoReplaceRejectsInvalidNamesAndMismatchedRootsBeforeMove(t *testing.T) {
	t.Run("invalid source basename", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")

		staged, err := StageNoReplace(context.Background(), source, "item/escape", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("StageNoReplace() invalid source basename error = %v, want ErrUnsupported", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("StageNoReplace() returned a staged object for an invalid source basename")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})

	t.Run("invalid staging token", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")

		staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage/token", stageTestAction, expected)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("StageNoReplace() invalid staging token error = %v, want ErrUnsupported", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("StageNoReplace() returned a staged object for an invalid token")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})

	t.Run("mismatched trusted root", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		otherRoot, err := domain.NewTrustedRootID("linuxfs-other-stage-root")
		if err != nil {
			t.Fatalf("NewTrustedRootID() error = %v", err)
		}
		staging.rootID = otherRoot

		staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("StageNoReplace() mismatched root error = %v, want ErrUnsupported", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("StageNoReplace() returned a staged object for a mismatched root")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})
}

func TestStageNoReplaceCollisionLeavesSourceAndExistingTokenUntouched(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	createStageTestFile(t, staging, "stage-token")
	expected := stageTestPrecondition(t, rootID, source, "item")
	reserved := stageTestSnapshot(t, staging, "stage-token")

	staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("StageNoReplace() collision error = %v, want ErrDrifted", err)
	}
	if staged != nil {
		defer staged.Close()
		t.Fatal("StageNoReplace() returned a staged object after an existing-token collision")
	}
	if !testEntryExists(t, source, "item") {
		t.Fatal("StageNoReplace() moved the source despite a no-replace token collision")
	}
	if observed := stageTestSnapshot(t, staging, "stage-token"); observed.Inode != reserved.Inode {
		t.Fatalf("StageNoReplace() changed the reserved token inode = %#v, want %#v", observed.Inode, reserved.Inode)
	}
}

func TestStageNoReplaceUncertainRenameReturnsIndeterminateRecoveryObject(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	createStageTestFile(t, source, "item")
	expected := stageTestPrecondition(t, rootID, source, "item")
	staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
		renameNoReplace: func(int, string, int, string) error {
			return unix.EINTR
		},
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("stageNoReplaceWith() uncertain rename error = %v, want ErrInterrupted", err)
	}
	if staged == nil {
		t.Fatal("stageNoReplaceWith() returned no recovery object for an uncertain rename")
	}
	defer staged.Close()
	if staged.state != stagedStateIndeterminate {
		t.Fatalf("uncertain staged state = %d, want indeterminate", staged.state)
	}
	if staged.RootID() != rootID {
		t.Fatalf("staged RootID() = %q, want %q", staged.RootID(), rootID)
	}
	if staged.Token() != "stage-token" {
		t.Fatalf("staged Token() = %q, want stage-token", staged.Token())
	}
	if err := VerifyStagedIdentity(context.Background(), staged); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("VerifyStagedIdentity() indeterminate error = %v, want ErrInterrupted", err)
	}
	if err := RestoreNoReplace(context.Background(), staged); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("RestoreNoReplace() indeterminate error = %v, want ErrInterrupted", err)
	}
}

func TestRestoreNoReplaceRetainsContentWhenStagedIdentityCannotBeReproved(t *testing.T) {
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

	if err := staging.withFD(func(parentFD int) error {
		if err := unix.Unlinkat(parentFD, "stage-token", 0); err != nil {
			return err
		}
		fd, err := unix.Openat(parentFD, "stage-token", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		if _, err := unix.Write(fd, []byte("replacement")); err != nil {
			_ = unix.Close(fd)
			return err
		}
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("replace staged entry: %v", err)
	}

	err = RestoreNoReplace(context.Background(), staged)
	if !errors.Is(err, ErrRetained) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("RestoreNoReplace() unverified staged identity error = %v, want ErrRetained + ErrDrifted", err)
	}
	if staged.state != stagedStateRetained {
		t.Fatalf("staged state after failed revalidation = %d, want retained", staged.state)
	}
	if testEntryExists(t, source, "item") {
		t.Fatal("RestoreNoReplace() restored an entry whose staged identity was no longer proven")
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("RestoreNoReplace() deleted content after staged identity could not be reproved")
	}
	if err := RestoreNoReplace(context.Background(), staged); !errors.Is(err, ErrRetained) {
		t.Fatalf("RestoreNoReplace() retained retry error = %v, want ErrRetained", err)
	}
}

func TestRestoreNoReplaceLeavesIndeterminateWhenPostMoveConfirmationFails(t *testing.T) {
	for _, test := range []struct {
		name  string
		hooks restoreHooks
	}{
		{
			name: "restored identity cannot be reproved",
			hooks: restoreHooks{
				verifyRestored: func(context.Context, *StagedObject) error {
					return unix.EIO
				},
			},
		},
		{
			name: "restored directory entries cannot be synced",
			hooks: restoreHooks{
				syncDirectory: func(int) error {
					return unix.EIO
				},
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
			staged, err := StageNoReplace(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected)
			if err != nil {
				t.Fatalf("StageNoReplace() error = %v", err)
			}
			defer staged.Close()

			if err := restoreNoReplaceWith(context.Background(), staged, test.hooks); !errors.Is(err, ErrInterrupted) {
				t.Fatalf("restoreNoReplaceWith() post-move confirmation error = %v, want ErrInterrupted", err)
			}
			if staged.state != stagedStateIndeterminate {
				t.Fatalf("state after failed post-move confirmation = %d, want indeterminate", staged.state)
			}
			if !testEntryExists(t, source, "item") {
				t.Fatal("post-move confirmation failure removed the restored source entry")
			}
			if testEntryExists(t, staging, "stage-token") {
				t.Fatal("post-move confirmation failure unexpectedly recreated the staging token")
			}
		})
	}
}

func TestRestoreNoReplaceSyncsBothParentsBeforeReportingRestored(t *testing.T) {
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

	syncCalls := 0
	if err := restoreNoReplaceWith(context.Background(), staged, restoreHooks{
		syncDirectory: func(int) error {
			syncCalls++
			return nil
		},
	}); err != nil {
		t.Fatalf("restoreNoReplaceWith() error = %v", err)
	}
	if syncCalls != 2 {
		t.Fatalf("restoration synced %d parent directories, want 2", syncCalls)
	}
	if staged.state != stagedStateRestored {
		t.Fatalf("state after confirmed restoration = %d, want restored", staged.state)
	}
}

func TestStagedObjectClosePreventsFurtherRestorationButPreservesRecoveryIdentity(t *testing.T) {
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
	if staged.RootID() != rootID || staged.Token() != "stage-token" {
		t.Fatalf("staged recovery identity = root %q, token %q; want root %q, token stage-token", staged.RootID(), staged.Token(), rootID)
	}
	if err := staged.Close(); err != nil {
		t.Fatalf("StagedObject.Close() error = %v", err)
	}
	if err := staged.Close(); err != nil {
		t.Fatalf("StagedObject.Close() second error = %v", err)
	}
	if staged.RootID() != rootID || staged.Token() != "stage-token" {
		t.Fatalf("closed staged recovery identity = root %q, token %q; want preserved identity", staged.RootID(), staged.Token())
	}
	if err := RestoreNoReplace(context.Background(), staged); !errors.Is(err, ErrDrifted) {
		t.Fatalf("RestoreNoReplace() closed object error = %v, want ErrDrifted", err)
	}
	if err := VerifyStagedIdentity(context.Background(), staged); !errors.Is(err, ErrDrifted) {
		t.Fatalf("VerifyStagedIdentity() closed object error = %v, want ErrDrifted", err)
	}
	if !testEntryExists(t, staging, "stage-token") {
		t.Fatal("closing a staged object unexpectedly removed its retained entry")
	}
}

func TestStageFailureClassifiersRemainFailClosed(t *testing.T) {
	for _, test := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{name: "existing token", err: unix.EEXIST},
		{name: "nonempty token", err: unix.ENOTEMPTY},
		{name: "cross device", err: unix.EXDEV, unsupported: true},
		{name: "missing renameat2", err: unix.ENOSYS, unsupported: true},
		{name: "unsupported rename flags", err: unix.EOPNOTSUPP, unsupported: true},
		{name: "unexpected I/O failure", err: unix.EIO},
	} {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyRenameFailure(test.err)
			if test.unsupported {
				if !errors.Is(classified, ErrUnsupported) {
					t.Fatalf("classifyRenameFailure(%v) = %v, want ErrUnsupported", test.err, classified)
				}
				return
			}
			if !errors.Is(classified, ErrDrifted) {
				t.Fatalf("classifyRenameFailure(%v) = %v, want ErrDrifted", test.err, classified)
			}
		})
	}

	if !renameFailureMayHaveMoved(unix.EINTR) || !renameFailureMayHaveMoved(unix.EAGAIN) {
		t.Fatal("interrupted rename outcomes must be treated as potentially moved")
	}
	if renameFailureMayHaveMoved(unix.EIO) {
		t.Fatal("unrelated rename failure was treated as a potentially moved outcome")
	}
	if err := classifyUncertainRename(unix.EINTR); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("classifyUncertainRename() = %v, want ErrInterrupted", err)
	}
	if err := retainedStageError(unix.EIO); !errors.Is(err, ErrRetained) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("retainedStageError() = %v, want ErrRetained + ErrDrifted", err)
	}
}

func assertStageRequestDidNotMove(t *testing.T, source, staging *ParentLease, sourceBasename, token string) {
	t.Helper()
	if !testEntryExists(t, source, sourceBasename) {
		t.Fatalf("StageNoReplace() moved source %q before rejecting the request", sourceBasename)
	}
	if testEntryExists(t, staging, token) {
		t.Fatalf("StageNoReplace() created staging token %q before rejecting the request", token)
	}
}
