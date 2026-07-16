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

func TestPostStageDriftRecordsSafeRecovery(t *testing.T) {
	handle := testRecoveryHandle(t)

	for _, test := range []struct {
		name     string
		recovery RecoveryDisposition
		handle   *RecoveryHandle
	}{
		{
			name:     "restored without retaining a handle",
			recovery: RecoveryRestored,
		},
		{
			name:     "retained with a recovery handle",
			recovery: RecoveryRetained,
			handle:   &handle,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewResult(Result{
				SchemaVersion: SchemaVersionV1,
				PlanDigest:    testDigest(t, 17),
				RunID:         testRunID(t),
				Actions: []ActionResult{{
					ActionID:       testActionID(t, "staged-drift"),
					Kind:           ActionTrashPath,
					Outcome:        OutcomeDrifted,
					Attempted:      true,
					Recovery:       test.recovery,
					RecoveryHandle: test.handle,
				}},
			})
			if err != nil {
				t.Fatalf("NewResult() error = %v", err)
			}
			if result.Summary.DriftedCount != 1 || result.Summary.SuccessCount != 0 {
				t.Fatalf("summary = %+v, want one drifted and no successful actions", result.Summary)
			}
			if got := result.Actions[0]; !got.Attempted || got.Reconciliation != ReconciliationNotRequired {
				t.Fatalf("staged drift = %#v, want attempted known drift without reconciliation", got)
			}
		})
	}

	invalid := []ActionResult{
		{
			ActionID:  testActionID(t, "attempted-drift-without-recovery"),
			Kind:      ActionTrashPath,
			Outcome:   OutcomeDrifted,
			Attempted: true,
		},
		{
			ActionID:  testActionID(t, "retained-drift-without-handle"),
			Kind:      ActionTrashPath,
			Outcome:   OutcomeDrifted,
			Attempted: true,
			Recovery:  RecoveryRetained,
		},
		{
			ActionID: testActionID(t, "restored-drift-before-attempt"),
			Kind:     ActionTrashPath,
			Outcome:  OutcomeDrifted,
			Recovery: RecoveryRestored,
		},
	}
	for _, action := range invalid {
		if _, err := NewResult(Result{
			SchemaVersion: SchemaVersionV1,
			PlanDigest:    testDigest(t, 18),
			RunID:         testRunID(t),
			Actions:       []ActionResult{action},
		}); err == nil {
			t.Fatalf("NewResult() accepted invalid staged-drift state: %#v", action)
		}
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
