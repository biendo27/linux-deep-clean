package domain

import (
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestUnknownCompletionRequiresReconciliation(t *testing.T) {
	actionID := testActionID(t, "trash-first")
	base := Result{
		SchemaVersion: SchemaVersionV1,
		PlanDigest:    testDigest(t, 8),
		RunID:         testRunID(t),
		Actions: []ActionResult{{
			ActionID:       actionID,
			Kind:           ActionTrashPath,
			Outcome:        OutcomeIndeterminate,
			Attempted:      true,
			Reconciliation: ReconciliationRequired,
			ReconciliationProbe: &ReconciliationProbe{
				Kind:   ReconciliationProbeFilesystemTarget,
				Target: testFilesystemTarget(t),
			},
		}},
	}
	result, err := NewResult(base)
	if err != nil {
		t.Fatalf("NewResult() error = %v", err)
	}
	if !result.Summary.Partial || !result.Summary.ReconciliationRequired {
		t.Fatalf("indeterminate summary = %+v, want partial reconciliation-required", result.Summary)
	}

	base.Actions[0].Reconciliation = ReconciliationNotRequired
	base.Actions[0].ReconciliationProbe = nil
	if _, err := NewResult(base); err == nil {
		t.Fatal("NewResult() accepted indeterminate action without reconciliation")
	}
}

func TestRecoveryHandleRetainsOnlyTypedRelativeAuthority(t *testing.T) {
	path, err := pathbytes.New([][]byte{[]byte("application"), []byte("cache")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	handle := RecoveryHandle{
		Kind:         RecoveryHandleTrash,
		Root:         testRoot(t),
		Token:        "trash-token-001",
		OriginalPath: path,
		RecordedAt:   time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC),
	}
	if err := handle.Validate(); err != nil {
		t.Fatalf("RecoveryHandle.Validate() error = %v", err)
	}

	handle.Token = "/absolute/path"
	if err := handle.Validate(); err == nil {
		t.Fatal("RecoveryHandle.Validate() accepted an absolute path token")
	}
}

func TestRecoveryHandleRequiresUnixNanosecondRepresentableTime(t *testing.T) {
	handle := testRecoveryHandle(t)
	minimum := time.Unix(0, -1<<63).UTC()
	maximum := time.Unix(0, 1<<63-1).UTC()

	for _, test := range []struct {
		name  string
		at    time.Time
		valid bool
	}{
		{name: "minimum boundary", at: minimum, valid: true},
		{name: "maximum boundary", at: maximum, valid: true},
		{name: "before minimum", at: minimum.Add(-time.Nanosecond)},
		{name: "after maximum", at: maximum.Add(time.Nanosecond)},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := handle
			candidate.RecordedAt = test.at
			err := candidate.Validate()
			if test.valid && err != nil {
				t.Fatalf("RecoveryHandle.Validate() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("RecoveryHandle.Validate() accepted a time outside the Unix-nanosecond range")
			}
		})
	}
}
