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

const trashMoveTestToken = "ldc-0123456789abcdef"

func TestValidateResolvedTargetBindsParentLeaseToPreconditionPath(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	if err := ValidateResolvedTarget(context.Background(), fixture.sourceParent, "item", fixture.expected); err != nil {
		t.Fatalf("ValidateResolvedTarget() matching source error = %v", err)
	}

	makeTestDirectory(t, fixture.source.rootFD, "other")
	otherParent := stageTestParent(t, fixture.source.rootFD, fixture.source.rootID, "other", "item")
	t.Cleanup(func() { _ = otherParent.Close() })
	createStageTestFile(t, otherParent, "item")
	if err := ValidateResolvedTarget(context.Background(), otherParent, "item", fixture.expected); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ValidateResolvedTarget() mismatched parent error = %v, want ErrUnsupported", err)
	}
}

func TestValidateResolvedParentForTargetBindsAbsentRestoreDestination(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	if err := fixture.sourceParent.withFD(func(parentFD int) error {
		return unix.Unlinkat(parentFD, "item", 0)
	}); err != nil {
		t.Fatalf("remove planned target: %v", err)
	}

	if err := ValidateResolvedParentForTarget(fixture.sourceParent, "item", fixture.expected); err != nil {
		t.Fatalf("ValidateResolvedParentForTarget() absent planned target error = %v", err)
	}
}

func TestMovePublishedTrashNoReplaceMovesReceiptBoundSource(t *testing.T) {
	fixture := newTrashMoveFixture(t)

	disposition, err := MovePublishedTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("MovePublishedTrashNoReplace() error = %v", err)
	}
	if disposition != TrashMoveVerified {
		t.Fatalf("MovePublishedTrashNoReplace() disposition = %v, want verified", disposition)
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("verified Trash move left the source entry in place")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("verified Trash move did not create the receipt token in files")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
		t.Fatalf("Trash metadata changed during verified move: got %q, want %q", got, fixture.publication.contents)
	}
}

func TestMovePublishedTrashNoReplaceRejectsLegacyDirectoriesBeforeRename(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	legacy, err := openTrashDirectoriesWithSource(fixture.source)
	if err != nil {
		t.Fatalf("openTrashDirectoriesWithSource() error = %v", err)
	}
	defer legacy.Close()

	disposition, err := MovePublishedTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		legacy,
		fixture.publication,
		fixture.expected,
	)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("MovePublishedTrashNoReplace() legacy-directory error = %v, want ErrUnsupported", err)
	}
	if disposition != TrashMoveNotApplied {
		t.Fatalf("MovePublishedTrashNoReplace() legacy-directory disposition = %v, want not applied", disposition)
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("legacy Trash directories moved the source")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("legacy Trash directories created a files entry")
	}
}

func TestMovePublishedTrashNoReplaceRejectsChangedMetadataBeforeRename(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	replaceTrashMoveMetadata(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix, []byte("[Trash Info]\nPath=replaced\n"))

	disposition, err := MovePublishedTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("MovePublishedTrashNoReplace() changed-metadata error = %v, want ErrDrifted", err)
	}
	if disposition != TrashMoveNotApplied {
		t.Fatalf("MovePublishedTrashNoReplace() changed-metadata disposition = %v, want not applied", disposition)
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("changed metadata allowed a source move")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("changed metadata created a files entry")
	}
}

func TestMovePublishedTrashNoReplaceDoesNotOverwriteExistingToken(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	createTrashMoveTestFile(t, fixture.source.filesFD, fixture.publication.token)
	previous := trashMoveSnapshot(t, fixture.source.filesFD, fixture.publication.token)

	disposition, err := MovePublishedTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("MovePublishedTrashNoReplace() token-collision error = %v, want ErrDrifted", err)
	}
	if disposition != TrashMoveNotApplied {
		t.Fatalf("MovePublishedTrashNoReplace() token-collision disposition = %v, want not applied", disposition)
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("Trash token collision moved the source")
	}
	if observed := trashMoveSnapshot(t, fixture.source.filesFD, fixture.publication.token); observed.Inode != previous.Inode {
		t.Fatal("Trash token collision overwrote the existing files entry")
	}
}

func TestMovePublishedTrashNoReplaceRejectsProtectedTrashSourcesBeforeRename(t *testing.T) {
	for _, test := range []struct {
		name       string
		prepare    func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition)
		assertSame func(t *testing.T, fixture trashMoveFixture)
	}{
		{
			name: "metadata entry beneath info",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				const basename = "other.trashinfo"
				createTrashMoveTestFile(t, fixture.source.infoFD, basename)
				parent := trashMoveTestParent(t, fixture.source, "anchor", "Trash", "info", basename)
				t.Cleanup(func() { _ = parent.Close() })
				return parent, basename, trashMoveTestPrecondition(t, fixture.source.rootID, parent, testBytePath(t, "anchor", "Trash", "info", basename), basename)
			},
			assertSame: func(t *testing.T, fixture trashMoveFixture) {
				if !trashMoveEntryExists(t, fixture.source.infoFD, "other.trashinfo") {
					t.Fatal("protected metadata source was moved")
				}
			},
		},
		{
			name: "topology info directory",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				const basename = "info"
				parent := trashMoveTestParent(t, fixture.source, "anchor", "Trash", basename)
				t.Cleanup(func() { _ = parent.Close() })
				return parent, basename, trashMoveTestPrecondition(t, fixture.source.rootID, parent, testBytePath(t, "anchor", "Trash", basename), basename)
			},
			assertSame: func(t *testing.T, fixture trashMoveFixture) {
				if !trashMoveEntryExists(t, fixture.source.trashRootFD, "info") {
					t.Fatal("protected Trash info directory was moved")
				}
				if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
					t.Fatal("protected Trash info directory changed the published metadata")
				}
			},
		},
		{
			name: "topology files directory",
			prepare: func(t *testing.T, fixture trashMoveFixture) (*ParentLease, string, domain.FilesystemPrecondition) {
				const basename = "files"
				parent := trashMoveTestParent(t, fixture.source, "anchor", "Trash", basename)
				t.Cleanup(func() { _ = parent.Close() })
				return parent, basename, trashMoveTestPrecondition(t, fixture.source.rootID, parent, testBytePath(t, "anchor", "Trash", basename), basename)
			},
			assertSame: func(t *testing.T, fixture trashMoveFixture) {
				if !trashMoveEntryExists(t, fixture.source.trashRootFD, "files") {
					t.Fatal("protected Trash files directory was moved")
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashMoveFixture(t)
			parent, basename, expected := test.prepare(t, fixture)

			disposition, err := MovePublishedTrashNoReplace(
				context.Background(),
				parent,
				basename,
				fixture.directories,
				fixture.publication,
				expected,
			)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("MovePublishedTrashNoReplace() protected-source error = %v, want ErrUnsupported", err)
			}
			if disposition != TrashMoveNotApplied {
				t.Fatalf("MovePublishedTrashNoReplace() protected-source disposition = %v, want not applied", disposition)
			}
			test.assertSame(t, fixture)
			if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
				t.Fatal("protected Trash source created a files entry")
			}
		})
	}
}

func TestMovePublishedTrashNoReplaceRejectsProtectedAnchorAndSharedTopTargetsBeforeRename(t *testing.T) {
	for _, test := range []struct {
		name       string
		placement  mounts.TrashPlacement
		components func(*topologyTrashTestSource) []string
		basename   func(*topologyTrashTestSource) string
	}{
		{
			name:      "home Trash root beneath anchor",
			placement: mounts.TrashPlacementHome,
			components: func(*topologyTrashTestSource) []string {
				return []string{"anchor", "Trash"}
			},
			basename: func(*topologyTrashTestSource) string { return "Trash" },
		},
		{
			name:      "top shared parent beneath anchor",
			placement: mounts.TrashPlacementTopShared,
			components: func(*topologyTrashTestSource) []string {
				return []string{"anchor", ".Trash"}
			},
			basename: func(*topologyTrashTestSource) string { return ".Trash" },
		},
		{
			name:      "topology anchor beneath root",
			placement: mounts.TrashPlacementHome,
			components: func(*topologyTrashTestSource) []string {
				return []string{"anchor"}
			},
			basename: func(*topologyTrashTestSource) string { return "anchor" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newTopologyTrashTestSource(t, test.placement)
			directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
			if err != nil {
				t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
			}
			t.Cleanup(func() { _ = directories.Close() })
			publication, err := PublishTrashInfoDurable(context.Background(), directories, trashMoveTestToken, []byte("[Trash Info]\nPath=source/item\n"))
			if err != nil {
				t.Fatalf("PublishTrashInfoDurable() error = %v", err)
			}

			components := test.components(source)
			parent := trashMoveTestParent(t, source, components...)
			t.Cleanup(func() { _ = parent.Close() })
			basename := test.basename(source)
			expected := trashMoveTestPrecondition(t, source.rootID, parent, testBytePath(t, components...), basename)

			disposition, err := MovePublishedTrashNoReplace(context.Background(), parent, basename, directories, publication, expected)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("MovePublishedTrashNoReplace() protected topology target error = %v, want ErrUnsupported", err)
			}
			if disposition != TrashMoveNotApplied {
				t.Fatalf("MovePublishedTrashNoReplace() protected topology target disposition = %v, want not applied", disposition)
			}
			if !trashMoveParentEntryExists(t, parent, basename) {
				t.Fatal("protected topology target was moved")
			}
			if trashMoveEntryExists(t, source.filesFD, publication.token) {
				t.Fatal("protected topology target created a files entry")
			}
		})
	}
}

func TestMovePublishedTrashNoReplaceAllowsOrdinaryEntryBeneathHomeAnchor(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	createTrashMoveTestFile(t, fixture.source.anchorFD, "ordinary")
	parent := trashMoveTestParent(t, fixture.source, "anchor", "ordinary")
	t.Cleanup(func() { _ = parent.Close() })
	expected := trashMoveTestPrecondition(t, fixture.source.rootID, parent, testBytePath(t, "anchor", "ordinary"), "ordinary")

	disposition, err := MovePublishedTrashNoReplace(context.Background(), parent, "ordinary", fixture.directories, fixture.publication, expected)
	if err != nil {
		t.Fatalf("MovePublishedTrashNoReplace() ordinary anchor entry error = %v", err)
	}
	if disposition != TrashMoveVerified {
		t.Fatalf("MovePublishedTrashNoReplace() ordinary anchor entry disposition = %v, want verified", disposition)
	}
	if trashMoveEntryExists(t, fixture.source.anchorFD, "ordinary") {
		t.Fatal("ordinary entry beneath the topology anchor was not moved")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("ordinary entry beneath the topology anchor did not create a files entry")
	}
}

func TestMovePublishedTrashNoReplaceLeavesPostRenameMismatchIndeterminate(t *testing.T) {
	fixture := newTrashMoveFixture(t)

	disposition, err := movePublishedTrashNoReplaceWith(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
		trashMoveHooks{
			renameNoReplace: func(oldParentFD int, oldName string, newParentFD int, newName string) error {
				if err := unix.Renameat2(oldParentFD, oldName, newParentFD, newName, unix.RENAME_NOREPLACE); err != nil {
					return err
				}
				if err := unix.Unlinkat(newParentFD, newName, 0); err != nil {
					return err
				}
				createTrashMoveTestFile(t, newParentFD, newName)
				return nil
			},
		},
	)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("movePublishedTrashNoReplaceWith() post-rename mismatch error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if disposition != TrashMoveIndeterminate {
		t.Fatalf("movePublishedTrashNoReplaceWith() post-rename mismatch disposition = %v, want indeterminate", disposition)
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("post-rename mismatch restored or replaced the source")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("post-rename mismatch removed the retained files entry")
	}
}

func TestMovePublishedTrashNoReplaceLeavesUnsyncedMoveIndeterminate(t *testing.T) {
	fixture := newTrashMoveFixture(t)

	disposition, err := movePublishedTrashNoReplaceWith(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
		trashMoveHooks{
			renameNoReplace: renameNoReplace,
			syncDirectory: func(int) error {
				return unix.EIO
			},
		},
	)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("movePublishedTrashNoReplaceWith() sync error = %v, want ErrInterrupted", err)
	}
	if disposition != TrashMoveIndeterminate {
		t.Fatalf("movePublishedTrashNoReplaceWith() sync disposition = %v, want indeterminate", disposition)
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("unsynced Trash move restored or replaced the source")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("unsynced Trash move removed the files entry")
	}
}

type trashMoveFixture struct {
	source       *topologyTrashTestSource
	sourceParent *ParentLease
	directories  *TrashDirectories
	publication  *TrashInfoPublication
	expected     domain.FilesystemPrecondition
}

func newTrashMoveFixture(t *testing.T) trashMoveFixture {
	t.Helper()

	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	makeTestDirectory(t, source.rootFD, "source")
	sourceParent := stageTestParent(t, source.rootFD, source.rootID, "source", "item")
	t.Cleanup(func() {
		_ = sourceParent.Close()
	})
	createStageTestFile(t, sourceParent, "item")
	expected := stageTestPrecondition(t, source.rootID, sourceParent, "item")

	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
	}
	t.Cleanup(func() {
		_ = directories.Close()
	})
	publication, err := PublishTrashInfoDurable(context.Background(), directories, trashMoveTestToken, []byte("[Trash Info]\nPath=source/item\n"))
	if err != nil {
		t.Fatalf("PublishTrashInfoDurable() error = %v", err)
	}
	if publication == nil {
		t.Fatal("PublishTrashInfoDurable() returned no receipt")
	}
	return trashMoveFixture{
		source:       source,
		sourceParent: sourceParent,
		directories:  directories,
		publication:  publication,
		expected:     expected,
	}
}

func trashMoveEntryExists(t *testing.T, parentFD int, name string) bool {
	t.Helper()
	var stat unix.Stat_t
	err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return true
	}
	if errors.Is(err, unix.ENOENT) {
		return false
	}
	t.Fatalf("inspect Trash test entry %q: %v", name, err)
	return false
}

func createTrashMoveTestFile(t *testing.T, parentFD int, name string) {
	t.Helper()
	fd, err := unix.Openat(parentFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create Trash test file %q: %v", name, err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close Trash test file %q: %v", name, err)
	}
}

func trashMoveSnapshot(t *testing.T, parentFD int, name string) domain.FilesystemSnapshot {
	t.Helper()
	fd, err := openTargetBeneath(context.Background(), unix.Openat2, parentFD, name)
	if err != nil {
		t.Fatalf("open Trash test entry %q: %v", name, err)
	}
	defer unix.Close(fd)
	required, err := RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	snapshot, err := SnapshotFD(fd, required)
	if err != nil {
		t.Fatalf("snapshot Trash test entry %q: %v", name, err)
	}
	return snapshot
}

func trashMoveTestParent(t *testing.T, source *topologyTrashTestSource, components ...string) *ParentLease {
	t.Helper()
	parent, _, err := resolveParentWithRootFD(context.Background(), source.rootFD, testBytePath(t, components...), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve protected Trash source parent: %v", err)
	}
	parent.rootID = source.rootID
	return parent
}

func trashMoveTestPrecondition(t *testing.T, rootID domain.TrustedRootID, parent *ParentLease, path pathbytes.BytePath, basename string) domain.FilesystemPrecondition {
	t.Helper()
	target, err := domain.NewFilesystemTarget(rootID, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	required, err := RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	return domain.FilesystemPrecondition{
		Target:   target,
		Required: required,
		Snapshot: trashMoveTestSnapshot(t, parent, basename, required),
	}
}

func trashMoveTestSnapshot(t *testing.T, parent *ParentLease, basename string, required domain.FilesystemFieldMask) domain.FilesystemSnapshot {
	t.Helper()
	fd, err := parent.duplicate()
	if err != nil {
		t.Fatalf("duplicate Trash test parent: %v", err)
	}
	defer unix.Close(fd)
	snapshot, err := snapshotNamedTarget(context.Background(), fd, basename, required)
	if err != nil {
		t.Fatalf("snapshot protected Trash source %q: %v", basename, err)
	}
	return snapshot
}

func trashMoveParentEntryExists(t *testing.T, parent *ParentLease, basename string) bool {
	t.Helper()
	fd, err := parent.duplicate()
	if err != nil {
		t.Fatalf("duplicate Trash test parent: %v", err)
	}
	defer unix.Close(fd)
	return trashMoveEntryExists(t, fd, basename)
}

func replaceTrashMoveMetadata(t *testing.T, infoFD int, name string, contents []byte) {
	t.Helper()
	if err := unix.Unlinkat(infoFD, name, 0); err != nil {
		t.Fatalf("remove Trash test metadata %q: %v", name, err)
	}
	fd, err := unix.Openat(infoFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("replace Trash test metadata %q: %v", name, err)
	}
	if _, err := unix.Write(fd, contents); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("write replacement Trash metadata %q: %v", name, err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close replacement Trash metadata %q: %v", name, err)
	}
}
