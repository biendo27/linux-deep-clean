//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

type stagedState uint8

const (
	stagedStateVerified stagedState = iota + 1
	stagedStateRemoving
	stagedStateRemoved
	stagedStateRetained
	stagedStateRestored
	stagedStateIndeterminate
	stagedStateClosed
)

const privateStagingDirectoryMask = baselineFilesystemMask

// PrivateStagingLease is an opaque qualification of a private staging
// directory. Its fields are deliberately private: a caller cannot turn an
// arbitrary ParentLease into mutation authority by constructing this value.
//
// Layout-backed leases are created only from an engine/helper-attested
// LayoutPrivateStaging authority. The legacy parent-backed constructor stays
// package-private while the safety foundation is migrated; neither form
// grants irreversible-removal authority.
type PrivateStagingLease struct {
	parent    *ParentLease
	directory *PrivateDirectoryLease
	rootID    domain.TrustedRootID
	expected  domain.FilesystemSnapshot
}

// OpenPrivateStaging converts the one qualified private-staging layout into
// nominal staging authority. It accepts no caller path or raw descriptor and
// requalifies the layout before every stage operation.
func OpenPrivateStaging(layout *mounts.LayoutLease) (*PrivateStagingLease, error) {
	return openPrivateStagingWithSource(layout)
}

func openPrivateStagingWithSource(source privateDirectorySource) (*PrivateStagingLease, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: trusted private staging layout is required", ErrUnsupported)
	}
	if source.Kind() != mounts.LayoutPrivateStaging {
		return nil, fmt.Errorf("%w: layout kind %q is not private staging", ErrUnsupported, source.Kind())
	}
	directory, err := openPrivateDirectoryWithSource(source)
	if err != nil {
		return nil, err
	}
	return &PrivateStagingLease{directory: directory, rootID: directory.RootID()}, nil
}

// newPrivateStagingLease is intentionally package-private until an
// engine/helper-owned private-layout registry can vouch for a staging
// directory. It requires a caller-owned, 0700 directory and records stable
// descriptor evidence that is rechecked at each stage operation.
func newPrivateStagingLease(parent *ParentLease) (*PrivateStagingLease, error) {
	if parent == nil {
		return nil, fmt.Errorf("%w: private staging parent is required", ErrUnsupported)
	}
	rootID := parent.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: private staging root identity: %v", ErrUnsupported, err)
	}

	fd, err := parent.duplicate()
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)

	expected, err := snapshotPrivateStagingDirectory(fd)
	if err != nil {
		return nil, err
	}
	return &PrivateStagingLease{parent: parent, rootID: rootID, expected: expected}, nil
}

// duplicate returns a caller-owned descriptor only to the staging primitive.
// It rechecks the directory's private ownership, exact permissions, stable
// identity, and trusted-root binding before a rename can use it.
func (lease *PrivateStagingLease) duplicate() (int, error) {
	if lease == nil {
		return -1, fmt.Errorf("%w: qualified private staging lease is required", ErrUnsupported)
	}
	if err := lease.rootID.Validate(); err != nil {
		return -1, fmt.Errorf("%w: private staging root identity: %v", ErrUnsupported, err)
	}
	if lease.directory != nil {
		if lease.parent != nil {
			return -1, fmt.Errorf("%w: private staging lease has conflicting authorities", ErrUnsupported)
		}
		if lease.directory.RootID() != lease.rootID {
			return -1, fmt.Errorf("%w: private staging root identity changed", ErrDrifted)
		}
		if lease.directory.Kind() != mounts.LayoutPrivateStaging {
			return -1, fmt.Errorf("%w: private staging layout kind changed", ErrDrifted)
		}
		return lease.directory.duplicate()
	}
	if lease.parent == nil {
		return -1, fmt.Errorf("%w: qualified private staging lease is required", ErrUnsupported)
	}
	if lease.parent.RootID() != lease.rootID {
		return -1, fmt.Errorf("%w: private staging root identity changed", ErrDrifted)
	}

	fd, err := lease.parent.duplicate()
	if err != nil {
		return -1, err
	}
	keepFD := false
	defer func() {
		if !keepFD {
			_ = unix.Close(fd)
		}
	}()

	observed, err := snapshotPrivateStagingDirectory(fd)
	if err != nil {
		return -1, err
	}
	if err := comparePrivateStagingIdentity(lease.expected, observed); err != nil {
		return -1, err
	}
	keepFD = true
	return fd, nil
}

func (lease *PrivateStagingLease) isQualified() bool {
	return lease != nil && (lease.parent != nil) != (lease.directory != nil)
}

func snapshotPrivateStagingDirectory(fd int) (domain.FilesystemSnapshot, error) {
	snapshot, err := SnapshotFD(fd, privateStagingDirectoryMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private staging descriptor is not a directory", ErrUnsupported)
	}
	if snapshot.UID.Value != uint32(unix.Geteuid()) {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private staging directory is not owned by the executor UID", ErrUnsupported)
	}
	if snapshot.Mode.Value&0o7777 != 0o700 {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: private staging directory must have exact mode 0700", ErrUnsupported)
	}
	return snapshot, nil
}

func comparePrivateStagingIdentity(expected, observed domain.FilesystemSnapshot) error {
	if expected.DeviceMajor != observed.DeviceMajor || expected.DeviceMinor != observed.DeviceMinor {
		return driftedField("private staging device")
	}
	if expected.Inode != observed.Inode {
		return driftedField("private staging inode")
	}
	if expected.Type != observed.Type {
		return driftedField("private staging type")
	}
	if expected.UID != observed.UID || expected.GID != observed.GID {
		return driftedField("private staging ownership")
	}
	if expected.Mode != observed.Mode {
		return driftedField("private staging mode")
	}
	if expected.MountID != observed.MountID {
		return driftedField("private staging mount ID")
	}
	return nil
}

// StagedObject owns duplicated source and private-staging directory
// descriptors after a no-replace move. It deliberately exposes neither raw
// descriptors nor a generic deletion operation: a verified object still needs
// a future exclusive-removal authority before descriptor-walking removal can
// have any effect.
type StagedObject struct {
	mu sync.Mutex

	sourceParentFD  int
	stagingParentFD int
	sourceBasename  string
	token           string
	kind            domain.ActionKind
	expected        domain.FilesystemPrecondition
	state           stagedState

	// removalAuthorized stays false for every production staging constructor.
	// A name-based unlink cannot atomically prove it still denotes the FD that
	// was just classified, and a 0700 directory does not exclude another
	// process running with the same UID. A future engine/helper authority may
	// enable irreversible removal only after it can prove exclusive ownership
	// of the staged namespace; until then content is retained.
	removalAuthorized bool
}

// RootID reports the typed root that was bound to the source precondition.
// It does not describe a filesystem path or create new authority.
func (object *StagedObject) RootID() domain.TrustedRootID {
	if object == nil || object.expected.Target.Filesystem == nil {
		return ""
	}
	return object.expected.Target.Filesystem.Root
}

// Token is the already-validated opaque one-component staging name. It is
// suitable for a typed recovery record but not for arbitrary pathname use.
func (object *StagedObject) Token() string {
	if object == nil {
		return ""
	}
	return object.token
}

// Close releases the duplicated directory descriptors. It does not mutate the
// staged entry and is idempotent so callers can defer it on every result path.
func (object *StagedObject) Close() error {
	if object == nil {
		return nil
	}

	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state == stagedStateClosed {
		return nil
	}
	object.state = stagedStateClosed
	return closeStagedDescriptors(object.sourceParentFD, object.stagingParentFD)
}

type stageHooks struct {
	renameNoReplace func(int, string, int, string) error
	syncDirectory   func(int) error
}

// StageNoReplace checks the named source against its precondition, moves it
// into a distinct held staging directory with RENAME_NOREPLACE, then reopens
// and verifies the staged object. A post-move mismatch returns a non-nil
// retained StagedObject with errors matching both ErrDrifted and ErrRetained;
// the object cannot be removed through the generic cleanup path.
func StageNoReplace(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	staging *PrivateStagingLease,
	token string,
	kind domain.ActionKind,
	expected domain.FilesystemPrecondition,
) (*StagedObject, error) {
	return stageNoReplaceWith(ctx, source, sourceBasename, staging, token, kind, expected, stageHooks{
		renameNoReplace: renameNoReplace,
		syncDirectory:   unix.Fsync,
	})
}

func stageNoReplaceWith(
	ctx context.Context,
	source *ParentLease,
	sourceBasename string,
	staging *PrivateStagingLease,
	token string,
	kind domain.ActionKind,
	expected domain.FilesystemPrecondition,
	hooks stageHooks,
) (*StagedObject, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = unix.Fsync
	}
	clonedExpected, err := validateStageRequest(source, sourceBasename, staging, token, kind, expected, hooks)
	if err != nil {
		return nil, err
	}

	sourceFD, err := source.duplicate()
	if err != nil {
		return nil, err
	}
	stagingFD, err := staging.duplicate()
	if err != nil {
		_ = unix.Close(sourceFD)
		return nil, err
	}
	object := &StagedObject{
		sourceParentFD:  sourceFD,
		stagingParentFD: stagingFD,
		sourceBasename:  sourceBasename,
		token:           token,
		kind:            kind,
		expected:        clonedExpected,
	}
	keepObject := false
	defer func() {
		if !keepObject {
			_ = object.Close()
		}
	}()

	if err := ensureQualifiedStagingDirectories(sourceFD, stagingFD); err != nil {
		return nil, err
	}
	observed, err := snapshotNamedTarget(ctx, sourceFD, sourceBasename, clonedExpected.Required)
	if err != nil {
		return nil, err
	}
	if err := ComparePrecondition(clonedExpected, observed); err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	if err := hooks.renameNoReplace(sourceFD, sourceBasename, stagingFD, token); err != nil {
		if renameFailureMayHaveMoved(err) {
			object.state = stagedStateIndeterminate
			keepObject = true
			return object, classifyUncertainRename(err)
		}
		return nil, classifyRenameFailure(err)
	}

	object.state = stagedStateRetained
	keepObject = true
	if err := verifyStagedIdentityLocked(ctx, object); err != nil {
		return object, retainedStageError(err)
	}
	if err := syncStagedDirectories(object, hooks.syncDirectory); err != nil {
		object.state = stagedStateIndeterminate
		return object, indeterminateDurabilityError("staged move", err)
	}
	object.state = stagedStateVerified
	return object, nil
}

// VerifyStagedIdentity reopens the staged token beneath its held directory
// descriptor. A verification failure changes the object to retained so a
// later generic removal cannot delete content whose identity is uncertain.
func VerifyStagedIdentity(ctx context.Context, object *StagedObject) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if object == nil {
		return fmt.Errorf("%w: nil staged object", ErrDrifted)
	}

	object.mu.Lock()
	defer object.mu.Unlock()
	switch object.state {
	case stagedStateVerified:
		if err := verifyStagedIdentityLocked(ctx, object); err != nil {
			object.state = stagedStateRetained
			return retainedStageError(err)
		}
		return nil
	case stagedStateRemoving:
		return fmt.Errorf("%w: staged object is being removed and requires reconciliation", ErrInterrupted)
	case stagedStateRemoved:
		return fmt.Errorf("%w: staged object was already removed", ErrDrifted)
	case stagedStateRetained:
		return fmt.Errorf("%w: %w: staged object is retained after failed identity verification", ErrDrifted, ErrRetained)
	case stagedStateIndeterminate:
		return fmt.Errorf("%w: staged rename state requires reconciliation", ErrInterrupted)
	case stagedStateRestored:
		return fmt.Errorf("%w: staged object was already restored", ErrDrifted)
	default:
		return fmt.Errorf("%w: staged object is closed", ErrDrifted)
	}
}

// RestoreNoReplace restores a still-verified staged object only if the
// original basename is vacant. It never overwrites an entry. A collision
// leaves content retained in staging; any uncertainty after a successful move
// becomes indeterminate for explicit reconciliation.
func RestoreNoReplace(ctx context.Context, object *StagedObject) error {
	return restoreNoReplaceWith(ctx, object, restoreHooks{
		renameNoReplace: renameNoReplace,
		verifyRestored:  verifyRestoredIdentityLocked,
		syncDirectory:   unix.Fsync,
	})
}

type restoreHooks struct {
	renameNoReplace func(int, string, int, string) error
	verifyRestored  func(context.Context, *StagedObject) error
	syncDirectory   func(int) error
}

func restoreNoReplaceWith(ctx context.Context, object *StagedObject, hooks restoreHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if object == nil {
		return fmt.Errorf("%w: nil staged object", ErrDrifted)
	}
	if hooks.renameNoReplace == nil {
		hooks.renameNoReplace = renameNoReplace
	}
	if hooks.verifyRestored == nil {
		hooks.verifyRestored = verifyRestoredIdentityLocked
	}
	if hooks.syncDirectory == nil {
		hooks.syncDirectory = unix.Fsync
	}

	object.mu.Lock()
	defer object.mu.Unlock()
	if object.state == stagedStateClosed {
		return fmt.Errorf("%w: staged object is closed", ErrDrifted)
	}
	if object.state == stagedStateRestored {
		return nil
	}
	if object.state == stagedStateIndeterminate {
		return fmt.Errorf("%w: staged rename state requires reconciliation", ErrInterrupted)
	}
	if object.state == stagedStateRemoving {
		return fmt.Errorf("%w: staged object is being removed and requires reconciliation", ErrInterrupted)
	}
	if object.state == stagedStateRemoved {
		return fmt.Errorf("%w: staged object was already removed", ErrDrifted)
	}
	if object.state != stagedStateVerified {
		return fmt.Errorf("%w: staged content is retained because its identity is not verified", ErrRetained)
	}
	if err := verifyStagedIdentityLocked(ctx, object); err != nil {
		object.state = stagedStateRetained
		return retainedStageError(err)
	}

	err := hooks.renameNoReplace(object.stagingParentFD, object.token, object.sourceParentFD, object.sourceBasename)
	if err == nil {
		if err := hooks.verifyRestored(ctx, object); err != nil {
			object.state = stagedStateIndeterminate
			return indeterminateRestoreError("restored target identity", err)
		}
		if err := syncStagedDirectories(object, hooks.syncDirectory); err != nil {
			object.state = stagedStateIndeterminate
			return indeterminateRestoreError("restored directory entries", err)
		}
		object.state = stagedStateRestored
		return nil
	}
	if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EXDEV) {
		object.state = stagedStateRetained
		return fmt.Errorf("%w: no-replace restoration did not move staged content: %v", ErrRetained, err)
	}
	if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
		object.state = stagedStateIndeterminate
		return fmt.Errorf("%w: no-replace restoration may require reconciliation: %v", ErrInterrupted, err)
	}
	object.state = stagedStateIndeterminate
	return fmt.Errorf("%w: no-replace restoration outcome is unknown: %v", ErrInterrupted, err)
}

func validateStageRequest(
	source *ParentLease,
	sourceBasename string,
	staging *PrivateStagingLease,
	token string,
	kind domain.ActionKind,
	expected domain.FilesystemPrecondition,
	hooks stageHooks,
) (domain.FilesystemPrecondition, error) {
	if source == nil || !staging.isQualified() {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source and qualified private staging leases are required", ErrUnsupported)
	}
	if err := validateBasename(sourceBasename); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := validateBasename(token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if hooks.renameNoReplace == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: no-replace rename implementation is unavailable", ErrUnsupported)
	}
	required, err := RequiredStatMask(kind)
	if err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	clonedExpected := cloneFilesystemPrecondition(expected)
	if err := clonedExpected.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid filesystem precondition: %v", ErrUnsupported, err)
	}
	if clonedExpected.Required != required {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: action %q requires mask %b, got %b", ErrUnsupported, kind, required, clonedExpected.Required)
	}
	if clonedExpected.Target.Filesystem == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: filesystem precondition has no filesystem target", ErrUnsupported)
	}
	expectedComponents, expectedBasename, err := pathComponentsAndBasename(clonedExpected.Target.Filesystem.Path)
	if err != nil || len(expectedComponents) == 0 {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid expected filesystem target path", ErrUnsupported)
	}
	if expectedBasename != sourceBasename {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source basename does not match the precondition target", ErrUnsupported)
	}
	if source.RootID() == "" || staging.rootID == "" {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: parent leases must retain trusted-root identities", ErrUnsupported)
	}
	if source.RootID() != staging.rootID || source.RootID() != clonedExpected.Target.Filesystem.Root {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source, staging, and precondition roots differ", ErrUnsupported)
	}
	if !sameBytePath(source.plannedPath, clonedExpected.Target.Filesystem.Path) {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source parent lease was not resolved for the precondition target", ErrUnsupported)
	}
	return clonedExpected, nil
}

func cloneFilesystemPrecondition(precondition domain.FilesystemPrecondition) domain.FilesystemPrecondition {
	cloned := precondition
	cloned.Target = precondition.Target.Clone()
	return cloned
}

func sameBytePath(left, right pathbytes.BytePath) bool {
	leftComponents := left.Components()
	rightComponents := right.Components()
	if len(leftComponents) != len(rightComponents) {
		return false
	}
	for index := range leftComponents {
		if string(leftComponents[index]) != string(rightComponents[index]) {
			return false
		}
	}
	return true
}

func snapshotNamedTarget(ctx context.Context, parentFD int, basename string, required domain.FilesystemFieldMask) (domain.FilesystemSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if err := validateBasename(basename); err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	fd, err := openTargetBeneath(ctx, unix.Openat2, parentFD, basename)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	defer unix.Close(fd)

	snapshot, err := SnapshotFD(fd, required)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeRegular && snapshot.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: staged target %q is %s, not a regular file or directory", ErrUnsupported, basename, snapshot.Type)
	}
	return snapshot, nil
}

func verifyStagedIdentityLocked(ctx context.Context, object *StagedObject) error {
	postMove, err := postMovePrecondition(object.expected)
	if err != nil {
		return err
	}
	observed, err := snapshotNamedTarget(ctx, object.stagingParentFD, object.token, postMove.Required)
	if err != nil {
		return err
	}
	return ComparePrecondition(postMove, observed)
}

func verifyRestoredIdentityLocked(ctx context.Context, object *StagedObject) error {
	postMove, err := postMovePrecondition(object.expected)
	if err != nil {
		return err
	}
	observed, err := snapshotNamedTarget(ctx, object.sourceParentFD, object.sourceBasename, postMove.Required)
	if err != nil {
		return err
	}
	return ComparePrecondition(postMove, observed)
}

func postMovePrecondition(expected domain.FilesystemPrecondition) (domain.FilesystemPrecondition, error) {
	postMove := expected
	// renameat2 changes ctime on a successfully moved inode. ChangedAt is a
	// useful freshness check before the move, but cannot distinguish identity
	// after it. Every other requested fact remains mandatory post-move.
	postMove.Required &^= domain.FilesystemFieldChangedAt
	if err := postMove.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: post-move identity mask: %v", ErrUnsupported, err)
	}
	return postMove, nil
}

func ensureQualifiedStagingDirectories(sourceFD, stagingFD int) error {
	const relationshipMask = domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldMountID

	source, err := SnapshotFD(sourceFD, relationshipMask)
	if err != nil {
		return fmt.Errorf("inspect source parent for staging: %w", err)
	}
	staging, err := SnapshotFD(stagingFD, relationshipMask)
	if err != nil {
		return fmt.Errorf("inspect private staging parent: %w", err)
	}
	if source.Type != domain.FileTypeDirectory || staging.Type != domain.FileTypeDirectory {
		return fmt.Errorf("%w: source and private staging descriptors must be directories", ErrDrifted)
	}
	if source.DeviceMajor != staging.DeviceMajor || source.DeviceMinor != staging.DeviceMinor || source.MountID != staging.MountID {
		return fmt.Errorf("%w: private staging directory is not on the source mount", ErrUnsupported)
	}
	if source.Inode == staging.Inode {
		return fmt.Errorf("%w: staging directory must differ from source parent", ErrUnsupported)
	}
	return nil
}

func syncStagedDirectories(object *StagedObject, syncDirectory func(int) error) error {
	if object == nil || syncDirectory == nil {
		return fmt.Errorf("invalid staged-directory sync operation")
	}
	for _, directory := range []struct {
		name string
		fd   int
	}{
		{name: "source parent", fd: object.sourceParentFD},
		{name: "private staging parent", fd: object.stagingParentFD},
	} {
		if err := syncDirectory(directory.fd); err != nil {
			return fmt.Errorf("sync %s: %w", directory.name, err)
		}
	}
	return nil
}

func indeterminateDurabilityError(operation string, err error) error {
	return fmt.Errorf("%w: %s could not be durably confirmed: %v", ErrInterrupted, operation, err)
}

func indeterminateRestoreError(operation string, err error) error {
	return fmt.Errorf("%w: no-replace restoration succeeded but %s could not be confirmed: %v", ErrInterrupted, operation, err)
}

func renameNoReplace(oldParentFD int, oldName string, newParentFD int, newName string) error {
	return unix.Renameat2(oldParentFD, oldName, newParentFD, newName, unix.RENAME_NOREPLACE)
}

func renameFailureMayHaveMoved(err error) bool {
	return errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN)
}

func classifyRenameFailure(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("%w: staging token already exists: %v", ErrDrifted, err)
	case errors.Is(err, unix.EXDEV), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP):
		return fmt.Errorf("%w: no-replace staging is unavailable: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: no-replace staging failed: %v", ErrDrifted, err)
	}
}

func classifyUncertainRename(err error) error {
	return fmt.Errorf("%w: no-replace staging may require reconciliation: %v", ErrInterrupted, err)
}

func retainedStageError(cause error) error {
	return fmt.Errorf("%w: %w: staged identity could not be proven: %v", ErrRetained, ErrDrifted, cause)
}

func closeStagedDescriptors(sourceFD, stagingFD int) error {
	var closeErr error
	if sourceFD >= 0 {
		if err := unix.Close(sourceFD); err != nil {
			closeErr = fmt.Errorf("close staged source parent: %w", err)
		}
	}
	if stagingFD >= 0 {
		if err := unix.Close(stagingFD); err != nil && closeErr == nil {
			closeErr = fmt.Errorf("close staged destination parent: %w", err)
		}
	}
	return closeErr
}
