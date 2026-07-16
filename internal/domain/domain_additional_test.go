package domain

import (
	"bytes"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func testMarkerPath(t *testing.T) pathbytes.BytePath {
	t.Helper()

	path, err := pathbytes.New([][]byte{[]byte("project"), []byte("marker")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	return path
}

func testManagerTarget(t *testing.T) Target {
	t.Helper()

	target, err := NewManagerObjectTarget(
		testProvider(t),
		testManagerObjectID(t, "example-package"),
		ManagerScopeSystem,
	)
	if err != nil {
		t.Fatalf("NewManagerObjectTarget() error = %v", err)
	}
	return target
}

func testTransactionGraph(t *testing.T) TransactionGraph {
	t.Helper()

	first := testManagerObjectID(t, "example-package")
	second := testManagerObjectID(t, "secondary-package")
	graph, err := NewTransactionGraph(TransactionGraph{
		Provider: testProvider(t),
		Nodes: []TransactionNode{
			{ID: first, Version: "1.0.0", Scope: ManagerScopeSystem},
			{ID: second, Version: "2.0.0", Scope: ManagerScopeSystem},
		},
		Edges: []TransactionEdge{{
			From:   ManagerObjectRef{ID: first, Scope: ManagerScopeSystem},
			To:     ManagerObjectRef{ID: second, Scope: ManagerScopeSystem},
			Reason: TransactionEdgeDependency,
		}},
		ProviderEvidenceDigest: testEvidenceDigest(t, 22),
		Guarantee: ProviderGuarantee{
			Kind:           ProviderGuaranteeExactGraphReprobeRequired,
			CoveredObjects: []ManagerObjectRef{{ID: first, Scope: ManagerScopeSystem}, {ID: second, Scope: ManagerScopeSystem}},
		},
	})
	if err != nil {
		t.Fatalf("NewTransactionGraph() error = %v", err)
	}
	return graph
}

func testManagerEvidence(t *testing.T, target Target) Evidence {
	t.Helper()

	evidence := Evidence{
		Kind: EvidenceManagerObject,
		ManagerObject: &ManagerObjectEvidence{
			Target: target, Version: "1.0.0", Present: true,
		},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("manager evidence validation error = %v", err)
	}
	return evidence
}

func testManagerPrecondition(t *testing.T, target Target) Precondition {
	t.Helper()

	precondition := Precondition{
		Kind: PreconditionManagerObjectState,
		ManagerObject: &ManagerObjectPrecondition{
			Target: target, ExpectedVersion: "1.0.0", ExpectedPresent: true,
		},
	}
	if err := precondition.Validate(); err != nil {
		t.Fatalf("manager precondition validation error = %v", err)
	}
	return precondition
}

func testManagerAction(t *testing.T, id string) Action {
	t.Helper()

	target := testManagerTarget(t)
	graph := testTransactionGraph(t)
	action := Action{
		ID:     testActionID(t, id),
		Kind:   ActionRemoveNativePackage,
		Target: target,
		Evidence: []Evidence{
			testManagerEvidence(t, target),
			{Kind: EvidencePackageTransaction, PackageTransaction: &PackageTransactionEvidence{Graph: graph}},
		},
		Precondition:       testManagerPrecondition(t, target),
		Risk:               RiskHigh,
		Reversibility:      ReversibilityNoRollback,
		EstimatedEffect:    testSizeFacts(),
		RequiredCapability: testCapabilityID(t, "native-package"),
		ProviderGuarantee: ProviderGuarantee{
			Kind:           ProviderGuaranteeExactTargetReprobeRequired,
			CoveredObjects: []ManagerObjectRef{{ID: target.ManagerObject.Object, Scope: target.ManagerObject.Scope}},
		},
		ExpectedPostcondition: PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("manager action validation error = %v", err)
	}
	return action
}

func testRecoveryHandle(t *testing.T) RecoveryHandle {
	t.Helper()

	return RecoveryHandle{
		Kind:         RecoveryHandleQuarantine,
		Root:         testRoot(t),
		Token:        "recover-001",
		OriginalPath: testMarkerPath(t),
		RecordedAt:   time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestClosedIdentifierAndCommandVocabulary(t *testing.T) {
	commands := []Command{
		CommandMenu, CommandClean, CommandUninstall, CommandOptimize, CommandAnalyze,
		CommandStatus, CommandHistory, CommandPurge, CommandInstaller, CommandFingerprint,
		CommandCompletion, CommandUpdate, CommandRemove,
	}
	for _, command := range commands {
		if err := command.Validate(); err != nil {
			t.Errorf("Command(%q).Validate() error = %v", command, err)
		}
	}
	if err := Command("future-command").Validate(); err == nil {
		t.Fatal("unknown command was accepted")
	}

	managerID, err := NewManagerObjectID("com.example/package:amd64=1.0+build@stable")
	if err != nil {
		t.Fatalf("NewManagerObjectID() error = %v", err)
	}
	if got := managerID.String(); got != "com.example/package:amd64=1.0+build@stable" {
		t.Fatalf("ManagerObjectID.String() = %q", got)
	}
	for _, value := range []string{"", "/absolute", "-option", "one..two", "one//two", "space value", "bad\x00value"} {
		if _, err := NewManagerObjectID(value); err == nil {
			t.Errorf("NewManagerObjectID(%q) unexpectedly succeeded", value)
		}
	}

	ids := []interface{ String() string }{
		testProvider(t), testRoot(t), testCandidateID(t, "candidate"), testActionID(t, "action"),
		testRunID(t), testCapabilityID(t, "capability"), managerID,
	}
	for _, id := range ids {
		if id.String() == "" {
			t.Error("validated ID rendered empty")
		}
	}
}

func TestDigestBindingAndDefensiveBytes(t *testing.T) {
	body := []byte("canonical-body")
	digest := ComputePlanDigest(body)
	if !digest.Verify(body) {
		t.Fatal("digest did not verify its canonical body")
	}
	if digest.Verify([]byte("canonical-body-with-change")) {
		t.Fatal("digest verified changed body")
	}
	if digest.String() == "" || testConfigDigest(t, 1).String() == "" || testEvidenceDigest(t, 2).String() == "" {
		t.Fatal("digest string was empty")
	}

	for _, value := range [][]byte{nil, make([]byte, planDigestLength-1), make([]byte, planDigestLength)} {
		if _, err := NewConfigDigest(value); err == nil {
			t.Errorf("NewConfigDigest(%d bytes) unexpectedly succeeded", len(value))
		}
		if _, err := NewEvidenceDigest(value); err == nil {
			t.Errorf("NewEvidenceDigest(%d bytes) unexpectedly succeeded", len(value))
		}
	}

	bytesCopy := digest.Bytes()
	bytesCopy[0] ^= 0xff
	if bytes.Equal(bytesCopy, digest.Bytes()) {
		t.Fatal("PlanDigest.Bytes() leaked mutable backing storage")
	}
	configCopy := testConfigDigest(t, 3).Bytes()
	configCopy[0] ^= 0xff
	if bytes.Equal(configCopy, testConfigDigest(t, 3).Bytes()) {
		t.Fatal("ConfigDigest.Bytes() leaked mutable backing storage")
	}
	evidenceCopy := testEvidenceDigest(t, 4).Bytes()
	evidenceCopy[0] ^= 0xff
	if bytes.Equal(evidenceCopy, testEvidenceDigest(t, 4).Bytes()) {
		t.Fatal("EvidenceDigest.Bytes() leaked mutable backing storage")
	}
}

func TestTargetCandidateCapabilityClosedValuesAndCopies(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	if err := filesystem.Validate(); err != nil {
		t.Fatalf("filesystem target error = %v", err)
	}
	if err := manager.Validate(); err != nil {
		t.Fatalf("manager target error = %v", err)
	}

	invalidTargets := []Target{
		{Kind: TargetFilesystem},
		{Kind: TargetManagerObject},
		{Kind: TargetKind("future")},
		{Kind: TargetFilesystem, Filesystem: filesystem.Filesystem, ManagerObject: manager.ManagerObject},
	}
	for _, target := range invalidTargets {
		if err := target.Validate(); err == nil {
			t.Error("invalid target union was accepted")
		}
	}
	if _, err := NewFilesystemTarget(TrustedRootID("bad root"), testMarkerPath(t)); err == nil {
		t.Fatal("NewFilesystemTarget accepted unsafe root ID")
	}
	if _, err := NewManagerObjectTarget(testProvider(t), testManagerObjectID(t, "example-package"), ManagerScope("future")); err == nil {
		t.Fatal("NewManagerObjectTarget accepted unknown scope")
	}
	for _, scope := range []ManagerScope{ManagerScopeUser, ManagerScopeSystem} {
		if err := scope.Validate(); err != nil {
			t.Errorf("ManagerScope(%q).Validate() = %v", scope, err)
		}
	}
	if err := ManagerScope("future").Validate(); err == nil {
		t.Fatal("unknown manager scope accepted")
	}
	for _, confidence := range []Confidence{ConfidenceLow, ConfidenceMedium, ConfidenceHigh, ConfidenceExact} {
		if err := confidence.Validate(); err != nil {
			t.Errorf("Confidence(%q).Validate() = %v", confidence, err)
		}
	}
	if err := Confidence("future").Validate(); err == nil {
		t.Fatal("unknown confidence accepted")
	}

	candidate := Candidate{
		ID:                    testCandidateID(t, "candidate"),
		Provider:              testProvider(t),
		Target:                filesystem,
		Evidence:              []Evidence{testFilesystemEvidence(t, filesystem)},
		Size:                  testSizeFacts(),
		Confidence:            ConfidenceExact,
		DiscoveryPrecondition: testFilesystemPrecondition(t, filesystem),
	}
	canonical, err := NewCandidate(candidate)
	if err != nil {
		t.Fatalf("NewCandidate() error = %v", err)
	}
	candidate.Evidence[0].Filesystem.Snapshot.Inode.Value++
	candidate.DiscoveryPrecondition.Filesystem.Snapshot.Inode.Value++
	if canonical.Evidence[0].Filesystem.Snapshot.Inode.Value != 42 || canonical.DiscoveryPrecondition.Filesystem.Snapshot.Inode.Value != 42 {
		t.Fatal("NewCandidate retained mutable nested evidence or precondition")
	}
	if _, err := NewCandidate(Candidate{}); err == nil {
		t.Fatal("NewCandidate accepted incomplete candidate")
	}

	for _, state := range []CapabilityState{CapabilitySupported, CapabilityUnsupported, CapabilityUnavailable} {
		if err := state.Validate(); err != nil {
			t.Errorf("CapabilityState(%q).Validate() = %v", state, err)
		}
	}
	if err := CapabilityState("future").Validate(); err == nil {
		t.Fatal("unknown capability state accepted")
	}
	facts := []CapabilityFact{
		{ID: testCapabilityID(t, "z-capability"), State: CapabilityUnavailable, Version: "1"},
		{ID: testCapabilityID(t, "a-capability"), State: CapabilitySupported, Version: "2"},
	}
	snapshot, err := NewCapabilitySnapshot(facts)
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot() error = %v", err)
	}
	if snapshot.Facts[0].ID != testCapabilityID(t, "a-capability") {
		t.Fatal("capability snapshot was not sorted")
	}
	facts[0].Version = "mutated"
	if snapshot.Facts[1].Version == "mutated" {
		t.Fatal("capability snapshot retained caller slice")
	}
	if _, err := NewCapabilitySnapshot([]CapabilityFact{{ID: testCapabilityID(t, "same"), State: CapabilitySupported, Version: "1"}, {ID: testCapabilityID(t, "same"), State: CapabilitySupported, Version: "1"}}); err == nil {
		t.Fatal("duplicate capability facts accepted")
	}
}

func TestEvidenceVariantsAreClosedAndCloneNestedState(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	graph := testTransactionGraph(t)

	evidence := []Evidence{
		testFilesystemEvidence(t, filesystem),
		{Kind: EvidencePackageTransaction, PackageTransaction: &PackageTransactionEvidence{Graph: graph}},
		testManagerEvidence(t, manager),
		{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}},
		{Kind: EvidenceProjectMarker, ProjectMarker: &ProjectMarkerEvidence{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: EvidenceInstallerMetadata, InstallerMetadata: &InstallerMetadataEvidence{Target: filesystem, Format: InstallerFormatDeb, Digest: testEvidenceDigest(t, 5)}},
		{Kind: EvidenceProfileManager, ProfileManager: &ProfileManagerEvidence{Provider: testProvider(t), Manager: ProfileManagerAuthselect, Version: "1"}},
	}
	for _, value := range evidence {
		if err := value.Validate(); err != nil {
			t.Errorf("Evidence(%q).Validate() error = %v", value.Kind, err)
		}
	}

	for _, format := range []InstallerFormat{InstallerFormatDeb, InstallerFormatRPM, InstallerFormatArchive} {
		if err := format.Validate(); err != nil {
			t.Errorf("InstallerFormat(%q).Validate() = %v", format, err)
		}
	}
	if err := InstallerFormat("future").Validate(); err == nil {
		t.Fatal("unknown installer format accepted")
	}
	for _, managerKind := range []ProfileManagerKind{ProfileManagerPAMAuthUpdate, ProfileManagerAuthselect} {
		if err := managerKind.Validate(); err != nil {
			t.Errorf("ProfileManagerKind(%q).Validate() = %v", managerKind, err)
		}
	}
	if err := ProfileManagerKind("future").Validate(); err == nil {
		t.Fatal("unknown profile manager accepted")
	}

	invalid := []Evidence{
		{},
		{Kind: EvidenceKind("future"), Filesystem: evidence[0].Filesystem},
		{Kind: EvidenceFilesystemIdentity, Filesystem: evidence[0].Filesystem, Capability: evidence[3].Capability},
		{Kind: EvidenceManagerObject, ManagerObject: &ManagerObjectEvidence{Target: filesystem, Version: "1"}},
		{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilityState("future"), Version: "1"}},
		{Kind: EvidenceProjectMarker, ProjectMarker: &ProjectMarkerEvidence{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "Bad Space"}},
		{Kind: EvidenceInstallerMetadata, InstallerMetadata: &InstallerMetadataEvidence{Target: filesystem, Format: InstallerFormat("future"), Digest: testEvidenceDigest(t, 5)}},
		{Kind: EvidenceProfileManager, ProfileManager: &ProfileManagerEvidence{Provider: testProvider(t), Manager: ProfileManagerKind("future"), Version: "1"}},
	}
	for _, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Errorf("invalid evidence %q was accepted", value.Kind)
		}
	}

	clone := evidence[1].Clone()
	evidence[1].PackageTransaction.Graph.Nodes[0].Version = "mutated"
	if clone.PackageTransaction.Graph.Nodes[0].Version == "mutated" {
		t.Fatal("Evidence.Clone() retained transaction graph backing storage")
	}
	projectClone := evidence[4].Clone()
	if !projectClone.ProjectMarker.Marker.Equal(evidence[4].ProjectMarker.Marker) {
		t.Fatal("Evidence.Clone() changed project marker bytes")
	}
}

func TestPreconditionVariantsMasksAndCloneNestedState(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	graph := testTransactionGraph(t)

	preconditions := []Precondition{
		testFilesystemPrecondition(t, filesystem),
		{Kind: PreconditionPackageTransaction, PackageTransaction: &PackageTransactionPrecondition{Graph: graph, ManagerStateDigest: testEvidenceDigest(t, 6)}},
		testManagerPrecondition(t, manager),
		{Kind: PreconditionCapability, Capability: &CapabilityPrecondition{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}},
		{Kind: PreconditionProjectMarker, ProjectMarker: &ProjectMarkerPrecondition{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "node"}},
		{Kind: PreconditionInstallerMetadata, InstallerMetadata: &InstallerMetadataPrecondition{Target: filesystem, Format: InstallerFormatRPM, Digest: testEvidenceDigest(t, 7)}},
		{Kind: PreconditionProfileManager, ProfileManager: &ProfileManagerPrecondition{Provider: testProvider(t), Manager: ProfileManagerPAMAuthUpdate, Version: "1"}},
	}
	for _, value := range preconditions {
		if err := value.Validate(); err != nil {
			t.Errorf("Precondition(%q).Validate() error = %v", value.Kind, err)
		}
	}

	for _, fileType := range []FileType{FileTypeRegular, FileTypeDirectory, FileTypeSymlink, FileTypeSpecial} {
		if err := fileType.Validate(); err != nil {
			t.Errorf("FileType(%q).Validate() = %v", fileType, err)
		}
	}
	if err := FileTypeUnknown.Validate(); err == nil {
		t.Fatal("unknown file type accepted")
	}

	observed := testFilesystemSnapshot()
	for _, mask := range []FilesystemFieldMask{
		FilesystemFieldDevice, FilesystemFieldInode, FilesystemFieldType, FilesystemFieldUID,
		FilesystemFieldGID, FilesystemFieldMode, FilesystemFieldLinkCount, FilesystemFieldSize,
		FilesystemFieldModifiedAt, FilesystemFieldChangedAt, FilesystemFieldMountID,
	} {
		if err := observed.ValidateFor(mask); err != nil {
			t.Errorf("FilesystemSnapshot.ValidateFor(%#x) = %v", mask, err)
		}
	}
	if err := (FilesystemSnapshot{}).ValidateObserved(); err == nil {
		t.Fatal("unobserved snapshot was accepted as filesystem evidence")
	}

	invalid := []Precondition{
		{},
		{Kind: PreconditionKind("future"), Filesystem: preconditions[0].Filesystem},
		{Kind: PreconditionFilesystemIdentity, Filesystem: preconditions[0].Filesystem, Capability: preconditions[3].Capability},
		{Kind: PreconditionManagerObjectState, ManagerObject: &ManagerObjectPrecondition{Target: filesystem, ExpectedVersion: "1"}},
		{Kind: PreconditionCapability, Capability: &CapabilityPrecondition{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported}},
		{Kind: PreconditionProjectMarker, ProjectMarker: &ProjectMarkerPrecondition{Root: testRoot(t), Marker: testMarkerPath(t), Ecosystem: "Bad Space"}},
		{Kind: PreconditionInstallerMetadata, InstallerMetadata: &InstallerMetadataPrecondition{Target: filesystem, Format: InstallerFormat("future"), Digest: testEvidenceDigest(t, 7)}},
		{Kind: PreconditionProfileManager, ProfileManager: &ProfileManagerPrecondition{Provider: testProvider(t), Manager: ProfileManagerKind("future"), Version: "1"}},
	}
	for _, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Errorf("invalid precondition %q was accepted", value.Kind)
		}
	}

	clone := preconditions[1].Clone()
	preconditions[1].PackageTransaction.Graph.Nodes[0].Version = "mutated"
	if clone.PackageTransaction.Graph.Nodes[0].Version == "mutated" {
		t.Fatal("Precondition.Clone() retained transaction graph backing storage")
	}
}

func TestTransactionGraphAndGuaranteeRejectBroadenedOrMalformedFacts(t *testing.T) {
	graph := testTransactionGraph(t)
	for _, reason := range []TransactionEdgeReason{
		TransactionEdgeDependency, TransactionEdgeReplacement, TransactionEdgeConflict, TransactionEdgeRemoval,
	} {
		if err := reason.Validate(); err != nil {
			t.Errorf("TransactionEdgeReason(%q).Validate() = %v", reason, err)
		}
	}
	if err := TransactionEdgeReason("future").Validate(); err == nil {
		t.Fatal("unknown transaction edge reason accepted")
	}

	for _, guarantee := range []ProviderGuarantee{
		{Kind: ProviderGuaranteeReadOnlyInventory},
		{Kind: ProviderGuaranteeBoundedEffectOnly, CoveredObjects: []ManagerObjectRef{graph.Nodes[0].reference()}},
		{Kind: ProviderGuaranteeExactTargetReprobeRequired, CoveredObjects: []ManagerObjectRef{graph.Nodes[0].reference()}},
		{Kind: ProviderGuaranteeExactGraphReprobeRequired, CoveredObjects: []ManagerObjectRef{graph.Nodes[0].reference(), graph.Nodes[1].reference()}},
	} {
		if err := guarantee.Validate(); err != nil {
			t.Errorf("ProviderGuarantee(%q).Validate() = %v", guarantee.Kind, err)
		}
	}

	invalidGraphs := []TransactionGraph{
		func() TransactionGraph { value := graph.Clone(); value.Nodes[0].Version = ""; return value }(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Nodes[0].Scope = ManagerScope("future")
			return value
		}(),
		func() TransactionGraph { value := graph.Clone(); value.Edges[0].From = value.Edges[0].To; return value }(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Edges[0].Reason = TransactionEdgeReason("future")
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Guarantee = ProviderGuarantee{Kind: ProviderGuaranteeBoundedEffectOnly}
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Guarantee.CoveredObjects = []ManagerObjectRef{value.Nodes[1].reference(), value.Nodes[0].reference()}
			return value
		}(),
		func() TransactionGraph {
			value := graph.Clone()
			value.Guarantee.CoveredObjects = append(value.Guarantee.CoveredObjects, testManagerObjectRef(t, "missing", ManagerScopeSystem))
			return value
		}(),
	}
	for _, value := range invalidGraphs {
		if err := value.Validate(); err == nil {
			t.Error("invalid transaction graph was accepted")
		}
	}

	clone := graph.Clone()
	graph.Nodes[0].Version = "mutated"
	graph.Edges[0].Reason = TransactionEdgeConflict
	graph.Guarantee.CoveredObjects[0] = testManagerObjectRef(t, "changed", ManagerScopeSystem)
	if clone.Nodes[0].Version == "mutated" || clone.Edges[0].Reason == TransactionEdgeConflict || clone.Guarantee.CoveredObjects[0].ID == testManagerObjectID(t, "changed") {
		t.Fatal("TransactionGraph.Clone() retained mutable backing storage")
	}
}

func TestActionClosedVocabularyBindingsAndCopies(t *testing.T) {
	for _, kind := range []ActionKind{
		ActionTrashPath, ActionDeleteRecreatablePath, ActionQuarantinePath, ActionRestoreTrashPath,
		ActionRestoreQuarantinePath, ActionRepairState, ActionRemoveNativePackage, ActionRemoveFlatpakRef,
		ActionRemoveSnap, ActionCleanPackageCache, ActionVacuumJournal, ActionRunOwnedCacheRebuild,
		ActionInstallCompletion, ActionConfigureFingerprintAuth, ActionUpdateLDCleanPackage, ActionRemoveLDCleanPackage,
	} {
		if err := kind.Validate(); err != nil {
			t.Errorf("ActionKind(%q).Validate() = %v", kind, err)
		}
	}
	if err := ActionKind("future").Validate(); err == nil {
		t.Fatal("unknown action kind accepted")
	}
	for _, risk := range []Risk{RiskLow, RiskMedium, RiskHigh, RiskCritical} {
		if err := risk.Validate(); err != nil {
			t.Errorf("Risk(%q).Validate() = %v", risk, err)
		}
	}
	for _, reversibility := range []Reversibility{ReversibilityRecoverable, ReversibilityRecreatable, ReversibilityIrreversible, ReversibilityNoRollback} {
		if err := reversibility.Validate(); err != nil {
			t.Errorf("Reversibility(%q).Validate() = %v", reversibility, err)
		}
	}
	for _, postcondition := range []Postcondition{
		PostconditionTargetAbsent, PostconditionTargetPresent, PostconditionStateRepaired, PostconditionCacheCleaned,
		PostconditionJournalVacuumed, PostconditionOwnedCacheRebuilt, PostconditionCompletionInstalled,
		PostconditionFingerprintConfigured, PostconditionPackageUpdated,
	} {
		if err := postcondition.Validate(); err != nil {
			t.Errorf("Postcondition(%q).Validate() = %v", postcondition, err)
		}
	}
	if err := Risk("future").Validate(); err == nil {
		t.Fatal("unknown risk accepted")
	}
	if err := Reversibility("future").Validate(); err == nil {
		t.Fatal("unknown reversibility accepted")
	}
	if err := Postcondition("future").Validate(); err == nil {
		t.Fatal("unknown postcondition accepted")
	}

	action := testAction(t, "copy-action")
	canonical, err := NewAction(action)
	if err != nil {
		t.Fatalf("NewAction() error = %v", err)
	}
	action.Evidence[0].Filesystem.Snapshot.Size.Value++
	action.Precondition.Filesystem.Snapshot.Size.Value++
	if canonical.Evidence[0].Filesystem.Snapshot.Size.Value != 4096 || canonical.Precondition.Filesystem.Snapshot.Size.Value != 4096 {
		t.Fatal("NewAction retained mutable nested state")
	}
	copyManagerAction := testManagerAction(t, "copy-manager-action")
	canonicalManagerAction, err := NewAction(copyManagerAction)
	if err != nil {
		t.Fatalf("NewAction(manager action) error = %v", err)
	}
	copyManagerAction.ProviderGuarantee.CoveredObjects[0] = testManagerObjectRef(t, "changed", ManagerScopeSystem)
	if canonicalManagerAction.ProviderGuarantee.CoveredObjects[0].ID == testManagerObjectID(t, "changed") {
		t.Fatal("NewAction retained manager guarantee coverage backing storage")
	}

	managerAction := testManagerAction(t, "manager-action")
	if err := managerAction.Validate(); err != nil {
		t.Fatalf("manager action validation error = %v", err)
	}
	badBindings := []Action{
		func() Action {
			value := testAction(t, "no-bound-evidence")
			value.Evidence = []Evidence{{Kind: EvidenceCapability, Capability: &CapabilityEvidence{Capability: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"}}}
			return value
		}(),
		func() Action {
			value := testAction(t, "wrong-precondition")
			value.Precondition = testManagerPrecondition(t, testManagerTarget(t))
			return value
		}(),
		func() Action {
			value := testAction(t, "self-dependency")
			value.Dependencies = []ActionID{value.ID}
			return value
		}(),
		func() Action {
			value := testAction(t, "duplicate-dependency")
			dep := testActionID(t, "dependency")
			value.Dependencies = []ActionID{dep, dep}
			return value
		}(),
	}
	for _, value := range badBindings {
		if err := value.Validate(); err == nil {
			t.Error("invalid action target binding was accepted")
		}
	}
}

func TestPlanCanonicalizationAndValidationBoundaries(t *testing.T) {
	first := testAction(t, "first")
	second := testAction(t, "second", first.ID)
	body := testPlanBody(t, []Action{first, second})
	body.Caller.SelectedCandidateIDs = []CandidateID{testCandidateID(t, "z-candidate"), testCandidateID(t, "a-candidate")}
	body.Capabilities = CapabilitySnapshot{Facts: []CapabilityFact{
		{ID: testCapabilityID(t, "z-capability"), State: CapabilitySupported, Version: "1"},
		{ID: testCapabilityID(t, "a-capability"), State: CapabilityUnsupported, Version: "1"},
	}}
	body.Creation.CreatedAt = time.Date(2026, time.July, 15, 19, 0, 0, 0, time.FixedZone("plus-seven", 7*60*60))
	plan, err := NewPlan(body, testDigest(t, 9))
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	canonical := plan.CanonicalBody()
	if canonical.Caller.SelectedCandidateIDs[0] != testCandidateID(t, "a-candidate") || canonical.Capabilities.Facts[0].ID != testCapabilityID(t, "a-capability") {
		t.Fatal("NewPlan did not canonicalize declared unordered fields")
	}
	if canonical.Creation.CreatedAt.Location() != time.UTC {
		t.Fatal("NewPlan did not normalize creation time to UTC")
	}
	if plan.Digest() != testDigest(t, 9) || plan.Validate() != nil {
		t.Fatal("validated plan did not retain digest or validate")
	}

	invalid := []PlanBody{
		func() PlanBody { value := body.Clone(); value.SchemaVersion = 2; return value }(),
		func() PlanBody { value := body.Clone(); value.Command = Command("future"); return value }(),
		func() PlanBody { value := body.Clone(); value.Caller.RunID = RunID("bad run"); return value }(),
		func() PlanBody { value := body.Clone(); value.ConfigDigest = ConfigDigest{}; return value }(),
		func() PlanBody {
			value := body.Clone()
			value.Totals = SizeFacts{Apparent: ByteQuantity{Bytes: 1}}
			return value
		}(),
		func() PlanBody { value := body.Clone(); value.Creation.CreatedAt = time.Time{}; return value }(),
		func() PlanBody { value := body.Clone(); value.Actions = []Action{first, first}; return value }(),
	}
	for _, value := range invalid {
		if _, err := NewPlan(value, testDigest(t, 9)); err == nil {
			t.Error("invalid plan body was accepted")
		}
	}
	if _, err := NewPlan(body, PlanDigest{}); err == nil {
		t.Fatal("NewPlan accepted zero digest")
	}

	copy := plan.CanonicalBody()
	copy.Actions[0].Evidence[0].Filesystem.Snapshot.Inode.Value++
	copy.Caller.SelectedCandidateIDs[0] = testCandidateID(t, "changed")
	if plan.CanonicalBody().Actions[0].Evidence[0].Filesystem.Snapshot.Inode.Value != 42 || plan.CanonicalBody().Caller.SelectedCandidateIDs[0] == testCandidateID(t, "changed") {
		t.Fatal("Plan.CanonicalBody() leaked mutable state")
	}
}

func TestSizeFactsRejectInvalidMeasurementsAndAggregateSafely(t *testing.T) {
	invalid := []SizeFacts{
		{Apparent: ByteQuantity{Bytes: 1}},
		func() SizeFacts { value := testSizeFacts(); value.LinkCount = Uint64Fact{Known: true}; return value }(),
		func() SizeFacts { value := testSizeFacts(); value.Aggregate = true; return value }(),
		func() SizeFacts { value := testSizeFacts(); value.LinkCount = Uint64Fact{}; return value }(),
	}
	for _, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Error("invalid size facts were accepted")
		}
	}
	left := testSizeFacts()
	right := testSizeFacts()
	sum, err := left.Add(right)
	if err != nil {
		t.Fatalf("SizeFacts.Add() error = %v", err)
	}
	if !sum.Aggregate || sum.Apparent.Bytes != 8192 || sum.Estimated.Allocated.Available || sum.Verified.Allocated.Available || sum.LinkCount.Known {
		t.Fatalf("SizeFacts.Add() = %+v, want valid aggregate", sum)
	}
	if _, err := (SizeFacts{Apparent: ByteQuantity{Bytes: 1}}).Add(testSizeFacts()); err == nil {
		t.Fatal("SizeFacts.Add accepted invalid left operand")
	}
	if _, err := testSizeFacts().Add(SizeFacts{Apparent: ByteQuantity{Bytes: 1}}); err == nil {
		t.Fatal("SizeFacts.Add accepted invalid right operand")
	}
}

func TestResultStateMachineAllOutcomesProbesRecoveryAndCopies(t *testing.T) {
	filesystem := testFilesystemTarget(t)
	manager := testManagerTarget(t)
	graph := testTransactionGraph(t)
	probes := []ReconciliationProbe{
		{Kind: ReconciliationProbeFilesystemTarget, Target: filesystem},
		{Kind: ReconciliationProbePackageTransaction, Graph: graph},
		{Kind: ReconciliationProbeManagerObject, Target: manager},
		{Kind: ReconciliationProbeStateRecord, StateRecord: &StateRecordProbe{RunID: testRunID(t), ActionID: testActionID(t, "state-action")}},
	}
	for _, probe := range probes {
		if err := probe.Validate(); err != nil {
			t.Errorf("ReconciliationProbe(%q).Validate() = %v", probe.Kind, err)
		}
	}
	invalidProbes := []ReconciliationProbe{
		{Kind: ReconciliationProbeKind("future")},
		{Kind: ReconciliationProbeFilesystemTarget, Target: filesystem, Graph: graph},
		{Kind: ReconciliationProbePackageTransaction, Target: filesystem, Graph: graph},
		{Kind: ReconciliationProbeManagerObject, Target: filesystem},
		{Kind: ReconciliationProbeStateRecord},
	}
	for _, probe := range invalidProbes {
		if err := probe.Validate(); err == nil {
			t.Error("invalid reconciliation probe was accepted")
		}
	}

	for _, outcome := range []Outcome{OutcomeSuccess, OutcomeSkipped, OutcomeDrifted, OutcomeDenied, OutcomeUnsupported, OutcomeFailed, OutcomeInterrupted, OutcomeIndeterminate} {
		if err := outcome.Validate(); err != nil {
			t.Errorf("Outcome(%q).Validate() = %v", outcome, err)
		}
	}
	if err := Outcome("future").Validate(); err == nil {
		t.Fatal("unknown outcome accepted")
	}
	for _, state := range []ReconciliationState{ReconciliationNotRequired, ReconciliationRequired} {
		if err := state.Validate(); err != nil {
			t.Errorf("ReconciliationState(%q).Validate() = %v", state, err)
		}
	}
	if err := ReconciliationState("future").Validate(); err == nil {
		t.Fatal("unknown reconciliation state accepted")
	}
	for _, kind := range []RecoveryHandleKind{RecoveryHandleTrash, RecoveryHandleQuarantine} {
		if err := kind.Validate(); err != nil {
			t.Errorf("RecoveryHandleKind(%q).Validate() = %v", kind, err)
		}
	}
	for _, disposition := range []RecoveryDisposition{RecoveryNotApplicable, RecoveryRetained, RecoveryRestored} {
		if err := disposition.Validate(); err != nil {
			t.Errorf("RecoveryDisposition(%q).Validate() = %v", disposition, err)
		}
	}

	actions := []ActionResult{
		{ActionID: testActionID(t, "success"), Kind: ActionTrashPath, Outcome: OutcomeSuccess, Attempted: true, VerifiedEffect: testSizeFacts()},
		{ActionID: testActionID(t, "skipped"), Kind: ActionTrashPath, Outcome: OutcomeSkipped},
		{ActionID: testActionID(t, "drifted"), Kind: ActionTrashPath, Outcome: OutcomeDrifted},
		{ActionID: testActionID(t, "denied"), Kind: ActionTrashPath, Outcome: OutcomeDenied},
		{ActionID: testActionID(t, "unsupported"), Kind: ActionTrashPath, Outcome: OutcomeUnsupported},
		{ActionID: testActionID(t, "failed"), Kind: ActionTrashPath, Outcome: OutcomeFailed, Attempted: true},
		{ActionID: testActionID(t, "interrupted"), Kind: ActionTrashPath, Outcome: OutcomeInterrupted},
		{ActionID: testActionID(t, "indeterminate"), Kind: ActionTrashPath, Outcome: OutcomeIndeterminate, Attempted: true, Reconciliation: ReconciliationRequired, ReconciliationProbe: &probes[0]},
	}
	result, err := NewResult(Result{SchemaVersion: SchemaVersionV1, PlanDigest: testDigest(t, 30), RunID: testRunID(t), Actions: actions})
	if err != nil {
		t.Fatalf("NewResult() error = %v", err)
	}
	if result.Summary.Total != 8 || result.Summary.SuccessCount != 1 || result.Summary.SkippedCount != 1 || result.Summary.DriftedCount != 1 || result.Summary.DeniedCount != 1 || result.Summary.UnsupportedCount != 1 || result.Summary.FailedCount != 1 || result.Summary.InterruptedCount != 1 || result.Summary.IndeterminateCount != 1 || !result.Summary.Partial || !result.Summary.ReconciliationRequired {
		t.Fatalf("derived summary = %+v", result.Summary)
	}

	recovery := testRecoveryHandle(t)
	retained, err := NewResult(Result{SchemaVersion: SchemaVersionV1, PlanDigest: testDigest(t, 31), RunID: testRunID(t), Actions: []ActionResult{{
		ActionID: testActionID(t, "retained"), Kind: ActionTrashPath, Outcome: OutcomeSuccess, Attempted: true,
		Recovery: RecoveryRetained, RecoveryHandle: &recovery,
	}}})
	if err != nil {
		t.Fatalf("NewResult(retained) error = %v", err)
	}
	if retained.Actions[0].Recovery != RecoveryRetained {
		t.Fatal("retained recovery state was not preserved")
	}
	if _, err := NewResult(Result{SchemaVersion: SchemaVersionV1, PlanDigest: testDigest(t, 32), RunID: testRunID(t), Actions: []ActionResult{{
		ActionID: testActionID(t, "bad-recovery"), Kind: ActionTrashPath, Outcome: OutcomeSuccess, Attempted: true,
		Recovery: RecoveryRestored, VerifiedEffect: testSizeFacts(),
	}}}); err == nil {
		t.Fatal("restored recovery incorrectly claimed freed space")
	}

	invalidResults := []ActionResult{
		{ActionID: testActionID(t, "success-not-attempted"), Kind: ActionTrashPath, Outcome: OutcomeSuccess},
		{ActionID: testActionID(t, "failed-not-attempted"), Kind: ActionTrashPath, Outcome: OutcomeFailed},
		{ActionID: testActionID(t, "skipped-attempted"), Kind: ActionTrashPath, Outcome: OutcomeSkipped, Attempted: true},
		{ActionID: testActionID(t, "indeterminate-no-probe"), Kind: ActionTrashPath, Outcome: OutcomeIndeterminate, Attempted: true},
		{ActionID: testActionID(t, "failed-verified"), Kind: ActionTrashPath, Outcome: OutcomeFailed, Attempted: true, VerifiedEffect: testSizeFacts()},
	}
	for _, action := range invalidResults {
		if _, err := NewResult(Result{SchemaVersion: SchemaVersionV1, PlanDigest: testDigest(t, 33), RunID: testRunID(t), Actions: []ActionResult{action}}); err == nil {
			t.Error("invalid action result was accepted")
		}
	}

	copy := result.Clone()
	result.Actions[7].ReconciliationProbe.Target.Filesystem.Root = TrustedRootID("changed")
	if copy.Actions[7].ReconciliationProbe.Target.Filesystem.Root == TrustedRootID("changed") {
		t.Fatal("Result.Clone() retained reconciliation target pointer")
	}
	recoveryCopy := recovery.Clone()
	if !recoveryCopy.OriginalPath.Equal(recovery.OriginalPath) {
		t.Fatal("RecoveryHandle.Clone() changed raw path")
	}
}
