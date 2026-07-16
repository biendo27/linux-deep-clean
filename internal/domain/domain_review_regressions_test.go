package domain

import "testing"

func TestAggregateSizeFactsCannotBypassAllocatedEffectSafety(t *testing.T) {
	facts := testSizeFacts()
	facts.Aggregate = true
	facts.LinkCount = Uint64Fact{}

	if err := facts.Validate(); err == nil {
		t.Fatal("SizeFacts.Validate() accepted aggregate allocated effects without per-entry proof")
	}
}

func TestUnknownFilesystemFactsCannotCarryHiddenValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FilesystemSnapshot)
	}{
		{"device major", func(snapshot *FilesystemSnapshot) { snapshot.DeviceMajor = Uint32Fact{Value: 1} }},
		{"device minor", func(snapshot *FilesystemSnapshot) { snapshot.DeviceMinor = Uint32Fact{Value: 1} }},
		{"inode", func(snapshot *FilesystemSnapshot) { snapshot.Inode = Uint64Fact{Value: 1} }},
		{"uid", func(snapshot *FilesystemSnapshot) { snapshot.UID = Uint32Fact{Value: 1} }},
		{"gid", func(snapshot *FilesystemSnapshot) { snapshot.GID = Uint32Fact{Value: 1} }},
		{"mode", func(snapshot *FilesystemSnapshot) { snapshot.Mode = Uint32Fact{Value: 1} }},
		{"link count", func(snapshot *FilesystemSnapshot) { snapshot.LinkCount = Uint64Fact{Value: 1} }},
		{"size", func(snapshot *FilesystemSnapshot) { snapshot.Size = Uint64Fact{Value: 1} }},
		{"modified time", func(snapshot *FilesystemSnapshot) { snapshot.ModifiedAt = Int64Fact{Value: 1} }},
		{"changed time", func(snapshot *FilesystemSnapshot) { snapshot.ChangedAt = Int64Fact{Value: 1} }},
		{"mount ID", func(snapshot *FilesystemSnapshot) { snapshot.MountID = Uint64Fact{Value: 1} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testFilesystemSnapshot()
			test.mutate(&snapshot)
			if err := snapshot.ValidateFor(FilesystemFieldType); err == nil {
				t.Fatal("FilesystemSnapshot.ValidateFor() accepted an unknown fact with a hidden nonzero value")
			}
		})
	}
}

func TestActionGuaranteeMustBindActionEvidence(t *testing.T) {
	action := testManagerAction(t, "unbound-guarantee")
	action.ProviderGuarantee = ProviderGuarantee{
		Kind:           ProviderGuaranteeExactTargetReprobeRequired,
		CoveredObjects: []ManagerObjectRef{testManagerObjectRef(t, "unrelated-package", ManagerScopeSystem)},
	}

	if err := action.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted a provider guarantee unrelated to its target and evidence")
	}

	action = testManagerAction(t, "overbroad-guarantee")
	target := action.Target.ManagerObject
	action.ProviderGuarantee = ProviderGuarantee{
		Kind: ProviderGuaranteeBoundedEffectOnly,
		CoveredObjects: []ManagerObjectRef{
			{ID: target.Object, Scope: target.Scope},
			testManagerObjectRef(t, "unrelated-package", ManagerScopeSystem),
		},
	}
	if err := action.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted a bounded guarantee broader than its manager-object evidence")
	}
}

func TestActionGuaranteeCannotExceedTransactionEvidence(t *testing.T) {
	action := testPackageTransactionAction(t, "weaker-transaction-evidence")
	target := action.Target.ManagerObject
	action.Evidence[0].PackageTransaction.Graph.Guarantee = ProviderGuarantee{
		Kind: ProviderGuaranteeReadOnlyInventory,
	}
	action.Precondition.PackageTransaction.Graph.Guarantee = ProviderGuarantee{
		Kind: ProviderGuaranteeReadOnlyInventory,
	}
	action.ProviderGuarantee = ProviderGuarantee{
		Kind:           ProviderGuaranteeBoundedEffectOnly,
		CoveredObjects: []ManagerObjectRef{{ID: target.Object, Scope: target.Scope}},
	}

	if err := action.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted a bounded action guarantee stronger than read-only transaction evidence")
	}
}

func TestTransactionGuaranteeStrengthAndCoverageBindEveryActionClaim(t *testing.T) {
	base := testPackageTransactionAction(t, "transaction-guarantee-strength")
	target := base.Target.ManagerObject
	targetRef := ManagerObjectRef{ID: target.Object, Scope: target.Scope}

	exactTarget := base.Clone()
	exactTarget.ProviderGuarantee = ProviderGuarantee{
		Kind:           ProviderGuaranteeExactTargetReprobeRequired,
		CoveredObjects: []ManagerObjectRef{targetRef},
	}
	exactTargetEvidence := ProviderGuarantee{
		Kind:           ProviderGuaranteeExactTargetReprobeRequired,
		CoveredObjects: []ManagerObjectRef{targetRef},
	}
	exactTarget.Evidence[0].PackageTransaction.Graph.Guarantee = exactTargetEvidence
	exactTarget.Precondition.PackageTransaction.Graph.Guarantee = exactTargetEvidence
	if err := exactTarget.Validate(); err != nil {
		t.Fatalf("Action.Validate() rejected exact-target guarantee supported by matching transaction evidence: %v", err)
	}

	weakerEvidence := exactTarget.Clone()
	weakerGuarantee := ProviderGuarantee{
		Kind:           ProviderGuaranteeBoundedEffectOnly,
		CoveredObjects: []ManagerObjectRef{targetRef},
	}
	weakerEvidence.Evidence[0].PackageTransaction.Graph.Guarantee = weakerGuarantee
	weakerEvidence.Precondition.PackageTransaction.Graph.Guarantee = weakerGuarantee
	if err := weakerEvidence.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted an exact-target action guarantee supported only by bounded transaction evidence")
	}

	expandedCoverage := base.Clone()
	expandedCoverage.ProviderGuarantee = ProviderGuarantee{
		Kind: ProviderGuaranteeBoundedEffectOnly,
		CoveredObjects: []ManagerObjectRef{
			targetRef,
			testManagerObjectRef(t, "secondary-package", ManagerScopeSystem),
		},
	}
	expandedEvidence := ProviderGuarantee{
		Kind:           ProviderGuaranteeBoundedEffectOnly,
		CoveredObjects: []ManagerObjectRef{targetRef},
	}
	expandedCoverage.Evidence[0].PackageTransaction.Graph.Guarantee = expandedEvidence
	expandedCoverage.Precondition.PackageTransaction.Graph.Guarantee = expandedEvidence
	if err := expandedCoverage.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted bounded coverage broader than its transaction evidence guarantee")
	}
}

func TestActionRequiresTargetBoundPrecondition(t *testing.T) {
	action := testAction(t, "generic-precondition")
	action.Precondition = Precondition{
		Kind: PreconditionCapability,
		Capability: &CapabilityPrecondition{
			Capability: testCapabilityID(t, "trash"),
			State:      CapabilitySupported,
			Version:    "1",
		},
	}

	if err := action.Validate(); err == nil {
		t.Fatal("Action.Validate() accepted a generic precondition that does not revalidate its target")
	}

	candidate := validFilesystemCandidate(t)
	candidate.DiscoveryPrecondition = action.Precondition
	if err := candidate.Validate(); err == nil {
		t.Fatal("Candidate.Validate() accepted a generic discovery precondition that does not bind its target")
	}
}

func TestTransactionGraphAllowsSameObjectIDInDistinctScopes(t *testing.T) {
	object := testManagerObjectID(t, "shared-package")
	system := ManagerObjectRef{ID: object, Scope: ManagerScopeSystem}
	user := ManagerObjectRef{ID: object, Scope: ManagerScopeUser}
	graph, err := NewTransactionGraph(TransactionGraph{
		Provider: testProvider(t),
		Nodes: []TransactionNode{
			{ID: object, Version: "1.0.0", Scope: ManagerScopeSystem},
			{ID: object, Version: "2.0.0", Scope: ManagerScopeUser},
		},
		Edges:                  []TransactionEdge{{From: user, To: system, Reason: TransactionEdgeReplacement}},
		ProviderEvidenceDigest: testEvidenceDigest(t, 44),
		Guarantee: ProviderGuarantee{
			Kind:           ProviderGuaranteeExactGraphReprobeRequired,
			CoveredObjects: []ManagerObjectRef{system, user},
		},
	})
	if err != nil {
		t.Fatalf("NewTransactionGraph() rejected distinct scoped objects: %v", err)
	}
	if len(graph.Nodes) != 2 || graph.Nodes[0].Scope == graph.Nodes[1].Scope || graph.Edges[0].From != user || graph.Guarantee.CoveredObjects[0] != system || graph.Guarantee.CoveredObjects[1] != user {
		t.Fatalf("TransactionGraph lost a scoped object: %#v", graph.Nodes)
	}
}

func TestStateRecordProbeMustBindResultRunAndAction(t *testing.T) {
	otherRun, err := NewRunID("run-20260715-0002")
	if err != nil {
		t.Fatal(err)
	}
	result := Result{
		SchemaVersion: SchemaVersionV1,
		PlanDigest:    testDigest(t, 41),
		RunID:         testRunID(t),
		Actions: []ActionResult{{
			ActionID:       testActionID(t, "reconcile-this"),
			Kind:           ActionTrashPath,
			Outcome:        OutcomeIndeterminate,
			Attempted:      true,
			Reconciliation: ReconciliationRequired,
			ReconciliationProbe: &ReconciliationProbe{
				Kind: ReconciliationProbeStateRecord,
				StateRecord: &StateRecordProbe{
					RunID:    otherRun,
					ActionID: testActionID(t, "another-action"),
				},
			},
		}},
	}

	if _, err := NewResult(result); err == nil {
		t.Fatal("NewResult() accepted a state-record probe for another run/action")
	}
}
