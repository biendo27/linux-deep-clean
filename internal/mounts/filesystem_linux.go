//go:build linux

package mounts

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	minimumKernelMajor          = 5
	minimumKernelMinor          = 15
	permissionModeMask          = 0o7777
	mountInspectionAttemptLimit = 3
)

type descriptorMountEvidence struct {
	Device     DeviceIdentity
	Inode      uint64
	UID        uint32
	GID        uint32
	Mode       uint32
	MountID    uint64
	Filesystem FilesystemType
	ReadOnly   bool
}

type descriptorMountInspector func(int) (descriptorMountEvidence, error)
type mountInfoReader func() ([]MountRecord, error)
type mountNamespaceInspector func() (MountNamespace, error)

// InspectMount collects descriptor-bound root evidence. It uses statx with
// AT_EMPTY_PATH, checks every requested return-mask bit, obtains filesystem
// flags from Fstatfs, and joins current mountinfo through the statx mount ID.
// The descriptor and mount-namespace evidence are sampled before and after the
// mountinfo read; an unstable sample is retried boundedly and then fails as
// drift. It does not itself authorize the root.
func InspectMount(fd int) (MountInspection, error) {
	return inspectMountWith(fd, inspectDescriptorMountEvidence, ReadMountInfo, currentMountNamespace)
}

func inspectMountWith(fd int, inspectDescriptor descriptorMountInspector, readMountInfo mountInfoReader, inspectNamespace mountNamespaceInspector) (MountInspection, error) {
	if fd < 0 {
		return MountInspection{}, fmt.Errorf("%w: negative root descriptor", ErrInvalidAuthority)
	}
	if inspectDescriptor == nil || readMountInfo == nil || inspectNamespace == nil {
		return MountInspection{}, fmt.Errorf("%w: missing mount-inspection dependency", ErrInvalidAuthority)
	}

	for attempt := 0; attempt < mountInspectionAttemptLimit; attempt++ {
		before, err := inspectDescriptor(fd)
		if err != nil {
			return MountInspection{}, err
		}
		namespaceBefore, err := inspectNamespace()
		if err != nil {
			return MountInspection{}, err
		}

		records, err := readMountInfo()
		if err != nil {
			return MountInspection{}, fmt.Errorf("%w: current mountinfo cannot be qualified: %v", ErrUnsupported, err)
		}
		record, found := FindMountRecord(records, before.MountID)
		if !found {
			return MountInspection{}, fmt.Errorf("%w: statx mount ID %d is absent from current mountinfo", ErrUnsupported, before.MountID)
		}
		if err := checkMountTopology(records, record); err != nil {
			return MountInspection{}, err
		}

		namespaceAfter, err := inspectNamespace()
		if err != nil {
			return MountInspection{}, err
		}
		after, err := inspectDescriptor(fd)
		if err != nil {
			return MountInspection{}, err
		}
		if before != after || namespaceBefore != namespaceAfter {
			continue
		}

		return MountInspection{
			Namespace: namespaceAfter,
			Device:    after.Device,
			Inode:     after.Inode,
			UID:       after.UID,
			GID:       after.GID,
			Mode:      after.Mode,
			Mount:     record,
			Filesystem: FilesystemFacts{
				Filesystem:      after.Filesystem,
				MountFilesystem: record.Filesystem,
				ReadOnly:        after.ReadOnly,
				MountRoot:       record.Root,
				RootDevice:      after.Device,
				MountDevice:     record.Device,
			},
		}, nil
	}

	return MountInspection{}, fmt.Errorf("%w: root descriptor or mount namespace changed while reading mountinfo after %d attempts", ErrDrifted, mountInspectionAttemptLimit)
}

func inspectDescriptorMountEvidence(fd int) (descriptorMountEvidence, error) {
	var statx unix.Statx_t
	requiredMask := unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_GID | unix.STATX_INO | unix.STATX_MNT_ID
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, requiredMask, &statx); err != nil {
		return descriptorMountEvidence{}, syscallQualificationError("statx root descriptor", err)
	}
	if statx.Mask&uint32(requiredMask) != uint32(requiredMask) {
		return descriptorMountEvidence{}, fmt.Errorf("%w: statx omitted required root evidence mask %#x", ErrUnsupported, uint32(requiredMask)&^statx.Mask)
	}
	if uint32(statx.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return descriptorMountEvidence{}, fmt.Errorf("%w: trusted root descriptor is not a directory", ErrDrifted)
	}

	var statfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &statfs); err != nil {
		return descriptorMountEvidence{}, syscallQualificationError("fstatfs root descriptor", err)
	}

	return descriptorMountEvidence{
		Device:     DeviceIdentity{Major: statx.Dev_major, Minor: statx.Dev_minor},
		Inode:      statx.Ino,
		UID:        statx.Uid,
		GID:        statx.Gid,
		Mode:       uint32(statx.Mode) & permissionModeMask,
		MountID:    statx.Mnt_id,
		Filesystem: filesystemFromMagic(statfs.Type),
		ReadOnly:   statfs.Flags&unix.ST_RDONLY != 0,
	}, nil
}

// checkMountTopology rejects a second full-root view of the same filesystem.
// This catches the common whole-filesystem bind case and intentionally also
// rejects otherwise legitimate duplicate mounts as ambiguous. It is not proof
// that no bind exists: a detached source or another namespace can hide it, so
// usable root authority still requires explicit bind-free provenance.
func checkMountTopology(records []MountRecord, target MountRecord) error {
	for _, record := range records {
		if record.ID == target.ID {
			continue
		}
		if record.Device == target.Device && record.Filesystem == target.Filesystem && record.Root == target.Root {
			return fmt.Errorf("%w: mount ID %d duplicates root %q of %s device %d:%d at %q; bind or duplicate mount topology is ambiguous", ErrUnsupported, record.ID, target.Root, target.Filesystem, target.Device.Major, target.Device.Minor, record.MountPoint)
		}
	}
	return nil
}

func syscallQualificationError(operation string, err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("%w: %s: %v", ErrUnsupported, operation, err)
	}
	return fmt.Errorf("%w: %s: %v", ErrDrifted, operation, err)
}

func filesystemFromMagic(magic int64) FilesystemType {
	switch magic {
	case unix.EXT4_SUPER_MAGIC:
		return FilesystemExt4
	case unix.XFS_SUPER_MAGIC:
		return FilesystemXFS
	case unix.BTRFS_SUPER_MAGIC:
		return FilesystemBtrfs
	default:
		return FilesystemUnknown
	}
}

func currentMountNamespace() (MountNamespace, error) {
	var stat unix.Stat_t
	if err := unix.Stat("/proc/self/ns/mnt", &stat); err != nil {
		return MountNamespace{}, fmt.Errorf("%w: stat mount namespace: %v", ErrUnsupported, err)
	}
	if stat.Dev == 0 || stat.Ino == 0 {
		return MountNamespace{}, fmt.Errorf("%w: mount namespace has incomplete identity", ErrUnsupported)
	}
	return MountNamespace{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

// CheckSupportedFilesystem applies the local, writable, mountinfo-root, and
// explicitly-attested fixed-device policy without issuing any mount-changing
// operation. A non-root mountinfo origin detects subtree mounts; full-root
// bind provenance requires the separate trusted attestation in RootExpectation.
// Descendant mount crossings remain the responsibility of the required
// openat2 RESOLVE_NO_XDEV policy. Unsupported is fail-closed and callers must
// not weaken it.
func CheckSupportedFilesystem(facts FilesystemFacts) error {
	if !facts.Filesystem.supported() {
		return fmt.Errorf("%w: filesystem magic %q is not ext4, XFS, or Btrfs", ErrUnsupported, facts.Filesystem)
	}
	if facts.MountFilesystem != facts.Filesystem {
		return fmt.Errorf("%w: statfs filesystem %q conflicts with mountinfo filesystem %q", ErrUnsupported, facts.Filesystem, facts.MountFilesystem)
	}
	if facts.ReadOnly {
		return fmt.Errorf("%w: filesystem is read-only", ErrUnsupported)
	}
	if facts.MountRoot != "/" {
		return fmt.Errorf("%w: mountinfo root %q is a bind or subtree mount", ErrUnsupported, facts.MountRoot)
	}
	if facts.RootDevice != facts.MountDevice {
		return fmt.Errorf("%w: statx device %d:%d conflicts with mountinfo device %d:%d", ErrUnsupported, facts.RootDevice.Major, facts.RootDevice.Minor, facts.MountDevice.Major, facts.MountDevice.Minor)
	}
	if !facts.FixedLocalDevice {
		return fmt.Errorf("%w: root lacks an explicit fixed-local-device attestation", ErrUnsupported)
	}
	return nil
}

// CheckMountNamespace compares full namespace device/inode evidence. A mount
// ID equality check is intentionally not accepted as a substitute.
func CheckMountNamespace(expected, observed MountNamespace) error {
	if err := expected.validate(); err != nil {
		return err
	}
	if observed.Device == 0 || observed.Inode == 0 {
		return fmt.Errorf("%w: current mount namespace has incomplete evidence", ErrDrifted)
	}
	if expected != observed {
		return fmt.Errorf("%w: mount namespace changed from %d:%d to %d:%d", ErrDrifted, expected.Device, expected.Inode, observed.Device, observed.Inode)
	}
	return nil
}

func checkRootEvidence(expected RootExpectation, actual MountInspection) error {
	if err := expected.validate(); err != nil {
		return err
	}
	if err := CheckMountNamespace(expected.Namespace, actual.Namespace); err != nil {
		return err
	}
	if expected.Device != actual.Device {
		return fmt.Errorf("%w: root device changed from %d:%d to %d:%d", ErrDrifted, expected.Device.Major, expected.Device.Minor, actual.Device.Major, actual.Device.Minor)
	}
	if expected.Inode != actual.Inode {
		return fmt.Errorf("%w: root inode changed from %d to %d", ErrDrifted, expected.Inode, actual.Inode)
	}
	if expected.UID != actual.UID || expected.GID != actual.GID {
		return fmt.Errorf("%w: root ownership changed from %d:%d to %d:%d", ErrDrifted, expected.UID, expected.GID, actual.UID, actual.GID)
	}
	if expected.Mode != actual.Mode {
		return fmt.Errorf("%w: root mode changed from %#o to %#o", ErrDrifted, expected.Mode, actual.Mode)
	}
	if expected.Mount.ID != actual.Mount.ID {
		return fmt.Errorf("%w: root mount ID changed from %d to %d", ErrDrifted, expected.Mount.ID, actual.Mount.ID)
	}
	if expected.Mount.ParentID != actual.Mount.ParentID {
		return fmt.Errorf("%w: root parent mount ID changed from %d to %d", ErrDrifted, expected.Mount.ParentID, actual.Mount.ParentID)
	}
	if expected.Mount.Device != actual.Mount.Device {
		return fmt.Errorf("%w: root mount device changed from %d:%d to %d:%d", ErrDrifted, expected.Mount.Device.Major, expected.Mount.Device.Minor, actual.Mount.Device.Major, actual.Mount.Device.Minor)
	}
	if expected.Mount.Root != actual.Mount.Root {
		return fmt.Errorf("%w: root mount root changed from %q to %q", ErrDrifted, expected.Mount.Root, actual.Mount.Root)
	}
	if expected.Mount.MountPoint != actual.Mount.MountPoint {
		return fmt.Errorf("%w: root mount point changed from %q to %q", ErrDrifted, expected.Mount.MountPoint, actual.Mount.MountPoint)
	}
	if expected.Mount.Filesystem != actual.Mount.Filesystem {
		return fmt.Errorf("%w: root mountinfo filesystem changed from %q to %q", ErrDrifted, expected.Mount.Filesystem, actual.Mount.Filesystem)
	}
	if expected.Mount.Source != actual.Mount.Source {
		return fmt.Errorf("%w: root mount source changed from %q to %q", ErrDrifted, expected.Mount.Source, actual.Mount.Source)
	}
	if expected.Mount.Filesystem != actual.Filesystem.Filesystem {
		return fmt.Errorf("%w: root filesystem changed from %q to statfs=%q", ErrDrifted, expected.Mount.Filesystem, actual.Filesystem.Filesystem)
	}
	if expected.Mount.Root != actual.Filesystem.MountRoot {
		return fmt.Errorf("%w: root mount root changed from %q to descriptor facts=%q", ErrDrifted, expected.Mount.Root, actual.Filesystem.MountRoot)
	}
	if actual.Device != actual.Mount.Device || actual.Device != actual.Filesystem.RootDevice || actual.Mount.Device != actual.Filesystem.MountDevice {
		return fmt.Errorf("%w: root device evidence is internally inconsistent", ErrDrifted)
	}
	return nil
}

// CaptureRootExpectation captures engine/helper registration facts from an
// already-held descriptor. The engine/helper authority owner must explicitly
// attest both fixed-local-device and bind-free provenance; this package never
// guesses either property from a path or mountinfo alone.
func CaptureRootExpectation(fd int, fixedLocalDevice, bindFreeAttested bool) (RootExpectation, error) {
	return captureRootExpectationWith(fd, fixedLocalDevice, bindFreeAttested, InspectMount, checkMinimumKernel)
}

func captureRootExpectationWith(fd int, fixedLocalDevice, bindFreeAttested bool, inspect rootInspector, checkKernel kernelChecker) (RootExpectation, error) {
	if inspect == nil || checkKernel == nil {
		return RootExpectation{}, fmt.Errorf("%w: missing root expectation capture dependency", ErrInvalidAuthority)
	}
	if err := checkKernel(); err != nil {
		return RootExpectation{}, err
	}
	inspection, err := inspect(fd)
	if err != nil {
		return RootExpectation{}, err
	}
	facts := inspection.Filesystem
	facts.FixedLocalDevice = fixedLocalDevice
	if err := CheckSupportedFilesystem(facts); err != nil {
		return RootExpectation{}, err
	}
	expectation := RootExpectation{
		Namespace:        inspection.Namespace,
		Device:           inspection.Device,
		Inode:            inspection.Inode,
		UID:              inspection.UID,
		GID:              inspection.GID,
		Mode:             inspection.Mode,
		Mount:            inspection.Mount,
		FixedLocalDevice: fixedLocalDevice,
		BindFreeAttested: bindFreeAttested,
	}
	if err := expectation.validate(); err != nil {
		return RootExpectation{}, err
	}
	return expectation, nil
}

func checkMinimumKernel() error {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return fmt.Errorf("%w: uname: %v", ErrUnsupported, err)
	}
	release := string(name.Release[:])
	if nul := bytes.IndexByte([]byte(release), 0); nul >= 0 {
		release = release[:nul]
	}
	return checkMinimumKernelRelease(release)
}

func checkMinimumKernelRelease(release string) error {
	major, minor, err := parseKernelRelease(release)
	if err != nil {
		return fmt.Errorf("%w: cannot parse kernel release %q: %v", ErrUnsupported, release, err)
	}
	if major < minimumKernelMajor || major == minimumKernelMajor && minor < minimumKernelMinor {
		return fmt.Errorf("%w: Linux kernel %d.%d is below required %d.%d", ErrUnsupported, major, minor, minimumKernelMajor, minimumKernelMinor)
	}
	return nil
}

func parseKernelRelease(release string) (int, int, error) {
	version := strings.SplitN(release, "-", 2)[0]
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("expected major.minor")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, fmt.Errorf("invalid major version")
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return 0, 0, fmt.Errorf("invalid minor version")
	}
	return major, minor, nil
}
