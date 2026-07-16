package domain

import (
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// PreconditionKind is a closed discriminator for apply-time evidence.
type PreconditionKind string

const (
	PreconditionFilesystemIdentity PreconditionKind = "filesystem_identity"
	PreconditionPackageTransaction PreconditionKind = "package_transaction"
	PreconditionManagerObjectState PreconditionKind = "manager_object_state"
	PreconditionCapability         PreconditionKind = "capability"
	PreconditionProjectMarker      PreconditionKind = "project_marker"
	PreconditionInstallerMetadata  PreconditionKind = "installer_metadata"
	PreconditionProfileManager     PreconditionKind = "profile_manager"
)

// Precondition is a closed typed union used for point-of-use revalidation.
type Precondition struct {
	Kind               PreconditionKind
	Filesystem         *FilesystemPrecondition
	PackageTransaction *PackageTransactionPrecondition
	ManagerObject      *ManagerObjectPrecondition
	Capability         *CapabilityPrecondition
	ProjectMarker      *ProjectMarkerPrecondition
	InstallerMetadata  *InstallerMetadataPrecondition
	ProfileManager     *ProfileManagerPrecondition
}

// FilesystemFieldMask identifies only the statx facts required by a specific
// action. It intentionally does not include atime or a compare-everything bit.
type FilesystemFieldMask uint32

const (
	FilesystemFieldDevice FilesystemFieldMask = 1 << iota
	FilesystemFieldInode
	FilesystemFieldType
	FilesystemFieldUID
	FilesystemFieldGID
	FilesystemFieldMode
	FilesystemFieldLinkCount
	FilesystemFieldSize
	FilesystemFieldModifiedAt
	FilesystemFieldChangedAt
	FilesystemFieldMountID
)

const knownFilesystemFieldMask = FilesystemFieldDevice |
	FilesystemFieldInode |
	FilesystemFieldType |
	FilesystemFieldUID |
	FilesystemFieldGID |
	FilesystemFieldMode |
	FilesystemFieldLinkCount |
	FilesystemFieldSize |
	FilesystemFieldModifiedAt |
	FilesystemFieldChangedAt |
	FilesystemFieldMountID

// FileType is a normalized filesystem object type. Phase 3 determines which
// action kinds may accept each type; Phase 2 only preserves evidence.
type FileType string

const (
	FileTypeUnknown   FileType = ""
	FileTypeRegular   FileType = "regular"
	FileTypeDirectory FileType = "directory"
	FileTypeSymlink   FileType = "symlink"
	FileTypeSpecial   FileType = "special"
)

// FilesystemSnapshot holds observed facts with per-field presence markers.
type FilesystemSnapshot struct {
	DeviceMajor Uint32Fact
	DeviceMinor Uint32Fact
	Inode       Uint64Fact
	Type        FileType
	UID         Uint32Fact
	GID         Uint32Fact
	Mode        Uint32Fact
	LinkCount   Uint64Fact
	Size        Uint64Fact
	ModifiedAt  Int64Fact
	ChangedAt   Int64Fact
	MountID     Uint64Fact
}

type FilesystemPrecondition struct {
	Target   Target
	Required FilesystemFieldMask
	Snapshot FilesystemSnapshot
}

type PackageTransactionPrecondition struct {
	Graph              TransactionGraph
	ManagerStateDigest EvidenceDigest
}

type ManagerObjectPrecondition struct {
	Target          Target
	ExpectedVersion string
	ExpectedPresent bool
}

type CapabilityPrecondition struct {
	Capability CapabilityID
	State      CapabilityState
	Version    string
}

type ProjectMarkerPrecondition struct {
	Root      TrustedRootID
	Marker    pathbytes.BytePath
	Ecosystem string
}

type InstallerMetadataPrecondition struct {
	Target Target
	Format InstallerFormat
	Digest EvidenceDigest
}

type ProfileManagerPrecondition struct {
	Provider ProviderID
	Manager  ProfileManagerKind
	Version  string
}

func (precondition Precondition) Validate() error {
	variantCount := 0
	for _, present := range []bool{
		precondition.Filesystem != nil,
		precondition.PackageTransaction != nil,
		precondition.ManagerObject != nil,
		precondition.Capability != nil,
		precondition.ProjectMarker != nil,
		precondition.InstallerMetadata != nil,
		precondition.ProfileManager != nil,
	} {
		if present {
			variantCount++
		}
	}
	if variantCount != 1 {
		return fmt.Errorf("precondition must contain exactly one typed variant")
	}

	switch precondition.Kind {
	case PreconditionFilesystemIdentity:
		if precondition.Filesystem == nil {
			return fmt.Errorf("filesystem precondition kind has no filesystem variant")
		}
		return precondition.Filesystem.Validate()
	case PreconditionPackageTransaction:
		if precondition.PackageTransaction == nil {
			return fmt.Errorf("package transaction precondition kind has no package variant")
		}
		return precondition.PackageTransaction.Validate()
	case PreconditionManagerObjectState:
		if precondition.ManagerObject == nil {
			return fmt.Errorf("manager object precondition kind has no manager variant")
		}
		return precondition.ManagerObject.Validate()
	case PreconditionCapability:
		if precondition.Capability == nil {
			return fmt.Errorf("capability precondition kind has no capability variant")
		}
		return precondition.Capability.Validate()
	case PreconditionProjectMarker:
		if precondition.ProjectMarker == nil {
			return fmt.Errorf("project marker precondition kind has no project variant")
		}
		return precondition.ProjectMarker.Validate()
	case PreconditionInstallerMetadata:
		if precondition.InstallerMetadata == nil {
			return fmt.Errorf("installer metadata precondition kind has no installer variant")
		}
		return precondition.InstallerMetadata.Validate()
	case PreconditionProfileManager:
		if precondition.ProfileManager == nil {
			return fmt.Errorf("profile manager precondition kind has no profile variant")
		}
		return precondition.ProfileManager.Validate()
	default:
		return fmt.Errorf("unknown precondition kind %q", precondition.Kind)
	}
}

func (precondition FilesystemPrecondition) Validate() error {
	if err := precondition.Target.Validate(); err != nil {
		return err
	}
	if precondition.Target.Kind != TargetFilesystem {
		return fmt.Errorf("filesystem precondition requires a filesystem target")
	}
	return precondition.Snapshot.ValidateFor(precondition.Required)
}

func (precondition PackageTransactionPrecondition) Validate() error {
	if err := precondition.Graph.Validate(); err != nil {
		return err
	}
	return precondition.ManagerStateDigest.Validate()
}

func (precondition ManagerObjectPrecondition) Validate() error {
	if err := precondition.Target.Validate(); err != nil {
		return err
	}
	if precondition.Target.Kind != TargetManagerObject {
		return fmt.Errorf("manager object precondition requires a manager-object target")
	}
	if precondition.ExpectedVersion == "" {
		return fmt.Errorf("manager object precondition requires an exact version")
	}
	return nil
}

func (precondition CapabilityPrecondition) Validate() error {
	if err := precondition.Capability.Validate(); err != nil {
		return err
	}
	if err := precondition.State.Validate(); err != nil {
		return err
	}
	if precondition.Version == "" {
		return fmt.Errorf("capability precondition requires a probe revision")
	}
	return nil
}

func (precondition ProjectMarkerPrecondition) Validate() error {
	if err := precondition.Root.Validate(); err != nil {
		return err
	}
	if _, err := pathbytes.New(precondition.Marker.Components()); err != nil {
		return fmt.Errorf("project marker path: %w", err)
	}
	return validateStableID("project ecosystem", precondition.Ecosystem)
}

func (precondition InstallerMetadataPrecondition) Validate() error {
	if err := precondition.Target.Validate(); err != nil {
		return err
	}
	if precondition.Target.Kind != TargetFilesystem {
		return fmt.Errorf("installer metadata precondition requires a filesystem target")
	}
	if err := precondition.Format.Validate(); err != nil {
		return err
	}
	return precondition.Digest.Validate()
}

func (precondition ProfileManagerPrecondition) Validate() error {
	if err := precondition.Provider.Validate(); err != nil {
		return err
	}
	if err := precondition.Manager.Validate(); err != nil {
		return err
	}
	if precondition.Version == "" {
		return fmt.Errorf("profile manager precondition requires a version")
	}
	return nil
}

func (snapshot FilesystemSnapshot) ValidateObserved() error {
	return snapshot.ValidateFor(FilesystemFieldType)
}

func (snapshot FilesystemSnapshot) ValidateFor(required FilesystemFieldMask) error {
	if required == 0 {
		return fmt.Errorf("filesystem precondition requires a named field mask")
	}
	if required&^knownFilesystemFieldMask != 0 {
		return fmt.Errorf("filesystem precondition contains unknown field-mask bits")
	}
	for _, fact := range []struct {
		name string
		err  error
	}{
		{"device major", snapshot.DeviceMajor.Validate()},
		{"device minor", snapshot.DeviceMinor.Validate()},
		{"inode", snapshot.Inode.Validate()},
		{"UID", snapshot.UID.Validate()},
		{"GID", snapshot.GID.Validate()},
		{"mode", snapshot.Mode.Validate()},
		{"link count", snapshot.LinkCount.Validate()},
		{"size", snapshot.Size.Validate()},
		{"modified time", snapshot.ModifiedAt.Validate()},
		{"changed time", snapshot.ChangedAt.Validate()},
		{"mount ID", snapshot.MountID.Validate()},
	} {
		if fact.err != nil {
			return fmt.Errorf("filesystem %s: %w", fact.name, fact.err)
		}
	}
	if snapshot.Type != FileTypeUnknown {
		if err := snapshot.Type.Validate(); err != nil {
			return fmt.Errorf("filesystem type: %w", err)
		}
	}
	if required&FilesystemFieldDevice != 0 && (!snapshot.DeviceMajor.Known || !snapshot.DeviceMinor.Known) {
		return fmt.Errorf("filesystem precondition requires observed device major and minor")
	}
	if required&FilesystemFieldInode != 0 && !snapshot.Inode.Known {
		return fmt.Errorf("filesystem precondition requires observed inode")
	}
	if required&FilesystemFieldType != 0 {
		if err := snapshot.Type.Validate(); err != nil {
			return fmt.Errorf("filesystem precondition requires observed type: %w", err)
		}
	}
	if required&FilesystemFieldUID != 0 && !snapshot.UID.Known {
		return fmt.Errorf("filesystem precondition requires observed UID")
	}
	if required&FilesystemFieldGID != 0 && !snapshot.GID.Known {
		return fmt.Errorf("filesystem precondition requires observed GID")
	}
	if required&FilesystemFieldMode != 0 && !snapshot.Mode.Known {
		return fmt.Errorf("filesystem precondition requires observed mode")
	}
	if required&FilesystemFieldLinkCount != 0 && !snapshot.LinkCount.Known {
		return fmt.Errorf("filesystem precondition requires observed link count")
	}
	if required&FilesystemFieldSize != 0 && !snapshot.Size.Known {
		return fmt.Errorf("filesystem precondition requires observed size")
	}
	if required&FilesystemFieldModifiedAt != 0 && !snapshot.ModifiedAt.Known {
		return fmt.Errorf("filesystem precondition requires observed modified time")
	}
	if required&FilesystemFieldChangedAt != 0 && !snapshot.ChangedAt.Known {
		return fmt.Errorf("filesystem precondition requires observed changed time")
	}
	if required&FilesystemFieldMountID != 0 && !snapshot.MountID.Known {
		return fmt.Errorf("filesystem precondition requires observed mount ID")
	}
	return nil
}

func (fileType FileType) Validate() error {
	switch fileType {
	case FileTypeRegular, FileTypeDirectory, FileTypeSymlink, FileTypeSpecial:
		return nil
	default:
		return fmt.Errorf("unknown file type %q", fileType)
	}
}

func (precondition Precondition) Clone() Precondition {
	cloned := Precondition{Kind: precondition.Kind}
	if precondition.Filesystem != nil {
		value := *precondition.Filesystem
		value.Target = value.Target.Clone()
		cloned.Filesystem = &value
	}
	if precondition.PackageTransaction != nil {
		value := *precondition.PackageTransaction
		value.Graph = value.Graph.Clone()
		cloned.PackageTransaction = &value
	}
	if precondition.ManagerObject != nil {
		value := *precondition.ManagerObject
		value.Target = value.Target.Clone()
		cloned.ManagerObject = &value
	}
	if precondition.Capability != nil {
		value := *precondition.Capability
		cloned.Capability = &value
	}
	if precondition.ProjectMarker != nil {
		value := *precondition.ProjectMarker
		value.Marker = cloneBytePath(value.Marker)
		cloned.ProjectMarker = &value
	}
	if precondition.InstallerMetadata != nil {
		value := *precondition.InstallerMetadata
		value.Target = value.Target.Clone()
		cloned.InstallerMetadata = &value
	}
	if precondition.ProfileManager != nil {
		value := *precondition.ProfileManager
		cloned.ProfileManager = &value
	}
	return cloned
}
