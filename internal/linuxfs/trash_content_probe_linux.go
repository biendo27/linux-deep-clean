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

type ownedTrashContentNameVerifier func(
	context.Context,
	int,
	string,
	domain.FilesystemPrecondition,
	domain.FilesystemSnapshot,
) error

type ownedTrashContentOpen func(int, string, *unix.OpenHow) (int, error)

// ProbeOwnedTrashContent proves only whether one exact LDC-owned
// files/<token> entry still has the durable source identity after a Trash
// move. It accepts topology-qualified directories, an LDC token, and the
// exact ActionTrashPath precondition selected before that move. It neither
// scans nor creates, moves, restores, deletes, or otherwise mutates Trash.
//
// present=false is an absence fact only after two constrained, no-follow
// lookups and topology requalification. A present result requires the exact
// name to match postMovePrecondition(expected), to retain a stable identity
// through a final re-open, and to survive final topology requalification.
func ProbeOwnedTrashContent(
	ctx context.Context,
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
) (present bool, err error) {
	return probeOwnedTrashContentWith(ctx, directories, token, expected, verifyOwnedTrashContentName)
}

func probeOwnedTrashContentWith(
	ctx context.Context,
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
	verifyName ownedTrashContentNameVerifier,
) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if verifyName == nil {
		return false, fmt.Errorf("%w: owned Trash content verification is required", ErrUnsupported)
	}
	clonedExpected, err := validateOwnedTrashContentProbeRequest(directories, token, expected)
	if err != nil {
		return false, err
	}

	directories.mu.Lock()
	defer directories.mu.Unlock()
	pair, err := directories.duplicateCheckedLocked()
	if err != nil {
		return false, err
	}
	defer pair.Close()
	return probeOwnedTrashContentLocked(ctx, directories, pair, token, clonedExpected, verifyName)
}

func validateOwnedTrashContentProbeRequest(
	directories *TrashDirectories,
	token string,
	expected domain.FilesystemPrecondition,
) (domain.FilesystemPrecondition, error) {
	if directories == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: topology-qualified Trash directories are required", ErrUnsupported)
	}
	if !directories.topologyQualified() {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash content probes require topology-qualified directories", ErrUnsupported)
	}
	if err := validateOwnedTrashToken(token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := validateBasename(token); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if err := directories.rootID.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash root identity: %v", ErrUnsupported, err)
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
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash content probe requires mask %b, got %b", ErrUnsupported, required, clonedExpected.Required)
	}
	if clonedExpected.Target.Filesystem == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: filesystem precondition has no filesystem target", ErrUnsupported)
	}
	if clonedExpected.Target.Filesystem.Root != directories.rootID {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: Trash directories and precondition roots differ", ErrUnsupported)
	}
	return clonedExpected, nil
}

func probeOwnedTrashContentLocked(
	ctx context.Context,
	directories *TrashDirectories,
	pair mounts.TrashDescriptorPair,
	token string,
	expected domain.FilesystemPrecondition,
	verifyName ownedTrashContentNameVerifier,
) (bool, error) {
	fd, absent, err := openOwnedTrashContentBeneath(ctx, pair.FilesFD, token)
	if err != nil {
		return false, err
	}
	if absent {
		if err := directories.verifyPairLocked(pair); err != nil {
			return false, err
		}
		fd, absent, err = openOwnedTrashContentBeneath(ctx, pair.FilesFD, token)
		if err != nil {
			return false, err
		}
		if absent {
			if err := directories.verifyPairLocked(pair); err != nil {
				return false, err
			}
			return false, nil
		}
	}
	defer unix.Close(fd)

	initial, err := snapshotOwnedTrashContent(fd, expected.Required)
	if err != nil {
		return false, err
	}
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		return false, err
	}
	if err := ComparePrecondition(postMove, initial); err != nil {
		return false, err
	}
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return false, err
	}
	if err := verifyName(ctx, pair.FilesFD, token, expected, initial); err != nil {
		return false, err
	}
	if err := directories.verifyPairLocked(pair); err != nil {
		return false, err
	}
	return true, nil
}

func openOwnedTrashContentBeneath(ctx context.Context, directoryFD int, token string) (int, bool, error) {
	return openOwnedTrashContentBeneathWith(ctx, directoryFD, token, unix.Openat2)
}

func openOwnedTrashContentBeneathWith(
	ctx context.Context,
	directoryFD int,
	token string,
	open ownedTrashContentOpen,
) (int, bool, error) {
	if directoryFD < 3 {
		return -1, false, fmt.Errorf("%w: Trash files directory has no held descriptor", ErrDrifted)
	}
	if err := validateBasename(token); err != nil {
		return -1, false, err
	}
	if open == nil {
		return -1, false, fmt.Errorf("%w: constrained Trash content open is required", ErrUnsupported)
	}

	// O_PATH observes regular files and directories without reading their
	// contents or activating special files. RESOLVE_* and O_NOFOLLOW keep the
	// exact token beneath the trusted files descriptor.
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	for attempt := 0; attempt < openat2AttemptLimit; attempt++ {
		if err := contextError(ctx); err != nil {
			return -1, false, err
		}
		fd, err := open(directoryFD, token, how)
		if err == nil {
			if fd < 3 {
				_ = unix.Close(fd)
				return -1, false, fmt.Errorf("%w: constrained Trash content open returned reserved descriptor", ErrDrifted)
			}
			return fd, false, nil
		}
		if errors.Is(err, unix.ENOENT) {
			return -1, true, nil
		}
		if errors.Is(err, unix.EINTR) {
			return -1, false, fmt.Errorf("%w: constrained Trash content open: %v", ErrInterrupted, err)
		}
		if !errors.Is(err, unix.EAGAIN) {
			return -1, false, classifyOpenat2Error(err)
		}
	}
	return -1, false, fmt.Errorf("%w: owned Trash content open could not stabilize after %d attempts", ErrDrifted, openat2AttemptLimit)
}

func snapshotOwnedTrashContent(fd int, required domain.FilesystemFieldMask) (domain.FilesystemSnapshot, error) {
	snapshot, err := SnapshotFD(fd, required)
	if err != nil {
		return domain.FilesystemSnapshot{}, err
	}
	if snapshot.Type != domain.FileTypeRegular && snapshot.Type != domain.FileTypeDirectory {
		return domain.FilesystemSnapshot{}, driftedField("owned Trash content type")
	}
	return snapshot, nil
}

func verifyOwnedTrashContentName(
	ctx context.Context,
	directoryFD int,
	token string,
	expected domain.FilesystemPrecondition,
	initial domain.FilesystemSnapshot,
) error {
	fd, absent, err := openOwnedTrashContentBeneath(ctx, directoryFD, token)
	if err != nil {
		return err
	}
	if absent {
		return driftedField("owned Trash content name")
	}
	defer unix.Close(fd)

	observed, err := snapshotOwnedTrashContent(fd, expected.Required)
	if err != nil {
		return err
	}
	postMove, err := postMovePrecondition(expected)
	if err != nil {
		return err
	}
	if err := ComparePrecondition(postMove, observed); err != nil {
		return err
	}
	if !sameOwnedTrashContentIdentity(initial, observed) {
		return driftedField("owned Trash content identity")
	}
	return nil
}

func sameOwnedTrashContentIdentity(left, right domain.FilesystemSnapshot) bool {
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
