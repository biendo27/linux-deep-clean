//go:build linux

package linuxfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

const (
	durableFileMode         = 0o600
	maximumDurableFileBytes = 1 << 20
)

const publishedFileIdentityMask = domain.FilesystemFieldDevice |
	domain.FilesystemFieldInode |
	domain.FilesystemFieldType |
	domain.FilesystemFieldUID |
	domain.FilesystemFieldGID |
	domain.FilesystemFieldMode |
	domain.FilesystemFieldLinkCount |
	domain.FilesystemFieldMountID

type durableFileHooks struct {
	open          func(int, string, int, uint32) (int, error)
	write         func(int, []byte) (int, error)
	syncFile      func(int) error
	syncDirectory func(int) error
	close         func(int) error
}

func defaultDurableFileHooks() durableFileHooks {
	return durableFileHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	}
}

// PublishFileDurable creates one new private record beneath a held,
// engine/helper-attested private layout. It never accepts a path, overwrites
// an existing entry, truncates a file, or cleans up an uncertain post-create
// result. A durable return means the created file and its containing directory
// synced and passed the final point-in-time descriptor checks.
func PublishFileDurable(ctx context.Context, directory *PrivateDirectoryLease, basename string, contents []byte) error {
	return publishFileDurableWith(ctx, directory, basename, contents, defaultDurableFileHooks())
}

func publishFileDurableWith(ctx context.Context, directory *PrivateDirectoryLease, basename string, contents []byte, hooks durableFileHooks) error {
	if directory == nil {
		return fmt.Errorf("%w: qualified private directory lease is required", ErrUnsupported)
	}
	if directory.Kind() == mounts.LayoutPrivateState && isReservedPrivateLedgerBasename(basename) {
		return fmt.Errorf("%w: private ledger records require an opaque locked ledger session", ErrUnsupported)
	}
	return directory.withFD(func(directoryFD int) error {
		return publishFileDurableAtWith(ctx, directoryFD, basename, contents, hooks)
	})
}

func isReservedPrivateLedgerBasename(basename string) bool {
	return strings.HasPrefix(basename, privateLedgerNamespacePrefix)
}

// publishFileDurableAtWith is the descriptor-scoped half of durable private
// publication. It remains package-private so only LinuxFS operations holding a
// requalified private directory descriptor can reuse the exact no-replace,
// fsync, and final-identity protocol.
func publishFileDurableAtWith(ctx context.Context, directoryFD int, basename string, contents []byte, hooks durableFileHooks) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if directoryFD < 0 {
		return fmt.Errorf("%w: private directory descriptor is required", ErrUnsupported)
	}
	if err := validateBasename(basename); err != nil {
		return err
	}
	if len(contents) > maximumDurableFileBytes {
		return fmt.Errorf("%w: durable file content exceeds %d-byte limit", ErrUnsupported, maximumDurableFileBytes)
	}
	if err := hooks.validate(); err != nil {
		return err
	}

	// A directory-sync capability check happens before openat creates an
	// entry. If it cannot promise durability, no state record is emitted.
	if err := hooks.syncDirectory(directoryFD); err != nil {
		return classifyPreflightDirectorySync(err)
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	fd, err := hooks.open(directoryFD, basename, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
	if err != nil {
		return classifyDurableOpenFailure(err)
	}
	openFD := fd
	closed := false
	defer func() {
		if !closed && openFD >= 0 {
			_ = hooks.close(openFD)
		}
	}()

	// From here a directory entry may exist. Every error must retain it
	// and become reconciliation rather than be disguised as a clean retry.
	if err := contextError(ctx); err != nil {
		return postCreateDurabilityError("context changed after record creation", err)
	}
	identity, err := snapshotPublishedRegularFile(openFD)
	if err != nil {
		return postCreateIdentityError("published entry identity", err)
	}
	if err := writeDurableFileContents(ctx, openFD, contents, hooks.write); err != nil {
		return postCreateDurabilityError("write private record", err)
	}
	if err := hooks.syncFile(openFD); err != nil {
		return postCreateDurabilityError("sync private record", err)
	}
	fdToClose := openFD
	openFD = -1
	closed = true
	if err := hooks.close(fdToClose); err != nil {
		return postCreateDurabilityError("close private record", err)
	}
	if err := contextError(ctx); err != nil {
		return postCreateDurabilityError("context changed before directory sync", err)
	}
	if err := hooks.syncDirectory(directoryFD); err != nil {
		return postCreateDurabilityError("sync private record directory", err)
	}
	if err := verifyPublishedName(ctx, directoryFD, basename, identity, contents); err != nil {
		return postCreateIdentityError("published record name changed after durability sync", err)
	}
	return nil
}

func (hooks durableFileHooks) validate() error {
	if hooks.open == nil || hooks.write == nil || hooks.syncFile == nil || hooks.syncDirectory == nil || hooks.close == nil {
		return fmt.Errorf("%w: durable-file safety implementation is incomplete", ErrUnsupported)
	}
	return nil
}

func openDurableFile(directoryFD int, basename string, flags int, mode uint32) (int, error) {
	return unix.Openat(directoryFD, basename, flags, mode)
}

func snapshotPublishedRegularFile(fd int) (domain.FilesystemSnapshot, error) {
	snapshot, err := SnapshotFD(fd, publishedFileIdentityMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeRegular {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: newly published entry is %s, not a regular file", ErrDrifted, snapshot.Type)
	}
	if snapshot.UID.Value != uint32(unix.Geteuid()) {
		return domain.FilesystemSnapshot{}, driftedField("published record owner")
	}
	if snapshot.Mode.Value&0o7777 != durableFileMode {
		return domain.FilesystemSnapshot{}, driftedField("published record mode")
	}
	if snapshot.LinkCount.Value != 1 {
		return domain.FilesystemSnapshot{}, driftedField("published record link count")
	}
	return snapshot, nil
}

func verifyPublishedName(ctx context.Context, directoryFD int, basename string, expected domain.FilesystemSnapshot, contents []byte) error {
	fd, err := openPublishedRecordBeneath(ctx, directoryFD, basename)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	observed, err := SnapshotFD(fd, publishedFileIdentityMask)
	if err != nil {
		return err
	}
	if observed.Type != domain.FileTypeRegular {
		return driftedField("published record type")
	}
	if expected.DeviceMajor != observed.DeviceMajor || expected.DeviceMinor != observed.DeviceMinor {
		return driftedField("published record device")
	}
	if expected.Inode != observed.Inode {
		return driftedField("published record inode")
	}
	if expected.UID != observed.UID || expected.GID != observed.GID {
		return driftedField("published record ownership")
	}
	if expected.Mode != observed.Mode {
		return driftedField("published record mode")
	}
	if expected.LinkCount != observed.LinkCount {
		return driftedField("published record link count")
	}
	if expected.MountID != observed.MountID {
		return driftedField("published record mount ID")
	}
	return verifyPublishedContents(ctx, fd, contents)
}

func openPublishedRecordBeneath(ctx context.Context, directoryFD int, basename string) (int, error) {
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	return openBeneathWithRetry(ctx, unix.Openat2, directoryFD, basename, how)
}

func verifyPublishedContents(ctx context.Context, fd int, expected []byte) error {
	const verificationReadBufferBytes = 32 << 10
	buffer := make([]byte, verificationReadBufferBytes)
	for len(expected) > 0 {
		if err := contextError(ctx); err != nil {
			return err
		}
		limit := len(expected)
		if limit > len(buffer) {
			limit = len(buffer)
		}
		count, err := unix.Read(fd, buffer[:limit])
		if count > 0 {
			if count > len(expected) || !bytes.Equal(buffer[:count], expected[:count]) {
				return driftedField("published record contents")
			}
			expected = expected[count:]
		}
		if err != nil {
			return fmt.Errorf("%w: read published record during verification: %v", ErrDrifted, err)
		}
		if count == 0 {
			return driftedField("published record content length")
		}
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	var trailing [1]byte
	count, err := unix.Read(fd, trailing[:])
	if count > 0 {
		return driftedField("published record content length")
	}
	if err != nil {
		return fmt.Errorf("%w: read published record terminator during verification: %v", ErrDrifted, err)
	}
	return nil
}

func writeDurableFileContents(ctx context.Context, fd int, contents []byte, write func(int, []byte) (int, error)) error {
	for len(contents) > 0 {
		if err := contextError(ctx); err != nil {
			return err
		}
		written, err := write(fd, contents)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(contents) {
			return fmt.Errorf("invalid durable-file write result %d for %d bytes", written, len(contents))
		}
		contents = contents[written:]
	}
	return nil
}

func classifyPreflightDirectorySync(err error) error {
	switch {
	case errors.Is(err, unix.EBADF):
		return fmt.Errorf("%w: private directory lease is no longer syncable: %v", ErrDrifted, err)
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: private directory cannot provide durable publication: %v", ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: private directory durability preflight did not complete: %v", ErrInterrupted, err)
	}
}

func classifyDurableOpenFailure(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY), errors.Is(err, unix.ELOOP):
		return fmt.Errorf("%w: durable record basename is occupied or hostile: %v", ErrDrifted, err)
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EROFS):
		return fmt.Errorf("%w: no-replace durable publication is unavailable: %v", ErrUnsupported, err)
	case errors.Is(err, unix.EINTR), errors.Is(err, unix.EAGAIN):
		return fmt.Errorf("%w: durable record creation may require reconciliation: %v", ErrInterrupted, err)
	default:
		return fmt.Errorf("%w: durable record creation failed: %v", ErrDrifted, err)
	}
}

func postCreateDurabilityError(operation string, err error) error {
	return fmt.Errorf("%w: %s could not be durably confirmed; retain the record for reconciliation: %v", ErrInterrupted, operation, err)
}

func postCreateIdentityError(operation string, err error) error {
	switch {
	case errors.Is(err, ErrUnsupported):
		return fmt.Errorf("%w: %w: %s; retain the observed entry for reconciliation: %v", ErrInterrupted, ErrUnsupported, operation, err)
	case errors.Is(err, ErrInterrupted):
		return fmt.Errorf("%w: %s; retain the observed entry for reconciliation: %v", ErrInterrupted, operation, err)
	case errors.Is(err, ErrDrifted):
		return fmt.Errorf("%w: %w: %s; retain the observed entry for reconciliation: %v", ErrInterrupted, ErrDrifted, operation, err)
	default:
		return fmt.Errorf("%w: %w: %s; retain the observed entry for reconciliation: %v", ErrInterrupted, ErrDrifted, operation, err)
	}
}
