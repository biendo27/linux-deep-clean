//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

// removalMaximumDepth bounds the descriptor stack used by the two-pass tree
// walker. A deeper tree is safe to retain but not safe to process through an
// unbounded descriptor recursion in the default executor.
const removalMaximumDepth = 120

type removalHooks struct {
	openTarget    func(context.Context, int, string) (int, error)
	openDirectory func(context.Context, int, string) (int, error)
	readNames     func(int) ([]string, error)
	unlink        func(int, string, int) error
	syncDirectory func(int) error
}

func defaultRemovalHooks() removalHooks {
	return removalHooks{
		openTarget: func(ctx context.Context, parentFD int, name string) (int, error) {
			return openTargetBeneath(ctx, unix.Openat2, parentFD, name)
		},
		openDirectory: func(ctx context.Context, parentFD int, name string) (int, error) {
			return openDirectoryBeneath(ctx, unix.Openat2, parentFD, name)
		},
		readNames:     readDirectoryNames,
		unlink:        unix.Unlinkat,
		syncDirectory: unix.Fsync,
	}
}

// RemoveStagedTree permanently removes only a still-verified staged object
// carrying an exclusive-removal authority. No current production staging
// constructor grants that authority: a same-UID peer can replace a name after
// classification but before unlinkat. When a future engine/helper authority
// can prove that race impossible, this core first proves that the complete
// descriptor-rooted tree contains only regular files and directories, then
// walks it again and reclassifies each entry immediately before unlinking it.
// If any effect may have occurred but durable completion cannot be proved, the
// staged object becomes indeterminate; it is never silently restored or
// retried.
func RemoveStagedTree(ctx context.Context, object *StagedObject) error {
	return removeStagedTreeWith(ctx, object, defaultRemovalHooks())
}

func removeStagedTreeWith(ctx context.Context, object *StagedObject, hooks removalHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if object == nil {
		return fmt.Errorf("%w: nil staged object", ErrDrifted)
	}
	if err := hooks.validate(); err != nil {
		return err
	}

	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state == stagedStateClosed {
		return fmt.Errorf("%w: staged object is closed", ErrDrifted)
	}
	if object.state == stagedStateRemoved {
		return fmt.Errorf("%w: staged object was already removed", ErrDrifted)
	}
	if object.state == stagedStateIndeterminate || object.state == stagedStateRemoving {
		return fmt.Errorf("%w: staged removal state requires reconciliation", ErrInterrupted)
	}
	if object.state != stagedStateVerified {
		return fmt.Errorf("%w: staged content is not verified for removal", ErrRetained)
	}
	if !object.removalAuthorized {
		return fmt.Errorf("%w: irreversible removal requires an authority that excludes same-UID staging races", ErrUnsupported)
	}

	if err := verifyStagedIdentityLocked(ctx, object); err != nil {
		object.state = stagedStateRetained
		return retainedStageError(err)
	}
	if err := preflightRemovalTree(ctx, object.stagingParentFD, object.token, 0, hooks); err != nil {
		return err
	}

	// A durable stage move must be observable at both directory boundaries
	// before the first irreversible unlink. A failure here has no removal
	// effect and leaves the object verified and restorable.
	if err := syncRemovalDirectory(object.sourceParentFD, hooks); err != nil {
		return err
	}
	if err := syncRemovalDirectory(object.stagingParentFD, hooks); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	// Recheck the full post-rename identity after the preflight/durability work
	// and immediately before the removal pass begins.
	if err := verifyStagedIdentityLocked(ctx, object); err != nil {
		object.state = stagedStateRetained
		return retainedStageError(err)
	}

	effectStarted := false
	startEffect := func() {
		if !effectStarted {
			effectStarted = true
			object.state = stagedStateRemoving
		}
	}
	if err := removeVerifiedEntry(ctx, object.stagingParentFD, object.token, 0, startEffect, hooks); err != nil {
		if effectStarted {
			object.state = stagedStateIndeterminate
			return indeterminateRemovalError(err)
		}
		return err
	}
	if !effectStarted {
		// A regular file or directory root must require its final unlink. Treat
		// a future implementation that violates that invariant as unknown
		// completion rather than reporting a false success.
		object.state = stagedStateIndeterminate
		return fmt.Errorf("%w: removal completed without a final unlink", ErrInterrupted)
	}
	if err := syncRemovalDirectory(object.stagingParentFD, hooks); err != nil {
		object.state = stagedStateIndeterminate
		return indeterminateRemovalError(err)
	}
	if err := syncRemovalDirectory(object.sourceParentFD, hooks); err != nil {
		object.state = stagedStateIndeterminate
		return indeterminateRemovalError(err)
	}
	object.state = stagedStateRemoved
	return nil
}

func (hooks removalHooks) validate() error {
	if hooks.openTarget == nil || hooks.openDirectory == nil || hooks.readNames == nil || hooks.unlink == nil || hooks.syncDirectory == nil {
		return fmt.Errorf("%w: removal safety implementation is incomplete", ErrUnsupported)
	}
	return nil
}

// preflightRemovalTree completes before the first unlink. It accepts only
// regular files and directories and intentionally treats every other d_type
// as hostile; directory entry type hints are never trusted over statx on a
// newly opened descriptor.
func preflightRemovalTree(ctx context.Context, parentFD int, basename string, depth int, hooks removalHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if depth > removalMaximumDepth {
		return fmt.Errorf("%w: staged tree exceeds descriptor-safe depth %d", ErrUnsupported, removalMaximumDepth)
	}
	typeOfEntry, err := removableEntryType(ctx, parentFD, basename, hooks)
	if err != nil {
		return err
	}
	if typeOfEntry == domain.FileTypeRegular {
		return nil
	}

	directoryFD, err := hooks.openDirectory(ctx, parentFD, basename)
	if err != nil {
		return err
	}
	defer unix.Close(directoryFD)
	names, err := hooks.readNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := validateBasename(name); err != nil {
			return err
		}
		if err := preflightRemovalTree(ctx, directoryFD, name, depth+1, hooks); err != nil {
			return err
		}
	}
	return nil
}

// removeVerifiedEntry repeats classification from a held parent descriptor at
// every recursion boundary. The callback is invoked before—not after—the
// irreversible syscall, so an interrupted syscall is always reconciled rather
// than assumed not to have changed the tree.
func removeVerifiedEntry(ctx context.Context, parentFD int, basename string, depth int, startEffect func(), hooks removalHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if depth > removalMaximumDepth {
		return fmt.Errorf("%w: staged tree exceeds descriptor-safe depth %d", ErrUnsupported, removalMaximumDepth)
	}
	typeOfEntry, err := removableEntryType(ctx, parentFD, basename, hooks)
	if err != nil {
		return err
	}
	if typeOfEntry == domain.FileTypeRegular {
		if err := contextError(ctx); err != nil {
			return err
		}
		startEffect()
		if err := hooks.unlink(parentFD, basename, 0); err != nil {
			return fmt.Errorf("unlink staged regular entry %q: %w", basename, err)
		}
		return nil
	}

	directoryFD, err := hooks.openDirectory(ctx, parentFD, basename)
	if err != nil {
		return err
	}
	defer unix.Close(directoryFD)
	names, err := hooks.readNames(directoryFD)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := validateBasename(name); err != nil {
			return err
		}
		if err := removeVerifiedEntry(ctx, directoryFD, name, depth+1, startEffect, hooks); err != nil {
			return err
		}
	}
	// Fsync a changed child directory before removing its name from the parent.
	// The outer directory's own fsync happens at its next ancestor boundary.
	if err := syncRemovalDirectory(directoryFD, hooks); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	startEffect()
	if err := hooks.unlink(parentFD, basename, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("remove staged directory %q: %w", basename, err)
	}
	return nil
}

func removableEntryType(ctx context.Context, parentFD int, basename string, hooks removalHooks) (domain.FileType, error) {
	if err := validateBasename(basename); err != nil {
		return "", err
	}
	fd, err := hooks.openTarget(ctx, parentFD, basename)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd)
	snapshot, err := SnapshotFD(fd, domain.FilesystemFieldType)
	if err != nil {
		return "", err
	}
	switch snapshot.Type {
	case domain.FileTypeRegular, domain.FileTypeDirectory:
		return snapshot.Type, nil
	default:
		return "", fmt.Errorf("%w: staged entry %q is %s, not a regular file or directory", ErrUnsupported, basename, snapshot.Type)
	}
}

func readDirectoryNames(directoryFD int) ([]string, error) {
	if directoryFD < 0 {
		return nil, fmt.Errorf("%w: invalid directory descriptor", ErrDrifted)
	}
	buffer := make([]byte, 32*1024)
	var names []string
	for {
		count, err := unix.ReadDirent(directoryFD, buffer)
		if err != nil {
			return nil, fmt.Errorf("%w: read staged directory entries: %v", ErrDrifted, err)
		}
		if count == 0 {
			return names, nil
		}
		consumed, _, parsed := unix.ParseDirent(buffer[:count], -1, names)
		if consumed != count {
			return nil, fmt.Errorf("%w: malformed staged directory entries", ErrDrifted)
		}
		names = parsed
	}
}

func syncRemovalDirectory(directoryFD int, hooks removalHooks) error {
	if err := hooks.syncDirectory(directoryFD); err != nil {
		switch {
		case errors.Is(err, unix.EBADF), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
			return fmt.Errorf("%w: staged directory durability is unavailable: %v", ErrUnsupported, err)
		default:
			return fmt.Errorf("%w: staged directory sync outcome is not durable: %v", ErrInterrupted, err)
		}
	}
	return nil
}

func indeterminateRemovalError(cause error) error {
	return fmt.Errorf("%w: staged removal may have changed content and requires reconciliation: %v", ErrInterrupted, cause)
}
