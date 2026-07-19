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

func TestProbeOwnedTrashContentConfirmsExactMovedFileWithoutMutation(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)
	beforeQualification := fixture.source.topologyDuplicateCalls

	present, err := ProbeOwnedTrashContent(
		context.Background(),
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("ProbeOwnedTrashContent() error = %v", err)
	}
	if !present {
		t.Fatal("ProbeOwnedTrashContent() reported the moved files entry absent")
	}
	if fixture.source.topologyDuplicateCalls <= beforeQualification {
		t.Fatal("ProbeOwnedTrashContent() did not requalify the descriptor-rooted topology")
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("content probe restored or created the original source entry")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("content probe removed the retained files entry")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
		t.Fatalf("content probe changed Trash metadata: got %q, want %q", got, fixture.publication.contents)
	}
}

func TestProbeOwnedTrashContentSupportsExactMovedDirectory(t *testing.T) {
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	makeTestDirectory(t, source.rootFD, "source")
	parent := stageTestParent(t, source.rootFD, source.rootID, "source", "directory")
	t.Cleanup(func() { _ = parent.Close() })
	if err := parent.withFD(func(parentFD int) error {
		return unix.Mkdirat(parentFD, "directory", 0o700)
	}); err != nil {
		t.Fatalf("create source directory: %v", err)
	}
	expected := stageTestPrecondition(t, source.rootID, parent, "directory")

	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
	}
	t.Cleanup(func() { _ = directories.Close() })
	const token = "ldc-aabbccddeeff0011"
	publication, err := PublishTrashInfoDurable(context.Background(), directories, token, []byte("[Trash Info]\nPath=source/directory\n"))
	if err != nil {
		t.Fatalf("PublishTrashInfoDurable() error = %v", err)
	}
	disposition, err := MovePublishedTrashNoReplace(context.Background(), parent, "directory", directories, publication, expected)
	if err != nil {
		t.Fatalf("MovePublishedTrashNoReplace() error = %v", err)
	}
	if disposition != TrashMoveVerified {
		t.Fatalf("MovePublishedTrashNoReplace() disposition = %v, want verified", disposition)
	}

	present, err := ProbeOwnedTrashContent(context.Background(), directories, token, expected)
	if err != nil {
		t.Fatalf("ProbeOwnedTrashContent() directory error = %v", err)
	}
	if !present {
		t.Fatal("ProbeOwnedTrashContent() reported the moved directory absent")
	}
	if testEntryExists(t, parent, "directory") {
		t.Fatal("content probe restored the original directory")
	}
	if !trashMoveEntryExists(t, source.filesFD, token) {
		t.Fatal("content probe removed the retained directory")
	}
}

func TestProbeOwnedTrashContentReportsStableExactAbsence(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	beforeQualification := fixture.source.topologyDuplicateCalls

	present, err := ProbeOwnedTrashContent(
		context.Background(),
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("ProbeOwnedTrashContent() error = %v", err)
	}
	if present {
		t.Fatal("ProbeOwnedTrashContent() reported an un-moved files entry present")
	}
	if fixture.source.topologyDuplicateCalls <= beforeQualification {
		t.Fatal("absence probe did not requalify the descriptor-rooted topology")
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("absence probe moved or removed the original source entry")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("absence probe created a files entry")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
		t.Fatalf("absence probe changed Trash metadata: got %q, want %q", got, fixture.publication.contents)
	}
}

func TestProbeOwnedTrashContentRejectsMismatchedOrUnsafeEntriesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, fixture trashMoveFixture)
	}{
		{
			name: "different regular file",
			prepare: func(t *testing.T, fixture trashMoveFixture) {
				t.Helper()
				createTrashMoveTestFile(t, fixture.source.filesFD, fixture.publication.token)
			},
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, fixture trashMoveFixture) {
				t.Helper()
				if err := unix.Symlinkat("untrusted-target", fixture.source.filesFD, fixture.publication.token); err != nil {
					t.Fatalf("Symlinkat() error = %v", err)
				}
			},
		},
		{
			name: "fifo",
			prepare: func(t *testing.T, fixture trashMoveFixture) {
				t.Helper()
				if err := unix.Mkfifoat(fixture.source.filesFD, fixture.publication.token, 0o600); err != nil {
					t.Fatalf("Mkfifoat() error = %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashMoveFixture(t)
			test.prepare(t, fixture)

			present, err := ProbeOwnedTrashContent(
				context.Background(),
				fixture.directories,
				fixture.publication.token,
				fixture.expected,
			)
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("ProbeOwnedTrashContent() error = %v, want ErrDrifted", err)
			}
			if present {
				t.Fatal("ProbeOwnedTrashContent() accepted a mismatched or unsafe files entry")
			}
			if !testEntryExists(t, fixture.sourceParent, "item") {
				t.Fatal("probe moved or removed the original source entry")
			}
			if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
				t.Fatal("probe removed the mismatched or unsafe files entry")
			}
			if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
				t.Fatalf("probe changed Trash metadata: got %q, want %q", got, fixture.publication.contents)
			}
		})
	}
}

func TestProbeOwnedTrashContentRejectsIncompatibleAuthorityAndPrecondition(t *testing.T) {
	fixture := newTrashMoveFixture(t)

	legacy, err := openTrashDirectoriesWithSource(fixture.source)
	if err != nil {
		t.Fatalf("openTrashDirectoriesWithSource() error = %v", err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := ProbeOwnedTrashContent(context.Background(), legacy, fixture.publication.token, fixture.expected); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ProbeOwnedTrashContent(legacy) error = %v, want ErrUnsupported", err)
	}

	otherRoot, err := domain.NewTrustedRootID("linuxfs-content-probe-other-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	wrongRoot := fixture.expected
	wrongTarget, err := domain.NewFilesystemTarget(otherRoot, fixture.expected.Target.Filesystem.Path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	wrongRoot.Target = wrongTarget
	if _, err := ProbeOwnedTrashContent(context.Background(), fixture.directories, fixture.publication.token, wrongRoot); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ProbeOwnedTrashContent(wrong root) error = %v, want ErrUnsupported", err)
	}

	incompatibleMask, err := RequiredStatMask(domain.ActionRestoreTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	incompatible := fixture.expected
	incompatible.Required = incompatibleMask
	if _, err := ProbeOwnedTrashContent(context.Background(), fixture.directories, fixture.publication.token, incompatible); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ProbeOwnedTrashContent(incompatible precondition) error = %v, want ErrUnsupported", err)
	}
	if _, err := ProbeOwnedTrashContent(context.Background(), fixture.directories, "foreign-token", fixture.expected); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ProbeOwnedTrashContent(unowned token) error = %v, want ErrUnsupported", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ProbeOwnedTrashContent(ctx, fixture.directories, fixture.publication.token, fixture.expected); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("ProbeOwnedTrashContent(canceled) error = %v, want ErrInterrupted", err)
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("rejected content probe moved or removed the original source entry")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("rejected content probe created a files entry")
	}
}

func TestProbeOwnedTrashContentRejectsReplacementAtFinalExactNameCheck(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)

	present, err := probeOwnedTrashContentWith(
		context.Background(),
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
		func(ctx context.Context, directoryFD int, name string, expected domain.FilesystemPrecondition, initial domain.FilesystemSnapshot) error {
			if err := unix.Unlinkat(directoryFD, name, 0); err != nil {
				return err
			}
			createTrashMoveTestFile(t, directoryFD, name)
			return verifyOwnedTrashContentName(ctx, directoryFD, name, expected, initial)
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("probeOwnedTrashContentWith() error = %v, want ErrDrifted", err)
	}
	if present {
		t.Fatal("probeOwnedTrashContentWith() accepted a replacement at the exact files name")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("final exact-name verifier removed the replacement files entry")
	}
}

func TestProbeOwnedTrashContentRejectsTopologyDriftAfterFinalExactNameCheck(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)

	present, err := probeOwnedTrashContentWith(
		context.Background(),
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
		func(ctx context.Context, directoryFD int, name string, expected domain.FilesystemPrecondition, initial domain.FilesystemSnapshot) error {
			if err := verifyOwnedTrashContentName(ctx, directoryFD, name, expected, initial); err != nil {
				return err
			}
			fixture.source.topologyDuplicateErr = mounts.ErrLeaseClosed
			return nil
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("probeOwnedTrashContentWith() error = %v, want ErrDrifted", err)
	}
	if present {
		t.Fatal("probeOwnedTrashContentWith() accepted topology drift after final name verification")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("topology-drift probe removed the retained files entry")
	}
}

func TestProbeOwnedTrashContentClassifiesInterruptedOpen(t *testing.T) {
	_, _, err := openOwnedTrashContentBeneathWith(
		context.Background(),
		42,
		"ldc-0123456789abcdef",
		func(int, string, *unix.OpenHow) (int, error) { return -1, unix.EINTR },
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("openOwnedTrashContentBeneathWith() error = %v, want ErrInterrupted", err)
	}
}
