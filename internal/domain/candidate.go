package domain

import (
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// TargetKind distinguishes filesystem authority from manager-owned objects.
// Neither variant grants execution authority.
type TargetKind string

const (
	TargetFilesystem    TargetKind = "filesystem"
	TargetManagerObject TargetKind = "manager_object"
)

// ManagerScope is the explicitly selected installation scope of a manager
// object. It is not derived from an ambient process environment.
type ManagerScope string

const (
	ManagerScopeUser   ManagerScope = "user"
	ManagerScopeSystem ManagerScope = "system"
)

// FilesystemTarget names a relative byte path beneath a compiled trusted root.
// It intentionally has no absolute pathname field.
type FilesystemTarget struct {
	Root TrustedRootID
	Path pathbytes.BytePath
}

// ManagerObjectTarget names an object that a supported manager owns.
type ManagerObjectTarget struct {
	Provider ProviderID
	Object   ManagerObjectID
	Scope    ManagerScope
}

// Target is a closed discriminated union. Exactly one matching variant is set.
type Target struct {
	Kind          TargetKind
	Filesystem    *FilesystemTarget
	ManagerObject *ManagerObjectTarget
}

// Confidence records how directly a candidate classification follows from
// typed evidence. It does not broaden authority.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
	ConfidenceExact  Confidence = "exact"
)

// Candidate is an immutable-by-convention provider observation. Creating a
// candidate validates and defensively copies its nested values.
type Candidate struct {
	ID                    CandidateID
	Provider              ProviderID
	Target                Target
	Evidence              []Evidence
	Size                  SizeFacts
	Confidence            Confidence
	DiscoveryPrecondition Precondition
}

func NewFilesystemTarget(root TrustedRootID, path pathbytes.BytePath) (Target, error) {
	target := Target{
		Kind: TargetFilesystem,
		Filesystem: &FilesystemTarget{
			Root: root,
			Path: cloneBytePath(path),
		},
	}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func NewManagerObjectTarget(provider ProviderID, object ManagerObjectID, scope ManagerScope) (Target, error) {
	target := Target{
		Kind: TargetManagerObject,
		ManagerObject: &ManagerObjectTarget{
			Provider: provider,
			Object:   object,
			Scope:    scope,
		},
	}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (target Target) Validate() error {
	switch target.Kind {
	case TargetFilesystem:
		if target.Filesystem == nil || target.ManagerObject != nil {
			return fmt.Errorf("filesystem target must contain exactly one filesystem variant")
		}
		if err := target.Filesystem.Root.Validate(); err != nil {
			return err
		}
		if _, err := pathbytes.New(target.Filesystem.Path.Components()); err != nil {
			return fmt.Errorf("filesystem target path: %w", err)
		}
		return nil
	case TargetManagerObject:
		if target.ManagerObject == nil || target.Filesystem != nil {
			return fmt.Errorf("manager target must contain exactly one manager-object variant")
		}
		if err := target.ManagerObject.Provider.Validate(); err != nil {
			return err
		}
		if err := target.ManagerObject.Object.Validate(); err != nil {
			return err
		}
		return target.ManagerObject.Scope.Validate()
	default:
		return fmt.Errorf("unknown target kind %q", target.Kind)
	}
}

func (scope ManagerScope) Validate() error {
	switch scope {
	case ManagerScopeUser, ManagerScopeSystem:
		return nil
	default:
		return fmt.Errorf("unknown manager scope %q", scope)
	}
}

func (confidence Confidence) Validate() error {
	switch confidence {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh, ConfidenceExact:
		return nil
	default:
		return fmt.Errorf("unknown confidence %q", confidence)
	}
}

func NewCandidate(candidate Candidate) (Candidate, error) {
	cloned := candidate.Clone()
	if err := cloned.Validate(); err != nil {
		return Candidate{}, err
	}
	return cloned, nil
}

func (candidate Candidate) Validate() error {
	if err := candidate.ID.Validate(); err != nil {
		return err
	}
	if err := candidate.Provider.Validate(); err != nil {
		return err
	}
	if err := candidate.Target.Validate(); err != nil {
		return err
	}
	if candidate.Target.Kind == TargetManagerObject && candidate.Target.ManagerObject.Provider != candidate.Provider {
		return fmt.Errorf("candidate provider must match its manager-object target provider")
	}
	if len(candidate.Evidence) == 0 {
		return fmt.Errorf("candidate requires typed evidence")
	}
	boundEvidence := false
	for index, evidence := range candidate.Evidence {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("candidate evidence %d: %w", index, err)
		}
		if evidenceBindsTarget(evidence, candidate.Target) {
			boundEvidence = true
		}
	}
	if !boundEvidence {
		return fmt.Errorf("candidate evidence does not bind the candidate target")
	}
	if err := candidate.Size.Validate(); err != nil {
		return fmt.Errorf("candidate size facts: %w", err)
	}
	if err := candidate.Confidence.Validate(); err != nil {
		return err
	}
	if err := candidate.DiscoveryPrecondition.Validate(); err != nil {
		return fmt.Errorf("candidate discovery precondition: %w", err)
	}
	if err := validatePreconditionTargetBinding(candidate.DiscoveryPrecondition, candidate.Target); err != nil {
		return fmt.Errorf("candidate discovery precondition: %w", err)
	}
	return nil
}

func (target Target) Clone() Target {
	cloned := Target{Kind: target.Kind}
	if target.Filesystem != nil {
		cloned.Filesystem = &FilesystemTarget{
			Root: target.Filesystem.Root,
			Path: cloneBytePath(target.Filesystem.Path),
		}
	}
	if target.ManagerObject != nil {
		cloned.ManagerObject = &ManagerObjectTarget{
			Provider: target.ManagerObject.Provider,
			Object:   target.ManagerObject.Object,
			Scope:    target.ManagerObject.Scope,
		}
	}
	return cloned
}

func (candidate Candidate) Clone() Candidate {
	cloned := candidate
	cloned.Target = candidate.Target.Clone()
	cloned.Evidence = cloneEvidenceSlice(candidate.Evidence)
	cloned.Size = candidate.Size.Clone()
	cloned.DiscoveryPrecondition = candidate.DiscoveryPrecondition.Clone()
	return cloned
}

func cloneBytePath(path pathbytes.BytePath) pathbytes.BytePath {
	cloned, err := pathbytes.New(path.Components())
	if err != nil {
		return pathbytes.BytePath{}
	}
	return cloned
}
