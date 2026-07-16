package planproto

import (
	"bytes"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestClosedDomainDTOConversionsCoverEveryVariant(t *testing.T) {
	limits := DefaultDecodeLimits()
	filesystemTarget := testPlan(t).CanonicalBody().Actions[0].Target
	managerTarget := testManagerTarget(t)
	graph := testGraph(t, domain.TransactionEdgeDependency)
	marker := mustPath(t, "project", "marker")
	evidenceDigest := mustEvidenceDigest(t, 3)

	for _, target := range []domain.Target{filesystemTarget, managerTarget} {
		got, err := fromTargetWire(toTargetWire(target), limits)
		if err != nil {
			t.Fatalf("target conversion error = %v", err)
		}
		requireCanonicalDTOEqual(t, toTargetWire(target), toTargetWire(got))
	}

	for _, state := range []domain.CapabilityState{
		domain.CapabilitySupported,
		domain.CapabilityUnsupported,
		domain.CapabilityUnavailable,
	} {
		snapshot, err := domain.NewCapabilitySnapshot([]domain.CapabilityFact{{
			ID: mustCapabilityID(t, "trash"), State: state, Version: "v1",
		}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := fromCapabilitySnapshotWire(toCapabilitySnapshotWire(snapshot))
		if err != nil {
			t.Fatalf("capability state %q conversion error = %v", state, err)
		}
		requireCanonicalDTOEqual(t, toCapabilitySnapshotWire(snapshot), toCapabilitySnapshotWire(got))
	}

	evidence := []domain.Evidence{
		{Kind: domain.EvidenceFilesystemIdentity, Filesystem: &domain.FilesystemEvidence{Target: filesystemTarget, Snapshot: testFilesystemSnapshot()}},
		{Kind: domain.EvidencePackageTransaction, PackageTransaction: &domain.PackageTransactionEvidence{Graph: graph}},
		{Kind: domain.EvidenceManagerObject, ManagerObject: &domain.ManagerObjectEvidence{Target: managerTarget, Version: "1.0", Present: true}},
		{Kind: domain.EvidenceCapability, Capability: &domain.CapabilityEvidence{Capability: mustCapabilityID(t, "trash"), State: domain.CapabilitySupported, Version: "v1"}},
		{Kind: domain.EvidenceProjectMarker, ProjectMarker: &domain.ProjectMarkerEvidence{Root: mustRoot(t, "user-cache"), Marker: marker, Ecosystem: "node"}},
		{Kind: domain.EvidenceInstallerMetadata, InstallerMetadata: &domain.InstallerMetadataEvidence{Target: filesystemTarget, Format: domain.InstallerFormatDeb, Digest: evidenceDigest}},
		{Kind: domain.EvidenceProfileManager, ProfileManager: &domain.ProfileManagerEvidence{Provider: mustProvider(t, "xdg-cache"), Manager: domain.ProfileManagerPAMAuthUpdate, Version: "v1"}},
	}
	for _, value := range evidence {
		got, err := fromEvidenceWire(toEvidenceWire(value), limits)
		if err != nil {
			t.Fatalf("evidence %q conversion error = %v", value.Kind, err)
		}
		requireCanonicalDTOEqual(t, toEvidenceWire(value), toEvidenceWire(got))
	}

	preconditions := []domain.Precondition{
		{Kind: domain.PreconditionFilesystemIdentity, Filesystem: &domain.FilesystemPrecondition{Target: filesystemTarget, Required: allFilesystemFields(), Snapshot: testFilesystemSnapshot()}},
		{Kind: domain.PreconditionPackageTransaction, PackageTransaction: &domain.PackageTransactionPrecondition{Graph: graph, ManagerStateDigest: evidenceDigest}},
		{Kind: domain.PreconditionManagerObjectState, ManagerObject: &domain.ManagerObjectPrecondition{Target: managerTarget, ExpectedVersion: "1.0", ExpectedPresent: true}},
		{Kind: domain.PreconditionCapability, Capability: &domain.CapabilityPrecondition{Capability: mustCapabilityID(t, "trash"), State: domain.CapabilitySupported, Version: "v1"}},
		{Kind: domain.PreconditionProjectMarker, ProjectMarker: &domain.ProjectMarkerPrecondition{Root: mustRoot(t, "user-cache"), Marker: marker, Ecosystem: "node"}},
		{Kind: domain.PreconditionInstallerMetadata, InstallerMetadata: &domain.InstallerMetadataPrecondition{Target: filesystemTarget, Format: domain.InstallerFormatRPM, Digest: evidenceDigest}},
		{Kind: domain.PreconditionProfileManager, ProfileManager: &domain.ProfileManagerPrecondition{Provider: mustProvider(t, "xdg-cache"), Manager: domain.ProfileManagerAuthselect, Version: "v1"}},
	}
	for _, value := range preconditions {
		got, err := fromPreconditionWire(toPreconditionWire(value), limits)
		if err != nil {
			t.Fatalf("precondition %q conversion error = %v", value.Kind, err)
		}
		requireCanonicalDTOEqual(t, toPreconditionWire(value), toPreconditionWire(got))
	}
}

func TestTransactionGraphGuaranteeAndActionDTOConversionsCoverEnums(t *testing.T) {
	limits := DefaultDecodeLimits()
	for _, reason := range []domain.TransactionEdgeReason{
		domain.TransactionEdgeDependency,
		domain.TransactionEdgeReplacement,
		domain.TransactionEdgeConflict,
		domain.TransactionEdgeRemoval,
	} {
		graph := testGraph(t, reason)
		got, err := fromTransactionGraphWire(toTransactionGraphWire(graph))
		if err != nil {
			t.Fatalf("graph reason %q conversion error = %v", reason, err)
		}
		requireCanonicalDTOEqual(t, toTransactionGraphWire(graph), toTransactionGraphWire(got))
	}

	object := mustManagerRef(t, "alpha-package", domain.ManagerScopeUser)
	for _, kind := range []domain.ProviderGuaranteeKind{
		domain.ProviderGuaranteeReadOnlyInventory,
		domain.ProviderGuaranteeBoundedEffectOnly,
		domain.ProviderGuaranteeExactTargetReprobeRequired,
		domain.ProviderGuaranteeExactGraphReprobeRequired,
	} {
		guarantee := domain.ProviderGuarantee{Kind: kind}
		if kind != domain.ProviderGuaranteeReadOnlyInventory {
			guarantee.CoveredObjects = []domain.ManagerObjectRef{object}
		}
		got, err := fromProviderGuaranteeWire(toProviderGuaranteeWire(guarantee))
		if err != nil {
			t.Fatalf("guarantee %q conversion error = %v", kind, err)
		}
		requireCanonicalDTOEqual(t, toProviderGuaranteeWire(guarantee), toProviderGuaranteeWire(got))
	}

	filesystemTarget := testPlan(t).CanonicalBody().Actions[0].Target
	evidence := domain.Evidence{Kind: domain.EvidenceFilesystemIdentity, Filesystem: &domain.FilesystemEvidence{Target: filesystemTarget, Snapshot: testFilesystemSnapshot()}}
	precondition := domain.Precondition{Kind: domain.PreconditionFilesystemIdentity, Filesystem: &domain.FilesystemPrecondition{Target: filesystemTarget, Required: allFilesystemFields(), Snapshot: testFilesystemSnapshot()}}
	kinds := []domain.ActionKind{
		domain.ActionTrashPath, domain.ActionDeleteRecreatablePath, domain.ActionQuarantinePath,
		domain.ActionRestoreTrashPath, domain.ActionRestoreQuarantinePath, domain.ActionRepairState,
		domain.ActionRemoveNativePackage, domain.ActionRemoveFlatpakRef, domain.ActionRemoveSnap,
		domain.ActionCleanPackageCache, domain.ActionVacuumJournal, domain.ActionRunOwnedCacheRebuild,
		domain.ActionInstallCompletion, domain.ActionConfigureFingerprintAuth,
		domain.ActionUpdateLDCleanPackage, domain.ActionRemoveLDCleanPackage,
	}
	risks := []domain.Risk{domain.RiskLow, domain.RiskMedium, domain.RiskHigh, domain.RiskCritical}
	reversibilities := []domain.Reversibility{domain.ReversibilityRecoverable, domain.ReversibilityRecreatable, domain.ReversibilityIrreversible, domain.ReversibilityNoRollback}
	postconditions := []domain.Postcondition{
		domain.PostconditionTargetAbsent, domain.PostconditionTargetPresent, domain.PostconditionStateRepaired,
		domain.PostconditionCacheCleaned, domain.PostconditionJournalVacuumed, domain.PostconditionOwnedCacheRebuilt,
		domain.PostconditionCompletionInstalled, domain.PostconditionFingerprintConfigured, domain.PostconditionPackageUpdated,
	}
	for index, kind := range kinds {
		action := domain.Action{
			ID:                    mustActionID(t, "action-"+string(rune('a'+index))),
			Kind:                  kind,
			Target:                filesystemTarget,
			Evidence:              []domain.Evidence{evidence},
			Precondition:          precondition,
			Risk:                  risks[index%len(risks)],
			Reversibility:         reversibilities[index%len(reversibilities)],
			EstimatedEffect:       testSizeFacts(),
			RequiredCapability:    mustCapabilityID(t, "trash"),
			ProviderGuarantee:     domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeReadOnlyInventory},
			ExpectedPostcondition: postconditions[index%len(postconditions)],
		}
		got, err := fromActionWire(toActionWire(action), limits)
		if err != nil {
			t.Fatalf("action %q conversion error = %v", kind, err)
		}
		requireCanonicalDTOEqual(t, toActionWire(action), toActionWire(got))
	}
}

func TestTransactionGraphDTOPreservesScopedObjectReferences(t *testing.T) {
	userReference := mustManagerRef(t, "shared-package", domain.ManagerScopeUser)
	systemReference := mustManagerRef(t, "shared-package", domain.ManagerScopeSystem)
	graph, err := domain.NewTransactionGraph(domain.TransactionGraph{
		Provider: mustProvider(t, "xdg-cache"),
		Nodes: []domain.TransactionNode{
			{ID: userReference.ID, Scope: userReference.Scope, Version: "1.0"},
			{ID: systemReference.ID, Scope: systemReference.Scope, Version: "2.0"},
		},
		Edges: []domain.TransactionEdge{{
			From:   userReference,
			To:     systemReference,
			Reason: domain.TransactionEdgeDependency,
		}},
		ProviderEvidenceDigest: mustEvidenceDigest(t, 5),
		Guarantee: domain.ProviderGuarantee{
			Kind:           domain.ProviderGuaranteeExactGraphReprobeRequired,
			CoveredObjects: []domain.ManagerObjectRef{userReference, systemReference},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	wire := toTransactionGraphWire(graph)
	if wire.Edges[0].From.Scope == wire.Edges[0].To.Scope {
		t.Fatal("transaction edge wire lost distinct source and target scopes")
	}
	decoded, err := fromTransactionGraphWire(wire)
	if err != nil {
		t.Fatal(err)
	}
	requireCanonicalDTOEqual(t, toTransactionGraphWire(graph), toTransactionGraphWire(decoded))

	wire.Edges[0].To.Scope = "not-a-scope"
	if _, err := fromTransactionGraphWire(wire); err == nil {
		t.Fatal("accepted a transaction edge whose scoped reference is invalid")
	}
}

func TestResultProbeAndRecoveryDTOConversionsCoverTerminals(t *testing.T) {
	limits := DefaultDecodeLimits()
	plan := testPlan(t)
	filesystemTarget := plan.CanonicalBody().Actions[0].Target
	managerTarget := testManagerTarget(t)
	graph := testGraph(t, domain.TransactionEdgeDependency)
	probes := []domain.ReconciliationProbe{
		{Kind: domain.ReconciliationProbeFilesystemTarget, Target: filesystemTarget},
		{Kind: domain.ReconciliationProbeManagerObject, Target: managerTarget},
		{Kind: domain.ReconciliationProbePackageTransaction, Graph: graph},
		{Kind: domain.ReconciliationProbeStateRecord, StateRecord: &domain.StateRecordProbe{RunID: mustRunID(t, "run-20260716-0001"), ActionID: mustActionID(t, "trash-cache")}},
	}
	for _, probe := range probes {
		got, err := fromReconciliationProbeWire(toReconciliationProbeWire(probe), limits)
		if err != nil {
			t.Fatalf("probe %q conversion error = %v", probe.Kind, err)
		}
		requireCanonicalDTOEqual(t, toReconciliationProbeWire(probe), toReconciliationProbeWire(got))
	}

	handle := testRecoveryHandle(t)
	gotHandle, err := fromRecoveryHandleWire(toRecoveryHandleWire(handle), limits)
	if err != nil {
		t.Fatalf("recovery handle conversion error = %v", err)
	}
	requireCanonicalDTOEqual(t, toRecoveryHandleWire(handle), toRecoveryHandleWire(gotHandle))

	cases := []domain.ActionResult{
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeSuccess, Attempted: true, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable, VerifiedEffect: testSizeFacts()},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeSuccess, Attempted: true, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryRetained, RecoveryHandle: &handle},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionRestoreTrashPath, Outcome: domain.OutcomeSuccess, Attempted: true, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryRestored},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeSkipped, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeDrifted, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeDenied, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeUnsupported, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeFailed, Attempted: true, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeInterrupted, Reconciliation: domain.ReconciliationNotRequired, Recovery: domain.RecoveryNotApplicable},
		{ActionID: mustActionID(t, "trash-cache"), Kind: domain.ActionTrashPath, Outcome: domain.OutcomeIndeterminate, Attempted: true, Reconciliation: domain.ReconciliationRequired, Recovery: domain.RecoveryNotApplicable, ReconciliationProbe: &probes[0]},
	}
	for _, value := range cases {
		got, err := fromActionResultWire(toActionResultWire(value), limits)
		if err != nil {
			t.Fatalf("action result %q conversion error = %v", value.Outcome, err)
		}
		requireCanonicalDTOEqual(t, toActionResultWire(value), toActionResultWire(got))
	}

	result := testIndeterminateResult(t, plan)
	gotResult, err := fromResultWire(toResultWire(result), limits)
	if err != nil {
		t.Fatalf("result conversion error = %v", err)
	}
	requireResultEqual(t, result, gotResult)
}

func testGraph(t *testing.T, reason domain.TransactionEdgeReason) domain.TransactionGraph {
	t.Helper()
	graph, err := domain.NewTransactionGraph(domain.TransactionGraph{
		Provider: mustProvider(t, "xdg-cache"),
		Nodes: []domain.TransactionNode{
			{ID: mustManagerObject(t, "alpha-package"), Version: "1.0", Scope: domain.ManagerScopeUser, Protected: true, MaintainerScripts: true},
			{ID: mustManagerObject(t, "beta-package"), Version: "2.0", Scope: domain.ManagerScopeUser, Essential: true, RestartRequired: true, NetworkRequired: true},
		},
		Edges: []domain.TransactionEdge{{
			From:   mustManagerRef(t, "alpha-package", domain.ManagerScopeUser),
			To:     mustManagerRef(t, "beta-package", domain.ManagerScopeUser),
			Reason: reason,
		}},
		ProviderEvidenceDigest: mustEvidenceDigest(t, 4),
		Guarantee: domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeExactGraphReprobeRequired, CoveredObjects: []domain.ManagerObjectRef{
			mustManagerRef(t, "alpha-package", domain.ManagerScopeUser), mustManagerRef(t, "beta-package", domain.ManagerScopeUser),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func testManagerTarget(t *testing.T) domain.Target {
	t.Helper()
	target, err := domain.NewManagerObjectTarget(mustProvider(t, "xdg-cache"), mustManagerObject(t, "alpha-package"), domain.ManagerScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func testRecoveryHandle(t *testing.T) domain.RecoveryHandle {
	t.Helper()
	return domain.RecoveryHandle{
		Kind:         domain.RecoveryHandleTrash,
		Root:         mustRoot(t, "user-cache"),
		Token:        "trash-token",
		OriginalPath: mustPath(t, "application", "cache"),
		RecordedAt:   time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
	}
}

func allFilesystemFields() domain.FilesystemFieldMask {
	return domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldUID |
		domain.FilesystemFieldGID |
		domain.FilesystemFieldMode |
		domain.FilesystemFieldLinkCount |
		domain.FilesystemFieldSize |
		domain.FilesystemFieldModifiedAt |
		domain.FilesystemFieldChangedAt |
		domain.FilesystemFieldMountID
}

func mustPath(t *testing.T, components ...string) pathbytes.BytePath {
	t.Helper()
	raw := make([][]byte, len(components))
	for index, component := range components {
		raw[index] = []byte(component)
	}
	path, err := pathbytes.New(raw)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func mustEvidenceDigest(t *testing.T, fill byte) domain.EvidenceDigest {
	t.Helper()
	digest, err := domain.NewEvidenceDigest(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
