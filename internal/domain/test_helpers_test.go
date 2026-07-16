package domain

import (
	"bytes"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func testProvider(t *testing.T) ProviderID {
	t.Helper()

	id, err := NewProviderID("xdg-cache")
	if err != nil {
		t.Fatalf("NewProviderID() error = %v", err)
	}
	return id
}

func testRoot(t *testing.T) TrustedRootID {
	t.Helper()

	id, err := NewTrustedRootID("user-cache")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	return id
}

func testCandidateID(t *testing.T, value string) CandidateID {
	t.Helper()

	id, err := NewCandidateID(value)
	if err != nil {
		t.Fatalf("NewCandidateID(%q) error = %v", value, err)
	}
	return id
}

func testActionID(t *testing.T, value string) ActionID {
	t.Helper()

	id, err := NewActionID(value)
	if err != nil {
		t.Fatalf("NewActionID(%q) error = %v", value, err)
	}
	return id
}

func testRunID(t *testing.T) RunID {
	t.Helper()

	id, err := NewRunID("run-20260715-0001")
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	return id
}

func testManagerObjectID(t *testing.T, value string) ManagerObjectID {
	t.Helper()

	id, err := NewManagerObjectID(value)
	if err != nil {
		t.Fatalf("NewManagerObjectID(%q) error = %v", value, err)
	}
	return id
}

func testManagerObjectRef(t *testing.T, value string, scope ManagerScope) ManagerObjectRef {
	t.Helper()

	reference, err := NewManagerObjectRef(testManagerObjectID(t, value), scope)
	if err != nil {
		t.Fatalf("NewManagerObjectRef(%q, %q) error = %v", value, scope, err)
	}
	return reference
}

func testCapabilityID(t *testing.T, value string) CapabilityID {
	t.Helper()

	id, err := NewCapabilityID(value)
	if err != nil {
		t.Fatalf("NewCapabilityID(%q) error = %v", value, err)
	}
	return id
}

func testDigest(t *testing.T, fill byte) PlanDigest {
	t.Helper()

	digest, err := NewPlanDigest(bytes.Repeat([]byte{fill}, planDigestLength))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	return digest
}

func testConfigDigest(t *testing.T, fill byte) ConfigDigest {
	t.Helper()

	digest, err := NewConfigDigest(bytes.Repeat([]byte{fill}, planDigestLength))
	if err != nil {
		t.Fatalf("NewConfigDigest() error = %v", err)
	}
	return digest
}

func testEvidenceDigest(t *testing.T, fill byte) EvidenceDigest {
	t.Helper()

	digest, err := NewEvidenceDigest(bytes.Repeat([]byte{fill}, planDigestLength))
	if err != nil {
		t.Fatalf("NewEvidenceDigest() error = %v", err)
	}
	return digest
}

func testFilesystemTarget(t *testing.T) Target {
	t.Helper()

	path, err := pathbytes.New([][]byte{[]byte("application"), []byte("cache")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := NewFilesystemTarget(testRoot(t), path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	return target
}

func testFilesystemSnapshot() FilesystemSnapshot {
	return FilesystemSnapshot{
		DeviceMajor: Uint32Fact{Known: true, Value: 8},
		DeviceMinor: Uint32Fact{Known: true, Value: 1},
		Inode:       Uint64Fact{Known: true, Value: 42},
		Type:        FileTypeRegular,
		UID:         Uint32Fact{Known: true, Value: 1000},
		GID:         Uint32Fact{Known: true, Value: 1000},
		Mode:        Uint32Fact{Known: true, Value: 0o600},
		LinkCount:   Uint64Fact{Known: true, Value: 1},
		Size:        Uint64Fact{Known: true, Value: 4096},
		ModifiedAt:  Int64Fact{Known: true, Value: 1721000000000000000},
		ChangedAt:   Int64Fact{Known: true, Value: 1721000000000000000},
		MountID:     Uint64Fact{Known: true, Value: 17},
	}
}

func testFilesystemEvidence(t *testing.T, target Target) Evidence {
	t.Helper()

	evidence := Evidence{
		Kind: EvidenceFilesystemIdentity,
		Filesystem: &FilesystemEvidence{
			Target:   target,
			Snapshot: testFilesystemSnapshot(),
		},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("filesystem evidence validation error = %v", err)
	}
	return evidence
}

func testFilesystemPrecondition(t *testing.T, target Target) Precondition {
	t.Helper()

	precondition := Precondition{
		Kind: PreconditionFilesystemIdentity,
		Filesystem: &FilesystemPrecondition{
			Target: target,
			Required: FilesystemFieldDevice |
				FilesystemFieldInode |
				FilesystemFieldType |
				FilesystemFieldUID |
				FilesystemFieldGID |
				FilesystemFieldMode |
				FilesystemFieldLinkCount |
				FilesystemFieldSize |
				FilesystemFieldModifiedAt |
				FilesystemFieldChangedAt |
				FilesystemFieldMountID,
			Snapshot: testFilesystemSnapshot(),
		},
	}
	if err := precondition.Validate(); err != nil {
		t.Fatalf("filesystem precondition validation error = %v", err)
	}
	return precondition
}

func testGuarantee(t *testing.T) ProviderGuarantee {
	t.Helper()

	guarantee := ProviderGuarantee{
		Kind: ProviderGuaranteeReadOnlyInventory,
	}
	if err := guarantee.Validate(); err != nil {
		t.Fatalf("ProviderGuarantee.Validate() error = %v", err)
	}
	return guarantee
}

func testAction(t *testing.T, id string, dependencies ...ActionID) Action {
	t.Helper()

	target := testFilesystemTarget(t)
	action := Action{
		ID:                    testActionID(t, id),
		Kind:                  ActionTrashPath,
		Target:                target,
		Evidence:              []Evidence{testFilesystemEvidence(t, target)},
		Precondition:          testFilesystemPrecondition(t, target),
		Dependencies:          append([]ActionID(nil), dependencies...),
		Risk:                  RiskLow,
		Reversibility:         ReversibilityRecoverable,
		EstimatedEffect:       testSizeFacts(),
		RequiredCapability:    testCapabilityID(t, "trash"),
		ProviderGuarantee:     testGuarantee(t),
		ExpectedPostcondition: PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("Action.Validate() error = %v", err)
	}
	return action
}

func testSizeFacts() SizeFacts {
	return SizeFacts{
		Apparent:  ByteQuantity{Available: true, Bytes: 4096},
		Allocated: ByteQuantity{Available: true, Bytes: 4096},
		Estimated: SizeEffect{
			Apparent:  ByteQuantity{Available: true, Bytes: 4096},
			Allocated: ByteQuantity{Available: true, Bytes: 4096},
		},
		Verified: SizeEffect{
			Apparent:  ByteQuantity{Available: true, Bytes: 4096},
			Allocated: ByteQuantity{Available: true, Bytes: 4096},
		},
		LinkCount: Uint64Fact{Known: true, Value: 1},
	}
}

func testPlanBody(t *testing.T, actions []Action) PlanBody {
	t.Helper()

	capabilities, err := NewCapabilitySnapshot([]CapabilityFact{
		{ID: testCapabilityID(t, "trash"), State: CapabilitySupported, Version: "1"},
	})
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot() error = %v", err)
	}
	return PlanBody{
		SchemaVersion: SchemaVersionV1,
		Command:       CommandClean,
		Caller: CallerEcho{
			RunID:                testRunID(t),
			UID:                  1000,
			Interactive:          true,
			SelectedCandidateIDs: []CandidateID{testCandidateID(t, "cache-candidate")},
		},
		Capabilities: capabilities,
		ConfigDigest: testConfigDigest(t, 2),
		Actions:      append([]Action(nil), actions...),
		Totals:       testSizeFacts(),
		Creation: CreationContext{
			CreatedAt:   time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC),
			ToolVersion: "1.0.0",
			HostProfile: "ubuntu-24.04",
		},
	}
}
