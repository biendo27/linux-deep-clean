//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

func TestProbeResolvedTargetAbsenceConfirmsStableMovedSourceWithoutMutation(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)
	beforeContent := trashMoveSnapshot(t, fixture.source.filesFD, fixture.publication.token)
	beforeMetadata := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix)

	absent, err := ProbeResolvedTargetAbsence(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("ProbeResolvedTargetAbsence() error = %v", err)
	}
	if !absent {
		t.Fatal("ProbeResolvedTargetAbsence() did not prove the moved source absent")
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("source absence probe restored or created the source entry")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("source absence probe removed the retained Trash content")
	}
	if afterContent := trashMoveSnapshot(t, fixture.source.filesFD, fixture.publication.token); afterContent != beforeContent {
		t.Fatal("source absence probe changed the retained Trash content identity")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(beforeMetadata) {
		t.Fatalf("source absence probe changed Trash metadata: got %q, want %q", got, beforeMetadata)
	}
}

func TestProbeResolvedTargetAbsenceRejectsPresentOrUnsafeSourceWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, parent *ParentLease)
	}{
		{
			name: "matching original regular file",
			prepare: func(t *testing.T, _ *ParentLease) {
				t.Helper()
			},
		},
		{
			name: "directory",
			prepare: func(t *testing.T, parent *ParentLease) {
				t.Helper()
				replaceSourceEntryForAbsenceProbe(t, parent, func(parentFD int) error {
					return unix.Mkdirat(parentFD, "item", 0o700)
				})
			},
		},
		{
			name: "trailing symlink",
			prepare: func(t *testing.T, parent *ParentLease) {
				t.Helper()
				replaceSourceEntryForAbsenceProbe(t, parent, func(parentFD int) error {
					return unix.Symlinkat("untrusted-target", parentFD, "item")
				})
			},
		},
		{
			name: "special file",
			prepare: func(t *testing.T, parent *ParentLease) {
				t.Helper()
				replaceSourceEntryForAbsenceProbe(t, parent, func(parentFD int) error {
					return unix.Mkfifoat(parentFD, "item", 0o600)
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashMoveFixture(t)
			test.prepare(t, fixture.sourceParent)
			beforeMetadata := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix)

			absent, err := ProbeResolvedTargetAbsence(
				context.Background(),
				fixture.sourceParent,
				"item",
				fixture.expected,
			)
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("ProbeResolvedTargetAbsence() error = %v, want ErrDrifted", err)
			}
			if absent {
				t.Fatal("ProbeResolvedTargetAbsence() accepted a present source entry as absent")
			}
			if !testEntryExists(t, fixture.sourceParent, "item") {
				t.Fatal("source absence probe removed the present source entry")
			}
			if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
				t.Fatal("source absence probe created retained Trash content")
			}
			if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(beforeMetadata) {
				t.Fatalf("source absence probe changed Trash metadata: got %q, want %q", got, beforeMetadata)
			}
		})
	}
}

func TestProbeResolvedTargetAbsenceRejectsCompensatedHardLinkSource(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	if err := fixture.sourceParent.withFD(func(parentFD int) error {
		return unix.Linkat(parentFD, "item", parentFD, "peer", 0)
	}); err != nil {
		t.Fatalf("create precondition peer link: %v", err)
	}
	fixture.expected = stageTestPrecondition(t, fixture.source.rootID, fixture.sourceParent, "item")
	if err := fixture.sourceParent.withFD(func(parentFD int) error {
		if err := unix.Linkat(parentFD, "item", fixture.source.filesFD, fixture.publication.token, 0); err != nil {
			return err
		}
		return unix.Unlinkat(parentFD, "peer", 0)
	}); err != nil {
		t.Fatalf("substitute Trash hard link while preserving link count: %v", err)
	}

	postMove, err := postMovePrecondition(fixture.expected)
	if err != nil {
		t.Fatalf("postMovePrecondition() error = %v", err)
	}
	if err := ComparePrecondition(postMove, trashMoveSnapshot(t, fixture.source.filesFD, fixture.publication.token)); err != nil {
		t.Fatalf("compensated Trash hard link did not match post-move identity: %v", err)
	}

	absent, err := ProbeResolvedTargetAbsence(context.Background(), fixture.sourceParent, "item", fixture.expected)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("ProbeResolvedTargetAbsence() error = %v, want ErrDrifted", err)
	}
	if absent {
		t.Fatal("source absence probe accepted a source name preserved by a compensated hard link")
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("source absence probe removed the original source name")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("source absence probe removed the compensated Trash hard link")
	}
}

func TestProbeResolvedTargetAbsenceRejectsReplacedSourceParent(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)

	if err := unix.Renameat(fixture.source.rootFD, "source", fixture.source.rootFD, "detached-source"); err != nil {
		t.Fatalf("detach source parent: %v", err)
	}
	if err := unix.Mkdirat(fixture.source.rootFD, "source", 0o700); err != nil {
		t.Fatalf("replace source parent: %v", err)
	}
	replacementParentFD, err := unix.Openat(
		fixture.source.rootFD,
		"source",
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		t.Fatalf("open replacement source parent: %v", err)
	}
	defer unix.Close(replacementParentFD)
	createTrashMoveTestFile(t, replacementParentFD, "item")

	absent, err := ProbeResolvedTargetAbsence(context.Background(), fixture.sourceParent, "item", fixture.expected)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("ProbeResolvedTargetAbsence() error = %v, want ErrDrifted", err)
	}
	if absent {
		t.Fatal("ProbeResolvedTargetAbsence() accepted absence beneath a detached source parent")
	}
	if !trashMoveEntryExists(t, replacementParentFD, "item") {
		t.Fatal("source absence probe removed the replacement source entry")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("source absence probe removed retained Trash content")
	}
}

func TestProbeResolvedTargetAbsenceRejectsIncompatibleParentOrPreconditionWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition)
	}{
		{
			name: "different resolved parent",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				t.Helper()
				makeTestDirectory(t, fixture.source.rootFD, "other")
				parent := stageTestParent(t, fixture.source.rootFD, fixture.source.rootID, "other", "item")
				t.Cleanup(func() { _ = parent.Close() })
				createStageTestFile(t, parent, "item")
				return parent, "item", fixture.expected
			},
		},
		{
			name: "different root identity",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				t.Helper()
				wrongRoot, err := domain.NewTrustedRootID("linuxfs-source-absence-other-root")
				if err != nil {
					t.Fatalf("NewTrustedRootID() error = %v", err)
				}
				candidate := fixture.expected
				target, err := domain.NewFilesystemTarget(wrongRoot, fixture.expected.Target.Filesystem.Path)
				if err != nil {
					t.Fatalf("NewFilesystemTarget() error = %v", err)
				}
				candidate.Target = target
				return fixture.sourceParent, "item", candidate
			},
		},
		{
			name: "different precondition path",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				t.Helper()
				candidate := fixture.expected
				target, err := domain.NewFilesystemTarget(fixture.source.rootID, testBytePath(t, "source", "other"))
				if err != nil {
					t.Fatalf("NewFilesystemTarget() error = %v", err)
				}
				candidate.Target = target
				return fixture.sourceParent, "item", candidate
			},
		},
		{
			name: "restore mask",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				t.Helper()
				candidate := fixture.expected
				mask, err := RequiredStatMask(domain.ActionRestoreTrashPath)
				if err != nil {
					t.Fatalf("RequiredStatMask() error = %v", err)
				}
				candidate.Required = mask
				return fixture.sourceParent, "item", candidate
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashMoveFixture(t)
			parent, basename, expected := test.prepare(t, fixture)
			beforeMetadata := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix)

			absent, err := ProbeResolvedTargetAbsence(context.Background(), parent, basename, expected)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("ProbeResolvedTargetAbsence() error = %v, want ErrUnsupported", err)
			}
			if absent {
				t.Fatal("ProbeResolvedTargetAbsence() accepted incompatible authority as absence")
			}
			if !testEntryExists(t, fixture.sourceParent, "item") {
				t.Fatal("rejected source absence probe changed the actual source entry")
			}
			if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
				t.Fatal("rejected source absence probe created retained Trash content")
			}
			if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(beforeMetadata) {
				t.Fatalf("rejected source absence probe changed Trash metadata: got %q, want %q", got, beforeMetadata)
			}
		})
	}
}

func TestProbeResolvedTargetAbsenceClassifiesCanceledAndInterruptedLookup(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	beforeMetadata := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	absent, err := ProbeResolvedTargetAbsence(ctx, fixture.sourceParent, "item", fixture.expected)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("ProbeResolvedTargetAbsence(canceled) error = %v, want ErrInterrupted", err)
	}
	if absent {
		t.Fatal("canceled source absence probe reported a positive absence fact")
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("canceled source absence probe changed the source entry")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("canceled source absence probe created retained Trash content")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(beforeMetadata) {
		t.Fatalf("canceled source absence probe changed Trash metadata: got %q, want %q", got, beforeMetadata)
	}

	absent, err = openResolvedTargetAbsenceBeneathWith(
		context.Background(),
		42,
		"item",
		func(int, string, *unix.OpenHow) (int, error) { return -1, unix.EINTR },
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("openResolvedTargetAbsenceBeneathWith(EINTR) error = %v, want ErrInterrupted", err)
	}
	if absent {
		t.Fatal("interrupted lookup reported a positive absence fact")
	}
}

func TestProbeResolvedTargetAbsenceRequiresTwoConstrainedENOENTLookups(t *testing.T) {
	lookups := 0
	absent, err := openResolvedTargetAbsenceBeneathWith(
		context.Background(),
		42,
		"item",
		func(parentFD int, basename string, how *unix.OpenHow) (int, error) {
			lookups++
			if parentFD != 42 {
				t.Fatalf("parent FD = %d, want 42", parentFD)
			}
			if basename != "item" {
				t.Fatalf("basename = %q, want item", basename)
			}
			if how.Flags != uint64(unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW) {
				t.Fatalf("open flags = %#x, want %#x", how.Flags, uint64(unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW))
			}
			if how.Resolve != requiredOpenat2ResolveFlags {
				t.Fatalf("open resolve flags = %#x, want %#x", how.Resolve, requiredOpenat2ResolveFlags)
			}
			return -1, unix.ENOENT
		},
	)
	if err != nil {
		t.Fatalf("openResolvedTargetAbsenceBeneathWith() error = %v", err)
	}
	if !absent {
		t.Fatal("two exact ENOENT lookups did not prove source absence")
	}
	if lookups != 2 {
		t.Fatalf("constrained lookup count = %d, want 2", lookups)
	}
}

func TestProbeResolvedTargetAbsenceRejectsUnstableLookup(t *testing.T) {
	lookups := 0
	absent, err := openResolvedTargetAbsenceBeneathWith(
		context.Background(),
		42,
		"item",
		func(int, string, *unix.OpenHow) (int, error) {
			lookups++
			return -1, unix.EAGAIN
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openResolvedTargetAbsenceBeneathWith(EAGAIN) error = %v, want ErrDrifted", err)
	}
	if absent {
		t.Fatal("unstable lookup reported a positive absence fact")
	}
	if lookups != openat2AttemptLimit {
		t.Fatalf("unstable lookup count = %d, want %d", lookups, openat2AttemptLimit)
	}
}

func TestProbeResolvedTargetAbsenceRejectsReservedFoundDescriptor(t *testing.T) {
	closed := -1
	absent, err := openResolvedTargetAbsenceBeneathWithClose(
		context.Background(),
		42,
		"item",
		func(int, string, *unix.OpenHow) (int, error) { return 0, nil },
		func(fd int) error {
			closed = fd
			return nil
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openResolvedTargetAbsenceBeneathWithClose() error = %v, want ErrDrifted", err)
	}
	if absent {
		t.Fatal("reserved descriptor reported a positive absence fact")
	}
	if closed != 0 {
		t.Fatalf("reserved descriptor close = %d, want 0", closed)
	}
}

func replaceSourceEntryForAbsenceProbe(t *testing.T, parent *ParentLease, create func(int) error) {
	t.Helper()
	if err := parent.withFD(func(parentFD int) error {
		if err := unix.Unlinkat(parentFD, "item", 0); err != nil {
			return err
		}
		return create(parentFD)
	}); err != nil {
		t.Fatalf("replace source entry for absence probe: %v", err)
	}
}
