//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

// TrashRestoreDisposition states the strongest fact the descriptor-rooted
// Trash restoration could establish. Callers must record or reconcile every
// non-verified outcome through their durable recovery authority.
type TrashRestoreDisposition uint8

const (
	// TrashRestoreNotApplied means no rename was attempted or a no-replace
	// rename failure conclusively retained the entry in Trash files.
	TrashRestoreNotApplied TrashRestoreDisposition = iota + 1
	// TrashRestoreIndeterminate means a rename may have happened, or it
	// happened but the required identity, durability, or topology proof did
	// not finish.
	TrashRestoreIndeterminate
	// TrashRestoreVerified means the exact Trash token was restored
	// no-replace, its original identity was verified, both changed directories
	// were synced, and the Trash topology was reproven.
	TrashRestoreVerified
)

type trashRestoreHooks struct {
	renameNoReplace func(int, string, int, string) error
	syncDirectory   func(int) error
}

// RestoreTrashNoReplace moves files/<token> back to its exact precondition-
// bound original basename only when that basename is vacant. It accepts only
// topology-qualified Trash directories and an owned token. It never copies,
// overwrites, removes .trashinfo metadata, or falls back to a pathname
// operation.
//
// A non-verified disposition never authorizes a blind retry: callers must
// use their durable recovery ticket to record the fact or reconcile it.
func RestoreTrashNoReplace(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
) (TrashRestoreDisposition, error) {
	return restoreTrashNoReplaceWith(ctx, source, sourceBasename, directories, token, expected, trashRestoreHooks{
		renameNoReplace: renameNoReplace,
		syncDirectory:   unix.Fsync,
	})
}

func restoreTrashNoReplaceWith(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
	hooks trashRestoreHooks,
) (TrashRestoreDisposition, error) {
	if err := contextError(ctx); err != nil {
		return TrashRestoreNotApplied, err
	}
	if hooks.renameNoReplace == nil {
		return TrashRestoreNotApplied, fmt.Errorf("%w: no-replace Trash restoration implementation is unavailable", ErrUnsupported)
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = unix.Fsync
	}

	clonedExpected, err := validateTrashRestoreRequest(source, sourceBasename, directories, token, expected)
	if err != nil {
		return TrashRestoreNotApplied, err
	}

	sourceFD, err := source.duplicate()
	if err != nil {
		return TrashRestoreNotApplied, err
	}
	defer unix.Close(sourceFD)

	directories.mu.Lock()
	defer directories.mu.Unlock()
	pair, err := directories.duplicateCheckedLocked()
	if err != nil {
		return TrashRestoreNotApplied, err
	}
	defer pair.Close()

	sourceParent, err := ensureQualifiedTrashMoveDirectories(sourceFD, pair.FilesFD)
	if err != nil {
		return TrashRestoreNotApplied, err
	}
	// The original target is absent while retained in files/<token>, so use the
	// immutable precondition identity to prevent restoring Trash structure or
	// metadata into a topology-protected destination.
	if err := rejectProtectedTrashSourceLocked(directories, sourceParent, clonedExpected.Snapshot); err != nil {
		return TrashRestoreNotApplied, err
	}
	if err := verifyTrashRestoreSource(ctx, pair.FilesFD, token, clonedExpected); err != nil {
		return TrashRestoreNotApplied, err
	}
	if err := contextError(ctx); err != nil {
		return TrashRestoreNotApplied, err
	}

	if err := hooks.renameNoReplace(pair.FilesFD, token, sourceFD, sourceBasename); err != nil {
		if trashMoveRenameDidNotApply(err) {
			return TrashRestoreNotApplied, classifyTrashRestoreRenameNotApplied(err)
		}
		return TrashRestoreIndeterminate, indeterminateTrashRestoreError("no-replace Trash restoration", err)
	}

	if err := verifyTrashRestoreDestination(ctx, sourceFD, sourceBasename, clonedExpected); err != nil {
		return TrashRestoreIndeterminate, indeterminateTrashRestoreError("restored source identity", err)
	}
	if err := syncTrashMoveDirectories(pair.FilesFD, sourceFD, hooks.syncDirectory); err != nil {
		return TrashRestoreIndeterminate, indeterminateTrashRestoreError("restored directory entries", err)
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return TrashRestoreIndeterminate, indeterminateTrashRestoreError("Trash topology", err)
	}
	return TrashRestoreVerified, nil
}

func validateTrashRestoreRequest(
	source *ParentLease,
	sourceBasename string,
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
) (domain.FilesystemPrecondition, error) {
	if source == nil || directories == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source and topology-qualified Trash directories are required", ErrUnsupported)
	}
	if !directories.topologyQualified() {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: content restorations require topology-qualified Trash directories", ErrUnsupported)
	}
	if err := validateBasename(sourceBasename); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := validateOwnedTrashToken(token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := validateBasename(token); err != nil {
		return domain.FilesystemPrecondition{}, err
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
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash restoration requires mask %b, got %b", ErrUnsupported, required, clonedExpected.Required)
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
	if source.RootID() != directories.rootID || source.RootID() != clonedExpected.Target.Filesystem.Root {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source, Trash directories, and precondition roots differ", ErrUnsupported)
	}
	if !sameBytePath(source.plannedPath, clonedExpected.Target.Filesystem.Path) {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source parent lease was not resolved for the precondition target", ErrUnsupported)
	}
	return clonedExpected, nil
}

func verifyTrashRestoreSource(
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

func verifyTrashRestoreDestination(
	ctx context.Context,
	sourceFD int,
	sourceBasename string,
	expected domain.FilesystemPrecondition,
) error {
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		return err
	}
	observed, err := snapshotNamedTarget(ctx, sourceFD, sourceBasename, postMove.Required)
	if err != nil {
		return err
	}
	return ComparePrecondition(postMove, observed)
}

func classifyTrashRestoreRenameNotApplied(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("%w: original source basename is occupied: %v", ErrDrifted, err)
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: no-replace Trash restoration is unavailable: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: no-replace Trash restoration did not apply: %v", ErrDrifted, err)
	}
}

func indeterminateTrashRestoreError(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s requires reconciliation", ErrInterrupted, operation)
	}
	return fmt.Errorf("%w: %w: %s requires reconciliation", ErrInterrupted, err, operation)
}
