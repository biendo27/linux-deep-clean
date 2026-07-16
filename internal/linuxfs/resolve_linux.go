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

const (
	openat2AttemptLimit         = 3
	requiredOpenat2ResolveFlags = uint64(unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV)
)

type openat2Func func(int, string, *unix.OpenHow) (int, error)
type closeFunc func(int) error

// ParentLease owns a syncable resolved directory descriptor immediately above
// a target. Every mutating operation must use this FD plus one validated
// basename; it has no API that accepts a multi-component apply path.
type ParentLease struct {
	rootID      domain.TrustedRootID
	plannedPath pathbytes.BytePath

	mu     sync.Mutex
	fd     int
	closed bool
}

// RootID reports the authority identity inherited from the trusted root lease.
// It is not a pathname and cannot be used to derive a new descriptor.
func (lease *ParentLease) RootID() domain.TrustedRootID {
	if lease == nil {
		return ""
	}
	return lease.rootID
}

// withFD borrows the held parent descriptor while preventing Close from
// racing the operation. It remains package-private so callers cannot replace
// the authority-bearing descriptor by closing and reusing its number.
func (lease *ParentLease) withFD(operation func(int) error) error {
	if lease == nil || operation == nil {
		return fmt.Errorf("%w: invalid parent lease operation", ErrDrifted)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.fd < 0 {
		return fmt.Errorf("%w: parent lease is closed", ErrDrifted)
	}
	return operation(lease.fd)
}

// duplicate returns a CLOEXEC descriptor for another internal
// descriptor-relative safety primitive; it never reconstructs the parent
// pathname or exposes raw descriptor authority outside linuxfs.
func (lease *ParentLease) duplicate() (int, error) {
	if lease == nil {
		return -1, fmt.Errorf("%w: parent lease is nil", ErrDrifted)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.fd < 0 {
		return -1, fmt.Errorf("%w: parent lease is closed", ErrDrifted)
	}
	fd, err := unix.FcntlInt(uintptr(lease.fd), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return -1, fmt.Errorf("%w: duplicate parent descriptor: %v", ErrDrifted, err)
	}
	return fd, nil
}

// Sync makes directory-entry changes durable when the caller has completed a
// mutation that promises durability. Parent leases are opened read-only rather
// than O_PATH specifically so this operation has a usable descriptor.
func (lease *ParentLease) Sync() error {
	return syncParentLeaseWith(lease, unix.Fsync)
}

func syncParentLeaseWith(lease *ParentLease, syncDirectory func(int) error) error {
	if lease == nil || syncDirectory == nil {
		return fmt.Errorf("%w: invalid parent-directory sync operation", ErrDrifted)
	}
	return lease.withFD(func(fd int) error {
		if err := syncDirectory(fd); err != nil {
			switch {
			case errors.Is(err, unix.EBADF), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
				return fmt.Errorf("%w: directory durability is unavailable: %v", ErrUnsupported, err)
			default:
				return fmt.Errorf("%w: directory sync outcome is not durable: %v", ErrInterrupted, err)
			}
		}
		return nil
	})
}

// Close releases the parent descriptor. It is idempotent so every error path
// can defer it without changing the operation result.
func (lease *ParentLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	fd := lease.fd
	lease.fd = -1
	if fd < 0 {
		return nil
	}
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("close parent descriptor: %w", err)
	}
	return nil
}

// TargetHandle owns an O_PATH descriptor for a non-symlink regular file or
// directory. Snapshot reads facts only from this FD, never by re-resolving the
// final basename.
type TargetHandle struct {
	mu     sync.Mutex
	fd     int
	closed bool
}

// Snapshot returns action-required statx facts from the held target FD.
func (handle *TargetHandle) Snapshot(required domain.FilesystemFieldMask) (domain.FilesystemSnapshot, error) {
	if handle == nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: target handle is nil", ErrDrifted)
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed || handle.fd < 0 {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: target handle is closed", ErrDrifted)
	}
	return SnapshotFD(handle.fd, required)
}

// withFD borrows the held target descriptor while preventing Close from
// racing a descriptor-relative operation. It is package-private so the target
// identity handle cannot be replaced by an arbitrary callback.
func (handle *TargetHandle) withFD(operation func(int) error) error {
	if handle == nil || operation == nil {
		return fmt.Errorf("%w: invalid target-handle operation", ErrDrifted)
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed || handle.fd < 0 {
		return fmt.Errorf("%w: target handle is closed", ErrDrifted)
	}
	return operation(handle.fd)
}

// Close releases the target descriptor and is safe to call repeatedly.
func (handle *TargetHandle) Close() error {
	if handle == nil {
		return nil
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed {
		return nil
	}
	handle.closed = true
	fd := handle.fd
	handle.fd = -1
	if fd < 0 {
		return nil
	}
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("close target descriptor: %w", err)
	}
	return nil
}

// ResolveParent walks all but the final BytePath component using the required
// openat2 constraints. It returns a held parent descriptor and exactly one
// final basename suitable for renameat2/unlinkat; no fallback string-path
// traversal exists.
func ResolveParent(ctx context.Context, root *mounts.RootLease, path pathbytes.BytePath) (*ParentLease, string, error) {
	if root == nil {
		return nil, "", fmt.Errorf("%w: trusted root lease is nil", ErrDrifted)
	}
	rootFD, err := root.DuplicateRootDescriptor()
	if err != nil {
		return nil, "", fmt.Errorf("%w: duplicate trusted root: %v", ErrDrifted, err)
	}
	return resolveParentFromOwnedRootFDForRoot(ctx, rootFD, root.RootID(), path, unix.Openat2)
}

func resolveParentWithRootFD(ctx context.Context, rootFD int, path pathbytes.BytePath, open openat2Func) (*ParentLease, string, error) {
	if rootFD < 0 {
		return nil, "", fmt.Errorf("%w: invalid trusted root descriptor", ErrDrifted)
	}
	duplicate, err := unix.FcntlInt(uintptr(rootFD), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, "", fmt.Errorf("%w: duplicate test root descriptor: %v", ErrDrifted, err)
	}
	return resolveParentFromOwnedRootFD(ctx, duplicate, path, open)
}

func resolveParentFromOwnedRootFD(ctx context.Context, rootFD int, path pathbytes.BytePath, open openat2Func) (*ParentLease, string, error) {
	return resolveParentFromOwnedRootFDForRoot(ctx, rootFD, "", path, open)
}

func resolveParentFromOwnedRootFDForRoot(ctx context.Context, rootFD int, rootID domain.TrustedRootID, path pathbytes.BytePath, open openat2Func) (*ParentLease, string, error) {
	return resolveParentFromOwnedRootFDForRootWithClose(ctx, rootFD, rootID, path, open, unix.Close)
}

// resolveParentFromOwnedRootFDForRootWithClose keeps close behavior injectable
// for the one ambiguous-close branch. Linux close errors must not be retried:
// the descriptor number may already have been released and reused. The caller
// therefore disarms ownership before observing an error.
func resolveParentFromOwnedRootFDForRootWithClose(ctx context.Context, rootFD int, rootID domain.TrustedRootID, path pathbytes.BytePath, open openat2Func, closeFD closeFunc) (*ParentLease, string, error) {
	if closeFD == nil {
		return nil, "", fmt.Errorf("%w: missing descriptor close implementation", ErrUnsupported)
	}
	if ctx == nil {
		_ = closeFD(rootFD)
		return nil, "", fmt.Errorf("%w: nil context", ErrInterrupted)
	}
	if open == nil {
		_ = closeFD(rootFD)
		return nil, "", fmt.Errorf("%w: missing openat2 implementation", ErrUnsupported)
	}
	components, basename, err := pathComponentsAndBasename(path)
	if err != nil {
		_ = closeFD(rootFD)
		return nil, "", err
	}
	plannedPath, err := pathbytes.New(path.Components())
	if err != nil {
		_ = closeFD(rootFD)
		return nil, "", fmt.Errorf("%w: copy resolved byte path: %v", ErrUnsupported, err)
	}

	currentFD := rootFD
	keepCurrent := false
	defer func() {
		if !keepCurrent && currentFD >= 0 {
			_ = closeFD(currentFD)
		}
	}()

	for _, component := range components[:len(components)-1] {
		if err := contextError(ctx); err != nil {
			return nil, "", err
		}
		nextFD, err := openDirectoryBeneath(ctx, open, currentFD, component)
		if err != nil {
			return nil, "", err
		}
		previousFD := currentFD
		// A failed close has ambiguous ownership. Disarm the deferred cleanup
		// before invoking it so the number is never closed twice/reused.
		currentFD = -1
		if err := closeFD(previousFD); err != nil {
			_ = closeFD(nextFD)
			return nil, "", fmt.Errorf("%w: close prior parent descriptor: %v", ErrDrifted, err)
		}
		currentFD = nextFD
	}
	if err := contextError(ctx); err != nil {
		return nil, "", err
	}

	keepCurrent = true
	return &ParentLease{rootID: rootID, plannedPath: plannedPath, fd: currentFD}, basename, nil
}

// OpenTargetHandle opens a single final basename beneath a held parent. The
// statx type check closes the O_PATH|O_NOFOLLOW trailing-symlink gap and rejects
// all special files before an action can stage or remove them.
func OpenTargetHandle(ctx context.Context, parent *ParentLease, basename string) (*TargetHandle, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validateBasename(basename); err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("%w: parent lease is nil", ErrDrifted)
	}

	var targetFD int = -1
	err := parent.withFD(func(parentFD int) error {
		fd, openErr := openTargetBeneath(ctx, unix.Openat2, parentFD, basename)
		if openErr != nil {
			return openErr
		}
		targetFD = fd
		return nil
	})
	if err != nil {
		return nil, err
	}
	keepTarget := false
	defer func() {
		if !keepTarget && targetFD >= 0 {
			_ = unix.Close(targetFD)
		}
	}()

	snapshot, err := SnapshotFD(targetFD, domain.FilesystemFieldType)
	if err != nil {
		return nil, err
	}
	if snapshot.Type != domain.FileTypeRegular && snapshot.Type != domain.FileTypeDirectory {
		return nil, fmt.Errorf("%w: final basename %q is %s, not a regular file or directory", ErrUnsupported, basename, snapshot.Type)
	}
	keepTarget = true
	return &TargetHandle{fd: targetFD}, nil
}

func pathComponentsAndBasename(path pathbytes.BytePath) ([]string, string, error) {
	components := path.Components()
	if _, err := pathbytes.New(components); err != nil {
		return nil, "", fmt.Errorf("%w: invalid relative byte path: %v", ErrUnsupported, err)
	}
	values := make([]string, len(components))
	for index, component := range components {
		values[index] = string(component)
	}
	basename := values[len(values)-1]
	if err := validateBasename(basename); err != nil {
		return nil, "", err
	}
	return values, basename, nil
}

func validateBasename(basename string) error {
	if basename == "" || basename == "." || basename == ".." {
		return fmt.Errorf("%w: basename must name exactly one entry", ErrUnsupported)
	}
	for _, value := range []byte(basename) {
		if value == 0 || value == '/' {
			return fmt.Errorf("%w: basename must not contain NUL or slash", ErrUnsupported)
		}
	}
	return nil
}

func openDirectoryBeneath(ctx context.Context, open openat2Func, parentFD int, component string) (int, error) {
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	return openBeneathWithRetry(ctx, open, parentFD, component, how)
}

func openTargetBeneath(ctx context.Context, open openat2Func, parentFD int, basename string) (int, error) {
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	return openBeneathWithRetry(ctx, open, parentFD, basename, how)
}

func openBeneathWithRetry(ctx context.Context, open openat2Func, parentFD int, name string, how *unix.OpenHow) (int, error) {
	for attempt := 0; attempt < openat2AttemptLimit; attempt++ {
		if err := contextError(ctx); err != nil {
			return -1, err
		}
		fd, err := open(parentFD, name, how)
		if err == nil {
			return fd, nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return -1, classifyOpenat2Error(err)
		}
	}
	return -1, fmt.Errorf("%w: openat2 could not stabilize after %d attempts", ErrDrifted, openat2AttemptLimit)
}

func classifyOpenat2Error(err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.E2BIG) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("%w: required openat2 safety guarantees unavailable: %v", ErrUnsupported, err)
	}
	if errors.Is(err, unix.EXDEV) {
		return fmt.Errorf("%w: resolution attempted to cross a mount boundary: %v", ErrUnsupported, err)
	}
	return fmt.Errorf("%w: constrained openat2 resolution failed: %v", ErrDrifted, err)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInterrupted)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrInterrupted, err)
	}
	return nil
}
