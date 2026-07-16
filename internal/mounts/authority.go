//go:build linux

// Package mounts owns trusted-root leases and the evidence that qualifies a
// Linux mount for filesystem work. It deliberately accepts root authority
// only through an engine/helper registry; it never accepts an apply-time
// absolute path.
package mounts

import (
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

var (
	// ErrUnsupported means the live Linux/kernel/mount layout cannot support
	// the promised filesystem-safety guarantees. Callers must not fall back to
	// a pathname-based operation.
	ErrUnsupported = errors.New("mounts: unsupported root or mount layout")

	// ErrDrifted means the current root evidence differs from the facts that an
	// engine/helper authority recorded. It is intentionally distinct from a
	// missing capability: a caller can report drift without treating it as an
	// authorization failure.
	ErrDrifted = errors.New("mounts: trusted root evidence drifted")

	// ErrInvalidAuthority means a registry entry is incomplete or internally
	// inconsistent. It is a configuration/programming error, never a reason to
	// try a caller-provided path.
	ErrInvalidAuthority = errors.New("mounts: invalid trusted-root authority")

	// ErrUnknownTrustedRoot means no engine/helper authority exists for the
	// requested typed root ID.
	ErrUnknownTrustedRoot = errors.New("mounts: unknown trusted root")

	// ErrLeaseClosed prevents a lease from lending or deriving any new root
	// descriptor after it has been closed.
	ErrLeaseClosed = errors.New("mounts: trusted-root lease is closed")
)

// DeviceIdentity is Linux major/minor device evidence returned by statx and
// mountinfo. Both components are required; a mount ID alone is never enough
// to identify an authorized root.
type DeviceIdentity struct {
	Major uint32
	Minor uint32
}

// MountNamespace is stable device/inode evidence for /proc/self/ns/mnt while
// the root descriptor is held. It must match the authority captured during
// planning; a matching mount ID in a different namespace never authorizes.
type MountNamespace struct {
	Device uint64
	Inode  uint64
}

func (namespace MountNamespace) validate() error {
	if namespace.Device == 0 || namespace.Inode == 0 {
		return fmt.Errorf("%w: mount namespace needs nonzero device and inode evidence", ErrInvalidAuthority)
	}
	return nil
}

// FilesystemType is the closed local filesystem set supported by the Phase 3
// rooted primitives. Other strings, including FUSE, overlay, network, and
// ZFS types, are deliberately unsupported.
type FilesystemType string

const (
	FilesystemUnknown FilesystemType = ""
	FilesystemExt4    FilesystemType = "ext4"
	FilesystemXFS     FilesystemType = "xfs"
	FilesystemBtrfs   FilesystemType = "btrfs"
)

func (filesystem FilesystemType) supported() bool {
	switch filesystem {
	case FilesystemExt4, FilesystemXFS, FilesystemBtrfs:
		return true
	default:
		return false
	}
}

// RootExpectation is immutable engine/helper-owned evidence captured for one
// trusted root. It contains no root path: Open is the sole way to derive the
// root descriptor again at apply time.
//
// Mount is the complete decoded mountinfo record captured for the root. Its
// mount point and source are drift evidence only: neither is reopened or used
// as apply-time path authority.
//
// FixedLocalDevice and BindFreeAttested are explicit attestations by the
// engine/helper authority owner. Linux cannot infer whether every block device
// is removable, nor can mountinfo prove that a whole-filesystem root is not a
// bind mount whose source is absent from the current namespace. Both default
// to false and make mutation unsupported until trusted composition supplies
// them.
type RootExpectation struct {
	Namespace        MountNamespace
	Device           DeviceIdentity
	Inode            uint64
	UID              uint32
	GID              uint32
	Mode             uint32
	Mount            MountRecord
	FixedLocalDevice bool
	BindFreeAttested bool
}

func (expectation RootExpectation) validate() error {
	if err := expectation.Namespace.validate(); err != nil {
		return err
	}
	if expectation.Inode == 0 {
		return fmt.Errorf("%w: root inode is required", ErrInvalidAuthority)
	}
	// Mode 0000 is unusual but valid Linux evidence. Mode has no separate
	// presence bit because CaptureRootExpectation always obtains it from statx.
	if err := validateTrustedRootMountRecord(expectation.Mount); err != nil {
		return err
	}
	if expectation.Device != expectation.Mount.Device {
		return fmt.Errorf("%w: expected statx device %d:%d conflicts with expected mountinfo device %d:%d", ErrInvalidAuthority, expectation.Device.Major, expectation.Device.Minor, expectation.Mount.Device.Major, expectation.Mount.Device.Minor)
	}
	if !expectation.FixedLocalDevice {
		return fmt.Errorf("%w: root lacks an explicit fixed-local-device attestation", ErrUnsupported)
	}
	if !expectation.BindFreeAttested {
		return fmt.Errorf("%w: root lacks an explicit bind-free provenance attestation", ErrUnsupported)
	}
	return nil
}

func validateTrustedRootMountRecord(record MountRecord) error {
	if record.ID == 0 {
		return fmt.Errorf("%w: root mount ID is required", ErrInvalidAuthority)
	}
	if record.ParentID == 0 {
		return fmt.Errorf("%w: root parent mount ID is required", ErrInvalidAuthority)
	}
	if record.Device == (DeviceIdentity{}) {
		return fmt.Errorf("%w: root mount device is required", ErrInvalidAuthority)
	}
	if !record.Filesystem.supported() {
		return fmt.Errorf("%w: unsupported expected filesystem %q", ErrInvalidAuthority, record.Filesystem)
	}
	if record.Root != "/" {
		return fmt.Errorf("%w: trusted root mount must originate at the filesystem root, got mount root %q", ErrInvalidAuthority, record.Root)
	}
	if record.MountPoint == "" || record.MountPoint[0] != '/' || hasNUL(record.MountPoint) {
		return fmt.Errorf("%w: root mount point must be a non-NUL absolute evidence value", ErrInvalidAuthority)
	}
	if record.Source == "" || hasNUL(record.Source) {
		return fmt.Errorf("%w: root mount source is required evidence", ErrInvalidAuthority)
	}
	return nil
}

func hasNUL(value string) bool {
	for _, character := range []byte(value) {
		if character == 0 {
			return true
		}
	}
	return false
}

// RootOpener is implemented only by engine/helper-owned configuration. It
// transfers exactly one newly opened O_RDONLY|O_DIRECTORY|O_CLOEXEC descriptor
// to OpenTrustedRoot, which closes it on every failed qualification and owns it
// for the resulting lease. O_PATH descriptors are forbidden because rooted
// durability operations must retain an fsync-capable directory descriptor. It
// deliberately has no string path argument.
type RootOpener func() (int, error)

// RootAuthority binds one typed root ID to a trusted opener and recorded
// evidence. Registry implementations must live in engine/helper composition,
// never in providers or presentation code.
type RootAuthority struct {
	ID       domain.TrustedRootID
	Open     RootOpener
	Expected RootExpectation
}

func (authority RootAuthority) validate(requested domain.TrustedRootID) error {
	if err := requested.Validate(); err != nil {
		return fmt.Errorf("%w: requested root ID: %v", ErrInvalidAuthority, err)
	}
	if err := authority.ID.Validate(); err != nil {
		return fmt.Errorf("%w: authority root ID: %v", ErrInvalidAuthority, err)
	}
	if authority.ID != requested {
		return fmt.Errorf("%w: authority root ID %q does not match request %q", ErrInvalidAuthority, authority.ID, requested)
	}
	if authority.Open == nil {
		return fmt.Errorf("%w: trusted root %q has no opener", ErrInvalidAuthority, authority.ID)
	}
	return authority.Expected.validate()
}

// Registry resolves only typed root IDs. It cannot be asked to open a caller
// supplied absolute path.
type Registry interface {
	Lookup(domain.TrustedRootID) (RootAuthority, bool)
}

// StaticRegistry is a small engine/helper-friendly registry implementation.
// Its values are copied out so callers cannot mutate the map entry during a
// lease operation.
type StaticRegistry map[domain.TrustedRootID]RootAuthority

// Lookup implements Registry.
func (registry StaticRegistry) Lookup(id domain.TrustedRootID) (RootAuthority, bool) {
	authority, ok := registry[id]
	return authority, ok
}

// FilesystemFacts contains live qualification evidence derived from a held
// descriptor and its matching mountinfo record. FixedLocalDevice is supplied
// by the authority after live evidence is collected.
type FilesystemFacts struct {
	Filesystem       FilesystemType
	MountFilesystem  FilesystemType
	ReadOnly         bool
	MountRoot        string
	FixedLocalDevice bool
	RootDevice       DeviceIdentity
	MountDevice      DeviceIdentity
}

// MountInspection is the complete current evidence collected while a root FD
// remains open. The mount record is matched by the statx mount ID, then device
// and filesystem facts are cross-checked before a lease is issued.
type MountInspection struct {
	Namespace  MountNamespace
	Device     DeviceIdentity
	Inode      uint64
	UID        uint32
	GID        uint32
	Mode       uint32
	Mount      MountRecord
	Filesystem FilesystemFacts
}
