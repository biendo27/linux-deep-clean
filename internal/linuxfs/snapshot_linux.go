//go:build linux

package linuxfs

import (
	"errors"
	"fmt"
	"math"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

const baselineFilesystemMask = domain.FilesystemFieldDevice |
	domain.FilesystemFieldInode |
	domain.FilesystemFieldType |
	domain.FilesystemFieldUID |
	domain.FilesystemFieldGID |
	domain.FilesystemFieldMode |
	domain.FilesystemFieldMountID

const destructiveFilesystemMask = baselineFilesystemMask |
	domain.FilesystemFieldLinkCount |
	domain.FilesystemFieldSize |
	domain.FilesystemFieldModifiedAt |
	domain.FilesystemFieldChangedAt

// RequiredStatMask names exactly the facts each filesystem action must
// compare. It never includes atime: observing an access time cannot authorize
// a mutation and would create spurious drift.
func RequiredStatMask(kind domain.ActionKind) (domain.FilesystemFieldMask, error) {
	switch kind {
	case domain.ActionTrashPath,
		domain.ActionDeleteRecreatablePath,
		domain.ActionQuarantinePath:
		return destructiveFilesystemMask, nil
	case domain.ActionRestoreTrashPath,
		domain.ActionRestoreQuarantinePath:
		return baselineFilesystemMask, nil
	default:
		return 0, fmt.Errorf("%w: action %q has no filesystem identity policy", ErrUnsupported, kind)
	}
}

// SnapshotFD captures only facts supplied by statx on an already-held
// descriptor. It never resolves a caller path. Required facts missing from the
// kernel response fail closed instead of becoming zero values.
func SnapshotFD(fd int, required domain.FilesystemFieldMask) (domain.FilesystemSnapshot, error) {
	if fd < 0 {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: invalid descriptor", ErrUnsupported)
	}
	if err := validateSnapshotRequirement(required); err != nil {
		return domain.FilesystemSnapshot{}, err
	}

	var statx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT, int(unix.STATX_BASIC_STATS|unix.STATX_MNT_ID), &statx); err != nil {
		return domain.FilesystemSnapshot{}, classifyStatxError(err)
	}
	return snapshotFromStatx(statx, required)
}

func snapshotFromStatx(statx unix.Statx_t, required domain.FilesystemFieldMask) (domain.FilesystemSnapshot, error) {
	if err := validateSnapshotRequirement(required); err != nil {
		return domain.FilesystemSnapshot{}, err
	}

	snapshot := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: statxHasAll(statx.Mask, unix.STATX_BASIC_STATS), Value: statx.Dev_major},
		DeviceMinor: domain.Uint32Fact{Known: statxHasAll(statx.Mask, unix.STATX_BASIC_STATS), Value: statx.Dev_minor},
		Inode:       domain.Uint64Fact{Known: statxHasAll(statx.Mask, unix.STATX_INO), Value: statx.Ino},
		UID:         domain.Uint32Fact{Known: statxHasAll(statx.Mask, unix.STATX_UID), Value: statx.Uid},
		GID:         domain.Uint32Fact{Known: statxHasAll(statx.Mask, unix.STATX_GID), Value: statx.Gid},
		Mode:        domain.Uint32Fact{Known: statxHasAll(statx.Mask, unix.STATX_MODE), Value: uint32(statx.Mode)},
		LinkCount:   domain.Uint64Fact{Known: statxHasAll(statx.Mask, unix.STATX_NLINK), Value: uint64(statx.Nlink)},
		Size:        domain.Uint64Fact{Known: statxHasAll(statx.Mask, unix.STATX_SIZE), Value: statx.Size},
		MountID:     domain.Uint64Fact{Known: statxHasAll(statx.Mask, unix.STATX_MNT_ID), Value: statx.Mnt_id},
	}
	if statxHasAll(statx.Mask, unix.STATX_TYPE) {
		snapshot.Type = fileTypeFromMode(statx.Mode)
	}
	if statxHasAll(statx.Mask, unix.STATX_MTIME) {
		value, err := statxUnixNano(statx.Mtime)
		if err != nil {
			return domain.FilesystemSnapshot{}, fmt.Errorf("%w: modified time: %v", ErrUnsupported, err)
		}
		snapshot.ModifiedAt = domain.Int64Fact{Known: true, Value: value}
	}
	if statxHasAll(statx.Mask, unix.STATX_CTIME) {
		value, err := statxUnixNano(statx.Ctime)
		if err != nil {
			return domain.FilesystemSnapshot{}, fmt.Errorf("%w: changed time: %v", ErrUnsupported, err)
		}
		snapshot.ChangedAt = domain.Int64Fact{Known: true, Value: value}
	}
	if err := snapshot.ValidateFor(required); err != nil {
		return domain.FilesystemSnapshot{}, fmt.Errorf("%w: statx returned incomplete facts: %v", ErrUnsupported, err)
	}
	return snapshot, nil
}

// ComparePrecondition checks exactly the requested facts. A matching mount ID
// is one drift signal among several, never a substitute for device, inode,
// ownership, mode, and type comparison.
func ComparePrecondition(expected domain.FilesystemPrecondition, observed domain.FilesystemSnapshot) error {
	if err := expected.Validate(); err != nil {
		return fmt.Errorf("%w: invalid expected filesystem precondition: %v", ErrUnsupported, err)
	}
	if err := observed.ValidateFor(expected.Required); err != nil {
		return fmt.Errorf("%w: observed filesystem facts unavailable: %v", ErrUnsupported, err)
	}

	if expected.Required&domain.FilesystemFieldDevice != 0 &&
		(expected.Snapshot.DeviceMajor != observed.DeviceMajor || expected.Snapshot.DeviceMinor != observed.DeviceMinor) {
		return driftedField("device")
	}
	if expected.Required&domain.FilesystemFieldInode != 0 && expected.Snapshot.Inode != observed.Inode {
		return driftedField("inode")
	}
	if expected.Required&domain.FilesystemFieldType != 0 && expected.Snapshot.Type != observed.Type {
		return driftedField("type")
	}
	if expected.Required&domain.FilesystemFieldUID != 0 && expected.Snapshot.UID != observed.UID {
		return driftedField("UID")
	}
	if expected.Required&domain.FilesystemFieldGID != 0 && expected.Snapshot.GID != observed.GID {
		return driftedField("GID")
	}
	if expected.Required&domain.FilesystemFieldMode != 0 && expected.Snapshot.Mode != observed.Mode {
		return driftedField("mode")
	}
	if expected.Required&domain.FilesystemFieldLinkCount != 0 && expected.Snapshot.LinkCount != observed.LinkCount {
		return driftedField("link count")
	}
	if expected.Required&domain.FilesystemFieldSize != 0 && expected.Snapshot.Size != observed.Size {
		return driftedField("size")
	}
	if expected.Required&domain.FilesystemFieldModifiedAt != 0 && expected.Snapshot.ModifiedAt != observed.ModifiedAt {
		return driftedField("modified time")
	}
	if expected.Required&domain.FilesystemFieldChangedAt != 0 && expected.Snapshot.ChangedAt != observed.ChangedAt {
		return driftedField("changed time")
	}
	if expected.Required&domain.FilesystemFieldMountID != 0 && expected.Snapshot.MountID != observed.MountID {
		return driftedField("mount ID")
	}
	return nil
}

func validateSnapshotRequirement(required domain.FilesystemFieldMask) error {
	complete := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true},
		DeviceMinor: domain.Uint32Fact{Known: true},
		Inode:       domain.Uint64Fact{Known: true},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true},
		GID:         domain.Uint32Fact{Known: true},
		Mode:        domain.Uint32Fact{Known: true},
		LinkCount:   domain.Uint64Fact{Known: true},
		Size:        domain.Uint64Fact{Known: true},
		ModifiedAt:  domain.Int64Fact{Known: true},
		ChangedAt:   domain.Int64Fact{Known: true},
		MountID:     domain.Uint64Fact{Known: true},
	}
	if err := complete.ValidateFor(required); err != nil {
		return fmt.Errorf("%w: invalid required filesystem mask: %v", ErrUnsupported, err)
	}
	return nil
}

func classifyStatxError(err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("%w: statx or its required flags are unavailable: %v", ErrUnsupported, err)
	}
	return fmt.Errorf("%w: statx held descriptor: %v", ErrDrifted, err)
}

func driftedField(field string) error {
	return fmt.Errorf("%w: %s changed", ErrDrifted, field)
}

func statxHasAll(mask, required uint32) bool {
	return mask&required == required
}

func fileTypeFromMode(mode uint16) domain.FileType {
	switch uint32(mode) & unix.S_IFMT {
	case unix.S_IFREG:
		return domain.FileTypeRegular
	case unix.S_IFDIR:
		return domain.FileTypeDirectory
	case unix.S_IFLNK:
		return domain.FileTypeSymlink
	default:
		return domain.FileTypeSpecial
	}
}

func statxUnixNano(timestamp unix.StatxTimestamp) (int64, error) {
	if timestamp.Nsec >= 1_000_000_000 {
		return 0, fmt.Errorf("nanoseconds %d are outside the Unix timestamp range", timestamp.Nsec)
	}
	const nanosecondsPerSecond int64 = 1_000_000_000
	if timestamp.Sec < math.MinInt64/nanosecondsPerSecond ||
		timestamp.Sec > (math.MaxInt64-int64(timestamp.Nsec))/nanosecondsPerSecond {
		return 0, fmt.Errorf("seconds %d are outside the int64 Unix-nanosecond range", timestamp.Sec)
	}
	return timestamp.Sec*nanosecondsPerSecond + int64(timestamp.Nsec), nil
}
