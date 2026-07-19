//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

const quarantineRetainTestToken = "ldc-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRetainQuarantineNoReplaceMovesOnlyVerifiedIdentity(t *testing.T) {
	fixture := newQuarantineRetainFixture(t)

	disposition, err := RetainQuarantineNoReplace(
		context.Background(),
		fixture.source,
		"item",
		fixture.quarantine,
		quarantineRetainTestToken,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("RetainQuarantineNoReplace() error = %v", err)
	}
	if disposition != QuarantineRetainVerified {
		t.Fatalf("RetainQuarantineNoReplace() disposition = %v, want verified", disposition)
	}
	if testEntryExists(t, fixture.source, "item") {
		t.Fatal("verified retain left the source entry in place")
	}
	if !testEntryExists(t, fixture.quarantineParent, quarantineRetainTestToken) {
		t.Fatal("verified retain did not create the quarantine token")
	}
	postMove, err := postMovePrecondition(fixture.expected)
	if err != nil {
		t.Fatalf("postMovePrecondition() error = %v", err)
	}
	if err := ComparePrecondition(postMove, quarantineRetainSnapshot(t, fixture.quarantineParent, quarantineRetainTestToken, postMove.Required)); err != nil {
		t.Fatalf("retained identity = %v, want source identity after rename", err)
	}
}

func TestRetainQuarantineNoReplaceRejectsInvalidRequestsBeforeRename(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture *quarantineRetainFixture)
		want   error
	}{
		{
			name: "wrong private layout kind",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				t.Helper()
				wrong, err := quarantineRetainPrivateDirectory(fixture.quarantineParent, fixture.rootID, mounts.LayoutPrivateStaging)
				if err != nil {
					t.Fatalf("open private staging directory: %v", err)
				}
				_ = fixture.quarantine.Close()
				fixture.quarantine = wrong
			},
			want: ErrUnsupported,
		},
		{
			name: "wrong root",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				t.Helper()
				rootID, err := domain.NewTrustedRootID("quarantine-retain-other-root")
				if err != nil {
					t.Fatalf("NewTrustedRootID() error = %v", err)
				}
				fixture.quarantine.rootID = rootID
			},
			want: ErrUnsupported,
		},
		{
			name: "wrong action mask",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				fixture.expected.Required &^= domain.FilesystemFieldChangedAt
			},
			want: ErrUnsupported,
		},
		{
			name: "wrong resolved target path",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				fixture.expected.Target.Filesystem.Path = testBytePath(t, "source", "other")
			},
			want: ErrUnsupported,
		},
		{
			name: "wrong token profile",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				fixture.token = "ldc-0123456789abcdef"
			},
			want: ErrUnsupported,
		},
		{
			name: "closed quarantine lease",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				if err := fixture.quarantine.Close(); err != nil {
					t.Fatalf("PrivateDirectoryLease.Close() error = %v", err)
				}
			},
			want: ErrDrifted,
		},
		{
			name: "stale source precondition",
			mutate: func(t *testing.T, fixture *quarantineRetainFixture) {
				createStageTestFile(t, fixture.source, "replacement")
				if err := fixture.source.withFD(func(fd int) error {
					return unix.Renameat(fd, "replacement", fd, "item")
				}); err != nil {
					t.Fatalf("replace source: %v", err)
				}
			},
			want: ErrDrifted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQuarantineRetainFixture(t)
			test.mutate(t, &fixture)

			disposition, err := RetainQuarantineNoReplace(
				context.Background(),
				fixture.source,
				"item",
				fixture.quarantine,
				fixture.token,
				fixture.expected,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("RetainQuarantineNoReplace() error = %v, want %v", err, test.want)
			}
			if disposition != QuarantineRetainNotApplied {
				t.Fatalf("RetainQuarantineNoReplace() disposition = %v, want not applied", disposition)
			}
			if !testEntryExists(t, fixture.source, "item") {
				t.Fatal("invalid retain request moved the source entry")
			}
			if testEntryExists(t, fixture.quarantineParent, quarantineRetainTestToken) {
				t.Fatal("invalid retain request created the quarantine token")
			}
		})
	}
}

func TestRetainQuarantineNoReplaceClassifiesRenameCertaintyWithoutRetry(t *testing.T) {
	for _, test := range []struct {
		name            string
		prepare         func(t *testing.T, fixture quarantineRetainFixture)
		rename          func(int, string, int, string) error
		wantDisposition QuarantineRetainDisposition
		wantError       error
		wantSource      bool
		wantToken       bool
		wantRenameCalls int
	}{
		{
			name: "occupied token is known not applied",
			prepare: func(t *testing.T, fixture quarantineRetainFixture) {
				createStageTestFile(t, fixture.quarantineParent, quarantineRetainTestToken)
			},
			wantDisposition: QuarantineRetainNotApplied,
			wantError:       ErrDrifted,
			wantSource:      true,
			wantToken:       true,
			wantRenameCalls: 1,
		},
		{
			name: "unavailable rename is known not applied",
			rename: func(int, string, int, string) error {
				return unix.EOPNOTSUPP
			},
			wantDisposition: QuarantineRetainNotApplied,
			wantError:       ErrUnsupported,
			wantSource:      true,
			wantToken:       false,
			wantRenameCalls: 1,
		},
		{
			name: "interrupted rename is indeterminate",
			rename: func(int, string, int, string) error {
				return unix.EINTR
			},
			wantDisposition: QuarantineRetainIndeterminate,
			wantError:       ErrInterrupted,
			wantSource:      true,
			wantToken:       false,
			wantRenameCalls: 1,
		},
		{
			name: "rename that moves before interruption remains indeterminate",
			rename: func(oldParentFD int, oldName string, newParentFD int, newName string) error {
				if err := unix.Renameat2(oldParentFD, oldName, newParentFD, newName, unix.RENAME_NOREPLACE); err != nil {
					return err
				}
				return unix.EINTR
			},
			wantDisposition: QuarantineRetainIndeterminate,
			wantError:       ErrInterrupted,
			wantSource:      false,
			wantToken:       true,
			wantRenameCalls: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQuarantineRetainFixture(t)
			if test.prepare != nil {
				test.prepare(t, fixture)
			}
			renameCalls := 0
			hooks := quarantineRetainHooks{
				renameNoReplace: func(oldParentFD int, oldName string, newParentFD int, newName string) error {
					renameCalls++
					if test.rename == nil {
						return renameNoReplace(oldParentFD, oldName, newParentFD, newName)
					}
					return test.rename(oldParentFD, oldName, newParentFD, newName)
				},
			}

			disposition, err := retainQuarantineNoReplaceWith(
				context.Background(),
				fixture.source,
				"item",
				fixture.quarantine,
				fixture.token,
				fixture.expected,
				hooks,
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("retainQuarantineNoReplaceWith() error = %v, want %v", err, test.wantError)
			}
			if disposition != test.wantDisposition {
				t.Fatalf("retainQuarantineNoReplaceWith() disposition = %v, want %v", disposition, test.wantDisposition)
			}
			if renameCalls != test.wantRenameCalls {
				t.Fatalf("rename calls = %d, want %d", renameCalls, test.wantRenameCalls)
			}
			if source := testEntryExists(t, fixture.source, "item"); source != test.wantSource {
				t.Fatalf("source present after retain = %t, want %t", source, test.wantSource)
			}
			if token := testEntryExists(t, fixture.quarantineParent, fixture.token); token != test.wantToken {
				t.Fatalf("quarantine token present after retain = %t, want %t", token, test.wantToken)
			}
		})
	}
}

func TestRetainQuarantineNoReplaceRetainsPostMoveUncertainty(t *testing.T) {
	for _, test := range []struct {
		name  string
		hooks quarantineRetainHooks
	}{
		{
			name: "destination identity cannot be proved",
			hooks: quarantineRetainHooks{
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
			},
		},
		{
			name: "directory entries cannot be synced",
			hooks: quarantineRetainHooks{
				renameNoReplace: renameNoReplace,
				syncDirectory: func(int) error {
					return unix.EIO
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQuarantineRetainFixture(t)
			disposition, err := retainQuarantineNoReplaceWith(
				context.Background(),
				fixture.source,
				"item",
				fixture.quarantine,
				fixture.token,
				fixture.expected,
				test.hooks,
			)
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("retainQuarantineNoReplaceWith() error = %v, want ErrInterrupted", err)
			}
			if disposition != QuarantineRetainIndeterminate {
				t.Fatalf("retainQuarantineNoReplaceWith() disposition = %v, want indeterminate", disposition)
			}
			if testEntryExists(t, fixture.source, "item") {
				t.Fatal("post-move uncertainty restored the source entry")
			}
			if !testEntryExists(t, fixture.quarantineParent, fixture.token) {
				t.Fatal("post-move uncertainty removed the quarantine entry")
			}
		})
	}
}

func TestRetainQuarantineNoReplaceSyncsBothDirectoriesBeforeVerified(t *testing.T) {
	fixture := newQuarantineRetainFixture(t)
	syncCalls := 0
	disposition, err := retainQuarantineNoReplaceWith(
		context.Background(),
		fixture.source,
		"item",
		fixture.quarantine,
		fixture.token,
		fixture.expected,
		quarantineRetainHooks{
			renameNoReplace: renameNoReplace,
			syncDirectory: func(int) error {
				syncCalls++
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("retainQuarantineNoReplaceWith() error = %v", err)
	}
	if disposition != QuarantineRetainVerified {
		t.Fatalf("retainQuarantineNoReplaceWith() disposition = %v, want verified", disposition)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want 2", syncCalls)
	}
}

func TestRetainQuarantineNoReplaceRejectsDetachedSourceParentBeforeMutation(t *testing.T) {
	fixture := newQuarantineRetainFixture(t)
	if err := unix.Renameat(fixture.rootFD, "source", fixture.rootFD, "detached-source"); err != nil {
		t.Fatalf("detach source parent: %v", err)
	}
	makeTestDirectory(t, fixture.rootFD, "source")
	replacement := stageTestParent(t, fixture.rootFD, fixture.rootID, "source", "item")
	defer replacement.Close()
	createStageTestFile(t, replacement, "item")

	disposition, err := RetainQuarantineNoReplace(
		context.Background(),
		fixture.source,
		"item",
		fixture.quarantine,
		fixture.token,
		fixture.expected,
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("RetainQuarantineNoReplace() error = %v, want ErrDrifted", err)
	}
	if disposition != QuarantineRetainNotApplied {
		t.Fatalf("RetainQuarantineNoReplace() disposition = %v, want not applied", disposition)
	}
	if !testEntryExists(t, fixture.source, "item") {
		t.Fatal("detached source entry changed after rejected retain")
	}
	if !testEntryExists(t, replacement, "item") {
		t.Fatal("replacement source entry changed after rejected retain")
	}
	if testEntryExists(t, fixture.quarantineParent, fixture.token) {
		t.Fatal("detached source parent created a quarantine entry")
	}
}

func TestRetainQuarantineNoReplaceRejectsQuarantineLayoutAsSourceBeforeRename(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	makeTestDirectory(t, rootFD, "quarantine")
	rootID, err := domain.NewTrustedRootID("quarantine-retain-layout-source")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source := quarantineRetainTestParent(t, rootFD, rootID, "quarantine")
	defer source.Close()
	quarantineParent := stageTestParent(t, rootFD, rootID, "quarantine", quarantineRetainTestToken)
	defer quarantineParent.Close()
	quarantine, err := quarantineRetainPrivateDirectory(quarantineParent, rootID, mounts.LayoutPrivateQuarantine)
	if err != nil {
		t.Fatalf("open private quarantine directory: %v", err)
	}
	defer quarantine.Close()
	expected := quarantineRetainPrecondition(t, rootID, source, testBytePath(t, "quarantine"), "quarantine")

	renameCalls := 0
	disposition, err := retainQuarantineNoReplaceWith(
		context.Background(),
		source,
		"quarantine",
		quarantine,
		quarantineRetainTestToken,
		expected,
		quarantineRetainHooks{
			renameNoReplace: func(int, string, int, string) error {
				renameCalls++
				return nil
			},
		},
	)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("retainQuarantineNoReplaceWith() error = %v, want ErrUnsupported", err)
	}
	if disposition != QuarantineRetainNotApplied {
		t.Fatalf("retainQuarantineNoReplaceWith() disposition = %v, want not applied", disposition)
	}
	if renameCalls != 0 {
		t.Fatalf("rename calls = %d, want 0 for quarantine layout source", renameCalls)
	}
	if !testEntryExists(t, source, "quarantine") {
		t.Fatal("layout-source request changed the quarantine directory")
	}
	if testEntryExists(t, quarantineParent, quarantineRetainTestToken) {
		t.Fatal("layout-source request created a token inside quarantine")
	}
}

type quarantineRetainFixture struct {
	rootFD           int
	rootID           domain.TrustedRootID
	source           *ParentLease
	quarantineParent *ParentLease
	quarantine       *PrivateDirectoryLease
	expected         domain.FilesystemPrecondition
	token            string
}

func newQuarantineRetainFixture(t *testing.T) quarantineRetainFixture {
	t.Helper()

	rootFD := openTestDirectory(t)
	t.Cleanup(func() { _ = unix.Close(rootFD) })
	makeTestDirectory(t, rootFD, "source")
	makeTestDirectory(t, rootFD, "quarantine")
	rootID, err := domain.NewTrustedRootID("linuxfs-quarantine-retain-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source := quarantineRetainTestParent(t, rootFD, rootID, "source", "item")
	t.Cleanup(func() { _ = source.Close() })
	quarantineParent := stageTestParent(t, rootFD, rootID, "quarantine", quarantineRetainTestToken)
	t.Cleanup(func() { _ = quarantineParent.Close() })
	quarantine, err := quarantineRetainPrivateDirectory(quarantineParent, rootID, mounts.LayoutPrivateQuarantine)
	if err != nil {
		t.Fatalf("open private quarantine directory: %v", err)
	}
	t.Cleanup(func() { _ = quarantine.Close() })
	createStageTestFile(t, source, "item")

	return quarantineRetainFixture{
		rootFD:           rootFD,
		rootID:           rootID,
		source:           source,
		quarantineParent: quarantineParent,
		quarantine:       quarantine,
		expected:         quarantineRetainPrecondition(t, rootID, source, testBytePath(t, "source", "item"), "item"),
		token:            quarantineRetainTestToken,
	}
}

func quarantineRetainPrivateDirectory(parent *ParentLease, rootID domain.TrustedRootID, kind mounts.LayoutKind) (*PrivateDirectoryLease, error) {
	return openPrivateDirectoryWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   kind,
		duplicate: func() (int, error) {
			return parent.duplicate()
		},
	})
}

func quarantineRetainTestParent(t *testing.T, rootFD int, rootID domain.TrustedRootID, components ...string) *ParentLease {
	t.Helper()
	rootCopy, err := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		t.Fatalf("duplicate source root: %v", err)
	}
	requalificationRoot, err := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		_ = unix.Close(rootCopy)
		t.Fatalf("duplicate source requalification root: %v", err)
	}
	parent, _, err := resolveParentFromOwnedRootFDForRootWithRequalification(
		context.Background(),
		rootCopy,
		requalificationRoot,
		rootID,
		testBytePath(t, components...),
		unix.Openat2,
	)
	if err != nil {
		t.Fatalf("resolve requalifiable source parent: %v", err)
	}
	return parent
}

func quarantineRetainPrecondition(
	t *testing.T,
	rootID domain.TrustedRootID,
	parent *ParentLease,
	path pathbytes.BytePath,
	basename string,
) domain.FilesystemPrecondition {
	t.Helper()
	target, err := domain.NewFilesystemTarget(rootID, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	required, err := RequiredStatMask(domain.ActionQuarantinePath)
	if err != nil {
		t.Fatalf("RequiredStatMask(ActionQuarantinePath) error = %v", err)
	}
	return domain.FilesystemPrecondition{
		Target:   target,
		Required: required,
		Snapshot: quarantineRetainSnapshot(t, parent, basename, required),
	}
}

func quarantineRetainSnapshot(t *testing.T, parent *ParentLease, basename string, required domain.FilesystemFieldMask) domain.FilesystemSnapshot {
	t.Helper()
	fd, err := parent.duplicate()
	if err != nil {
		t.Fatalf("duplicate parent for quarantine snapshot: %v", err)
	}
	defer unix.Close(fd)
	snapshot, err := snapshotNamedTarget(context.Background(), fd, basename, required)
	if err != nil {
		t.Fatalf("snapshot quarantine entry %q: %v", basename, err)
	}
	return snapshot
}
