//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRestoreTrashNoReplaceRestoresVerifiedTokenAndRetainsMetadata(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)

	disposition, err := RestoreTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("RestoreTrashNoReplace() error = %v", err)
	}
	if disposition != TrashRestoreVerified {
		t.Fatalf("RestoreTrashNoReplace() disposition = %v, want verified", disposition)
	}
	if !testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("verified Trash restoration did not recreate the original source entry")
	}
	if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("verified Trash restoration retained the files token")
	}
	if got := readTopologyTestFile(t, fixture.source.infoFD, fixture.publication.token+trashInfoFilenameSuffix); string(got) != string(fixture.publication.contents) {
		t.Fatalf("Trash metadata changed during verified restoration: got %q, want %q", got, fixture.publication.contents)
	}
}

func TestRestoreTrashNoReplaceDoesNotOverwriteOccupiedSource(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)
	createStageTestFile(t, fixture.sourceParent, "item")
	replacement := trashMoveTestSnapshot(t, fixture.sourceParent, "item", fixture.expected.Required)

	disposition, err := RestoreTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("RestoreTrashNoReplace() occupied-source error = %v, want ErrDrifted", err)
	}
	if disposition != TrashRestoreNotApplied {
		t.Fatalf("RestoreTrashNoReplace() occupied-source disposition = %v, want not applied", disposition)
	}
	if observed := trashMoveTestSnapshot(t, fixture.sourceParent, "item", fixture.expected.Required); observed.Inode != replacement.Inode {
		t.Fatal("Trash restoration overwrote the occupied original source entry")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("Trash restoration removed the files token after a no-replace collision")
	}
}

func TestRestoreTrashNoReplaceRejectsStaleFilesIdentityBeforeRename(t *testing.T) {
	fixture := newTrashMoveFixture(t)
	moveTrashMoveFixture(t, fixture)
	if err := unix.Unlinkat(fixture.source.filesFD, fixture.publication.token, 0); err != nil {
		t.Fatalf("remove retained Trash token: %v", err)
	}
	createTrashMoveTestFile(t, fixture.source.filesFD, fixture.publication.token)

	disposition, err := RestoreTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication.token,
		fixture.expected,
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("RestoreTrashNoReplace() stale-files error = %v, want ErrDrifted", err)
	}
	if disposition != TrashRestoreNotApplied {
		t.Fatalf("RestoreTrashNoReplace() stale-files disposition = %v, want not applied", disposition)
	}
	if testEntryExists(t, fixture.sourceParent, "item") {
		t.Fatal("stale Trash token was restored into the original source path")
	}
	if !trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
		t.Fatal("stale Trash token was removed before restoration was rejected")
	}
}

func TestRestoreTrashNoReplaceLeavesPostRenameUncertaintyIndeterminate(t *testing.T) {
	for _, test := range []struct {
		name  string
		hooks trashRestoreHooks
	}{
		{
			name: "destination identity mismatch",
			hooks: trashRestoreHooks{
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
		},
		{
			name: "directory sync failure",
			hooks: trashRestoreHooks{
				renameNoReplace: renameNoReplace,
				syncDirectory: func(int) error {
					return unix.EIO
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTrashMoveFixture(t)
			moveTrashMoveFixture(t, fixture)

			disposition, err := restoreTrashNoReplaceWith(
				context.Background(),
				fixture.sourceParent,
				"item",
				fixture.directories,
				fixture.publication.token,
				fixture.expected,
				test.hooks,
			)
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("restoreTrashNoReplaceWith() post-rename error = %v, want ErrInterrupted", err)
			}
			if disposition != TrashRestoreIndeterminate {
				t.Fatalf("restoreTrashNoReplaceWith() post-rename disposition = %v, want indeterminate", disposition)
			}
			if !testEntryExists(t, fixture.sourceParent, "item") {
				t.Fatal("post-rename uncertainty removed the restored source entry")
			}
			if trashMoveEntryExists(t, fixture.source.filesFD, fixture.publication.token) {
				t.Fatal("post-rename uncertainty rolled the restored token back into Trash")
			}
		})
	}
}

func moveTrashMoveFixture(t *testing.T, fixture trashMoveFixture) {
	t.Helper()
	disposition, err := MovePublishedTrashNoReplace(
		context.Background(),
		fixture.sourceParent,
		"item",
		fixture.directories,
		fixture.publication,
		fixture.expected,
	)
	if err != nil {
		t.Fatalf("MovePublishedTrashNoReplace() setup error = %v", err)
	}
	if disposition != TrashMoveVerified {
		t.Fatalf("MovePublishedTrashNoReplace() setup disposition = %v, want verified", disposition)
	}
}
