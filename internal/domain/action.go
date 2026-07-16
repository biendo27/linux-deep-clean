package domain

import "fmt"

// ActionKind is the closed, versioned operation vocabulary carried by a plan.
// Naming an action here does not register an executor or grant any authority.
type ActionKind string

const (
	ActionTrashPath                ActionKind = "trash_path"
	ActionDeleteRecreatablePath    ActionKind = "delete_recreatable_path"
	ActionQuarantinePath           ActionKind = "quarantine_path"
	ActionRestoreTrashPath         ActionKind = "restore_trash_path"
	ActionRestoreQuarantinePath    ActionKind = "restore_quarantine_path"
	ActionRepairState              ActionKind = "repair_state"
	ActionRemoveNativePackage      ActionKind = "remove_native_package"
	ActionRemoveFlatpakRef         ActionKind = "remove_flatpak_ref"
	ActionRemoveSnap               ActionKind = "remove_snap"
	ActionCleanPackageCache        ActionKind = "clean_package_cache"
	ActionVacuumJournal            ActionKind = "vacuum_journal"
	ActionRunOwnedCacheRebuild     ActionKind = "run_owned_cache_rebuild"
	ActionInstallCompletion        ActionKind = "install_completion"
	ActionConfigureFingerprintAuth ActionKind = "configure_fingerprint_auth"
	ActionUpdateLDCleanPackage     ActionKind = "update_ldclean_package"
	ActionRemoveLDCleanPackage     ActionKind = "remove_ldclean_package"
)

// Risk is the disclosed safety class selected by policy. It is descriptive
// plan data only and cannot make an otherwise unsupported action executable.
type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

// Reversibility describes the strongest honest recovery claim for an action.
// A package reinstall suggestion, for example, is no rollback.
type Reversibility string

const (
	ReversibilityRecoverable  Reversibility = "recoverable"
	ReversibilityRecreatable  Reversibility = "recreatable"
	ReversibilityIrreversible Reversibility = "irreversible"
	ReversibilityNoRollback   Reversibility = "no_rollback"
)

// Postcondition is the closed observable state an executor must verify before
// reporting success. Detailed executor-specific checks are introduced by the
// later action registries; this domain type keeps their public result shape
// stable from schema v1 onward.
type Postcondition string

const (
	PostconditionTargetAbsent          Postcondition = "target_absent"
	PostconditionTargetPresent         Postcondition = "target_present"
	PostconditionStateRepaired         Postcondition = "state_repaired"
	PostconditionCacheCleaned          Postcondition = "cache_cleaned"
	PostconditionJournalVacuumed       Postcondition = "journal_vacuumed"
	PostconditionOwnedCacheRebuilt     Postcondition = "owned_cache_rebuilt"
	PostconditionCompletionInstalled   Postcondition = "completion_installed"
	PostconditionFingerprintConfigured Postcondition = "fingerprint_configured"
	PostconditionPackageUpdated        Postcondition = "package_updated"
)

// Action is one immutable-by-convention planned operation. It contains only
// typed data: no argv, executable path, shell text, or absolute filesystem
// authority. Dependencies retain planner order; Plan owns graph validation.
type Action struct {
	ID                    ActionID
	Kind                  ActionKind
	Target                Target
	Evidence              []Evidence
	Precondition          Precondition
	Dependencies          []ActionID
	Risk                  Risk
	Reversibility         Reversibility
	EstimatedEffect       SizeFacts
	RequiredCapability    CapabilityID
	ProviderGuarantee     ProviderGuarantee
	ExpectedPostcondition Postcondition
}

// NewAction validates and defensively copies a planned action. It intentionally
// preserves dependency order because a plan declares that order explicitly.
func NewAction(action Action) (Action, error) {
	cloned := action.Clone()
	if err := cloned.Validate(); err != nil {
		return Action{}, err
	}
	return cloned, nil
}

// Validate rejects unknown vocabulary values and inconsistent typed bindings.
// It does not decide whether an action is enabled; capability and executor
// registration remain later, separate authority checks.
func (action Action) Validate() error {
	if err := action.ID.Validate(); err != nil {
		return fmt.Errorf("action ID: %w", err)
	}
	if err := action.Kind.Validate(); err != nil {
		return err
	}
	if err := action.Target.Validate(); err != nil {
		return fmt.Errorf("action target: %w", err)
	}
	if len(action.Evidence) == 0 {
		return fmt.Errorf("action requires typed evidence")
	}

	boundEvidence := false
	for index, evidence := range action.Evidence {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("action evidence %d: %w", index, err)
		}
		if evidenceBindsTarget(evidence, action.Target) {
			boundEvidence = true
		}
	}
	if !boundEvidence {
		return fmt.Errorf("action evidence does not bind the action target")
	}

	if err := action.Precondition.Validate(); err != nil {
		return fmt.Errorf("action precondition: %w", err)
	}
	if err := validatePreconditionTargetBinding(action.Precondition, action.Target); err != nil {
		return err
	}

	seenDependencies := make(map[ActionID]struct{}, len(action.Dependencies))
	for index, dependency := range action.Dependencies {
		if err := dependency.Validate(); err != nil {
			return fmt.Errorf("action dependency %d: %w", index, err)
		}
		if dependency == action.ID {
			return fmt.Errorf("action dependency %d must not reference itself", index)
		}
		if _, exists := seenDependencies[dependency]; exists {
			return fmt.Errorf("action dependency %d duplicates %q", index, dependency)
		}
		seenDependencies[dependency] = struct{}{}
	}

	if err := action.Risk.Validate(); err != nil {
		return err
	}
	if err := action.Reversibility.Validate(); err != nil {
		return err
	}
	if err := action.EstimatedEffect.Validate(); err != nil {
		return fmt.Errorf("action estimated effect: %w", err)
	}
	if err := action.RequiredCapability.Validate(); err != nil {
		return fmt.Errorf("action required capability: %w", err)
	}
	if err := action.ProviderGuarantee.Validate(); err != nil {
		return fmt.Errorf("action provider guarantee: %w", err)
	}
	if err := validateActionGuaranteeBinding(action); err != nil {
		return err
	}
	if err := action.ExpectedPostcondition.Validate(); err != nil {
		return err
	}
	return nil
}

func (kind ActionKind) Validate() error {
	switch kind {
	case ActionTrashPath,
		ActionDeleteRecreatablePath,
		ActionQuarantinePath,
		ActionRestoreTrashPath,
		ActionRestoreQuarantinePath,
		ActionRepairState,
		ActionRemoveNativePackage,
		ActionRemoveFlatpakRef,
		ActionRemoveSnap,
		ActionCleanPackageCache,
		ActionVacuumJournal,
		ActionRunOwnedCacheRebuild,
		ActionInstallCompletion,
		ActionConfigureFingerprintAuth,
		ActionUpdateLDCleanPackage,
		ActionRemoveLDCleanPackage:
		return nil
	default:
		return fmt.Errorf("unknown action kind %q", kind)
	}
}

func (risk Risk) Validate() error {
	switch risk {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return nil
	default:
		return fmt.Errorf("unknown action risk %q", risk)
	}
}

func (reversibility Reversibility) Validate() error {
	switch reversibility {
	case ReversibilityRecoverable, ReversibilityRecreatable, ReversibilityIrreversible, ReversibilityNoRollback:
		return nil
	default:
		return fmt.Errorf("unknown action reversibility %q", reversibility)
	}
}

func (postcondition Postcondition) Validate() error {
	switch postcondition {
	case PostconditionTargetAbsent,
		PostconditionTargetPresent,
		PostconditionStateRepaired,
		PostconditionCacheCleaned,
		PostconditionJournalVacuumed,
		PostconditionOwnedCacheRebuilt,
		PostconditionCompletionInstalled,
		PostconditionFingerprintConfigured,
		PostconditionPackageUpdated:
		return nil
	default:
		return fmt.Errorf("unknown action postcondition %q", postcondition)
	}
}

func evidenceBindsTarget(evidence Evidence, target Target) bool {
	switch evidence.Kind {
	case EvidenceFilesystemIdentity:
		return evidence.Filesystem != nil && sameTarget(evidence.Filesystem.Target, target)
	case EvidenceManagerObject:
		return evidence.ManagerObject != nil && sameTarget(evidence.ManagerObject.Target, target)
	case EvidenceInstallerMetadata:
		return evidence.InstallerMetadata != nil && sameTarget(evidence.InstallerMetadata.Target, target)
	case EvidencePackageTransaction:
		return evidence.PackageTransaction != nil && graphBindsTarget(evidence.PackageTransaction.Graph, target)
	default:
		return false
	}
}

func validatePreconditionTargetBinding(precondition Precondition, target Target) error {
	switch precondition.Kind {
	case PreconditionFilesystemIdentity:
		if precondition.Filesystem == nil || !sameTarget(precondition.Filesystem.Target, target) {
			return fmt.Errorf("filesystem precondition does not bind the action target")
		}
	case PreconditionManagerObjectState:
		if precondition.ManagerObject == nil || !sameTarget(precondition.ManagerObject.Target, target) {
			return fmt.Errorf("manager-object precondition does not bind the action target")
		}
	case PreconditionInstallerMetadata:
		if precondition.InstallerMetadata == nil || !sameTarget(precondition.InstallerMetadata.Target, target) {
			return fmt.Errorf("installer metadata precondition does not bind the action target")
		}
	case PreconditionPackageTransaction:
		if precondition.PackageTransaction == nil || !graphBindsTarget(precondition.PackageTransaction.Graph, target) {
			return fmt.Errorf("package transaction precondition does not bind the action target")
		}
	default:
		return fmt.Errorf("precondition kind %q does not bind the action target", precondition.Kind)
	}
	return nil
}

func validateActionGuaranteeBinding(action Action) error {
	guarantee := action.ProviderGuarantee
	switch action.Target.Kind {
	case TargetFilesystem:
		if guarantee.Kind != ProviderGuaranteeReadOnlyInventory {
			return fmt.Errorf("filesystem action provider guarantee must be read-only inventory")
		}
		return nil
	case TargetManagerObject:
		target := action.Target.ManagerObject
		if target == nil {
			return fmt.Errorf("manager action has no manager-object target")
		}
		reference := ManagerObjectRef{ID: target.Object, Scope: target.Scope}
		transactionGraphs := matchingTransactionEvidenceForAction(action, reference, target.Provider)
		if guarantee.Kind != ProviderGuaranteeReadOnlyInventory && len(transactionGraphs) == 0 {
			return fmt.Errorf("manager action provider guarantee has no matching transaction evidence")
		}
		switch guarantee.Kind {
		case ProviderGuaranteeReadOnlyInventory:
			return fmt.Errorf("manager action provider guarantee must bind its target")
		case ProviderGuaranteeBoundedEffectOnly:
			if !guaranteeCovers(guarantee, reference) {
				return fmt.Errorf("bounded-effect provider guarantee does not cover the manager target")
			}
			for _, graph := range transactionGraphs {
				if err := validateBoundedGuaranteeAgainstGraph(guarantee, graph); err != nil {
					return err
				}
			}
		case ProviderGuaranteeExactTargetReprobeRequired:
			if len(guarantee.CoveredObjects) != 1 || guarantee.CoveredObjects[0] != reference {
				return fmt.Errorf("exact-target provider guarantee must cover only the manager target")
			}
			for _, graph := range transactionGraphs {
				if err := validateExactTargetGuaranteeAgainstGraph(reference, graph); err != nil {
					return err
				}
			}
		case ProviderGuaranteeExactGraphReprobeRequired:
			if len(transactionGraphs) == 0 {
				return fmt.Errorf("exact-graph provider guarantee has no matching transaction evidence")
			}
			for _, graph := range transactionGraphs {
				if graph.Guarantee.Kind != ProviderGuaranteeExactGraphReprobeRequired || !sameNodeSet(graph.Nodes, guarantee.CoveredObjects) {
					return fmt.Errorf("exact-graph provider guarantee does not match transaction evidence")
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown action target kind %q", action.Target.Kind)
	}
}

func guaranteeCovers(guarantee ProviderGuarantee, reference ManagerObjectRef) bool {
	for _, covered := range guarantee.CoveredObjects {
		if covered == reference {
			return true
		}
	}
	return false
}

func validateBoundedGuaranteeAgainstGraph(guarantee ProviderGuarantee, graph TransactionGraph) error {
	switch graph.Guarantee.Kind {
	case ProviderGuaranteeBoundedEffectOnly, ProviderGuaranteeExactTargetReprobeRequired, ProviderGuaranteeExactGraphReprobeRequired:
	default:
		return fmt.Errorf("bounded-effect provider guarantee exceeds transaction evidence")
	}
	for _, covered := range guarantee.CoveredObjects {
		if !graph.hasNode(covered) || !guaranteeCovers(graph.Guarantee, covered) {
			return fmt.Errorf("bounded-effect provider guarantee exceeds transaction evidence")
		}
	}
	return nil
}

func validateExactTargetGuaranteeAgainstGraph(reference ManagerObjectRef, graph TransactionGraph) error {
	switch graph.Guarantee.Kind {
	case ProviderGuaranteeExactTargetReprobeRequired, ProviderGuaranteeExactGraphReprobeRequired:
		if guaranteeCovers(graph.Guarantee, reference) {
			return nil
		}
	}
	return fmt.Errorf("exact-target provider guarantee exceeds transaction evidence")
}

func matchingTransactionEvidenceForAction(action Action, reference ManagerObjectRef, provider ProviderID) []TransactionGraph {
	var graphs []TransactionGraph
	for _, evidence := range action.Evidence {
		if evidence.Kind != EvidencePackageTransaction || evidence.PackageTransaction == nil {
			continue
		}
		graph := evidence.PackageTransaction.Graph
		if graph.Provider == provider && graph.hasNode(reference) {
			graphs = append(graphs, graph)
		}
	}
	if action.Precondition.Kind == PreconditionPackageTransaction && action.Precondition.PackageTransaction != nil {
		graph := action.Precondition.PackageTransaction.Graph
		if graph.Provider == provider && graph.hasNode(reference) {
			graphs = append(graphs, graph)
		}
	}
	return graphs
}

func graphBindsTarget(graph TransactionGraph, target Target) bool {
	if target.Kind != TargetManagerObject || target.ManagerObject == nil {
		return false
	}
	if graph.Provider != target.ManagerObject.Provider {
		return false
	}
	return graph.hasNode(ManagerObjectRef{ID: target.ManagerObject.Object, Scope: target.ManagerObject.Scope})
}

func sameTarget(left, right Target) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case TargetFilesystem:
		return left.Filesystem != nil && right.Filesystem != nil &&
			left.Filesystem.Root == right.Filesystem.Root &&
			left.Filesystem.Path.Equal(right.Filesystem.Path)
	case TargetManagerObject:
		return left.ManagerObject != nil && right.ManagerObject != nil &&
			left.ManagerObject.Provider == right.ManagerObject.Provider &&
			left.ManagerObject.Object == right.ManagerObject.Object &&
			left.ManagerObject.Scope == right.ManagerObject.Scope
	default:
		return false
	}
}

// Clone returns an action with no mutable backing storage shared with the
// source. It preserves all declared ordering, including dependencies.
func (action Action) Clone() Action {
	cloned := action
	cloned.Target = action.Target.Clone()
	cloned.Evidence = cloneEvidenceSlice(action.Evidence)
	cloned.Precondition = action.Precondition.Clone()
	cloned.Dependencies = append([]ActionID(nil), action.Dependencies...)
	cloned.EstimatedEffect = action.EstimatedEffect.Clone()
	cloned.ProviderGuarantee = ProviderGuarantee{
		Kind:           action.ProviderGuarantee.Kind,
		CoveredObjects: append([]ManagerObjectRef(nil), action.ProviderGuarantee.CoveredObjects...),
	}
	return cloned
}
