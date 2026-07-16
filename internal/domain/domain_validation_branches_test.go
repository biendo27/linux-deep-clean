package domain

import (
	"math"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func validFilesystemCandidate(t *testing.T) Candidate {
	t.Helper()

	target := testFilesystemTarget(t)
	return Candidate{
		ID:                    testCandidateID(t, "filesystem-candidate"),
		Provider:              testProvider(t),
		Target:                target,
		Evidence:              []Evidence{testFilesystemEvidence(t, target)},
		Size:                  testSizeFacts(),
		Confidence:            ConfidenceHigh,
		DiscoveryPrecondition: testFilesystemPrecondition(t, target),
	}
}

func validManagerCandidate(t *testing.T) Candidate {
	t.Helper()

	target := testManagerTarget(t)
	return Candidate{
		ID:                    testCandidateID(t, "manager-candidate"),
		Provider:              target.ManagerObject.Provider,
		Target:                target,
		Evidence:              []Evidence{testManagerEvidence(t, target)},
		Size:                  testSizeFacts(),
		Confidence:            ConfidenceHigh,
		DiscoveryPrecondition: testManagerPrecondition(t, target),
	}
}

func testPackageTransactionAction(t *testing.T, id string) Action {
	t.Helper()

	target := testManagerTarget(t)
	graph := testTransactionGraph(t)
	action := Action{
		ID:                    testActionID(t, id),
		Kind:                  ActionRemoveNativePackage,
		Target:                target,
		Evidence:              []Evidence{{Kind: EvidencePackageTransaction, PackageTransaction: &PackageTransactionEvidence{Graph: graph}}},
		Precondition:          Precondition{Kind: PreconditionPackageTransaction, PackageTransaction: &PackageTransactionPrecondition{Graph: graph, ManagerStateDigest: testEvidenceDigest(t, 41)}},
		Risk:                  RiskHigh,
		Reversibility:         ReversibilityNoRollback,
		EstimatedEffect:       testSizeFacts(),
		RequiredCapability:    testCapabilityID(t, "native-package"),
		ProviderGuarantee:     graph.Guarantee,
		ExpectedPostcondition: PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("package transaction action validation error = %v", err)
	}
	return action
}

func testInstallerMetadataAction(t *testing.T, id string) Action {
	t.Helper()

	target := testFilesystemTarget(t)
	action := Action{
		ID:     testActionID(t, id),
		Kind:   ActionQuarantinePath,
		Target: target,
		Evidence: []Evidence{{
			Kind: EvidenceInstallerMetadata,
			InstallerMetadata: &InstallerMetadataEvidence{
				Target: target, Format: InstallerFormatArchive, Digest: testEvidenceDigest(t, 42),
			},
		}},
		Precondition: Precondition{
			Kind: PreconditionInstallerMetadata,
			InstallerMetadata: &InstallerMetadataPrecondition{
				Target: target, Format: InstallerFormatArchive, Digest: testEvidenceDigest(t, 42),
			},
		},
		Risk:                  RiskMedium,
		Reversibility:         ReversibilityRecoverable,
		EstimatedEffect:       testSizeFacts(),
		RequiredCapability:    testCapabilityID(t, "quarantine"),
		ProviderGuarantee:     testGuarantee(t),
		ExpectedPostcondition: PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("installer metadata action validation error = %v", err)
	}
	return action
}

func testRecoveryHandlePointer(t *testing.T) *RecoveryHandle {
	t.Helper()

	handle := testRecoveryHandle(t)
	return &handle
}

func TestCandidateRequiresProviderEvidenceAndPreconditionTargetBinding(t *testing.T) {
	validFilesystem := validFilesystemCandidate(t)
	if err := validFilesystem.Validate(); err != nil {
		t.Fatalf("valid filesystem candidate error = %v", err)
	}
	validManager := validManagerCandidate(t)
	if err := validManager.Validate(); err != nil {
		t.Fatalf("valid manager candidate error = %v", err)
	}

	otherProvider, err := NewProviderID("other-provider")
	if err != nil {
		t.Fatalf("NewProviderID() error = %v", err)
	}
	wrongFilesystem := testFilesystemTarget(t)
	wrongFilesystem.Filesystem.Root = testRoot(t)
	wrongFilesystem.Filesystem.Root = TrustedRootID("other-root")
	if err := wrongFilesystem.Validate(); err != nil {
		t.Fatalf("wrongFilesystem target error = %v", err)
	}
	wrongManager := testManagerTarget(t)
	wrongManager.ManagerObject.Object = testManagerObjectID(t, "other-package")
	if err := wrongManager.Validate(); err != nil {
		t.Fatalf("wrongManager target error = %v", err)
	}

	tests := []struct {
		name   string
		value  Candidate
		mutate func(*Candidate)
	}{
		{
			name:  "manager provider mismatch",
			value: validManager,
			mutate: func(candidate *Candidate) {
				candidate.Provider = otherProvider
			},
		},
		{
			name:  "no evidence",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.Evidence = nil
			},
		},
		{
			name:  "unbound evidence",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.Evidence = []Evidence{{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}}}
			},
		},
		{
			name:  "invalid evidence",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.Evidence[0].Filesystem.Snapshot.Type = FileTypeUnknown
			},
		},
		{
			name:  "invalid size",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.Size.Apparent = ByteQuantity{Bytes: 1}
			},
		},
		{
			name:  "invalid confidence",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.Confidence = Confidence("future")
			},
		},
		{
			name:  "invalid discovery precondition",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.DiscoveryPrecondition = Precondition{}
			},
		},
		{
			name:  "unbound filesystem discovery precondition",
			value: validFilesystem,
			mutate: func(candidate *Candidate) {
				candidate.DiscoveryPrecondition = testFilesystemPrecondition(t, wrongFilesystem)
			},
		},
		{
			name:  "unbound manager discovery precondition",
			value: validManager,
			mutate: func(candidate *Candidate) {
				candidate.DiscoveryPrecondition = testManagerPrecondition(t, wrongManager)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := test.value.Clone()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Candidate.Validate() accepted unsafe or unbound data")
			}
		})
	}
}

func TestActionValidationRejectsEveryUnboundOrUnknownField(t *testing.T) {
	valid := testAction(t, "branch-action")
	tests := []struct {
		name   string
		mutate func(*Action)
	}{
		{"invalid ID", func(action *Action) { action.ID = ActionID("bad id") }},
		{"unknown kind", func(action *Action) { action.Kind = ActionKind("future") }},
		{"invalid target", func(action *Action) { action.Target = Target{} }},
		{"no evidence", func(action *Action) { action.Evidence = nil }},
		{"invalid evidence", func(action *Action) { action.Evidence = []Evidence{{Kind: EvidenceFilesystemIdentity}} }},
		{"invalid precondition", func(action *Action) { action.Precondition = Precondition{} }},
		{"invalid dependency", func(action *Action) { action.Dependencies = []ActionID{ActionID("bad id")} }},
		{"invalid risk", func(action *Action) { action.Risk = Risk("future") }},
		{"invalid reversibility", func(action *Action) { action.Reversibility = Reversibility("future") }},
		{"invalid size", func(action *Action) { action.EstimatedEffect.Apparent = ByteQuantity{Bytes: 1} }},
		{"invalid capability", func(action *Action) { action.RequiredCapability = CapabilityID("bad id") }},
		{"invalid guarantee", func(action *Action) { action.ProviderGuarantee.Kind = ProviderGuaranteeKind("future") }},
		{"invalid postcondition", func(action *Action) { action.ExpectedPostcondition = Postcondition("future") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid.Clone()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Action.Validate() accepted invalid field")
			}
		})
	}
	if _, err := NewAction(Action{}); err == nil {
		t.Fatal("NewAction accepted zero action")
	}

	packageAction := testPackageTransactionAction(t, "package-action")
	if err := packageAction.Validate(); err != nil {
		t.Fatalf("package transaction action error = %v", err)
	}
	installerAction := testInstallerMetadataAction(t, "installer-action")
	if err := installerAction.Validate(); err != nil {
		t.Fatalf("installer metadata action error = %v", err)
	}

	wrongProvider, err := NewProviderID("other-provider")
	if err != nil {
		t.Fatalf("NewProviderID() error = %v", err)
	}
	wrongGraphEvidence := packageAction.Clone()
	wrongGraphEvidence.Evidence[0].PackageTransaction.Graph.Provider = wrongProvider
	if err := wrongGraphEvidence.Validate(); err == nil {
		t.Fatal("action accepted package evidence from a different provider")
	}
	wrongGraphPrecondition := packageAction.Clone()
	wrongGraphPrecondition.Precondition.PackageTransaction.Graph.Provider = wrongProvider
	wrongGraphPrecondition.Evidence = []Evidence{testManagerEvidence(t, wrongGraphPrecondition.Target)}
	if err := wrongGraphPrecondition.Validate(); err == nil {
		t.Fatal("action accepted package precondition from a different provider")
	}
	wrongInstallerPrecondition := installerAction.Clone()
	otherTarget := testFilesystemTarget(t)
	otherTarget.Filesystem.Root = TrustedRootID("other-root")
	wrongInstallerPrecondition.Precondition.InstallerMetadata.Target = otherTarget
	if err := wrongInstallerPrecondition.Validate(); err == nil {
		t.Fatal("action accepted installer precondition for a different target")
	}
}

func TestEvidenceSpecificValidatorsAndAllCloneVariants(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	graph := testTransactionGraph(t)
	valid := []Evidence{
		{Kind: EvidenceFilesystemIdentity, Filesystem: &FilesystemEvidence{Target: filesystem, Snapshot: testFilesystemSnapshot()}},
		{Kind: EvidencePackageTransaction, PackageTransaction: &PackageTransactionEvidence{Graph: graph}},
		{Kind: EvidenceManagerObject, ManagerObject: &ManagerObjectEvidence{Target: manager, Version: "1", Present: true}},
		{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}},
		{Kind: EvidenceProjectMarker, ProjectMarker: &ProjectMarkerEvidence{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: EvidenceInstallerMetadata, InstallerMetadata: &InstallerMetadataEvidence{Target: filesystem, Format: InstallerFormatDeb, Digest: testEvidenceDigest(t, 51)}},
		{Kind: EvidenceProfileManager, ProfileManager: &ProfileManagerEvidence{Provider: testProvider(t), Manager: ProfileManagerAuthselect, Version: "1"}},
	}
	for _, evidence := range valid {
		if err := evidence.Validate(); err != nil {
			t.Fatalf("valid Evidence(%q): %v", evidence.Kind, err)
		}
		clone := evidence.Clone()
		if err := clone.Validate(); err != nil {
			t.Fatalf("Evidence.Clone(%q) did not validate: %v", evidence.Kind, err)
		}
	}

	invalid := []Evidence{
		{Kind: EvidenceFilesystemIdentity, Capability: valid[3].Capability},
		{Kind: EvidencePackageTransaction, Capability: valid[3].Capability},
		{Kind: EvidenceManagerObject, Capability: valid[3].Capability},
		{Kind: EvidenceCapability, Filesystem: valid[0].Filesystem},
		{Kind: EvidenceProjectMarker, Capability: valid[3].Capability},
		{Kind: EvidenceInstallerMetadata, Capability: valid[3].Capability},
		{Kind: EvidenceProfileManager, Capability: valid[3].Capability},
		{Kind: EvidenceFilesystemIdentity, Filesystem: &FilesystemEvidence{Target: manager, Snapshot: testFilesystemSnapshot()}},
		{Kind: EvidencePackageTransaction, PackageTransaction: &PackageTransactionEvidence{}},
		{Kind: EvidenceManagerObject, ManagerObject: &ManagerObjectEvidence{Target: filesystem, Version: "1"}},
		{Kind: EvidenceManagerObject, ManagerObject: &ManagerObjectEvidence{Target: manager}},
		{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: CapabilityID("bad id"), State: CapabilitySupported, Version: "1"}},
		{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported}},
		{Kind: EvidenceProjectMarker, ProjectMarker: &ProjectMarkerEvidence{Root: TrustedRootID("bad root"), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: EvidenceProjectMarker, ProjectMarker: &ProjectMarkerEvidence{Root: testRoot(t), Ecosystem: "node"}},
		{Kind: EvidenceInstallerMetadata, InstallerMetadata: &InstallerMetadataEvidence{Target: manager, Format: InstallerFormatDeb, Digest: testEvidenceDigest(t, 51)}},
		{Kind: EvidenceInstallerMetadata, InstallerMetadata: &InstallerMetadataEvidence{Target: filesystem, Format: InstallerFormatDeb}},
		{Kind: EvidenceProfileManager, ProfileManager: &ProfileManagerEvidence{Provider: ProviderID("bad provider"), Manager: ProfileManagerAuthselect, Version: "1"}},
		{Kind: EvidenceProfileManager, ProfileManager: &ProfileManagerEvidence{Provider: testProvider(t), Manager: ProfileManagerAuthselect}},
	}
	for _, evidence := range invalid {
		if err := evidence.Validate(); err == nil {
			t.Errorf("Evidence(%q) accepted malformed variant", evidence.Kind)
		}
	}
}

func TestPreconditionSpecificValidatorsMasksAndAllCloneVariants(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	graph := testTransactionGraph(t)
	valid := []Precondition{
		{Kind: PreconditionFilesystemIdentity, Filesystem: &FilesystemPrecondition{Target: filesystem, Required: FilesystemFieldType, Snapshot: testFilesystemSnapshot()}},
		{Kind: PreconditionPackageTransaction, PackageTransaction: &PackageTransactionPrecondition{Graph: graph, ManagerStateDigest: testEvidenceDigest(t, 52)}},
		{Kind: PreconditionManagerObjectState, ManagerObject: &ManagerObjectPrecondition{Target: manager, ExpectedVersion: "1", ExpectedPresent: true}},
		{Kind: PreconditionCapability, Capability: &CapabilityPrecondition{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}},
		{Kind: PreconditionProjectMarker, ProjectMarker: &ProjectMarkerPrecondition{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: PreconditionInstallerMetadata, InstallerMetadata: &InstallerMetadataPrecondition{Target: filesystem, Format: InstallerFormatDeb, Digest: testEvidenceDigest(t, 52)}},
		{Kind: PreconditionProfileManager, ProfileManager: &ProfileManagerPrecondition{Provider: testProvider(t), Manager: ProfileManagerAuthselect, Version: "1"}},
	}
	for _, precondition := range valid {
		if err := precondition.Validate(); err != nil {
			t.Fatalf("valid Precondition(%q): %v", precondition.Kind, err)
		}
		clone := precondition.Clone()
		if err := clone.Validate(); err != nil {
			t.Fatalf("Precondition.Clone(%q) did not validate: %v", precondition.Kind, err)
		}
	}

	invalid := []Precondition{
		{Kind: PreconditionFilesystemIdentity, Capability: valid[3].Capability},
		{Kind: PreconditionPackageTransaction, Capability: valid[3].Capability},
		{Kind: PreconditionManagerObjectState, Capability: valid[3].Capability},
		{Kind: PreconditionCapability, Filesystem: valid[0].Filesystem},
		{Kind: PreconditionProjectMarker, Capability: valid[3].Capability},
		{Kind: PreconditionInstallerMetadata, Capability: valid[3].Capability},
		{Kind: PreconditionProfileManager, Capability: valid[3].Capability},
		{Kind: PreconditionFilesystemIdentity, Filesystem: &FilesystemPrecondition{Target: manager, Required: FilesystemFieldType, Snapshot: testFilesystemSnapshot()}},
		{Kind: PreconditionPackageTransaction, PackageTransaction: &PackageTransactionPrecondition{Graph: graph}},
		{Kind: PreconditionManagerObjectState, ManagerObject: &ManagerObjectPrecondition{Target: filesystem, ExpectedVersion: "1"}},
		{Kind: PreconditionManagerObjectState, ManagerObject: &ManagerObjectPrecondition{Target: manager}},
		{Kind: PreconditionCapability, Capability: &CapabilityPrecondition{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported}},
		{Kind: PreconditionProjectMarker, ProjectMarker: &ProjectMarkerPrecondition{Root: TrustedRootID("bad root"), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: PreconditionProjectMarker, ProjectMarker: &ProjectMarkerPrecondition{Root: testRoot(t), Ecosystem: "node"}},
		{Kind: PreconditionInstallerMetadata, InstallerMetadata: &InstallerMetadataPrecondition{Target: manager, Format: InstallerFormatDeb, Digest: testEvidenceDigest(t, 52)}},
		{Kind: PreconditionInstallerMetadata, InstallerMetadata: &InstallerMetadataPrecondition{Target: filesystem, Format: InstallerFormatDeb}},
		{Kind: PreconditionProfileManager, ProfileManager: &ProfileManagerPrecondition{Provider: ProviderID("bad provider"), Manager: ProfileManagerAuthselect, Version: "1"}},
		{Kind: PreconditionProfileManager, ProfileManager: &ProfileManagerPrecondition{Provider: testProvider(t), Manager: ProfileManagerAuthselect}},
	}
	for _, precondition := range invalid {
		if err := precondition.Validate(); err == nil {
			t.Errorf("Precondition(%q) accepted malformed variant", precondition.Kind)
		}
	}

	for _, test := range []struct {
		name   string
		mutate func(*FilesystemSnapshot)
	}{
		{"device", func(snapshot *FilesystemSnapshot) { snapshot.DeviceMajor.Known = false }},
		{"inode", func(snapshot *FilesystemSnapshot) { snapshot.Inode.Known = false }},
		{"type", func(snapshot *FilesystemSnapshot) { snapshot.Type = FileTypeUnknown }},
		{"uid", func(snapshot *FilesystemSnapshot) { snapshot.UID.Known = false }},
		{"gid", func(snapshot *FilesystemSnapshot) { snapshot.GID.Known = false }},
		{"mode", func(snapshot *FilesystemSnapshot) { snapshot.Mode.Known = false }},
		{"link count", func(snapshot *FilesystemSnapshot) { snapshot.LinkCount.Known = false }},
		{"size", func(snapshot *FilesystemSnapshot) { snapshot.Size.Known = false }},
		{"modified", func(snapshot *FilesystemSnapshot) { snapshot.ModifiedAt.Known = false }},
		{"changed", func(snapshot *FilesystemSnapshot) { snapshot.ChangedAt.Known = false }},
		{"mount ID", func(snapshot *FilesystemSnapshot) { snapshot.MountID.Known = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testFilesystemSnapshot()
			test.mutate(&snapshot)
			if err := snapshot.ValidateFor(knownFilesystemFieldMask); err == nil {
				t.Fatal("missing required observed filesystem fact was accepted")
			}
		})
	}
}

func TestTransactionGraphValidationCoversOrderingAndAllGuaranteeKinds(t *testing.T) {
	graph := testTransactionGraph(t)
	first, second := graph.Nodes[0].reference(), graph.Nodes[1].reference()
	for _, value := range []TransactionGraph{
		{},
		func() TransactionGraph {
			value := graph.Clone()
			value.Provider = ProviderID("bad provider")
			return value
		}(),
		func() TransactionGraph { value := graph.Clone(); value.Nodes = nil; return value }(),
		func() TransactionGraph {
			value := graph.Clone()
			value.ProviderEvidenceDigest = EvidenceDigest{}
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Nodes[0], value.Nodes[1] = value.Nodes[1], value.Nodes[0]
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Edges = []TransactionEdge{{From: ManagerObjectRef{ID: ManagerObjectID("bad id"), Scope: ManagerScopeSystem}, To: second, Reason: TransactionEdgeDependency}}
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Edges = []TransactionEdge{{From: first, To: ManagerObjectRef{ID: ManagerObjectID("bad id"), Scope: ManagerScopeSystem}, Reason: TransactionEdgeDependency}}
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Edges = []TransactionEdge{{From: first, To: testManagerObjectRef(t, "missing", ManagerScopeSystem), Reason: TransactionEdgeDependency}}
			return value
		}(),
	} {
		if err := value.Validate(); err == nil {
			t.Error("TransactionGraph.Validate() accepted malformed graph")
		}
	}

	for _, guarantee := range []ProviderGuarantee{
		{Kind: ProviderGuaranteeKind("future")},
		{Kind: ProviderGuaranteeBoundedEffectOnly},
		{Kind: ProviderGuaranteeExactTargetReprobeRequired},
		{Kind: ProviderGuaranteeExactGraphReprobeRequired},
		{Kind: ProviderGuaranteeReadOnlyInventory, CoveredObjects: []ManagerObjectRef{{ID: ManagerObjectID("bad id"), Scope: ManagerScopeSystem}}},
		{Kind: ProviderGuaranteeExactTargetReprobeRequired, CoveredObjects: []ManagerObjectRef{second, first}},
	} {
		if err := guarantee.Validate(); err == nil {
			t.Errorf("ProviderGuarantee(%q) accepted malformed coverage", guarantee.Kind)
		}
	}

	unsorted, err := NewTransactionGraph(TransactionGraph{
		Provider: testProvider(t),
		Nodes: []TransactionNode{
			{ID: second.ID, Version: "2", Scope: second.Scope},
			{ID: first.ID, Version: "1", Scope: first.Scope},
		},
		Edges: []TransactionEdge{
			{From: second, To: first, Reason: TransactionEdgeReplacement},
			{From: first, To: second, Reason: TransactionEdgeRemoval},
			{From: first, To: second, Reason: TransactionEdgeConflict},
			{From: first, To: second, Reason: TransactionEdgeDependency},
		},
		ProviderEvidenceDigest: testEvidenceDigest(t, 53),
		Guarantee:              ProviderGuarantee{Kind: ProviderGuaranteeExactGraphReprobeRequired, CoveredObjects: []ManagerObjectRef{second, first}},
	})
	if err != nil {
		t.Fatalf("NewTransactionGraph(unsorted) error = %v", err)
	}
	if err := unsorted.Validate(); err != nil {
		t.Fatalf("canonical graph validation error = %v", err)
	}
	if unsorted.Edges[0].Reason != TransactionEdgeConflict || unsorted.Edges[len(unsorted.Edges)-1].From != second {
		t.Fatalf("NewTransactionGraph did not canonicalize edges: %+v", unsorted.Edges)
	}
}

func TestPlanCopiesInputsAndRejectsNoncanonicalStoredValues(t *testing.T) {
	first := testAction(t, "copy-first")
	second := testAction(t, "copy-second", first.ID)
	body := testPlanBody(t, []Action{first, second})
	body.Caller.SelectedCandidateIDs = []CandidateID{testCandidateID(t, "z"), testCandidateID(t, "a")}
	body.Capabilities.Facts = []CapabilityFact{
		{ID: testCapabilityID(t, "z"), State: CapabilitySupported, Version: "1"},
		{ID: testCapabilityID(t, "a"), State: CapabilitySupported, Version: "1"},
	}
	originalDependency := body.Actions[1].Dependencies[0]
	originalSelected := append([]CandidateID(nil), body.Caller.SelectedCandidateIDs...)
	originalCapability := append([]CapabilityFact(nil), body.Capabilities.Facts...)
	plan, err := NewPlan(body, testDigest(t, 61))
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if body.Actions[1].Dependencies[0] != originalDependency {
		t.Fatal("NewPlan mutated caller action dependencies")
	}
	if body.Caller.SelectedCandidateIDs[0] != originalSelected[0] || body.Capabilities.Facts[0] != originalCapability[0] {
		t.Fatal("NewPlan mutated caller-declared ordering")
	}
	body.Actions[1].Dependencies[0] = testActionID(t, "changed")
	body.Caller.SelectedCandidateIDs[0] = testCandidateID(t, "changed")
	body.Capabilities.Facts[0].Version = "changed"
	canonical := plan.CanonicalBody()
	if canonical.Actions[1].Dependencies[0] != originalDependency || canonical.Caller.SelectedCandidateIDs[0] != testCandidateID(t, "a") || canonical.Capabilities.Facts[0].Version == "changed" {
		t.Fatal("NewPlan retained mutable caller data")
	}

	invalidStored := []Plan{
		{body: plan.CanonicalBody(), digest: PlanDigest{}},
		{body: func() PlanBody { value := plan.CanonicalBody(); value.SchemaVersion = 2; return value }(), digest: testDigest(t, 61)},
	}
	for _, value := range invalidStored {
		if err := value.Validate(); err == nil {
			t.Fatal("Plan.Validate() accepted invalid stored state")
		}
	}
	if clone := (PlanBody{}).Clone(); clone.Actions != nil {
		t.Fatal("PlanBody.Clone() changed a nil action collection")
	}
}

func TestCreationContextAllowsConstrainedBuildMetadataAndRejectsUnsafeText(t *testing.T) {
	body := testPlanBody(t, []Action{testAction(t, "versioned-action")})
	body.Creation.ToolVersion = "1.2.3-rc.1+build.42"
	if _, err := NewPlan(body, testDigest(t, 63)); err != nil {
		t.Fatalf("NewPlan() rejected constrained semantic version metadata: %v", err)
	}

	for _, value := range []string{"bad version", "bad\x00version"} {
		context := body.Creation
		context.ToolVersion = value
		if err := context.Validate(); err == nil {
			t.Errorf("CreationContext.Validate() accepted unsafe tool version %q", value)
		}
	}
	context := body.Creation
	context.HostProfile = "bad profile"
	if err := context.Validate(); err == nil {
		t.Fatal("CreationContext.Validate() accepted unsafe host profile")
	}
}

func TestSizeAdditionReportsEachOverflowAndInvalidEffect(t *testing.T) {
	if err := (SizeEffect{Apparent: ByteQuantity{Bytes: 1}}).Validate(); err == nil {
		t.Fatal("SizeEffect accepted unavailable nonzero bytes")
	}
	fields := []struct {
		name string
		set  func(*SizeFacts, uint64)
	}{
		{"apparent size", func(facts *SizeFacts, value uint64) { facts.Apparent = ByteQuantity{Available: true, Bytes: value} }},
		{"allocated size", func(facts *SizeFacts, value uint64) { facts.Allocated = ByteQuantity{Available: true, Bytes: value} }},
		{"estimated apparent", func(facts *SizeFacts, value uint64) {
			facts.Estimated.Apparent = ByteQuantity{Available: true, Bytes: value}
		}},
		{"verified apparent", func(facts *SizeFacts, value uint64) {
			facts.Verified.Apparent = ByteQuantity{Available: true, Bytes: value}
		}},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			left, right := testSizeFacts(), testSizeFacts()
			field.set(&left, math.MaxUint64)
			field.set(&right, 1)
			if _, err := left.Add(right); err == nil {
				t.Fatal("SizeFacts.Add() accepted overflow")
			}
		})
	}
}

func TestResultRejectsAllInvalidStateMachineEdgesAndRecordIdentity(t *testing.T) {
	validResult := ActionResult{
		ActionID:       testActionID(t, "valid-result"),
		Kind:           ActionTrashPath,
		Outcome:        OutcomeSuccess,
		Attempted:      true,
		Reconciliation: ReconciliationNotRequired,
		Recovery:       RecoveryNotApplicable,
	}
	invalid := []ActionResult{
		func() ActionResult { value := validResult; value.ActionID = ActionID("bad id"); return value }(),
		func() ActionResult { value := validResult; value.Kind = ActionKind("future"); return value }(),
		func() ActionResult { value := validResult; value.Outcome = Outcome("future"); return value }(),
		func() ActionResult {
			value := validResult
			value.Reconciliation = ReconciliationState("future")
			return value
		}(),
		func() ActionResult {
			value := validResult
			value.Recovery = RecoveryDisposition("future")
			return value
		}(),
		func() ActionResult {
			value := validResult
			value.ReconciliationProbe = &ReconciliationProbe{Kind: ReconciliationProbeKind("future")}
			return value
		}(),
		func() ActionResult { value := validResult; value.RecoveryHandle = &RecoveryHandle{}; return value }(),
		func() ActionResult {
			value := validResult
			value.VerifiedEffect.Apparent = ByteQuantity{Bytes: 1}
			return value
		}(),
		func() ActionResult { value := validResult; value.Reconciliation = ReconciliationRequired; return value }(),
		func() ActionResult {
			value := validResult
			value.ReconciliationProbe = &ReconciliationProbe{Kind: ReconciliationProbeFilesystemTarget, Target: testFilesystemTarget(t)}
			return value
		}(),
		func() ActionResult {
			value := validResult
			value.Outcome = OutcomeFailed
			value.Attempted = false
			value.Recovery = RecoveryNotApplicable
			return value
		}(),
		func() ActionResult {
			value := validResult
			value.Outcome = OutcomeIndeterminate
			value.Reconciliation = ReconciliationRequired
			value.Recovery = RecoveryNotApplicable
			return value
		}(),
		func() ActionResult {
			value := validResult
			value.Outcome = OutcomeDenied
			value.Attempted = false
			value.Recovery = RecoveryNotApplicable
			value.RecoveryHandle = testRecoveryHandlePointer(t)
			return value
		}(),
	}
	for _, action := range invalid {
		if err := action.Validate(); err == nil {
			t.Error("ActionResult.Validate() accepted invalid state-machine edge")
		}
	}

	handle := testRecoveryHandle(t)
	for _, value := range []RecoveryHandle{
		func() RecoveryHandle { copy := handle; copy.Kind = RecoveryHandleKind("future"); return copy }(),
		func() RecoveryHandle { copy := handle; copy.Root = TrustedRootID("bad root"); return copy }(),
		func() RecoveryHandle { copy := handle; copy.Token = "bad token"; return copy }(),
		func() RecoveryHandle {
			copy := handle
			copy.OriginalPath = cloneBytePath(testMarkerPath(t))
			copy.OriginalPath = cloneBytePath(pathbytesZero())
			return copy
		}(),
		func() RecoveryHandle { copy := handle; copy.RecordedAt = time.Time{}; return copy }(),
		func() RecoveryHandle { copy := handle; copy.RecordedAt = time.Now(); return copy }(),
	} {
		if err := value.Validate(); err == nil {
			t.Error("RecoveryHandle.Validate() accepted malformed typed authority")
		}
	}

	validRestored := validResult
	validRestored.Recovery = RecoveryRestored
	if err := validRestored.Validate(); err != nil {
		t.Fatalf("restored recovery action should validate: %v", err)
	}
	validRetained := validResult
	validRetained.Recovery = RecoveryRetained
	validRetained.RecoveryHandle = &handle
	if err := validRetained.Validate(); err != nil {
		t.Fatalf("retained recovery action should validate: %v", err)
	}

	valid, err := NewResult(Result{SchemaVersion: SchemaVersionV1, PlanDigest: testDigest(t, 62), RunID: testRunID(t), Actions: []ActionResult{validResult}})
	if err != nil {
		t.Fatalf("NewResult() error = %v", err)
	}
	badResults := []Result{
		func() Result { value := valid.Clone(); value.SchemaVersion = 2; return value }(),
		func() Result { value := valid.Clone(); value.PlanDigest = PlanDigest{}; return value }(),
		func() Result { value := valid.Clone(); value.RunID = RunID("bad run"); return value }(),
		func() Result {
			value := valid.Clone()
			value.Actions = append(value.Actions, value.Actions[0])
			return value
		}(),
		func() Result { value := valid.Clone(); value.Summary.Total++; return value }(),
	}
	for _, result := range badResults {
		if err := result.Validate(); err == nil {
			t.Error("Result.Validate() accepted invalid record identity or summary")
		}
	}
}

func pathbytesZero() pathbytes.BytePath { return pathbytes.BytePath{} }
