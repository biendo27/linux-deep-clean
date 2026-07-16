package domain

import (
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// EvidenceKind is a closed discriminant for observations that justify a
// candidate or action. Unknown kinds are rejected rather than treated as a
// generic extension point.
type EvidenceKind string

const (
	EvidenceFilesystemIdentity EvidenceKind = "filesystem_identity"
	EvidencePackageTransaction EvidenceKind = "package_transaction"
	EvidenceManagerObject      EvidenceKind = "manager_object"
	EvidenceCapability         EvidenceKind = "capability"
	EvidenceProjectMarker      EvidenceKind = "project_marker"
	EvidenceInstallerMetadata  EvidenceKind = "installer_metadata"
	EvidenceProfileManager     EvidenceKind = "profile_manager"
)

// Evidence is a closed typed union. Exactly one field corresponding to Kind is
// populated. It deliberately has no untyped metadata map.
type Evidence struct {
	Kind               EvidenceKind
	Filesystem         *FilesystemEvidence
	PackageTransaction *PackageTransactionEvidence
	ManagerObject      *ManagerObjectEvidence
	Capability         *CapabilityEvidence
	ProjectMarker      *ProjectMarkerEvidence
	InstallerMetadata  *InstallerMetadataEvidence
	ProfileManager     *ProfileManagerEvidence
}

type FilesystemEvidence struct {
	Target   Target
	Snapshot FilesystemSnapshot
}

type PackageTransactionEvidence struct {
	Graph TransactionGraph
}

type ManagerObjectEvidence struct {
	Target  Target
	Version string
	Present bool
}

type CapabilityEvidence struct {
	Capability CapabilityID
	State      CapabilityState
	Version    string
}

type ProjectMarkerEvidence struct {
	Root      TrustedRootID
	Marker    pathbytes.BytePath
	Ecosystem string
}

// InstallerFormat is a closed metadata classification rather than a file path
// or command selector.
type InstallerFormat string

const (
	InstallerFormatDeb     InstallerFormat = "deb"
	InstallerFormatRPM     InstallerFormat = "rpm"
	InstallerFormatArchive InstallerFormat = "archive"
)

type InstallerMetadataEvidence struct {
	Target Target
	Format InstallerFormat
	Digest EvidenceDigest
}

// ProfileManagerKind names only distribution-supported profile managers.
type ProfileManagerKind string

const (
	ProfileManagerPAMAuthUpdate ProfileManagerKind = "pam_auth_update"
	ProfileManagerAuthselect    ProfileManagerKind = "authselect"
)

type ProfileManagerEvidence struct {
	Provider ProviderID
	Manager  ProfileManagerKind
	Version  string
}

func (evidence Evidence) Validate() error {
	variantCount := 0
	for _, present := range []bool{
		evidence.Filesystem != nil,
		evidence.PackageTransaction != nil,
		evidence.ManagerObject != nil,
		evidence.Capability != nil,
		evidence.ProjectMarker != nil,
		evidence.InstallerMetadata != nil,
		evidence.ProfileManager != nil,
	} {
		if present {
			variantCount++
		}
	}
	if variantCount != 1 {
		return fmt.Errorf("evidence must contain exactly one typed variant")
	}

	switch evidence.Kind {
	case EvidenceFilesystemIdentity:
		if evidence.Filesystem == nil {
			return fmt.Errorf("filesystem evidence kind has no filesystem variant")
		}
		return evidence.Filesystem.Validate()
	case EvidencePackageTransaction:
		if evidence.PackageTransaction == nil {
			return fmt.Errorf("package transaction evidence kind has no package variant")
		}
		return evidence.PackageTransaction.Validate()
	case EvidenceManagerObject:
		if evidence.ManagerObject == nil {
			return fmt.Errorf("manager object evidence kind has no manager variant")
		}
		return evidence.ManagerObject.Validate()
	case EvidenceCapability:
		if evidence.Capability == nil {
			return fmt.Errorf("capability evidence kind has no capability variant")
		}
		return evidence.Capability.Validate()
	case EvidenceProjectMarker:
		if evidence.ProjectMarker == nil {
			return fmt.Errorf("project marker evidence kind has no project variant")
		}
		return evidence.ProjectMarker.Validate()
	case EvidenceInstallerMetadata:
		if evidence.InstallerMetadata == nil {
			return fmt.Errorf("installer metadata evidence kind has no installer variant")
		}
		return evidence.InstallerMetadata.Validate()
	case EvidenceProfileManager:
		if evidence.ProfileManager == nil {
			return fmt.Errorf("profile manager evidence kind has no profile variant")
		}
		return evidence.ProfileManager.Validate()
	default:
		return fmt.Errorf("unknown evidence kind %q", evidence.Kind)
	}
}

func (evidence FilesystemEvidence) Validate() error {
	if err := evidence.Target.Validate(); err != nil {
		return err
	}
	if evidence.Target.Kind != TargetFilesystem {
		return fmt.Errorf("filesystem evidence requires a filesystem target")
	}
	return evidence.Snapshot.ValidateObserved()
}

func (evidence PackageTransactionEvidence) Validate() error {
	return evidence.Graph.Validate()
}

func (evidence ManagerObjectEvidence) Validate() error {
	if err := evidence.Target.Validate(); err != nil {
		return err
	}
	if evidence.Target.Kind != TargetManagerObject {
		return fmt.Errorf("manager object evidence requires a manager-object target")
	}
	if evidence.Version == "" {
		return fmt.Errorf("manager object evidence requires an exact version")
	}
	return nil
}

func (evidence CapabilityEvidence) Validate() error {
	if err := evidence.Capability.Validate(); err != nil {
		return err
	}
	if err := evidence.State.Validate(); err != nil {
		return err
	}
	if evidence.Version == "" {
		return fmt.Errorf("capability evidence requires a version or explicit probe revision")
	}
	return nil
}

func (evidence ProjectMarkerEvidence) Validate() error {
	if err := evidence.Root.Validate(); err != nil {
		return err
	}
	if _, err := pathbytes.New(evidence.Marker.Components()); err != nil {
		return fmt.Errorf("project marker path: %w", err)
	}
	return validateStableID("project ecosystem", evidence.Ecosystem)
}

func (evidence InstallerMetadataEvidence) Validate() error {
	if err := evidence.Target.Validate(); err != nil {
		return err
	}
	if evidence.Target.Kind != TargetFilesystem {
		return fmt.Errorf("installer metadata evidence requires a filesystem target")
	}
	if err := evidence.Format.Validate(); err != nil {
		return err
	}
	return evidence.Digest.Validate()
}

func (evidence ProfileManagerEvidence) Validate() error {
	if err := evidence.Provider.Validate(); err != nil {
		return err
	}
	if err := evidence.Manager.Validate(); err != nil {
		return err
	}
	if evidence.Version == "" {
		return fmt.Errorf("profile manager evidence requires a version")
	}
	return nil
}

func (format InstallerFormat) Validate() error {
	switch format {
	case InstallerFormatDeb, InstallerFormatRPM, InstallerFormatArchive:
		return nil
	default:
		return fmt.Errorf("unknown installer format %q", format)
	}
}

func (manager ProfileManagerKind) Validate() error {
	switch manager {
	case ProfileManagerPAMAuthUpdate, ProfileManagerAuthselect:
		return nil
	default:
		return fmt.Errorf("unknown profile manager %q", manager)
	}
}

func (evidence Evidence) Clone() Evidence {
	cloned := Evidence{Kind: evidence.Kind}
	if evidence.Filesystem != nil {
		value := *evidence.Filesystem
		value.Target = value.Target.Clone()
		cloned.Filesystem = &value
	}
	if evidence.PackageTransaction != nil {
		value := *evidence.PackageTransaction
		value.Graph = value.Graph.Clone()
		cloned.PackageTransaction = &value
	}
	if evidence.ManagerObject != nil {
		value := *evidence.ManagerObject
		value.Target = value.Target.Clone()
		cloned.ManagerObject = &value
	}
	if evidence.Capability != nil {
		value := *evidence.Capability
		cloned.Capability = &value
	}
	if evidence.ProjectMarker != nil {
		value := *evidence.ProjectMarker
		value.Marker = cloneBytePath(value.Marker)
		cloned.ProjectMarker = &value
	}
	if evidence.InstallerMetadata != nil {
		value := *evidence.InstallerMetadata
		value.Target = value.Target.Clone()
		cloned.InstallerMetadata = &value
	}
	if evidence.ProfileManager != nil {
		value := *evidence.ProfileManager
		cloned.ProfileManager = &value
	}
	return cloned
}

func cloneEvidenceSlice(evidence []Evidence) []Evidence {
	if evidence == nil {
		return nil
	}
	cloned := make([]Evidence, len(evidence))
	for index := range evidence {
		cloned[index] = evidence[index].Clone()
	}
	return cloned
}
