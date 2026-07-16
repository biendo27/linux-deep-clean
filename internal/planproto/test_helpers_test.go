package planproto

import (
	"bytes"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func testPlan(t *testing.T, components ...[]byte) domain.Plan {
	t.Helper()

	if len(components) == 0 {
		components = [][]byte{[]byte("application"), []byte("cache")}
	}
	path, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	root := mustRoot(t, "user-cache")
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	guarantee := domain.ProviderGuarantee{
		Kind: domain.ProviderGuaranteeReadOnlyInventory,
	}
	if err := guarantee.Validate(); err != nil {
		t.Fatalf("ProviderGuarantee.Validate() error = %v", err)
	}

	evidence := domain.Evidence{
		Kind: domain.EvidenceFilesystemIdentity,
		Filesystem: &domain.FilesystemEvidence{
			Target:   target,
			Snapshot: testFilesystemSnapshot(),
		},
	}
	precondition := domain.Precondition{
		Kind: domain.PreconditionFilesystemIdentity,
		Filesystem: &domain.FilesystemPrecondition{
			Target: target,
			Required: domain.FilesystemFieldDevice |
				domain.FilesystemFieldInode |
				domain.FilesystemFieldType |
				domain.FilesystemFieldUID |
				domain.FilesystemFieldGID |
				domain.FilesystemFieldMode |
				domain.FilesystemFieldLinkCount |
				domain.FilesystemFieldSize |
				domain.FilesystemFieldModifiedAt |
				domain.FilesystemFieldChangedAt |
				domain.FilesystemFieldMountID,
			Snapshot: testFilesystemSnapshot(),
		},
	}
	action := domain.Action{
		ID:                    mustActionID(t, "trash-cache"),
		Kind:                  domain.ActionTrashPath,
		Target:                target,
		Evidence:              []domain.Evidence{evidence},
		Precondition:          precondition,
		Risk:                  domain.RiskLow,
		Reversibility:         domain.ReversibilityRecoverable,
		EstimatedEffect:       testSizeFacts(),
		RequiredCapability:    mustCapabilityID(t, "trash"),
		ProviderGuarantee:     guarantee,
		ExpectedPostcondition: domain.PostconditionTargetAbsent,
	}
	capabilities, err := domain.NewCapabilitySnapshot([]domain.CapabilityFact{{
		ID:      mustCapabilityID(t, "trash"),
		State:   domain.CapabilitySupported,
		Version: "1",
	}})
	if err != nil {
		t.Fatalf("NewCapabilitySnapshot() error = %v", err)
	}
	body := domain.PlanBody{
		SchemaVersion: domain.SchemaVersionV1,
		Command:       domain.CommandClean,
		Caller: domain.CallerEcho{
			RunID:                mustRunID(t, "run-20260716-0001"),
			UID:                  1000,
			Interactive:          true,
			SelectedCandidateIDs: []domain.CandidateID{mustCandidateID(t, "cache-candidate")},
		},
		Capabilities: capabilities,
		ConfigDigest: mustConfigDigest(t, 2),
		Actions:      []domain.Action{action},
		Totals: domain.SizeFacts{
			Apparent:  domain.ByteQuantity{Available: true, Bytes: 4096},
			Allocated: domain.ByteQuantity{Available: true, Bytes: 4096},
			Estimated: domain.SizeEffect{
				Apparent: domain.ByteQuantity{Available: true, Bytes: 4096},
			},
			Verified: domain.SizeEffect{
				Apparent: domain.ByteQuantity{Available: true, Bytes: 4096},
			},
			Aggregate: true,
		},
		Creation: domain.CreationContext{
			CreatedAt:   time.Date(2026, time.July, 16, 12, 0, 0, 123456789, time.UTC),
			ToolVersion: "1.0.0",
			HostProfile: "ubuntu-24.04",
		},
	}
	digest, err := ComputeDigest(body)
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	plan, err := domain.NewPlan(body, digest)
	if err != nil {
		t.Fatalf("domain.NewPlan() error = %v", err)
	}
	return plan
}

func testResult(t *testing.T, plan domain.Plan) domain.Result {
	t.Helper()
	result, err := domain.NewResult(domain.Result{
		SchemaVersion: domain.SchemaVersionV1,
		PlanDigest:    plan.Digest(),
		RunID:         mustRunID(t, "run-20260716-0001"),
		Actions: []domain.ActionResult{{
			ActionID:       mustActionID(t, "trash-cache"),
			Kind:           domain.ActionTrashPath,
			Outcome:        domain.OutcomeSuccess,
			Attempted:      true,
			Reconciliation: domain.ReconciliationNotRequired,
		}},
	})
	if err != nil {
		t.Fatalf("domain.NewResult() error = %v", err)
	}
	return result
}

func testIndeterminateResult(t *testing.T, plan domain.Plan) domain.Result {
	t.Helper()
	target := plan.CanonicalBody().Actions[0].Target
	result, err := domain.NewResult(domain.Result{
		SchemaVersion: domain.SchemaVersionV1,
		PlanDigest:    plan.Digest(),
		RunID:         mustRunID(t, "run-20260716-0001"),
		Actions: []domain.ActionResult{{
			ActionID:       mustActionID(t, "trash-cache"),
			Kind:           domain.ActionTrashPath,
			Outcome:        domain.OutcomeIndeterminate,
			Attempted:      true,
			Reconciliation: domain.ReconciliationRequired,
			ReconciliationProbe: &domain.ReconciliationProbe{
				Kind:   domain.ReconciliationProbeFilesystemTarget,
				Target: target,
			},
		}},
	})
	if err != nil {
		t.Fatalf("domain.NewResult() error = %v", err)
	}
	return result
}

func testFilesystemSnapshot() domain.FilesystemSnapshot {
	return domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 8},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 1},
		Inode:       domain.Uint64Fact{Known: true, Value: 42},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true, Value: 1000},
		GID:         domain.Uint32Fact{Known: true, Value: 1000},
		Mode:        domain.Uint32Fact{Known: true, Value: 0o600},
		LinkCount:   domain.Uint64Fact{Known: true, Value: 1},
		Size:        domain.Uint64Fact{Known: true, Value: 4096},
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 1721000000000000000},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 1721000000000000000},
		MountID:     domain.Uint64Fact{Known: true, Value: 17},
	}
}

func testSizeFacts() domain.SizeFacts {
	return domain.SizeFacts{
		Apparent:  domain.ByteQuantity{Available: true, Bytes: 4096},
		Allocated: domain.ByteQuantity{Available: true, Bytes: 4096},
		Estimated: domain.SizeEffect{
			Apparent:  domain.ByteQuantity{Available: true, Bytes: 4096},
			Allocated: domain.ByteQuantity{Available: true, Bytes: 4096},
		},
		Verified: domain.SizeEffect{
			Apparent:  domain.ByteQuantity{Available: true, Bytes: 4096},
			Allocated: domain.ByteQuantity{Available: true, Bytes: 4096},
		},
		LinkCount: domain.Uint64Fact{Known: true, Value: 1},
	}
}

func mustProvider(t *testing.T, value string) domain.ProviderID {
	t.Helper()
	id, err := domain.NewProviderID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustRoot(t *testing.T, value string) domain.TrustedRootID {
	t.Helper()
	id, err := domain.NewTrustedRootID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustCandidateID(t *testing.T, value string) domain.CandidateID {
	t.Helper()
	id, err := domain.NewCandidateID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustActionID(t *testing.T, value string) domain.ActionID {
	t.Helper()
	id, err := domain.NewActionID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustRunID(t *testing.T, value string) domain.RunID {
	t.Helper()
	id, err := domain.NewRunID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustCapabilityID(t *testing.T, value string) domain.CapabilityID {
	t.Helper()
	id, err := domain.NewCapabilityID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func mustManagerObject(t *testing.T, value string) domain.ManagerObjectID {
	t.Helper()
	id, err := domain.NewManagerObjectID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustManagerRef(t *testing.T, value string, scope domain.ManagerScope) domain.ManagerObjectRef {
	t.Helper()
	reference, err := domain.NewManagerObjectRef(mustManagerObject(t, value), scope)
	if err != nil {
		t.Fatal(err)
	}
	return reference
}
func mustConfigDigest(t *testing.T, fill byte) domain.ConfigDigest {
	t.Helper()
	digest, err := domain.NewConfigDigest(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func requirePlanEqual(t *testing.T, want, got domain.Plan) {
	t.Helper()
	wantBytes, err := EncodePlan(want)
	if err != nil {
		t.Fatalf("encode expected plan: %v", err)
	}
	gotBytes, err := EncodePlan(got)
	if err != nil {
		t.Fatalf("encode actual plan: %v", err)
	}
	if !bytes.Equal(wantBytes, gotBytes) {
		t.Fatalf("plan mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func requireResultEqual(t *testing.T, want, got domain.Result) {
	t.Helper()
	wantBytes, err := EncodeResult(want)
	if err != nil {
		t.Fatalf("encode expected result: %v", err)
	}
	gotBytes, err := EncodeResult(got)
	if err != nil {
		t.Fatalf("encode actual result: %v", err)
	}
	if !bytes.Equal(wantBytes, gotBytes) {
		t.Fatalf("result mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func requireCanonicalDTOEqual(t *testing.T, want, got any) {
	t.Helper()
	wantBytes, err := encodeCanonical(want)
	if err != nil {
		t.Fatalf("encode expected DTO: %v", err)
	}
	gotBytes, err := encodeCanonical(got)
	if err != nil {
		t.Fatalf("encode actual DTO: %v", err)
	}
	if !bytes.Equal(wantBytes, gotBytes) {
		t.Fatalf("DTO mismatch\nwant: %#v\n got: %#v", want, got)
	}
}
