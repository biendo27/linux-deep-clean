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

const trashInfoProbeReadBufferBytes = 32 << 10

const ownedTrashInfoProbeMask = publishedFileIdentityMask |
	domain.FilesystemFieldSize |
	domain.FilesystemFieldModifiedAt |
	domain.FilesystemFieldChangedAt

type ownedTrashInfoNameVerifier func(
	context.Context,
	int,
	string,
	domain.FilesystemSnapshot,
	[]byte,
) error

type ownedTrashInfoReader func(int, []byte) (int, error)

// ProbeOwnedTrashInfo reads exactly one LDC-owned Trash metadata name beneath
// freshly requalified topology-qualified info directories. It returns
// present=false only after two constrained no-follow lookups both prove that
// exact name absent. It does not scan, create, remove, rename, or otherwise
// modify either Trash directory.
//
// A present record must remain an owned, single-link 0600 regular file with
// stable identity and bounded contents through a final re-open verification.
// The returned bytes are a defensive copy and remain metadata only; callers
// must not derive filesystem authority from their Path field.
func ProbeOwnedTrashInfo(ctx context.Context, directories *TrashDirectories, token string) ([]byte, bool, error) {
	return probeOwnedTrashInfoWith(ctx, directories, token, verifyOwnedTrashInfoName)
}

func probeOwnedTrashInfoWith(
	ctx context.Context,
	directories *TrashDirectories,
	token string,
	verifyName ownedTrashInfoNameVerifier,
) ([]byte, bool, error) {
	if err := contextError(ctx); err != nil {
		return nil, false, err
	}
	if directories == nil {
		return nil, false, fmt.Errorf("%w: topology-qualified Trash directories are required", ErrUnsupported)
	}
	if !directories.topologyQualified() {
		return nil, false, fmt.Errorf("%w: Trash metadata probes require topology-qualified directories", ErrUnsupported)
	}
	if verifyName == nil {
		return nil, false, fmt.Errorf("%w: owned Trash metadata verification is required", ErrUnsupported)
	}
	basename, err := trashInfoBasename(token)
	if err != nil {
		return nil, false, err
	}

	directories.mu.Lock()
	defer directories.mu.Unlock()
	pair, err := directories.duplicateCheckedLocked()
	if err != nil {
		return nil, false, err
	}
	defer pair.Close()
	return probeOwnedTrashInfoLocked(ctx, directories, pair, basename, verifyName)
}

func probeOwnedTrashInfoLocked(
	ctx context.Context,
	directories *TrashDirectories,
	pair mounts.TrashDescriptorPair,
	basename string,
	verifyName ownedTrashInfoNameVerifier,
) ([]byte, bool, error) {
	fd, absent, err := openOwnedTrashInfoBeneath(ctx, pair.InfoFD, basename)
	if err != nil {
		return nil, false, err
	}
	if absent {
		if err := directories.verifyPairLocked(pair); err != nil {
			return nil, false, err
		}
		fd, absent, err = openOwnedTrashInfoBeneath(ctx, pair.InfoFD, basename)
		if err != nil {
			return nil, false, err
		}
		if absent {
			if err := directories.verifyPairLocked(pair); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
	}
	defer unix.Close(fd)

	identity, err := snapshotOwnedTrashInfo(fd)
	if err != nil {
		return nil, false, err
	}
	contents, err := readOwnedTrashInfoContents(ctx, fd, identity.Size.Value)
	if err != nil {
		return nil, false, err
	}
	observed, err := snapshotOwnedTrashInfo(fd)
	if err != nil {
		return nil, false, err
	}
	if !sameOwnedTrashInfoIdentity(identity, observed) {
		return nil, false, fmt.Errorf("%w: owned Trash metadata identity changed while reading", ErrDrifted)
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return nil, false, err
	}
	if err := verifyName(ctx, pair.InfoFD, basename, identity, contents); err != nil {
		return nil, false, err
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return nil, false, err
	}
	return append([]byte(nil), contents...), true, nil
}

func openOwnedTrashInfoBeneath(ctx context.Context, directoryFD int, basename string) (int, bool, error) {
	// Keep ENOENT distinct from constrained-resolution drift: unlike a content
	// operation, this narrow read-only probe must be able to establish the
	// exact-name absence fact after its second lookup.
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	for attempt := 0; attempt < openat2AttemptLimit; attempt++ {
		if err := contextError(ctx); err != nil {
			return -1, false, err
		}
		fd, err := unix.Openat2(directoryFD, basename, how)
		if err == nil {
			if fd < 3 {
				_ = unix.Close(fd)
				return -1, false, fmt.Errorf("%w: constrained Trash metadata open returned reserved descriptor", ErrDrifted)
			}
			return fd, false, nil
		}
		if errors.Is(err, unix.ENOENT) {
			return -1, true, nil
		}
		if !errors.Is(err, unix.EAGAIN) {
			return -1, false, classifyOpenat2Error(err)
		}
	}
	return -1, false, fmt.Errorf("%w: owned Trash metadata open could not stabilize after %d attempts", ErrDrifted, openat2AttemptLimit)
}

func snapshotOwnedTrashInfo(fd int) (domain.FilesystemSnapshot, error) {
	snapshot, err := SnapshotFD(fd, ownedTrashInfoProbeMask)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeRegular {
		return domain.FilesystemSnapshot{}, driftedField("owned Trash metadata type")
	}
	if snapshot.UID.Value != uint32(unix.Geteuid()) {
		return domain.FilesystemSnapshot{}, driftedField("owned Trash metadata owner")
	}
	if snapshot.Mode.Value&0o7777 != durableFileMode {
		return domain.FilesystemSnapshot{}, driftedField("owned Trash metadata mode")
	}
	if snapshot.LinkCount.Value != 1 {
		return domain.FilesystemSnapshot{}, driftedField("owned Trash metadata link count")
	}
	if snapshot.Size.Value > maximumDurableFileBytes {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: owned Trash metadata exceeds %d-byte limit", ErrDrifted, maximumDurableFileBytes)
	}
	return snapshot, nil
}

func readOwnedTrashInfoContents(ctx context.Context, fd int, expectedSize uint64) ([]byte, error) {
	return readOwnedTrashInfoContentsWith(ctx, fd, expectedSize, unix.Read)
}

func readOwnedTrashInfoContentsWith(
	ctx context.Context,
	fd int,
	expectedSize uint64,
	read ownedTrashInfoReader,
) ([]byte, error) {
	if expectedSize > maximumDurableFileBytes {
		return nil, fmt.Errorf("%w: owned Trash metadata exceeds %d-byte limit", ErrDrifted, maximumDurableFileBytes)
	}
	if read == nil {
		return nil, fmt.Errorf("%w: owned Trash metadata reader is required", ErrUnsupported)
	}
	contents := make([]byte, int(expectedSize))
	for offset := 0; offset < len(contents); {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		limit := len(contents) - offset
		if limit > trashInfoProbeReadBufferBytes {
			limit = trashInfoProbeReadBufferBytes
		}
		count, err := read(fd, contents[offset:offset+limit])
		if count < 0 || count > limit {
			return nil, fmt.Errorf("%w: owned Trash metadata read returned an invalid byte count", ErrDrifted)
		}
		if err != nil {
			return nil, classifyOwnedTrashInfoReadError("read owned Trash metadata", err)
		}
		if count == 0 {
			return nil, driftedField("owned Trash metadata content length")
		}
		offset += count
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	var trailing [1]byte
	count, err := read(fd, trailing[:])
	if count < 0 || count > len(trailing) {
		return nil, fmt.Errorf("%w: owned Trash metadata terminator read returned an invalid byte count", ErrDrifted)
	}
	if count > 0 {
		return nil, driftedField("owned Trash metadata content length")
	}
	if err != nil {
		return nil, classifyOwnedTrashInfoReadError("read owned Trash metadata terminator", err)
	}
	return contents, nil
}

func classifyOwnedTrashInfoReadError(operation string, err error) error {
	if errors.Is(err, unix.EINTR) {
		return fmt.Errorf("%w: %s: %v", ErrInterrupted, operation, err)
	}
	return fmt.Errorf("%w: %s: %v", ErrDrifted, operation, err)
}

func verifyOwnedTrashInfoName(
	ctx context.Context,
	directoryFD int,
	basename string,
	expected domain.FilesystemSnapshot,
	contents []byte,
) error {
	fd, absent, err := openOwnedTrashInfoBeneath(ctx, directoryFD, basename)
	if err != nil {
		return err
	}
	if absent {
		return driftedField("owned Trash metadata name")
	}
	defer unix.Close(fd)
	observed, err := snapshotOwnedTrashInfo(fd)
	if err != nil {
		return err
	}
	if !sameOwnedTrashInfoIdentity(expected, observed) {
		return driftedField("owned Trash metadata identity")
	}
	return verifyPublishedContents(ctx, fd, contents)
}

func sameOwnedTrashInfoIdentity(left, right domain.FilesystemSnapshot) bool {
	return left.DeviceMajor == right.DeviceMajor &&
		left.DeviceMinor == right.DeviceMinor &&
		left.Inode == right.Inode &&
		left.Type == right.Type &&
		left.UID == right.UID &&
		left.GID == right.GID &&
		left.Mode == right.Mode &&
		left.LinkCount == right.LinkCount &&
		left.Size == right.Size &&
		left.ModifiedAt == right.ModifiedAt &&
		left.ChangedAt == right.ChangedAt &&
		left.MountID == right.MountID
}
