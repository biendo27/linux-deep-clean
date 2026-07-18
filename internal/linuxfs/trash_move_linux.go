//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

// TrashMoveDisposition states the strongest fact the descriptor-rooted Trash
// move could establish. It is data only: callers still need a durable ticket
// to record or reconcile the effect.
type TrashMoveDisposition uint8

const (
	// TrashMoveNotApplied means no rename was attempted or a no-replace rename
	// failure conclusively left the source in place.
	TrashMoveNotApplied TrashMoveDisposition = iota + 1
	// TrashMoveIndeterminate means a rename may have happened, or it happened
	// but the required identity, durability, or topology proof did not finish.
	TrashMoveIndeterminate
	// TrashMoveVerified means the receipt-bound source was moved no-replace,
	// its destination identity was verified, and both changed directories were
	// synced before the Trash topology was reproven.
	TrashMoveVerified
)

type trashMoveHooks struct {
	renameNoReplace func(int, string, int, string) error
	syncDirectory   func(int) error
}

// MovePublishedTrashNoReplace moves one exact precondition-bound source into
// files/<receipt token>. It accepts only a topology-qualified Trash directory
// lease and the exact durable metadata receipt that was published for that
// token. It never copies, overwrites, removes metadata, or falls back to a
// pathname operation.
//
// A non-verified disposition never authorizes a blind retry: the caller must
// use its durable recovery ticket to record the fact or reconcile it.
func MovePublishedTrashNoReplace(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	publication *TrashInfoPublication,
	expected domain.FilesystemPrecondition,
) (TrashMoveDisposition, error) {
	return movePublishedTrashNoReplaceWith(ctx, source, sourceBasename, directories, publication, expected, trashMoveHooks{
		renameNoReplace: renameNoReplace,
		syncDirectory:   unix.Fsync,
	})
}

func movePublishedTrashNoReplaceWith(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	publication *TrashInfoPublication,
	expected domain.FilesystemPrecondition,
	hooks trashMoveHooks,
) (TrashMoveDisposition, error) {
	if err := contextError(ctx); err != nil {
		return TrashMoveNotApplied, err
	}
	if hooks.renameNoReplace == nil {
		return TrashMoveNotApplied, fmt.Errorf("%w: no-replace Trash rename implementation is unavailable", ErrUnsupported)
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = unix.Fsync
	}

	clonedExpected, err := validateTrashMoveRequest(source, sourceBasename, directories, publication, expected)
	if err != nil {
		return TrashMoveNotApplied, err
	}

	sourceFD, err := source.duplicate()
	if err != nil {
		return TrashMoveNotApplied, err
	}
	defer unix.Close(sourceFD)

	directories.mu.Lock()
	defer directories.mu.Unlock()
	pair, err := directories.duplicateCheckedLocked()
	if err != nil {
		return TrashMoveNotApplied, err
	}
	defer pair.Close()

	sourceParent, err := ensureQualifiedTrashMoveDirectories(sourceFD, pair.FilesFD)
	if err != nil {
		return TrashMoveNotApplied, err
	}
	if err := verifyTrashMovePublicationLocked(ctx, directories, pair, publication); err != nil {
		return TrashMoveNotApplied, err
	}
	observed, err := snapshotNamedTarget(ctx, sourceFD, sourceBasename, clonedExpected.Required)
	if err != nil {
		return TrashMoveNotApplied, err
	}
	if err := ComparePrecondition(clonedExpected, observed); err != nil {
		return TrashMoveNotApplied, err
	}
	if err := rejectProtectedTrashSourceLocked(directories, sourceParent, observed); err != nil {
		return TrashMoveNotApplied, err
	}
	if err := contextError(ctx); err != nil {
		return TrashMoveNotApplied, err
	}

	if err := hooks.renameNoReplace(sourceFD, sourceBasename, pair.FilesFD, publication.token); err != nil {
		if trashMoveRenameDidNotApply(err) {
			return TrashMoveNotApplied, classifyTrashMoveRenameNotApplied(err)
		}
		return TrashMoveIndeterminate, indeterminateTrashMoveError("no-replace Trash rename", err)
	}

	if err := verifyTrashMoveDestination(ctx, pair.FilesFD, publication.token, clonedExpected); err != nil {
		return TrashMoveIndeterminate, indeterminateTrashMoveError("Trash destination identity", err)
	}
	if err := syncTrashMoveDirectories(pair.FilesFD, sourceFD, hooks.syncDirectory); err != nil {
		return TrashMoveIndeterminate, indeterminateTrashMoveError("Trash directory entries", err)
	}
	if err := verifyTrashMovePublicationLocked(ctx, directories, pair, publication); err != nil {
		return TrashMoveIndeterminate, indeterminateTrashMoveError("Trash metadata and topology", err)
	}
	return TrashMoveVerified, nil
}

func validateTrashMoveRequest(
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	publication *TrashInfoPublication,
	expected domain.FilesystemPrecondition,
) (domain.FilesystemPrecondition, error) {
	if source == nil || directories == nil || publication == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source, topology-qualified Trash directories, and metadata receipt are required", ErrUnsupported)
	}
	if !directories.topologyQualified() {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: content moves require topology-qualified Trash directories", ErrUnsupported)
	}
	if err := validateBasename(sourceBasename); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := publication.rootID.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: metadata receipt root identity: %v", ErrUnsupported, err)
	}
	if err := validateOwnedTrashToken(publication.token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if _, err := trashInfoBasename(publication.token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if len(publication.contents) > maximumDurableFileBytes {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: metadata receipt contents exceed %d-byte limit", ErrUnsupported, maximumDurableFileBytes)
	}
	if err := publication.infoRecord.ValidateFor(publishedFileIdentityMask); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid metadata receipt identity: %v", ErrUnsupported, err)
	}

	clonedExpected := cloneFilesystemPrecondition(expected)
	if err := clonedExpected.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid filesystem precondition: %v", ErrUnsupported, err)
	}
	required, err := RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if clonedExpected.Required != required {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash move requires mask %b, got %b", ErrUnsupported, required, clonedExpected.Required)
	}
	if clonedExpected.Target.Filesystem == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: filesystem precondition has no filesystem target", ErrUnsupported)
	}
	components, expectedBasename, err := pathComponentsAndBasename(clonedExpected.Target.Filesystem.Path)
	if err != nil || len(components) == 0 {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid expected filesystem target path", ErrUnsupported)
	}
	if expectedBasename != sourceBasename {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source basename does not match the precondition target", ErrUnsupported)
	}
	if source.RootID() == "" || directories.rootID == "" {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source and Trash directories must retain trusted-root identities", ErrUnsupported)
	}
	if source.RootID() != directories.rootID ||
		source.RootID() != publication.rootID ||
		source.RootID() != clonedExpected.Target.Filesystem.Root {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source, Trash directories, metadata receipt, and precondition roots differ", ErrUnsupported)
	}
	if !sameBytePath(source.plannedPath, clonedExpected.Target.Filesystem.Path) {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source parent lease was not resolved for the precondition target", ErrUnsupported)
	}
	if err := requirePublicationDirectoryIdentity("Trash files directory", publication.files, directories.files); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := requirePublicationDirectoryIdentity("Trash info directory", publication.infoDirectory, directories.info); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	return clonedExpected, nil
}

func requirePublicationDirectoryIdentity(role string, receipt, directories domain.FilesystemSnapshot) error {
	if err := compareTrashDirectoryIdentity(role, directories, receipt); err != nil {
		return fmt.Errorf("%w: metadata receipt does not bind these %s: %v", ErrUnsupported, role, err)
	}
	return nil
}

func ensureQualifiedTrashMoveDirectories(sourceFD, filesFD int) (domain.FilesystemSnapshot, error) {
	const relationshipMask = domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldMountID

	source, err := SnapshotFD(sourceFD, relationshipMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("inspect source parent for Trash move: %w", err)
	}
	files, err := SnapshotFD(filesFD, relationshipMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("inspect Trash files directory: %w", err)
	}
	if source.Type != domain.FileTypeDirectory || files.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: source parent and Trash files descriptors must be directories", ErrDrifted)
	}
	if source.DeviceMajor != files.DeviceMajor || source.DeviceMinor != files.DeviceMinor || source.MountID != files.MountID {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: Trash files directory is not on the source mount", ErrUnsupported)
	}
	if source.Inode == files.Inode {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: Trash files directory must differ from the source parent", ErrUnsupported)
	}
	return source, nil
}

// rejectProtectedTrashSourceLocked prevents this content primitive from
// relocating the metadata or structure that grants its own authority. The
// topology evidence is held under directories.mu and has already been
// requalified through the descriptor pair before this check runs.
func rejectProtectedTrashSourceLocked(
	directories *TrashDirectories,
	sourceParent domain.FilesystemSnapshot,
	sourceTarget domain.FilesystemSnapshot,
) error {
	if directories == nil || directories.topology == nil {
		return fmt.Errorf("%w: content moves require topology-qualified Trash directories", ErrUnsupported)
	}

	parentRoles := []struct {
		name     string
		snapshot domain.FilesystemSnapshot
	}{
		{name: "Trash root", snapshot: directories.topology.trashRoot},
		{name: "Trash files directory", snapshot: directories.files},
		{name: "Trash info directory", snapshot: directories.info},
	}
	targetRoles := []struct {
		name     string
		snapshot domain.FilesystemSnapshot
	}{
		{name: "Trash topology anchor", snapshot: directories.topology.anchor},
		{name: "Trash root", snapshot: directories.topology.trashRoot},
		{name: "Trash files directory", snapshot: directories.files},
		{name: "Trash info directory", snapshot: directories.info},
	}
	if directories.topology.placement == mounts.TrashPlacementTopShared {
		sharedTop := struct {
			name     string
			snapshot domain.FilesystemSnapshot
		}{name: "shared Trash parent", snapshot: directories.topology.sharedTop}
		parentRoles = append(parentRoles, sharedTop)
		targetRoles = append(targetRoles, sharedTop)
	}

	for _, role := range parentRoles {
		matches, err := sameTrashMoveObjectIdentity(sourceParent, role.snapshot)
		if err != nil {
			return fmt.Errorf("%w: compare source parent with protected %s: %v", ErrUnsupported, role.name, err)
		}
		if matches {
			return fmt.Errorf("%w: source parent is protected %s", ErrUnsupported, role.name)
		}
	}
	for _, role := range targetRoles {
		matches, err := sameTrashMoveObjectIdentity(sourceTarget, role.snapshot)
		if err != nil {
			return fmt.Errorf("%w: compare source target with protected %s: %v", ErrUnsupported, role.name, err)
		}
		if matches {
			return fmt.Errorf("%w: source target is protected %s", ErrUnsupported, role.name)
		}
	}
	return nil
}

func sameTrashMoveObjectIdentity(left, right domain.FilesystemSnapshot) (bool, error) {
	required := domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldMountID
	if err := left.ValidateFor(required); err != nil {
		return false, err
	}
	if err := right.ValidateFor(required); err != nil {
		return false, err
	}
	return left.DeviceMajor == right.DeviceMajor &&
		left.DeviceMinor == right.DeviceMinor &&
		left.Inode == right.Inode &&
		left.Type == right.Type &&
		left.MountID == right.MountID, nil
}

func verifyTrashMovePublicationLocked(
	ctx context.Context,
	directories *TrashDirectories,
	pair mounts.TrashDescriptorPair,
	publication *TrashInfoPublication,
) error {
	if directories == nil || publication == nil {
		return fmt.Errorf("%w: topology-qualified Trash directories and metadata receipt are required", ErrUnsupported)
	}
	if !directories.topologyQualified() {
		return fmt.Errorf("%w: content moves require topology-qualified Trash directories", ErrUnsupported)
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return err
	}
	basename, err := trashInfoBasename(publication.token)
	if err != nil {
		return err
	}
	if err := verifyPublishedName(ctx, pair.InfoFD, basename, publication.infoRecord, publication.contents); err != nil {
		return err
	}
	return nil
}

func verifyTrashMoveDestination(
	ctx context.Context,
	filesFD int,
	token string,
	expected domain.FilesystemPrecondition,
) error {
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		return err
	}
	observed, err := snapshotNamedTarget(ctx, filesFD, token, postMove.Required)
	if err != nil {
		return err
	}
	return ComparePrecondition(postMove, observed)
}

func syncTrashMoveDirectories(filesFD, sourceFD int, syncDirectory func(int) error) error {
	if syncDirectory == nil {
		return fmt.Errorf("invalid Trash-directory sync operation")
	}
	for _, directory := range []struct {
		name string
		fd   int
	}{
		{name: "Trash files directory", fd: filesFD},
		{name: "source parent", fd: sourceFD},
	} {
		if err := syncDirectory(directory.fd); err != nil {
			return fmt.Errorf("sync %s: %w", directory.name, err)
		}
	}
	return nil
}

func trashMoveRenameDidNotApply(err error) bool {
	switch {
	case errors.Is(err, unix.EEXIST),
		errors.Is(err, unix.ENOTEMPTY),
		errors.Is(err, unix.EXDEV),
		errors.Is(err, unix.ENOSYS),
		errors.Is(err, unix.EINVAL),
		errors.Is(err, unix.EOPNOTSUPP),
		errors.Is(err, unix.EROFS),
		errors.Is(err, unix.ENOENT),
		errors.Is(err, unix.ENOTDIR),
		errors.Is(err, unix.ELOOP):
		return true
	default:
		return false
	}
}

func classifyTrashMoveRenameNotApplied(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("%w: Trash files token already exists: %v", ErrDrifted, err)
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: no-replace Trash move is unavailable: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: no-replace Trash move did not apply: %v", ErrDrifted, err)
	}
}

func indeterminateTrashMoveError(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s requires reconciliation", ErrInterrupted, operation)
	}
	return fmt.Errorf("%w: %w: %s requires reconciliation", ErrInterrupted, err, operation)
}
