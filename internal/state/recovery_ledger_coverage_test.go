package state

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestRecoveryLedgerCoverageClosedVocabularyAndAccessors(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	closedFrame, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
		MetadataDisposition:   RecoveryMetadataRetained,
	})
	if err != nil {
		t.Fatalf("appendRecoveryTransition() error = %v", err)
	}
	recovery, err := replayRecoveryFrames([]recoveryFrame{intent, closedFrame})
	if err != nil {
		t.Fatalf("replayRecoveryFrames() error = %v", err)
	}
	if recovery.PlanDigest() != binding.planDigest || recovery.ActionID() != binding.actionID || recovery.ActionKind() != binding.actionKind {
		t.Fatal("recovery accessors did not preserve immutable action facts")
	}
	if recovery.Outcome() != RecoveryOutcomeNotApplied || recovery.MetadataDisposition() != RecoveryMetadataRetained || !recovery.Closed() {
		t.Fatalf("recovery terminal facts = (%q, %q, closed=%t), want retained no-effect closure", recovery.Outcome(), recovery.MetadataDisposition(), recovery.Closed())
	}

	for name, validate := range map[string]func() error{
		"event":       func() error { return RecoveryEvent("future_event").validate() },
		"destination": func() error { return RecoveryDestination("future_destination").validate() },
		"outcome":     func() error { return RecoveryOutcome("future_outcome").validate() },
		"metadata":    func() error { return RecoveryMetadataDisposition("future_metadata").validate() },
	} {
		t.Run(name, func(t *testing.T) {
			if err := validate(); !errors.Is(err, ErrRecoveryUnsupported) {
				t.Fatalf("validate() error = %v, want ErrRecoveryUnsupported", err)
			}
		})
	}
}

func TestRecoveryLedgerCoverageActionAndBindingValidation(t *testing.T) {
	validAction := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)

	wrongMask := validAction.Clone()
	wrongMask.Precondition.Filesystem.Required = domain.FilesystemFieldType
	if err := wrongMask.Validate(); err != nil {
		t.Fatalf("wrong-mask action should remain domain-valid: %v", err)
	}
	if err := validateRecoveryAction(wrongMask); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("validateRecoveryAction(wrong mask) error = %v, want ErrRecoveryUnsupported", err)
	}

	nonRecoverable := testRecoveryAction(t, domain.ActionDeleteRecreatablePath)
	if err := validateRecoveryAction(nonRecoverable); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("validateRecoveryAction(non-recoverable) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, err := newRecoveryBinding(validAction, domain.PlanDigest{}, "ldc-"+string(bytes.Repeat([]byte{'c'}, 64)), testRecoveryReservation(t, validAction.Kind).TrashLayoutBinding); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("newRecoveryBinding(zero digest) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, err := newRecoveryBinding(validAction, planDigest, "invalid-token", testRecoveryReservation(t, validAction.Kind).TrashLayoutBinding); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("newRecoveryBinding(invalid token) error = %v, want ErrRecoveryUnsupported", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*recoveryBinding)
	}{
		{
			name: "zero digest",
			mutate: func(binding *recoveryBinding) {
				binding.actionBindingDigest = domain.ActionBindingDigest{}
			},
		},
		{
			name: "empty payload",
			mutate: func(binding *recoveryBinding) {
				binding.actionBindingPayload = nil
			},
		},
		{
			name: "zero plan digest",
			mutate: func(binding *recoveryBinding) {
				binding.planDigest = domain.PlanDigest{}
			},
		},
		{
			name: "zero root",
			mutate: func(binding *recoveryBinding) {
				binding.root = ""
			},
		},
		{
			name: "empty source",
			mutate: func(binding *recoveryBinding) {
				binding.source = pathbytes.BytePath{}
			},
		},
		{
			name: "zero action ID",
			mutate: func(binding *recoveryBinding) {
				binding.actionID = ""
			},
		},
		{
			name: "unknown action kind",
			mutate: func(binding *recoveryBinding) {
				binding.actionKind = domain.ActionKind("future_action")
			},
		},
		{
			name: "non-recoverable action kind",
			mutate: func(binding *recoveryBinding) {
				binding.actionKind = domain.ActionDeleteRecreatablePath
			},
		},
		{
			name: "unknown destination",
			mutate: func(binding *recoveryBinding) {
				binding.destination = RecoveryDestination("future_destination")
			},
		},
		{
			name: "mismatched destination",
			mutate: func(binding *recoveryBinding) {
				binding.destination = RecoveryDestinationQuarantine
			},
		},
		{
			name: "invalid token",
			mutate: func(binding *recoveryBinding) {
				binding.token = "not-a-ledger-token"
			},
		},
		{
			name: "precondition source mismatch",
			mutate: func(binding *recoveryBinding) {
				binding.precondition.Target.Filesystem.Root = mustRecoveryRoot(t, "other-state-root")
			},
		},
		{
			name: "precondition required-mask mismatch",
			mutate: func(binding *recoveryBinding) {
				binding.precondition.Required = domain.FilesystemFieldType
			},
		},
		{
			name: "malformed payload with matching digest",
			mutate: func(binding *recoveryBinding) {
				binding.actionBindingPayload = []byte{0}
				binding.actionBindingDigest = domain.ComputeActionBindingDigest(binding.actionBindingPayload)
			},
		},
		{
			name: "payload projection mismatch",
			mutate: func(binding *recoveryBinding) {
				binding.actionID = domain.ActionID("different-action")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := testRecoveryBinding(t, domain.ActionTrashPath)
			test.mutate(&binding)
			if err := validateRecoveryBinding(binding); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("validateRecoveryBinding() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}
}

func TestRecoveryLedgerCoverageFrameAndReplayValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*recoveryFrame)
	}{
		{
			name: "unsupported schema",
			mutate: func(frame *recoveryFrame) {
				frame.schemaVersion++
			},
		},
		{
			name: "unknown event",
			mutate: func(frame *recoveryFrame) {
				frame.event = RecoveryEvent("future_event")
			},
		},
		{
			name: "initial transition fields",
			mutate: func(frame *recoveryFrame) {
				frame.outcome = RecoveryOutcomeNotApplied
			},
		},
		{
			name: "ordinal above capacity",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventMetadataDispatchRecorded)
				frame.ordinal = linuxfsMaximumLedgerOrdinal() + 1
			},
		},
		{
			name: "noninitial intent",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventIntentReserved)
			},
		},
		{
			name: "noninitial missing predecessor",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventMetadataDispatchRecorded)
				frame.predecessor = [32]byte{}
			},
		},
		{
			name: "unknown reconciliation outcome",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventReconciliationResolved)
				frame.outcome = RecoveryOutcome("future_outcome")
			},
		},
		{
			name: "unknown reconciliation metadata",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventReconciliationResolved)
				frame.outcome = RecoveryOutcomeNotApplied
				frame.metadata = RecoveryMetadataDisposition("future_metadata")
			},
		},
		{
			name: "ordinary transition carries outcome",
			mutate: func(frame *recoveryFrame) {
				*frame = coverageNoninitialFrame(t, frame.binding, RecoveryEventMetadataDispatchRecorded)
				frame.outcome = RecoveryOutcomeNotApplied
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
			if err != nil {
				t.Fatalf("newIntentFrame() error = %v", err)
			}
			test.mutate(&frame)
			if err := validateRecoveryFrame(frame); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("validateRecoveryFrame() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}

	if _, err := replayRecoveryFrames(nil); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(nil) error = %v, want ErrRecoveryCorrupt", err)
	}
	if _, err := replayRecoveryFrames(make([]recoveryFrame, int(linuxfsMaximumLedgerOrdinal())+2)); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(over capacity) error = %v, want ErrRecoveryCorrupt", err)
	}

	intent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	next, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("appendRecoveryTransition() error = %v", err)
	}
	wrongOrdinal := next
	wrongOrdinal.ordinal = 2
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, wrongOrdinal}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(wrong ordinal) error = %v, want ErrRecoveryCorrupt", err)
	}

	secondBinding := testRecoveryBinding(t, domain.ActionTrashPath)
	secondBinding.token = "ldc-" + string(bytes.Repeat([]byte{'d'}, 64))
	changedBinding := coverageNoninitialFrame(t, secondBinding, RecoveryEventMetadataDispatchRecorded)
	previousDigest, err := recoveryFrameDigest(intent)
	if err != nil {
		t.Fatalf("recoveryFrameDigest() error = %v", err)
	}
	changedBinding.predecessor = previousDigest
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, changedBinding}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(changed binding) error = %v, want ErrRecoveryCorrupt", err)
	}

	illegal := coverageNoninitialFrame(t, intent.binding, RecoveryEventMoveDispatchRecorded)
	illegal.predecessor = previousDigest
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, illegal}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(illegal transition) error = %v, want ErrRecoveryCorrupt", err)
	}
	if _, err := appendRecoveryTransition(nil, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("appendRecoveryTransition(nil) error = %v, want ErrRecoveryCorrupt", err)
	}
	if _, err := recoveryFromFrame(recoveryFrame{}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("recoveryFromFrame(zero) error = %v, want ErrRecoveryCorrupt", err)
	}
	if err := (&Recovery{}).apply(recoveryFrame{}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("Recovery.apply(zero) error = %v, want ErrRecoveryCorrupt", err)
	}
}

func TestRecoveryLedgerCoverageTransitionReconciliationValidation(t *testing.T) {
	trashBinding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(trashBinding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	intentRecovery, err := replayRecoveryFrames([]recoveryFrame{intent})
	if err != nil {
		t.Fatalf("replay intent error = %v", err)
	}

	for _, test := range []struct {
		name       string
		validation func() error
		want       error
	}{
		{
			name: "ordinary transition carries reconciliation facts",
			validation: func() error {
				return validateOrdinaryTransition(RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded, ReconciliationOutcome: RecoveryOutcomeNotApplied})
			},
			want: ErrRecoveryUnsupported,
		},
		{
			name: "no-effect uses wrong event",
			validation: func() error {
				return validateNoEffectReconciliation(RecoveryDestinationTrash, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "no-effect uses wrong outcome",
			validation: func() error {
				return validateNoEffectReconciliation(RecoveryDestinationTrash, RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeMoveVerified})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "no-effect uses unknown metadata",
			validation: func() error {
				return validateNoEffectReconciliation(RecoveryDestinationTrash, RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied, MetadataDisposition: RecoveryMetadataDisposition("future_metadata")})
			},
			want: ErrRecoveryUnsupported,
		},
		{
			name: "quarantine no-effect retains metadata",
			validation: func() error {
				return validateNoEffectReconciliation(RecoveryDestinationQuarantine, RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied, MetadataDisposition: RecoveryMetadataRetained})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "move indeterminate uses wrong event",
			validation: func() error {
				return validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventMoveVerified})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "move indeterminate uses restore outcome",
			validation: func() error {
				return validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeRestoreVerified})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "move indeterminate carries metadata",
			validation: func() error {
				return validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied, MetadataDisposition: RecoveryMetadataAbsent})
			},
			want: ErrRecoveryUnsupported,
		},
		{
			name: "restore indeterminate uses wrong event",
			validation: func() error {
				return validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventRestoreVerified})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "restore indeterminate uses not-applied outcome",
			validation: func() error {
				return validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied})
			},
			want: ErrRecoveryConflict,
		},
		{
			name: "restore indeterminate carries metadata",
			validation: func() error {
				return validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeMoveVerified, MetadataDisposition: RecoveryMetadataAbsent})
			},
			want: ErrRecoveryUnsupported,
		},
		{
			name: "cannot append another intent",
			validation: func() error {
				return validateRecoveryTransition(intentRecovery, RecoveryTransition{Event: RecoveryEventIntentReserved})
			},
			want: ErrRecoveryUnsupported,
		},
		{
			name: "closed recovery",
			validation: func() error {
				closed := intentRecovery
				closed.event = RecoveryEventRestoreVerified
				return validateRecoveryTransition(closed, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
			},
			want: ErrRecoveryConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validation(); !errors.Is(err, test.want) {
				t.Fatalf("validation error = %v, want %v", err, test.want)
			}
		})
	}

	for _, outcome := range []RecoveryOutcome{RecoveryOutcomeNotApplied, RecoveryOutcomeMoveVerified} {
		if err := validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: outcome}); err != nil {
			t.Fatalf("move-indeterminate reconciliation %q error = %v", outcome, err)
		}
	}
	for _, outcome := range []RecoveryOutcome{RecoveryOutcomeMoveVerified, RecoveryOutcomeRestoreVerified} {
		if err := validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: outcome}); err != nil {
			t.Fatalf("restore-indeterminate reconciliation %q error = %v", outcome, err)
		}
	}

	moveVerified := intentRecovery
	moveVerified.event = RecoveryEventReconciliationResolved
	moveVerified.outcome = RecoveryOutcomeMoveVerified
	if err := validateRecoveryTransition(moveVerified, RecoveryTransition{Event: RecoveryEventRestoreDispatchRecorded}); err != nil {
		t.Fatalf("move-verified reconciliation should reopen restore boundary: %v", err)
	}
}

func TestRecoveryLedgerCoverageWirePathAndRecordHelpers(t *testing.T) {
	intent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	wire := toRecoveryFrameWire(intent)
	for _, test := range []struct {
		name   string
		mutate func(*recoveryFrameWire)
	}{
		{
			name: "zero binding digest",
			mutate: func(wire *recoveryFrameWire) {
				wire.ActionBindingDigest = nil
			},
		},
		{
			name: "zero plan digest",
			mutate: func(wire *recoveryFrameWire) {
				wire.PlanDigest = nil
			},
		},
		{
			name: "invalid root",
			mutate: func(wire *recoveryFrameWire) {
				wire.Root = ""
			},
		},
		{
			name: "invalid source",
			mutate: func(wire *recoveryFrameWire) {
				wire.Source = [][]byte{[]byte("..")}
			},
		},
		{
			name: "invalid action ID",
			mutate: func(wire *recoveryFrameWire) {
				wire.ActionID = ""
			},
		},
		{
			name: "wrong predecessor size",
			mutate: func(wire *recoveryFrameWire) {
				wire.PredecessorDigest = []byte{1}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := wire
			test.mutate(&candidate)
			if _, err := fromRecoveryFrameWire(candidate); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("fromRecoveryFrameWire() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}

	for name, components := range map[string][][]byte{
		"empty":        nil,
		"too many":     coveragePathComponents(maximumRecoveryPathComponents + 1),
		"too many raw": {bytes.Repeat([]byte{'x'}, maximumRecoveryPathBytes+1)},
		"dot dot":      {[]byte("..")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newBoundedRecoveryPath(components); err == nil {
				t.Fatal("newBoundedRecoveryPath() accepted an invalid bounded path")
			}
		})
	}
	if err := validateRecoveryPath(pathbytes.BytePath{}); err == nil {
		t.Fatal("validateRecoveryPath() accepted a zero path")
	}
	if clone := cloneRecoveryPath(pathbytes.BytePath{}); len(clone.Components()) != 0 {
		t.Fatalf("cloneRecoveryPath(zero) = %q, want zero path", clone.Display())
	}
	if _, err := decodeRecoveryFrame(nil); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("decodeRecoveryFrame(nil) error = %v, want ErrRecoveryCorrupt", err)
	}
	if _, err := decodeRecoveryFrame(make([]byte, maximumRecoveryFrameBytes+1)); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("decodeRecoveryFrame(oversized) error = %v, want ErrRecoveryCorrupt", err)
	}
	if _, err := recoveryFrameDigest(recoveryFrame{}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("recoveryFrameDigest(zero) error = %v, want ErrRecoveryCorrupt", err)
	}

	record := testRecoveryLedgerRecord(t, intent)
	if _, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{record, record}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryLedgerRecords(duplicate ordinal) error = %v, want ErrRecoveryCorrupt", err)
	}
	if histories, err := replayRecoveryLedgerRecords(nil); err != nil || len(histories) != 0 {
		t.Fatalf("replayRecoveryLedgerRecords(nil) = (%#v, %v), want empty histories", histories, err)
	}

	recovery, err := replayRecoveryFrames([]recoveryFrame{intent})
	if err != nil {
		t.Fatalf("replayRecoveryFrames() error = %v", err)
	}
	history := recoveryHistory{frames: []recoveryFrame{intent}, recovery: recovery}
	if _, err := findRecoveryHistory([]recoveryHistory{history}, Recovery{}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("findRecoveryHistory(missing) error = %v, want ErrRecoveryConflict", err)
	}
	if _, err := findOutstandingRecovery([]recoveryHistory{history, history}, recovery.RootID(), recovery.Source()); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("findOutstandingRecovery(duplicate) error = %v, want ErrRecoveryConflict", err)
	}
	if _, err := outstandingRecoveries([]recoveryHistory{history}, 0); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("outstandingRecoveries(zero limit) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, err := outstandingRecoveries([]recoveryHistory{history}, maximumRecoveryLedgerRecords+1); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("outstandingRecoveries(oversized limit) error = %v, want ErrRecoveryUnsupported", err)
	}
}

func TestRecoveryLedgerCoverageOpaqueSessionFailures(t *testing.T) {
	sentinel := errors.New("injected opaque session failure")
	if _, err := readRecoveryHistories(context.Background(), coverageRecoveryLedgerSession{listErr: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("readRecoveryHistories(list error) = %v, want sentinel", err)
	}
	intent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	record := testRecoveryLedgerRecord(t, intent)
	if _, err := readRecoveryHistories(context.Background(), coverageRecoveryLedgerSession{ids: []linuxfs.PrivateLedgerRecordID{record.id}, readErr: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("readRecoveryHistories(read error) = %v, want sentinel", err)
	}

	ledger := &RecoveryLedger{sessions: coverageRecoveryLedgerSessions{writeErr: sentinel}}
	if _, err := ledger.Reserve(context.Background(), testRecoveryAction(t, domain.ActionTrashPath), testRecoveryPlanDigest(t), testRecoveryReservation(t, domain.ActionTrashPath)); !errors.Is(err, sentinel) {
		t.Fatalf("Reserve(write session error) = %v, want sentinel", err)
	}
	if _, err := ledger.Transition(context.Background(), Recovery{binding: intent.binding}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded}); !errors.Is(err, sentinel) {
		t.Fatalf("Transition(write session error) = %v, want sentinel", err)
	}

	readLedger := &RecoveryLedger{sessions: coverageRecoveryLedgerSessions{readErr: sentinel}}
	if _, _, err := readLedger.FindOutstanding(context.Background(), intent.binding.root, intent.binding.source); !errors.Is(err, sentinel) {
		t.Fatalf("FindOutstanding(read session error) = %v, want sentinel", err)
	}
	if _, err := readLedger.ListOutstanding(context.Background(), 1); !errors.Is(err, sentinel) {
		t.Fatalf("ListOutstanding(read session error) = %v, want sentinel", err)
	}
	if _, err := readLedger.Reload(context.Background(), Recovery{binding: intent.binding}); !errors.Is(err, sentinel) {
		t.Fatalf("Reload(read session error) = %v, want sentinel", err)
	}

	productionSessions := linuxfsRecoveryLedgerSessions{}
	if err := productionSessions.withRead(context.Background(), func(recoveryLedgerSession) error { return nil }); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("linuxfsRecoveryLedgerSessions.withRead() error = %v, want ErrUnsupported", err)
	}
	if err := productionSessions.withWrite(context.Background(), func(recoveryLedgerSession) error { return nil }); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("linuxfsRecoveryLedgerSessions.withWrite() error = %v, want ErrUnsupported", err)
	}
}

func coverageNoninitialFrame(t *testing.T, binding recoveryBinding, event RecoveryEvent) recoveryFrame {
	t.Helper()
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	predecessor, err := recoveryFrameDigest(intent)
	if err != nil {
		t.Fatalf("recoveryFrameDigest() error = %v", err)
	}
	return recoveryFrame{
		schemaVersion: recoveryLedgerSchemaVersion,
		binding:       binding,
		event:         event,
		ordinal:       1,
		predecessor:   predecessor,
	}
}

func coveragePathComponents(count int) [][]byte {
	components := make([][]byte, count)
	for index := range components {
		components[index] = []byte{'x'}
	}
	return components
}

type coverageRecoveryLedgerSession struct {
	ids      []linuxfs.PrivateLedgerRecordID
	contents map[linuxfs.PrivateLedgerRecordID][]byte
	listErr  error
	readErr  error
}

func (session coverageRecoveryLedgerSession) Publish(context.Context, linuxfs.PrivateLedgerRecordID, []byte) error {
	return errors.New("unexpected publish through read fixture")
}

func (session coverageRecoveryLedgerSession) Read(_ context.Context, id linuxfs.PrivateLedgerRecordID, _ int) ([]byte, error) {
	if session.readErr != nil {
		return nil, session.readErr
	}
	return append([]byte(nil), session.contents[id]...), nil
}

func (session coverageRecoveryLedgerSession) List(context.Context, int) ([]linuxfs.PrivateLedgerRecordID, error) {
	if session.listErr != nil {
		return nil, session.listErr
	}
	return append([]linuxfs.PrivateLedgerRecordID(nil), session.ids...), nil
}

type coverageRecoveryLedgerSessions struct {
	readErr  error
	writeErr error
}

func (sessions coverageRecoveryLedgerSessions) withRead(_ context.Context, callback func(recoveryLedgerSession) error) error {
	if sessions.readErr != nil {
		return sessions.readErr
	}
	return callback(coverageRecoveryLedgerSession{})
}

func (sessions coverageRecoveryLedgerSessions) withWrite(_ context.Context, callback func(recoveryLedgerSession) error) error {
	if sessions.writeErr != nil {
		return sessions.writeErr
	}
	return callback(coverageRecoveryLedgerSession{})
}
