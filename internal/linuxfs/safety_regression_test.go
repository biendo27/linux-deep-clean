//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

func TestSafetyRegressionPrivateStagingLeaseRejectsAuthorityDrift(t *testing.T) {
	t.Run("nil or unbound parent", func(t *testing.T) {
		if _, err := newPrivateStagingLease(nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("newPrivateStagingLease(nil) error = %v, want ErrUnsupported", err)
		}
		if _, err := (&PrivateStagingLease{}).duplicate(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("empty PrivateStagingLease.duplicate() error = %v, want ErrUnsupported", err)
		}

		rootFD := openTestDirectory(t)
		defer unix.Close(rootFD)
		makeTestDirectory(t, rootFD, "private")
		parent, _, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "private", "token"), unix.Openat2)
		if err != nil {
			t.Fatalf("resolve private staging parent: %v", err)
		}
		defer parent.Close()

		if _, err := newPrivateStagingLease(parent); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("newPrivateStagingLease(unbound parent) error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("permissions change after qualification", func(t *testing.T) {
		rootFD, source, staging, _ := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		lease := stageTestPrivateStaging(t, staging)
		if err := staging.withFD(func(fd int) error { return unix.Fchmod(fd, 0o750) }); err != nil {
			t.Fatalf("relax private staging permissions: %v", err)
		}
		if _, err := lease.duplicate(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("private staging duplicate after permission drift = %v, want ErrUnsupported", err)
		}
	})

	t.Run("trusted root or descriptor lifetime changes", func(t *testing.T) {
		rootFD, source, staging, _ := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		lease := stageTestPrivateStaging(t, staging)
		otherRoot, err := domain.NewTrustedRootID("linuxfs-private-stage-other-root")
		if err != nil {
			t.Fatalf("NewTrustedRootID() error = %v", err)
		}
		staging.rootID = otherRoot
		if _, err := lease.duplicate(); !errors.Is(err, ErrDrifted) {
			t.Fatalf("private staging duplicate after root drift = %v, want ErrDrifted", err)
		}

		staging.rootID = lease.rootID
		if err := staging.Close(); err != nil {
			t.Fatalf("close private staging parent: %v", err)
		}
		if _, err := lease.duplicate(); !errors.Is(err, ErrDrifted) {
			t.Fatalf("private staging duplicate after parent close = %v, want ErrDrifted", err)
		}
	})
}

func TestSafetyRegressionStageHooksFailClosedAtEveryBoundary(t *testing.T) {
	t.Run("missing rename implementation", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("stageNoReplaceWith() missing rename error = %v, want ErrUnsupported", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("stageNoReplaceWith() returned a staged object without rename authority")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})

	t.Run("definite rename failure leaves source intact", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
			renameNoReplace: func(int, string, int, string) error { return unix.EIO },
		})
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("stageNoReplaceWith() definite rename failure = %v, want ErrDrifted", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("stageNoReplaceWith() returned a recovery object for a definite non-move")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})

	t.Run("source cannot also be private staging parent", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		selfStaging, err := newPrivateStagingLease(source)
		if err != nil {
			t.Fatalf("newPrivateStagingLease(source) error = %v", err)
		}
		staged, err := stageNoReplaceWith(context.Background(), source, "item", selfStaging, "stage-token", stageTestAction, expected, stageHooks{
			renameNoReplace: renameNoReplace,
		})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("stageNoReplaceWith() same-parent staging error = %v, want ErrUnsupported", err)
		}
		if staged != nil {
			defer staged.Close()
			t.Fatal("stageNoReplaceWith() permitted a source directory to stage into itself")
		}
		assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
	})

	t.Run("successful move syncs both directory boundaries", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		syncCalls := 0
		staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
			renameNoReplace: renameNoReplace,
			syncDirectory: func(int) error {
				syncCalls++
				return nil
			},
		})
		if err != nil {
			t.Fatalf("stageNoReplaceWith() durable move error = %v", err)
		}
		defer staged.Close()
		if syncCalls != 2 {
			t.Fatalf("stageNoReplaceWith() synced %d boundaries, want 2", syncCalls)
		}
		if staged.state != stagedStateVerified {
			t.Fatalf("state after durably staged move = %d, want verified", staged.state)
		}
	})

	t.Run("second durability boundary failure is indeterminate", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		expected := stageTestPrecondition(t, rootID, source, "item")
		syncCalls := 0
		staged, err := stageNoReplaceWith(context.Background(), source, "item", stageTestPrivateStaging(t, staging), "stage-token", stageTestAction, expected, stageHooks{
			renameNoReplace: renameNoReplace,
			syncDirectory: func(int) error {
				syncCalls++
				if syncCalls == 2 {
					return unix.EIO
				}
				return nil
			},
		})
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("stageNoReplaceWith() second sync failure = %v, want ErrInterrupted", err)
		}
		if staged == nil {
			t.Fatal("stageNoReplaceWith() discarded a moved object after a durability uncertainty")
		}
		defer staged.Close()
		if syncCalls != 2 || staged.state != stagedStateIndeterminate {
			t.Fatalf("state after second sync failure = calls %d, state %d; want calls 2 and indeterminate", syncCalls, staged.state)
		}
		if testEntryExists(t, source, "item") || !testEntryExists(t, staging, "stage-token") {
			t.Fatal("durability uncertainty did not retain the moved object exclusively in staging")
		}
	})
}

func TestSafetyRegressionRestoreHookOutcomesRetainOrReconcile(t *testing.T) {
	for _, test := range []struct {
		name      string
		renameErr error
		wantErr   error
		wantState stagedState
	}{
		{name: "occupied source is retained", renameErr: unix.EEXIST, wantErr: ErrRetained, wantState: stagedStateRetained},
		{name: "cross-device result is retained", renameErr: unix.EXDEV, wantErr: ErrRetained, wantState: stagedStateRetained},
		{name: "interrupted result is indeterminate", renameErr: unix.EAGAIN, wantErr: ErrInterrupted, wantState: stagedStateIndeterminate},
		{name: "unknown result is indeterminate", renameErr: unix.EIO, wantErr: ErrInterrupted, wantState: stagedStateIndeterminate},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, source, staging, rootID := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer source.Close()
			defer staging.Close()

			createStageTestFile(t, source, "item")
			staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
			defer staged.Close()

			err := restoreNoReplaceWith(context.Background(), staged, restoreHooks{
				renameNoReplace: func(int, string, int, string) error { return test.renameErr },
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("restoreNoReplaceWith() error = %v, want %v", err, test.wantErr)
			}
			if staged.state != test.wantState {
				t.Fatalf("state after restore outcome = %d, want %d", staged.state, test.wantState)
			}
			if testEntryExists(t, source, "item") || !testEntryExists(t, staging, "stage-token") {
				t.Fatal("failed restore outcome moved or deleted staged content")
			}
		})
	}

	t.Run("canceled restore leaves verified recovery object untouched", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestFile(t, source, "item")
		staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
		defer staged.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := RestoreNoReplace(ctx, staged); !errors.Is(err, ErrInterrupted) {
			t.Fatalf("RestoreNoReplace() canceled error = %v, want ErrInterrupted", err)
		}
		if staged.state != stagedStateVerified || !testEntryExists(t, staging, "stage-token") {
			t.Fatal("canceled restore changed the verified staged recovery object")
		}
	})
}

func TestSafetyRegressionDescriptorAuthorityRejectsNilClosedAndUnsafeCallbacks(t *testing.T) {
	if _, err := OpenTargetHandle(context.Background(), nil, "item"); !errors.Is(err, ErrDrifted) {
		t.Fatalf("OpenTargetHandle(nil parent) error = %v, want ErrDrifted", err)
	}
	var nilParent *ParentLease
	if nilParent.RootID() != "" {
		t.Fatalf("nil ParentLease.RootID() = %q, want empty", nilParent.RootID())
	}
	if err := nilParent.withFD(func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("nil ParentLease.withFD() error = %v, want ErrDrifted", err)
	}
	if _, err := nilParent.duplicate(); !errors.Is(err, ErrDrifted) {
		t.Fatalf("nil ParentLease.duplicate() error = %v, want ErrDrifted", err)
	}
	if err := syncParentLeaseWith(nil, func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("syncParentLeaseWith(nil) error = %v, want ErrDrifted", err)
	}

	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	fileFD, err := unix.Openat(rootFD, "item", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create target file: %v", err)
	}
	if err := unix.Close(fileFD); err != nil {
		t.Fatalf("close target file: %v", err)
	}
	parent, basename, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "item"), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve target parent: %v", err)
	}
	handle, err := OpenTargetHandle(context.Background(), parent, basename)
	if err != nil {
		t.Fatalf("OpenTargetHandle() error = %v", err)
	}
	if err := handle.withFD(nil); !errors.Is(err, ErrDrifted) {
		t.Fatalf("TargetHandle.withFD(nil) error = %v, want ErrDrifted", err)
	}
	var nilHandle *TargetHandle
	if _, err := nilHandle.Snapshot(domain.FilesystemFieldType); !errors.Is(err, ErrDrifted) {
		t.Fatalf("nil TargetHandle.Snapshot() error = %v, want ErrDrifted", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close target handle: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close target handle twice: %v", err)
	}
	if _, err := handle.Snapshot(domain.FilesystemFieldType); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed TargetHandle.Snapshot() error = %v, want ErrDrifted", err)
	}
	if err := handle.withFD(func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed TargetHandle.withFD() error = %v, want ErrDrifted", err)
	}

	if err := parent.withFD(nil); !errors.Is(err, ErrDrifted) {
		t.Fatalf("ParentLease.withFD(nil) error = %v, want ErrDrifted", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatalf("close parent: %v", err)
	}
	if _, err := parent.duplicate(); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed ParentLease.duplicate() error = %v, want ErrDrifted", err)
	}
	if _, err := OpenTargetHandle(context.Background(), parent, basename); !errors.Is(err, ErrDrifted) {
		t.Fatalf("OpenTargetHandle(closed parent) error = %v, want ErrDrifted", err)
	}
}

func TestSafetyRegressionSnapshotRejectsEveryDriftedDestructiveFact(t *testing.T) {
	required, err := RequiredStatMask(domain.ActionDeleteRecreatablePath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	expected := testFilesystemPrecondition(t, required, testKnownSnapshot())

	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{name: "device major", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMajor.Value++ }},
		{name: "device minor", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMinor.Value++ }},
		{name: "inode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Inode.Value++ }},
		{name: "file type", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Type = domain.FileTypeDirectory }},
		{name: "uid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.UID.Value++ }},
		{name: "gid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.GID.Value++ }},
		{name: "mode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Mode.Value++ }},
		{name: "link count", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.LinkCount.Value++ }},
		{name: "size", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Size.Value++ }},
		{name: "modified time", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.ModifiedAt.Value++ }},
		{name: "changed time", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.ChangedAt.Value++ }},
		{name: "mount ID", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.MountID.Value++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := testKnownSnapshot()
			test.mutate(&observed)
			if err := ComparePrecondition(expected, observed); !errors.Is(err, ErrDrifted) {
				t.Fatalf("ComparePrecondition() after %s drift = %v, want ErrDrifted", test.name, err)
			}
		})
	}

	observed := testKnownSnapshot()
	observed.Size.Known = false
	if err := ComparePrecondition(expected, observed); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ComparePrecondition() with missing required fact = %v, want ErrUnsupported", err)
	}
	invalidExpected := expected
	invalidExpected.Required = domain.FilesystemFieldMask(1 << 31)
	if err := ComparePrecondition(invalidExpected, testKnownSnapshot()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ComparePrecondition() with unknown required field = %v, want ErrUnsupported", err)
	}
	if _, err := SnapshotFD(-1, required); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SnapshotFD(-1) error = %v, want ErrUnsupported", err)
	}
}

func TestSafetyRegressionStatxAndOpenat2ClassifiersFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "statx unavailable", err: unix.ENOSYS, want: ErrUnsupported},
		{name: "statx invalid flags", err: unix.EINVAL, want: ErrUnsupported},
		{name: "statx operation unsupported", err: unix.EOPNOTSUPP, want: ErrUnsupported},
		{name: "statx interrupted", err: unix.EINTR, want: ErrInterrupted},
		{name: "statx io uncertainty", err: unix.EIO, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := classifyStatxError(test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyStatxError(%v) = %v, want %v", test.err, err, test.want)
			}
		})
	}
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "openat2 unavailable", err: unix.ENOSYS, want: ErrUnsupported},
		{name: "openat2 oversized contract", err: unix.E2BIG, want: ErrUnsupported},
		{name: "mount boundary", err: unix.EXDEV, want: ErrUnsupported},
		{name: "permission or lookup uncertainty", err: unix.EACCES, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := classifyOpenat2Error(test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyOpenat2Error(%v) = %v, want %v", test.err, err, test.want)
			}
		})
	}
	for _, timestamp := range []unix.StatxTimestamp{
		{Nsec: 1_000_000_000},
		{Sec: math.MaxInt64},
	} {
		if _, err := statxUnixNano(timestamp); err == nil {
			t.Fatalf("statxUnixNano(%+v) accepted a timestamp outside int64 Unix nanoseconds", timestamp)
		}
	}
	var invalidTime unix.Statx_t
	invalidTime.Mask = unix.STATX_MTIME
	invalidTime.Mtime = unix.StatxTimestamp{Nsec: 1_000_000_000}
	if _, err := snapshotFromStatx(invalidTime, domain.FilesystemFieldModifiedAt); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("snapshotFromStatx() invalid timestamp error = %v, want ErrUnsupported", err)
	}
}

func TestSafetyRegressionResolverRejectsMissingDependenciesAndUnsafePaths(t *testing.T) {
	path := testBytePath(t, "parent", "target")
	rootID, err := domain.NewTrustedRootID("linuxfs-resolver-regression-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}

	var closed []int
	closeRecorder := func(fd int) error {
		closed = append(closed, fd)
		return nil
	}
	if _, _, err := resolveParentFromOwnedRootFDForRootWithClose(nil, 401, rootID, path, unix.Openat2, closeRecorder); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("resolve with nil context error = %v, want ErrInterrupted", err)
	}
	if len(closed) != 1 || closed[0] != 401 {
		t.Fatalf("nil-context resolver closed %v, want [401]", closed)
	}

	closed = nil
	if _, _, err := resolveParentFromOwnedRootFDForRootWithClose(context.Background(), 402, rootID, path, nil, closeRecorder); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("resolve with nil openat2 implementation error = %v, want ErrUnsupported", err)
	}
	if len(closed) != 1 || closed[0] != 402 {
		t.Fatalf("nil-open resolver closed %v, want [402]", closed)
	}

	closed = nil
	if _, _, err := resolveParentFromOwnedRootFDForRootWithClose(context.Background(), 403, rootID, pathbytes.BytePath{}, unix.Openat2, closeRecorder); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("resolve with invalid byte path error = %v, want ErrUnsupported", err)
	}
	if len(closed) != 1 || closed[0] != 403 {
		t.Fatalf("invalid-path resolver closed %v, want [403]", closed)
	}

	if _, _, err := resolveParentFromOwnedRootFDForRootWithClose(context.Background(), 404, rootID, path, unix.Openat2, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("resolve with nil close implementation error = %v, want ErrUnsupported", err)
	}
	if _, _, err := ResolveParent(context.Background(), nil, path); !errors.Is(err, ErrDrifted) {
		t.Fatalf("ResolveParent(nil root) error = %v, want ErrDrifted", err)
	}
	if _, _, err := resolveParentWithRootFD(context.Background(), -1, path, unix.Openat2); !errors.Is(err, ErrDrifted) {
		t.Fatalf("resolveParentWithRootFD(-1) error = %v, want ErrDrifted", err)
	}
	if _, _, err := pathComponentsAndBasename(pathbytes.BytePath{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("pathComponentsAndBasename(zero path) error = %v, want ErrUnsupported", err)
	}
}

func TestSafetyRegressionRemovalStateGatesAndChildDurabilityFailure(t *testing.T) {
	if err := RemoveStagedTree(context.Background(), nil); !errors.Is(err, ErrDrifted) {
		t.Fatalf("RemoveStagedTree(nil) error = %v, want ErrDrifted", err)
	}

	t.Run("non-verified objects cannot be removed", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()
		createStageTestFile(t, source, "item")
		staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
		defer staged.Close()
		staged.state = stagedStateRetained
		if err := RemoveStagedTree(context.Background(), staged); !errors.Is(err, ErrRetained) {
			t.Fatalf("RemoveStagedTree(retained) error = %v, want ErrRetained", err)
		}
		if !testEntryExists(t, staging, "stage-token") {
			t.Fatal("refusing retained removal deleted staged content")
		}

		if err := staged.Close(); err != nil {
			t.Fatalf("close retained staged object: %v", err)
		}
		if err := RemoveStagedTree(context.Background(), staged); !errors.Is(err, ErrDrifted) {
			t.Fatalf("RemoveStagedTree(closed) error = %v, want ErrDrifted", err)
		}
	})

	t.Run("failure after a child unlink is indeterminate", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestDirectory(t, source, "item")
		itemFD := openStageTestDirectory(t, source, "item")
		createRawTestFile(t, itemFD, "child")
		if err := unix.Close(itemFD); err != nil {
			t.Fatalf("close source item directory: %v", err)
		}
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
			t.Fatalf("removeStagedTreeWith() child durability error = %v, want ErrInterrupted", err)
		}
		if staged.state != stagedStateIndeterminate {
			t.Fatalf("state after child durability error = %d, want indeterminate", staged.state)
		}
		if !testEntryExists(t, staging, "stage-token") || testChildEntryExists(t, staging, "stage-token", "child") {
			t.Fatal("child durability uncertainty did not preserve the exact known partial state")
		}
	})

	t.Run("invalid second-pass name is rejected before effects", func(t *testing.T) {
		rootFD, source, staging, rootID := stageTestDirectories(t)
		defer unix.Close(rootFD)
		defer source.Close()
		defer staging.Close()

		createStageTestDirectory(t, source, "item")
		staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
		defer staged.Close()
		hooks := defaultRemovalHooks()
		hooks.readNames = func(int) ([]string, error) { return []string{".."}, nil }
		if err := removeStagedTreeWith(context.Background(), staged, hooks); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("removeStagedTreeWith() invalid name error = %v, want ErrUnsupported", err)
		}
		if staged.state != stagedStateVerified || !testEntryExists(t, staging, "stage-token") {
			t.Fatal("invalid preflight name had a removal effect")
		}
	})
}

func TestSafetyRegressionPrivateIdentityAndStateMachinesFailClosed(t *testing.T) {
	expected := testKnownSnapshot()
	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{name: "device", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMajor.Value++ }},
		{name: "inode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Inode.Value++ }},
		{name: "type", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Type = domain.FileTypeDirectory }},
		{name: "ownership", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.GID.Value++ }},
		{name: "mode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Mode.Value++ }},
		{name: "mount", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.MountID.Value++ }},
	} {
		t.Run("private staging "+test.name, func(t *testing.T) {
			observed := expected
			test.mutate(&observed)
			if err := comparePrivateStagingIdentity(expected, observed); !errors.Is(err, ErrDrifted) {
				t.Fatalf("comparePrivateStagingIdentity() after %s drift = %v, want ErrDrifted", test.name, err)
			}
		})
	}

	for _, test := range []struct {
		name  string
		state stagedState
		want  error
	}{
		{name: "removing verification", state: stagedStateRemoving, want: ErrInterrupted},
		{name: "removed verification", state: stagedStateRemoved, want: ErrDrifted},
		{name: "retained verification", state: stagedStateRetained, want: ErrRetained},
		{name: "restored verification", state: stagedStateRestored, want: ErrDrifted},
		{name: "indeterminate verification", state: stagedStateIndeterminate, want: ErrInterrupted},
		{name: "closed verification", state: stagedStateClosed, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := VerifyStagedIdentity(context.Background(), &StagedObject{state: test.state}); !errors.Is(err, test.want) {
				t.Fatalf("VerifyStagedIdentity(%d) error = %v, want %v", test.state, err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name  string
		state stagedState
		want  error
	}{
		{name: "closed removal", state: stagedStateClosed, want: ErrDrifted},
		{name: "removed removal", state: stagedStateRemoved, want: ErrDrifted},
		{name: "indeterminate removal", state: stagedStateIndeterminate, want: ErrInterrupted},
		{name: "concurrent removal", state: stagedStateRemoving, want: ErrInterrupted},
		{name: "retained removal", state: stagedStateRetained, want: ErrRetained},
		{name: "restored removal", state: stagedStateRestored, want: ErrRetained},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := removeStagedTreeWith(context.Background(), &StagedObject{state: test.state}, defaultRemovalHooks()); !errors.Is(err, test.want) {
				t.Fatalf("removeStagedTreeWith(%d) error = %v, want %v", test.state, err, test.want)
			}
		})
	}
}

func TestSafetyRegressionStagingSnapshotsRejectUnsafeDescriptorTargets(t *testing.T) {
	rootFD, source, staging, _ := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()

	required, err := RequiredStatMask(stageTestAction)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	if _, err := snapshotNamedTarget(nil, -1, "item", required); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("snapshotNamedTarget(nil context) error = %v, want ErrInterrupted", err)
	}
	if _, err := snapshotNamedTarget(context.Background(), -1, "item/escape", required); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("snapshotNamedTarget(invalid basename) error = %v, want ErrUnsupported", err)
	}

	if err := source.withFD(func(parentFD int) error {
		return unix.Mkfifoat(parentFD, "fifo", 0o600)
	}); err != nil {
		t.Fatalf("create FIFO target: %v", err)
	}
	if err := source.withFD(func(parentFD int) error {
		_, err := snapshotNamedTarget(context.Background(), parentFD, "fifo", required)
		return err
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("snapshotNamedTarget(FIFO) error = %v, want ErrUnsupported", err)
	}

	fileFD, err := unix.Openat(rootFD, "ordinary-file", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create ordinary file: %v", err)
	}
	if err := unix.Close(fileFD); err != nil {
		t.Fatalf("close ordinary file: %v", err)
	}
	fileFD, err = unix.Openat(rootFD, "ordinary-file", unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatalf("open ordinary file as O_PATH: %v", err)
	}
	defer unix.Close(fileFD)
	if _, err := snapshotPrivateStagingDirectory(fileFD); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("snapshotPrivateStagingDirectory(file) error = %v, want ErrUnsupported", err)
	}

	stagingFD, err := staging.duplicate()
	if err != nil {
		t.Fatalf("duplicate private staging parent: %v", err)
	}
	defer unix.Close(stagingFD)
	if err := ensureQualifiedStagingDirectories(fileFD, stagingFD); !errors.Is(err, ErrDrifted) {
		t.Fatalf("ensureQualifiedStagingDirectories(file, directory) error = %v, want ErrDrifted", err)
	}
}

func TestSafetyRegressionGuardHelpersRefuseEffectsWithoutCompleteSafetyState(t *testing.T) {
	hooks := defaultRemovalHooks()
	if err := preflightRemovalTree(context.Background(), -1, "item", removalMaximumDepth+1, hooks); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("preflightRemovalTree(over-depth) error = %v, want ErrUnsupported", err)
	}
	effectStarted := false
	if err := removeVerifiedEntry(context.Background(), -1, "item", removalMaximumDepth+1, func() { effectStarted = true }, hooks); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("removeVerifiedEntry(over-depth) error = %v, want ErrUnsupported", err)
	}
	if effectStarted {
		t.Fatal("over-depth removal reached an effect callback")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := removeVerifiedEntry(canceled, -1, "item", 0, func() { effectStarted = true }, hooks); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("removeVerifiedEntry(canceled) error = %v, want ErrInterrupted", err)
	}
	if effectStarted {
		t.Fatal("canceled removal reached an effect callback")
	}
	if _, err := removableEntryType(context.Background(), -1, "item/escape", hooks); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("removableEntryType(invalid basename) error = %v, want ErrUnsupported", err)
	}
	hooks.openTarget = func(context.Context, int, string) (int, error) { return -1, unix.EIO }
	if _, err := removableEntryType(context.Background(), -1, "item", hooks); !errors.Is(err, unix.EIO) {
		t.Fatalf("removableEntryType(open failure) error = %v, want EIO", err)
	}

	if err := syncStagedDirectories(nil, nil); err == nil {
		t.Fatal("syncStagedDirectories(nil) accepted missing recovery descriptors")
	}
	if err := closeStagedDescriptors(-1, -1); err != nil {
		t.Fatalf("closeStagedDescriptors(-1, -1) error = %v", err)
	}
	fd, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open disposable descriptor: %v", err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close disposable descriptor: %v", err)
	}
	if err := closeStagedDescriptors(fd, -1); err == nil {
		t.Fatal("closeStagedDescriptors() accepted an already-closed owned descriptor")
	}

	if _, err := postMovePrecondition(domain.FilesystemPrecondition{Required: domain.FilesystemFieldChangedAt}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("postMovePrecondition(invalid identity) error = %v, want ErrUnsupported", err)
	}
	if sameBytePath(testBytePath(t, "one"), testBytePath(t, "one", "two")) {
		t.Fatal("sameBytePath() accepted paths with different component counts")
	}
	var nilStaged *StagedObject
	if nilStaged.RootID() != "" || nilStaged.Token() != "" {
		t.Fatal("nil staged recovery object exposed authority metadata")
	}
	if err := nilStaged.Close(); err != nil {
		t.Fatalf("nil staged Close() error = %v", err)
	}
}

func TestSafetyRegressionStageAndRestoreRequestValidationStopsBeforeMutation(t *testing.T) {
	if err := restoreNoReplaceWith(context.Background(), nil, restoreHooks{}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("restoreNoReplaceWith(nil) error = %v, want ErrDrifted", err)
	}
	for _, test := range []struct {
		name  string
		state stagedState
		want  error
	}{
		{name: "already restored", state: stagedStateRestored, want: nil},
		{name: "removing", state: stagedStateRemoving, want: ErrInterrupted},
		{name: "removed", state: stagedStateRemoved, want: ErrDrifted},
	} {
		t.Run("restore "+test.name, func(t *testing.T) {
			err := restoreNoReplaceWith(context.Background(), &StagedObject{state: test.state}, restoreHooks{})
			if test.want == nil {
				if err != nil {
					t.Fatalf("restoreNoReplaceWith(%d) error = %v, want nil", test.state, err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("restoreNoReplaceWith(%d) error = %v, want %v", test.state, err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*ParentLease, *PrivateStagingLease, *domain.FilesystemPrecondition) domain.ActionKind
	}{
		{
			name: "unknown action kind",
			mutate: func(*ParentLease, *PrivateStagingLease, *domain.FilesystemPrecondition) domain.ActionKind {
				return domain.ActionRepairState
			},
		},
		{
			name: "unbound source root",
			mutate: func(source *ParentLease, _ *PrivateStagingLease, _ *domain.FilesystemPrecondition) domain.ActionKind {
				source.rootID = ""
				return stageTestAction
			},
		},
		{
			name: "same basename but unbound resolved path",
			mutate: func(_ *ParentLease, _ *PrivateStagingLease, expected *domain.FilesystemPrecondition) domain.ActionKind {
				expected.Target.Filesystem.Path = testBytePath(t, "item")
				return stageTestAction
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
			privateStaging := stageTestPrivateStaging(t, staging)
			kind := test.mutate(source, privateStaging, &expected)
			staged, err := stageNoReplaceWith(context.Background(), source, "item", privateStaging, "stage-token", kind, expected, stageHooks{
				renameNoReplace: renameNoReplace,
			})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("stageNoReplaceWith() validation error = %v, want ErrUnsupported", err)
			}
			if staged != nil {
				defer staged.Close()
				t.Fatal("stageNoReplaceWith() returned a staged object after request validation failure")
			}
			assertStageRequestDidNotMove(t, source, staging, "item", "stage-token")
		})
	}
}

func TestSafetyRegressionLiveDescriptorGuardsRejectUnavailableEvidence(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	makeTestDirectory(t, rootFD, "parent")
	parent, basename, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "parent", "item"), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve syncable parent: %v", err)
	}
	defer parent.Close()
	createStageTestFile(t, parent, "item")

	if err := parent.Sync(); err != nil && !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ParentLease.Sync() error = %v, want success or ErrUnsupported", err)
	}
	if _, err := OpenTargetHandle(nil, parent, basename); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("OpenTargetHandle(nil context) error = %v, want ErrInterrupted", err)
	}
	if _, err := OpenTargetHandle(context.Background(), parent, "item/escape"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenTargetHandle(invalid basename) error = %v, want ErrUnsupported", err)
	}
	if _, err := OpenTargetHandle(context.Background(), parent, "missing"); !errors.Is(err, ErrDrifted) {
		t.Fatalf("OpenTargetHandle(missing target) error = %v, want ErrDrifted", err)
	}

	handle, err := OpenTargetHandle(context.Background(), parent, basename)
	if err != nil {
		t.Fatalf("OpenTargetHandle() error = %v", err)
	}
	defer handle.Close()
	borrowed := false
	if err := handle.withFD(func(fd int) error {
		borrowed = fd >= 0
		return nil
	}); err != nil {
		t.Fatalf("TargetHandle.withFD() error = %v", err)
	}
	if !borrowed {
		t.Fatal("TargetHandle.withFD() did not lend a valid held descriptor")
	}

	fd, err := unix.Open(t.TempDir(), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open statx descriptor: %v", err)
	}
	if _, err := SnapshotFD(fd, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SnapshotFD(empty requirement) error = %v, want ErrUnsupported", err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close statx descriptor: %v", err)
	}
	if _, err := SnapshotFD(fd, domain.FilesystemFieldType); !errors.Is(err, ErrDrifted) {
		t.Fatalf("SnapshotFD(closed descriptor) error = %v, want ErrDrifted", err)
	}
}

func TestSafetyRegressionVerificationDriftRetainsInsteadOfRemoving(t *testing.T) {
	rootFD, source, staging, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer staging.Close()
	createStageTestFile(t, source, "item")
	staged := stageVerifiedTestObject(t, source, staging, rootID, "item")
	defer staged.Close()

	if err := staging.withFD(func(parentFD int) error {
		if err := unix.Unlinkat(parentFD, "stage-token", 0); err != nil {
			return err
		}
		fd, err := unix.Openat(parentFD, "stage-token", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("replace staged token: %v", err)
	}
	if err := VerifyStagedIdentity(context.Background(), staged); !errors.Is(err, ErrRetained) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("VerifyStagedIdentity() after drift = %v, want ErrRetained + ErrDrifted", err)
	}
	if staged.state != stagedStateRetained || !testEntryExists(t, staging, "stage-token") {
		t.Fatal("failed staged verification did not retain the unproven entry")
	}
}
