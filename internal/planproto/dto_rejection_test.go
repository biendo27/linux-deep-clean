package planproto

import (
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestClosedUnionDTOConvertersRejectMismatchedArms(t *testing.T) {
	limits := DefaultDecodeLimits()
	evidenceCases := []struct {
		name string
		wire evidenceWire
	}{
		{"filesystem", evidenceWire{Kind: string(domain.EvidenceFilesystemIdentity), Capability: &capabilityEvidenceWire{}}},
		{"package transaction", evidenceWire{Kind: string(domain.EvidencePackageTransaction), Filesystem: &filesystemEvidenceWire{}}},
		{"manager object", evidenceWire{Kind: string(domain.EvidenceManagerObject), Filesystem: &filesystemEvidenceWire{}}},
		{"capability", evidenceWire{Kind: string(domain.EvidenceCapability), Filesystem: &filesystemEvidenceWire{}}},
		{"project marker", evidenceWire{Kind: string(domain.EvidenceProjectMarker), Filesystem: &filesystemEvidenceWire{}}},
		{"installer metadata", evidenceWire{Kind: string(domain.EvidenceInstallerMetadata), Filesystem: &filesystemEvidenceWire{}}},
		{"profile manager", evidenceWire{Kind: string(domain.EvidenceProfileManager), Filesystem: &filesystemEvidenceWire{}}},
		{"unknown", evidenceWire{Kind: "not-a-v1-evidence", Filesystem: &filesystemEvidenceWire{}}},
		{"multiple arms", evidenceWire{Kind: string(domain.EvidenceFilesystemIdentity), Filesystem: &filesystemEvidenceWire{}, Capability: &capabilityEvidenceWire{}}},
	}
	for _, test := range evidenceCases {
		t.Run("evidence "+test.name, func(t *testing.T) {
			if _, err := fromEvidenceWire(test.wire, limits); err == nil {
				t.Fatal("accepted a mismatched evidence union")
			}
		})
	}

	preconditionCases := []struct {
		name string
		wire preconditionWire
	}{
		{"filesystem", preconditionWire{Kind: string(domain.PreconditionFilesystemIdentity), Capability: &capabilityPreconditionWire{}}},
		{"package transaction", preconditionWire{Kind: string(domain.PreconditionPackageTransaction), Filesystem: &filesystemPreconditionWire{}}},
		{"manager object", preconditionWire{Kind: string(domain.PreconditionManagerObjectState), Filesystem: &filesystemPreconditionWire{}}},
		{"capability", preconditionWire{Kind: string(domain.PreconditionCapability), Filesystem: &filesystemPreconditionWire{}}},
		{"project marker", preconditionWire{Kind: string(domain.PreconditionProjectMarker), Filesystem: &filesystemPreconditionWire{}}},
		{"installer metadata", preconditionWire{Kind: string(domain.PreconditionInstallerMetadata), Filesystem: &filesystemPreconditionWire{}}},
		{"profile manager", preconditionWire{Kind: string(domain.PreconditionProfileManager), Filesystem: &filesystemPreconditionWire{}}},
		{"unknown", preconditionWire{Kind: "not-a-v1-precondition", Filesystem: &filesystemPreconditionWire{}}},
		{"multiple arms", preconditionWire{Kind: string(domain.PreconditionFilesystemIdentity), Filesystem: &filesystemPreconditionWire{}, Capability: &capabilityPreconditionWire{}}},
	}
	for _, test := range preconditionCases {
		t.Run("precondition "+test.name, func(t *testing.T) {
			if _, err := fromPreconditionWire(test.wire, limits); err == nil {
				t.Fatal("accepted a mismatched precondition union")
			}
		})
	}
}

func TestReferenceAndTargetDTOConvertersRejectAuthorityExpansion(t *testing.T) {
	limits := DefaultDecodeLimits()
	validFilesystem := toTargetWire(testPlan(t).CanonicalBody().Actions[0].Target)
	validManager := toTargetWire(testManagerTarget(t))
	if _, err := fromManagerObjectRefWire(managerObjectRefWire{ID: "bad id", Scope: string(domain.ManagerScopeUser)}); err == nil {
		t.Fatal("accepted an invalid scoped manager reference ID")
	}
	if _, err := fromManagerObjectRefWire(managerObjectRefWire{ID: "package", Scope: "not-a-scope"}); err == nil {
		t.Fatal("accepted an invalid scoped manager reference scope")
	}
	if _, err := fromProviderGuaranteeWire(providerGuaranteeWire{
		Kind:           string(domain.ProviderGuaranteeExactTargetReprobeRequired),
		CoveredObjects: []managerObjectRefWire{{ID: "package", Scope: "not-a-scope"}},
	}); err == nil {
		t.Fatal("accepted an invalid guarantee coverage reference")
	}
	if _, err := fromProviderGuaranteeWire(providerGuaranteeWire{Kind: "expanded-guarantee"}); err == nil {
		t.Fatal("accepted an unknown provider guarantee")
	}

	targetCases := []struct {
		name string
		wire targetWire
	}{
		{"unknown kind", targetWire{Kind: "outside-contract"}},
		{"filesystem missing arm", targetWire{Kind: string(domain.TargetFilesystem)}},
		{"filesystem dual arm", targetWire{Kind: string(domain.TargetFilesystem), Filesystem: validFilesystem.Filesystem, ManagerObject: validManager.ManagerObject}},
		{"filesystem root", targetWire{Kind: string(domain.TargetFilesystem), Filesystem: &filesystemTargetWire{Root: "-untrusted", Path: validFilesystem.Filesystem.Path}}},
		{"filesystem path", targetWire{Kind: string(domain.TargetFilesystem), Filesystem: &filesystemTargetWire{Root: validFilesystem.Filesystem.Root, Path: pathWire{}}}},
		{"manager missing arm", targetWire{Kind: string(domain.TargetManagerObject)}},
		{"manager dual arm", targetWire{Kind: string(domain.TargetManagerObject), Filesystem: validFilesystem.Filesystem, ManagerObject: validManager.ManagerObject}},
		{"manager provider", targetWire{Kind: string(domain.TargetManagerObject), ManagerObject: &managerObjectTargetWire{Provider: "-provider", Object: "package", Scope: string(domain.ManagerScopeUser)}}},
		{"manager object", targetWire{Kind: string(domain.TargetManagerObject), ManagerObject: &managerObjectTargetWire{Provider: "xdg-cache", Object: "bad object", Scope: string(domain.ManagerScopeUser)}}},
		{"manager scope", targetWire{Kind: string(domain.TargetManagerObject), ManagerObject: &managerObjectTargetWire{Provider: "xdg-cache", Object: "package", Scope: "not-a-scope"}}},
	}
	for _, test := range targetCases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fromTargetWire(test.wire, limits); err == nil {
				t.Fatal("accepted an invalid target union or authority value")
			}
		})
	}
}

func TestTransactionGraphDTOConverterRejectsMalformedStructure(t *testing.T) {
	graphForTest := func() transactionGraphWire {
		return toTransactionGraphWire(testGraph(t, domain.TransactionEdgeDependency))
	}
	cases := []struct {
		name   string
		mutate func(*transactionGraphWire)
	}{
		{"provider", func(wire *transactionGraphWire) { wire.Provider = "bad provider" }},
		{"evidence digest", func(wire *transactionGraphWire) { wire.ProviderEvidenceDigest = nil }},
		{"guarantee", func(wire *transactionGraphWire) { wire.Guarantee.Kind = "outside-contract" }},
		{"node ID", func(wire *transactionGraphWire) { wire.Nodes[0].ID = "bad object" }},
		{"edge source", func(wire *transactionGraphWire) { wire.Edges[0].From.Scope = "outside-contract" }},
		{"edge target", func(wire *transactionGraphWire) { wire.Edges[0].To.Scope = "outside-contract" }},
		{"edge reason", func(wire *transactionGraphWire) { wire.Edges[0].Reason = "outside-contract" }},
		{"dangling guarantee", func(wire *transactionGraphWire) {
			wire.Guarantee.CoveredObjects[0].ID = "outside-contract"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			wire := graphForTest()
			test.mutate(&wire)
			if _, err := fromTransactionGraphWire(wire); err == nil {
				t.Fatal("accepted a malformed transaction graph")
			}
		})
	}
}

func TestActionAndPlanDTOConvertersRejectInvalidValues(t *testing.T) {
	limits := DefaultDecodeLimits()
	actionForTest := func() actionWire {
		return toActionWire(testPlan(t).CanonicalBody().Actions[0])
	}
	actionCases := []struct {
		name   string
		mutate func(*actionWire)
	}{
		{"ID", func(wire *actionWire) { wire.ID = "bad ID" }},
		{"target", func(wire *actionWire) { wire.Target.Kind = "outside-contract" }},
		{"evidence", func(wire *actionWire) { wire.Evidence[0].Kind = "outside-contract" }},
		{"precondition", func(wire *actionWire) { wire.Precondition.Kind = "outside-contract" }},
		{"dependency", func(wire *actionWire) { wire.Dependencies = []string{"bad ID"} }},
		{"required capability", func(wire *actionWire) { wire.RequiredCapability = "bad ID" }},
		{"provider guarantee", func(wire *actionWire) { wire.ProviderGuarantee.Kind = "outside-contract" }},
		{"action kind", func(wire *actionWire) { wire.Kind = "outside-contract" }},
		{"risk", func(wire *actionWire) { wire.Risk = "outside-contract" }},
		{"reversibility", func(wire *actionWire) { wire.Reversibility = "outside-contract" }},
		{"postcondition", func(wire *actionWire) { wire.ExpectedPostcondition = "outside-contract" }},
		{"size facts", func(wire *actionWire) { wire.EstimatedEffect.Apparent = byteQuantityWire{Bytes: 1} }},
	}
	for _, test := range actionCases {
		t.Run("action "+test.name, func(t *testing.T) {
			wire := actionForTest()
			test.mutate(&wire)
			if _, err := fromActionWire(wire, limits); err == nil {
				t.Fatal("accepted an invalid action wire")
			}
		})
	}

	bodyForTest := func() planBodyWire {
		return toPlanBodyWire(testPlan(t).CanonicalBody())
	}
	bodyCases := []struct {
		name   string
		mutate func(*planBodyWire, *DecodeLimits)
	}{
		{"action limit", func(_ *planBodyWire, configured *DecodeLimits) { configured.MaxActions = 0 }},
		{"caller", func(wire *planBodyWire, _ *DecodeLimits) { wire.Caller.RunID = "bad ID" }},
		{"selected candidate", func(wire *planBodyWire, _ *DecodeLimits) { wire.Caller.SelectedCandidateIDs = []string{"bad ID"} }},
		{"capability", func(wire *planBodyWire, _ *DecodeLimits) { wire.Capabilities.Facts[0].ID = "bad ID" }},
		{"config digest", func(wire *planBodyWire, _ *DecodeLimits) { wire.ConfigDigest = nil }},
		{"action", func(wire *planBodyWire, _ *DecodeLimits) { wire.Actions[0].ID = "bad ID" }},
		{"command", func(wire *planBodyWire, _ *DecodeLimits) { wire.Command = "outside-contract" }},
		{"totals", func(wire *planBodyWire, _ *DecodeLimits) { wire.Totals.Apparent = byteQuantityWire{Bytes: 1} }},
	}
	for _, test := range bodyCases {
		t.Run("plan body "+test.name, func(t *testing.T) {
			wire := bodyForTest()
			configured := limits
			test.mutate(&wire, &configured)
			if _, err := fromPlanBodyWire(wire, configured); err == nil {
				t.Fatal("accepted an invalid plan body wire")
			}
		})
	}

	planForTest := func() planWire {
		return toPlanWire(testPlan(t))
	}
	for _, test := range []struct {
		name   string
		mutate func(*planWire)
	}{
		{"digest", func(wire *planWire) { wire.Digest = nil }},
		{"body", func(wire *planWire) { wire.Actions[0].ID = "bad ID" }},
	} {
		t.Run("plan "+test.name, func(t *testing.T) {
			wire := planForTest()
			test.mutate(&wire)
			if _, err := fromPlanWire(wire, limits); err == nil {
				t.Fatal("accepted an invalid plan wire")
			}
		})
	}
}

func TestResultDTOConvertersRejectInvalidValues(t *testing.T) {
	limits := DefaultDecodeLimits()
	plan := testPlan(t)
	resultForTest := func() resultWire {
		return toResultWire(testResult(t, plan))
	}
	actionForTest := func() actionResultWire {
		return resultForTest().Actions[0]
	}
	actionCases := []struct {
		name   string
		mutate func(*actionResultWire)
	}{
		{"action ID", func(wire *actionResultWire) { wire.ActionID = "bad ID" }},
		{"action kind", func(wire *actionResultWire) { wire.Kind = "outside-contract" }},
		{"outcome", func(wire *actionResultWire) { wire.Outcome = "outside-contract" }},
		{"reconciliation", func(wire *actionResultWire) { wire.Reconciliation = "outside-contract" }},
		{"recovery", func(wire *actionResultWire) { wire.Recovery = "outside-contract" }},
		{"probe", func(wire *actionResultWire) {
			wire.ReconciliationProbe = &reconciliationProbeWire{Kind: "outside-contract"}
		}},
		{"recovery handle", func(wire *actionResultWire) { wire.RecoveryHandle = &recoveryHandleWire{} }},
		{"terminal invariant", func(wire *actionResultWire) { wire.Attempted = false }},
	}
	for _, test := range actionCases {
		t.Run("action result "+test.name, func(t *testing.T) {
			wire := actionForTest()
			test.mutate(&wire)
			if _, err := fromActionResultWire(wire, limits); err == nil {
				t.Fatal("accepted an invalid action result wire")
			}
		})
	}

	target := toTargetWire(plan.CanonicalBody().Actions[0].Target)
	graph := toTransactionGraphWire(testGraph(t, domain.TransactionEdgeDependency))
	probeCases := []struct {
		name string
		wire func() reconciliationProbeWire
	}{
		{"target missing", func() reconciliationProbeWire {
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbeFilesystemTarget)}
		}},
		{"target malformed", func() reconciliationProbeWire {
			invalid := target
			invalid.Kind = "outside-contract"
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbeFilesystemTarget), Target: &invalid}
		}},
		{"package graph missing", func() reconciliationProbeWire {
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbePackageTransaction)}
		}},
		{"package graph malformed", func() reconciliationProbeWire {
			invalid := graph
			invalid.Provider = "bad provider"
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbePackageTransaction), Graph: &invalid}
		}},
		{"state record missing", func() reconciliationProbeWire {
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbeStateRecord)}
		}},
		{"state record run ID", func() reconciliationProbeWire {
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbeStateRecord), StateRecord: &stateRecordProbeWire{RunID: "bad ID", ActionID: "trash-cache"}}
		}},
		{"state record action ID", func() reconciliationProbeWire {
			return reconciliationProbeWire{Kind: string(domain.ReconciliationProbeStateRecord), StateRecord: &stateRecordProbeWire{RunID: "run-20260716-0001", ActionID: "bad ID"}}
		}},
		{"unknown", func() reconciliationProbeWire { return reconciliationProbeWire{Kind: "outside-contract"} }},
	}
	for _, test := range probeCases {
		t.Run("probe "+test.name, func(t *testing.T) {
			if _, err := fromReconciliationProbeWire(test.wire(), limits); err == nil {
				t.Fatal("accepted an invalid reconciliation probe wire")
			}
		})
	}

	handleForTest := func() recoveryHandleWire {
		return toRecoveryHandleWire(testRecoveryHandle(t))
	}
	for _, test := range []struct {
		name   string
		mutate func(*recoveryHandleWire)
	}{
		{"root", func(wire *recoveryHandleWire) { wire.Root = "bad root" }},
		{"path", func(wire *recoveryHandleWire) { wire.OriginalPath = pathWire{} }},
		{"kind", func(wire *recoveryHandleWire) { wire.Kind = "outside-contract" }},
		{"token", func(wire *recoveryHandleWire) { wire.Token = "" }},
	} {
		t.Run("recovery handle "+test.name, func(t *testing.T) {
			wire := handleForTest()
			test.mutate(&wire)
			if _, err := fromRecoveryHandleWire(wire, limits); err == nil {
				t.Fatal("accepted an invalid recovery handle wire")
			}
		})
	}

	resultCases := []struct {
		name   string
		mutate func(*resultWire, *DecodeLimits)
	}{
		{"action limit", func(_ *resultWire, configured *DecodeLimits) { configured.MaxActions = 0 }},
		{"plan digest", func(wire *resultWire, _ *DecodeLimits) { wire.PlanDigest = nil }},
		{"run ID", func(wire *resultWire, _ *DecodeLimits) { wire.RunID = "bad ID" }},
		{"action", func(wire *resultWire, _ *DecodeLimits) { wire.Actions[0].ActionID = "bad ID" }},
		{"schema version", func(wire *resultWire, _ *DecodeLimits) { wire.SchemaVersion++ }},
		{"summary", func(wire *resultWire, _ *DecodeLimits) { wire.Summary.Total++ }},
	}
	for _, test := range resultCases {
		t.Run("result "+test.name, func(t *testing.T) {
			wire := resultForTest()
			configured := limits
			test.mutate(&wire, &configured)
			if _, err := fromResultWire(wire, configured); err == nil {
				t.Fatal("accepted an invalid result wire")
			}
		})
	}
}
