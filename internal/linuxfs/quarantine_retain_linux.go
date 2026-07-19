//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

const (
	quarantineRetainTokenPrefix   = "ldc-"
	quarantineRetainTokenHexBytes = 64
)

// QuarantineRetainDisposition states the strongest fact a descriptor-rooted
// quarantine retain operation established. A non-verified result must not be
// blindly retried; callers retain their durable recovery ticket for a later,
// separately authorized reconciliation path.
type QuarantineRetainDisposition uint8

const (
	// QuarantineRetainNotApplied means no rename was attempted or a no-replace
	// rename failure conclusively left the source in place.
	QuarantineRetainNotApplied QuarantineRetainDisposition = iota + 1
	// QuarantineRetainIndeterminate means a rename may have happened, or it
	// happened but identity or durability proof did not complete.
	QuarantineRetainIndeterminate
	// QuarantineRetainVerified means the exact source was retained under its
	// opaque token, its post-move identity was verified, and both changed
	// directories were synced.
	QuarantineRetainVerified
)

type quarantineRetainHooks struct {
	renameNoReplace func(int, string, int, string) error
	syncDirectory   func(int) error
}

// RetainQuarantineNoReplace moves one exact ActionQuarantinePath source into
// an engine/helper-attested private quarantine directory using
// RENAME_NOREPLACE. It accepts neither a path nor a descriptor from callers,
// never copies, overwrites, restores, removes, scans, or exposes retained
// content, and never reports a non-verified result as safely retryable.
func RetainQuarantineNoReplace(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	quarantine *PrivateDirectoryLease,
	token string,
	expected domain.FilesystemPrecondition,
) (QuarantineRetainDisposition, error) {
	return retainQuarantineNoReplaceWith(ctx, source, sourceBasename, quarantine, token, expected, quarantineRetainHooks{
		renameNoReplace: renameNoReplace,
		syncDirectory:   unix.Fsync,
	})
}

func retainQuarantineNoReplaceWith(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	quarantine *PrivateDirectoryLease,
	token string,
	expected domain.FilesystemPrecondition,
	hooks quarantineRetainHooks,
) (QuarantineRetainDisposition, error) {
	if err := contextError(ctx); err != nil {
		return QuarantineRetainNotApplied, err
	}
	if hooks.renameNoReplace == nil {
		return QuarantineRetainNotApplied, fmt.Errorf("%w: no-replace quarantine rename implementation is unavailable", ErrUnsupported)
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = unix.Fsync
	}

	clonedExpected, err := validateQuarantineRetainRequest(source, sourceBasename, quarantine, token, expected)
	if err != nil {
		return QuarantineRetainNotApplied, err
	}

	// Requalification proves that the held source parent is still reachable at
	// its immutable planned location beneath the retained trusted root. A
	// detached parent is drift, never alternate rename authority.
	sourceFD, err := source.requalifiedPlannedParent(ctx)
	if err != nil {
		return QuarantineRetainNotApplied, err
	}
	defer unix.Close(sourceFD)

	quarantineFD, err := quarantine.duplicate()
	if err != nil {
		return QuarantineRetainNotApplied, err
	}
	defer unix.Close(quarantineFD)

	quarantineDirectory, err := ensureQualifiedQuarantineRetainDirectories(sourceFD, quarantineFD)
	if err != nil {
		return QuarantineRetainNotApplied, err
	}
	observed, err := snapshotNamedTarget(ctx, sourceFD, sourceBasename, clonedExpected.Required)
	if err != nil {
		return QuarantineRetainNotApplied, err
	}
	if err := ComparePrecondition(clonedExpected, observed); err != nil {
		return QuarantineRetainNotApplied, err
	}
	if sameQuarantineRetainObjectIdentity(observed, quarantineDirectory) {
		return QuarantineRetainNotApplied, fmt.Errorf("%w: source target is the private quarantine layout", ErrUnsupported)
	}
	if err := contextError(ctx); err != nil {
		return QuarantineRetainNotApplied, err
	}

	if err := hooks.renameNoReplace(sourceFD, sourceBasename, quarantineFD, token); err != nil {
		if quarantineRetainRenameDidNotApply(err) {
			return QuarantineRetainNotApplied, classifyQuarantineRetainRenameNotApplied(err)
		}
		return QuarantineRetainIndeterminate, indeterminateQuarantineRetainError("no-replace quarantine rename", err)
	}

	if err := verifyQuarantineRetainDestination(ctx, quarantineFD, token, clonedExpected); err != nil {
		return QuarantineRetainIndeterminate, indeterminateQuarantineRetainError("quarantine destination identity", err)
	}
	if err := syncQuarantineRetainDirectories(quarantineFD, sourceFD, hooks.syncDirectory); err != nil {
		return QuarantineRetainIndeterminate, indeterminateQuarantineRetainError("quarantine directory entries", err)
	}
	return QuarantineRetainVerified, nil
}

func validateQuarantineRetainRequest(
	source *ParentLease,
	sourceBasename string,
	quarantine *PrivateDirectoryLease,
	token string,
	expected domain.FilesystemPrecondition,
) (domain.FilesystemPrecondition, error) {
	if source == nil || quarantine == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: resolved source and qualified private quarantine leases are required", ErrUnsupported)
	}
	if quarantine.Kind() != mounts.LayoutPrivateQuarantine {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: private directory kind %q is not quarantine", ErrUnsupported, quarantine.Kind())
	}
	if err := validateBasename(sourceBasename); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := validateOwnedQuarantineToken(token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}

	required, err := RequiredStatMask(domain.ActionQuarantinePath)
	if err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	clonedExpected := cloneFilesystemPrecondition(expected)
	if err := clonedExpected.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid filesystem precondition: %v", ErrUnsupported, err)
	}
	if clonedExpected.Required != required {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: ActionQuarantinePath requires mask %b, got %b", ErrUnsupported, required, clonedExpected.Required)
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
	if err := source.RootID().Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source root identity: %v", ErrUnsupported, err)
	}
	if err := quarantine.RootID().Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: private quarantine root identity: %v", ErrUnsupported, err)
	}
	if source.RootID() != quarantine.RootID() || source.RootID() != clonedExpected.Target.Filesystem.Root {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source, private quarantine, and precondition roots differ", ErrUnsupported)
	}
	if !sameBytePath(source.plannedPath, clonedExpected.Target.Filesystem.Path) {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source parent lease was not resolved for the precondition target", ErrUnsupported)
	}
	return clonedExpected, nil
}

func validateOwnedQuarantineToken(token string) error {
	if !strings.HasPrefix(token, quarantineRetainTokenPrefix) || len(token) != len(quarantineRetainTokenPrefix)+quarantineRetainTokenHexBytes {
		return fmt.Errorf("%w: quarantine token must be %q plus %d lowercase hexadecimal bytes", ErrUnsupported, quarantineRetainTokenPrefix, quarantineRetainTokenHexBytes)
	}
	for _, value := range []byte(token[len(quarantineRetainTokenPrefix):]) {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return fmt.Errorf("%w: quarantine token contains an unsupported byte %q", ErrUnsupported, value)
		}
	}
	return validateBasename(token)
}

func ensureQualifiedQuarantineRetainDirectories(sourceFD, quarantineFD int) (domain.FilesystemSnapshot, error) {
	const relationshipMask = domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldMountID

	source, err := SnapshotFD(sourceFD, relationshipMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("inspect source parent for quarantine retain: %w", err)
	}
	quarantine, err := SnapshotFD(quarantineFD, relationshipMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("inspect private quarantine directory: %w", err)
	}
	if source.Type != domain.FileTypeDirectory || quarantine.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: source parent and private quarantine descriptors must be directories", ErrDrifted)
	}
	if source.DeviceMajor != quarantine.DeviceMajor || source.DeviceMinor != quarantine.DeviceMinor || source.MountID != quarantine.MountID {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private quarantine directory is not on the source mount", ErrUnsupported)
	}
	if source.Inode == quarantine.Inode {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private quarantine directory must differ from the source parent", ErrUnsupported)
	}
	return quarantine, nil
}

func sameQuarantineRetainObjectIdentity(left, right domain.FilesystemSnapshot) bool {
	return left.DeviceMajor == right.DeviceMajor &&
		left.DeviceMinor == right.DeviceMinor &&
		left.Inode == right.Inode &&
		left.Type == right.Type &&
		left.MountID == right.MountID
}

func verifyQuarantineRetainDestination(
	ctx context.Context,
	quarantineFD int,
	token string,
	expected domain.FilesystemPrecondition,
) error {
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		return err
	}
	observed, err := snapshotNamedTarget(ctx, quarantineFD, token, postMove.Required)
	if err != nil {
		return err
	}
	return ComparePrecondition(postMove, observed)
}

func syncQuarantineRetainDirectories(quarantineFD, sourceFD int, syncDirectory func(int) error) error {
	if syncDirectory == nil {
		return fmt.Errorf("invalid quarantine-directory sync operation")
	}
	for _, directory := range []struct {
		name string
		fd   int
	}{
		{name: "private quarantine directory", fd: quarantineFD},
		{name: "source parent", fd: sourceFD},
	} {
		if err := syncDirectory(directory.fd); err != nil {
			return fmt.Errorf("sync %s: %w", directory.name, err)
		}
	}
	return nil
}

func quarantineRetainRenameDidNotApply(err error) bool {
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

func classifyQuarantineRetainRenameNotApplied(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("%w: quarantine token already exists: %v", ErrDrifted, err)
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: no-replace quarantine retain is unavailable: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: no-replace quarantine retain did not apply: %v", ErrDrifted, err)
	}
}

func indeterminateQuarantineRetainError(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s requires reconciliation", ErrInterrupted, operation)
	}
	return fmt.Errorf("%w: %w: %s requires reconciliation", ErrInterrupted, err, operation)
}
