package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/planproto"
)

func TestRecoveryFrameRoundTripsCanonicalImmutableBinding(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	encoded, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encodeRecoveryFrame() error = %v", err)
	}
	decoded, err := decodeRecoveryFrame(encoded)
	if err != nil {
		t.Fatalf("decodeRecoveryFrame() error = %v", err)
	}
	recovery, err := replayRecoveryFrames([]recoveryFrame{decoded})
	if err != nil {
		t.Fatalf("replayRecoveryFrames() error = %v", err)
	}
	if recovery.Event() != RecoveryEventIntentReserved {
		t.Fatalf("recovery event = %q, want %q", recovery.Event(), RecoveryEventIntentReserved)
	}
	if recovery.Destination() != RecoveryDestinationTrash {
		t.Fatalf("recovery destination = %q, want trash", recovery.Destination())
	}
	if recovery.RootID() != binding.root || !recovery.Source().Equal(binding.source) {
		t.Fatalf("recovery source = (%q, %q), want (%q, %q)", recovery.RootID(), recovery.Source().Display(), binding.root, binding.source.Display())
	}
	if recovery.Token() != binding.token || recovery.ActionBindingDigest() != binding.actionBindingDigest {
		t.Fatal("replay changed the immutable reservation identity")
	}

	canonical, err := encodeRecoveryFrame(decoded)
	if err != nil {
		t.Fatalf("encode decoded frame: %v", err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Fatal("frame did not retain canonical bytes after decode/re-encode")
	}
}

func TestRecoveryReducerEnforcesClosedTrashAndQuarantineGraphs(t *testing.T) {
	trashBinding := testRecoveryBinding(t, domain.ActionTrashPath)
	trashIntent, err := newIntentFrame(trashBinding)
	if err != nil {
		t.Fatalf("new trash intent: %v", err)
	}
	trashHistory := []recoveryFrame{trashIntent}
	for _, transition := range []RecoveryTransition{
		{Event: RecoveryEventMetadataDispatchRecorded},
		{Event: RecoveryEventMetadataVerified},
		{Event: RecoveryEventMoveDispatchRecorded},
		{Event: RecoveryEventMoveVerified},
		{Event: RecoveryEventRestoreDispatchRecorded},
		{Event: RecoveryEventRestoreVerified},
	} {
		next, err := appendRecoveryTransition(trashHistory, transition)
		if err != nil {
			t.Fatalf("append %q: %v", transition.Event, err)
		}
		trashHistory = append(trashHistory, next)
	}
	trashRecovery, err := replayRecoveryFrames(trashHistory)
	if err != nil {
		t.Fatalf("replay full trash graph: %v", err)
	}
	if !trashRecovery.Closed() {
		t.Fatal("verified restore did not close recovery")
	}
	if _, err := appendRecoveryTransition(trashHistory, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err == nil {
		t.Fatal("closed recovery accepted another transition")
	}

	if _, err := appendRecoveryTransition([]recoveryFrame{trashIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err == nil {
		t.Fatal("trash skipped required metadata boundary")
	}

	quarantineBinding := testRecoveryBinding(t, domain.ActionQuarantinePath)
	quarantineIntent, err := newIntentFrame(quarantineBinding)
	if err != nil {
		t.Fatalf("new quarantine intent: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded}); err == nil {
		t.Fatal("quarantine accepted a metadata event")
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err != nil {
		t.Fatalf("quarantine did not permit its first move boundary: %v", err)
	}
}

func TestRecoveryReducerConstrainsIndeterminateAndCancellationFacts(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}

	cancelled, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
		MetadataDisposition:   RecoveryMetadataAbsent,
	})
	if err != nil {
		t.Fatalf("cancellation reconciliation: %v", err)
	}
	recovery, err := replayRecoveryFrames([]recoveryFrame{intent, cancelled})
	if err != nil || !recovery.Closed() {
		t.Fatalf("cancelled recovery = %#v, %v; want closed", recovery, err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
	}); err == nil {
		t.Fatal("cancellation reconciliation omitted its metadata fact")
	}

	metadataDispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("metadata dispatch: %v", err)
	}
	metadataIndeterminate, err := appendRecoveryTransition([]recoveryFrame{intent, metadataDispatch}, RecoveryTransition{Event: RecoveryEventMetadataIndeterminate})
	if err != nil {
		t.Fatalf("metadata indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{intent, metadataDispatch, metadataIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeMoveVerified,
		MetadataDisposition:   RecoveryMetadataRetained,
	}); err == nil {
		t.Fatal("metadata indeterminate accepted a content-side reconciliation outcome")
	}

	quarantineIntent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionQuarantinePath))
	if err != nil {
		t.Fatalf("new quarantine intent: %v", err)
	}
	moveDispatch, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if err != nil {
		t.Fatalf("move dispatch: %v", err)
	}
	moveIndeterminate, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch}, RecoveryTransition{Event: RecoveryEventMoveIndeterminate})
	if err != nil {
		t.Fatalf("move indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeRestoreVerified,
	}); err == nil {
		t.Fatal("move indeterminate accepted a restore-verified reconciliation without a restore dispatch")
	}

	moveVerified, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch}, RecoveryTransition{Event: RecoveryEventMoveVerified})
	if err != nil {
		t.Fatalf("move verified: %v", err)
	}
	restoreDispatch, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified}, RecoveryTransition{Event: RecoveryEventRestoreDispatchRecorded})
	if err != nil {
		t.Fatalf("restore dispatch: %v", err)
	}
	restoreIndeterminate, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch}, RecoveryTransition{Event: RecoveryEventRestoreIndeterminate})
	if err != nil {
		t.Fatalf("restore indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch, restoreIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
	}); err == nil {
		t.Fatal("restore indeterminate accepted a closed not-applied fact while the verified move remains retained")
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch, restoreIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeMoveVerified,
	}); err != nil {
		t.Fatalf("restore indeterminate did not preserve a verified retained move: %v", err)
	}
}

func TestRecoveryFramesRejectNoncanonicalBindingAndChainGaps(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	encoded, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if _, err := decodeRecoveryFrame(append(encoded, 0)); err == nil {
		t.Fatal("decoder accepted a frame with trailing noncanonical bytes")
	}

	next, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("append transition: %v", err)
	}
	next.predecessor[0] ^= 1
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, next}); err == nil {
		t.Fatal("replay accepted a predecessor digest mismatch")
	}

	badBinding := intent
	badBinding.binding.precondition.Target.Filesystem.Root = mustRecoveryRoot(t, "other-root")
	if _, err := encodeRecoveryFrame(badBinding); err == nil {
		t.Fatal("encoder accepted a precondition/root binding mismatch")
	}

	changedAction := testRecoveryAction(t, domain.ActionTrashPath)
	changedAction.Risk = domain.RiskMedium
	if err := changedAction.Validate(); err != nil {
		t.Fatalf("changed action validation: %v", err)
	}
	changedPayload, err := planproto.EncodeActionBinding(changedAction, binding.planDigest)
	if err != nil {
		t.Fatalf("EncodeActionBinding(changed action) error = %v", err)
	}
	badPayload := intent
	badPayload.binding.actionBindingPayload = changedPayload
	if _, err := encodeRecoveryFrame(badPayload); err == nil {
		t.Fatal("encoder accepted a binding payload that does not match the stored action digest")
	}
}

func TestRecoveryFrameDecoderRejectsStrictOuterCBORViolations(t *testing.T) {
	intent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	valid, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if len(valid) == 0 || valid[0] != 0xb0 {
		t.Fatalf("canonical frame map header = %x, want b0 for the fixed v1 field count", valid)
	}

	unknownFields := testRecoveryFrameWireValues(toRecoveryFrameWire(intent))
	unknownFields["future_field"] = "retained-but-unsupported"
	unknown, err := recoveryFrameEncMode.Marshal(unknownFields)
	if err != nil {
		t.Fatalf("encode unknown-field frame: %v", err)
	}
	duplicateKey, err := recoveryFrameEncMode.Marshal("event")
	if err != nil {
		t.Fatalf("encode duplicate key: %v", err)
	}
	duplicateValue, err := recoveryFrameEncMode.Marshal(string(RecoveryEventIntentReserved))
	if err != nil {
		t.Fatalf("encode duplicate value: %v", err)
	}
	duplicate := append([]byte{valid[0] + 1}, valid[1:]...)
	duplicate = append(duplicate, duplicateKey...)
	duplicate = append(duplicate, duplicateValue...)
	indefinite := append([]byte{0xbf}, valid[1:]...)
	indefinite = append(indefinite, 0xff)
	nonMinimal := append([]byte{0xb8, 0x10}, valid[1:]...)
	tagged := append([]byte{0xc0}, valid...)

	for name, encoded := range map[string][]byte{
		"unknown field":          unknown,
		"duplicate key":          duplicate,
		"indefinite length map":  indefinite,
		"non-minimal map header": nonMinimal,
		"tagged frame":           tagged,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRecoveryFrame(encoded); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("decodeRecoveryFrame() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}
}

func testRecoveryFrameWireValues(wire recoveryFrameWire) map[string]any {
	return map[string]any{
		"schema_version":         wire.SchemaVersion,
		"action_binding_digest":  wire.ActionBindingDigest,
		"action_binding_payload": wire.ActionBindingPayload,
		"plan_digest":            wire.PlanDigest,
		"token":                  wire.Token,
		"root":                   wire.Root,
		"source":                 wire.Source,
		"action_id":              wire.ActionID,
		"action_kind":            wire.ActionKind,
		"destination":            wire.Destination,
		"precondition":           wire.Precondition,
		"event":                  wire.Event,
		"ordinal":                wire.Ordinal,
		"predecessor_digest":     wire.PredecessorDigest,
		"reconciliation_outcome": wire.ReconciliationOutcome,
		"metadata_disposition":   wire.MetadataDisposition,
	}
}

func TestReplayRecoveryLedgerRecordsRejectsIdentifierMismatchAndDuplicateOutstandingSource(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	record := testRecoveryLedgerRecord(t, intent)
	wrongID, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, 1)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	record.id = wrongID
	if _, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{record}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("identifier/frame mismatch error = %v, want ErrRecoveryCorrupt", err)
	}

	first := testRecoveryLedgerRecord(t, intent)
	secondBinding := binding
	secondBinding.token = "ldc-" + string(bytes.Repeat([]byte{'b'}, 64))
	secondIntent, err := newIntentFrame(secondBinding)
	if err != nil {
		t.Fatalf("new second intent: %v", err)
	}
	second := testRecoveryLedgerRecord(t, secondIntent)
	if _, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{second, first}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("duplicate outstanding source error = %v, want ErrRecoveryConflict", err)
	}
}

func TestReplayRecoveryLedgerRecordsUsesImmutableRecordOrderAndRejectsCapacity(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionQuarantinePath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	dispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if err != nil {
		t.Fatalf("append dispatch: %v", err)
	}
	verified, err := appendRecoveryTransition([]recoveryFrame{intent, dispatch}, RecoveryTransition{Event: RecoveryEventMoveVerified})
	if err != nil {
		t.Fatalf("append verification: %v", err)
	}
	histories, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{
		testRecoveryLedgerRecord(t, verified),
		testRecoveryLedgerRecord(t, intent),
		testRecoveryLedgerRecord(t, dispatch),
	})
	if err != nil {
		t.Fatalf("out-of-order replay error = %v", err)
	}
	if len(histories) != 1 || histories[0].recovery.Event() != RecoveryEventMoveVerified {
		t.Fatalf("out-of-order replay result = %#v, want move-verified recovery", histories)
	}

	overCapacity := make([]recoveryLedgerRecord, maximumRecoveryLedgerRecords+1)
	for index := range overCapacity {
		overCapacity[index] = testRecoveryLedgerRecord(t, intent)
	}
	if _, err := replayRecoveryLedgerRecords(overCapacity); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("over-capacity replay error = %v, want ErrRecoveryUnsupported", err)
	}
}

func TestRecoveryLedgerPublicationCapacityBlocksTheNextRecord(t *testing.T) {
	atCapacity := []recoveryHistory{{frames: make([]recoveryFrame, maximumRecoveryLedgerRecords)}}
	if err := requireRecoveryLedgerPublicationCapacity(atCapacity); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("exact-capacity publication guard error = %v, want ErrRecoveryConflict", err)
	}

	belowCapacity := []recoveryHistory{{frames: make([]recoveryFrame, maximumRecoveryLedgerRecords-1)}}
	if err := requireRecoveryLedgerPublicationCapacity(belowCapacity); err != nil {
		t.Fatalf("below-capacity publication guard error = %v", err)
	}
}

func TestRecoverySourceKeySeparatesRawComponentBoundaries(t *testing.T) {
	first := testRecoveryBinding(t, domain.ActionTrashPath)
	second := first
	firstPath, err := pathbytes.New([][]byte{[]byte("a"), []byte("bc")})
	if err != nil {
		t.Fatalf("first path: %v", err)
	}
	secondPath, err := pathbytes.New([][]byte{[]byte("ab"), []byte("c")})
	if err != nil {
		t.Fatalf("second path: %v", err)
	}
	first.source = firstPath
	second.source = secondPath
	if recoverySourceKey(first) == recoverySourceKey(second) {
		t.Fatal("recovery source key merged distinct raw-component paths")
	}
}

func TestRecoveryLedgerPublicLifecycleReplaysRetainedFacts(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	action := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)

	reserved, err := ledger.Reserve(ctx, action, planDigest)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if reserved.Event() != RecoveryEventIntentReserved || reserved.Ordinal() != 0 {
		t.Fatalf("reserved recovery = (%q, %d), want intent reservation at ordinal zero", reserved.Event(), reserved.Ordinal())
	}
	if _, err := ledger.Reserve(ctx, action, planDigest); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("duplicate Reserve() error = %v, want ErrRecoveryConflict", err)
	}

	found, ok, err := ledger.FindOutstanding(ctx, reserved.RootID(), reserved.Source())
	if err != nil || !ok || found.Token() != reserved.Token() {
		t.Fatalf("FindOutstanding() = (%#v, %t, %v), want retained reservation", found, ok, err)
	}
	outstanding, err := ledger.ListOutstanding(ctx, 1)
	if err != nil || len(outstanding) != 1 || outstanding[0].Token() != reserved.Token() {
		t.Fatalf("ListOutstanding() = (%#v, %v), want retained reservation", outstanding, err)
	}

	dispatch, err := ledger.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("metadata dispatch Transition() error = %v", err)
	}
	if dispatch.Ordinal() != 1 {
		t.Fatalf("metadata dispatch ordinal = %d, want 1", dispatch.Ordinal())
	}
	verified, err := ledger.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMetadataVerified})
	if err != nil {
		t.Fatalf("stale-view metadata verification Transition() error = %v", err)
	}
	if verified.Ordinal() != 2 || verified.Event() != RecoveryEventMetadataVerified {
		t.Fatalf("verified recovery = (%q, %d), want metadata verified ordinal 2", verified.Event(), verified.Ordinal())
	}

	fresh := newTestRecoveryLedger(sessions)
	reloaded, err := fresh.Reload(ctx, reserved)
	if err != nil {
		t.Fatalf("fresh Reload() error = %v", err)
	}
	if reloaded.Event() != RecoveryEventMetadataVerified || reloaded.Ordinal() != 2 {
		t.Fatalf("fresh Reload() = (%q, %d), want metadata verified ordinal 2", reloaded.Event(), reloaded.Ordinal())
	}
	if reloaded.PlanDigest() != planDigest || reloaded.ActionID() != action.ID || reloaded.ActionKind() != action.Kind || reloaded.Outcome() != "" || reloaded.MetadataDisposition() != "" {
		t.Fatal("fresh Reload() changed immutable action facts or added transition facts")
	}

	closed, err := fresh.Transition(ctx, reserved, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
		MetadataDisposition:   RecoveryMetadataAbsent,
	})
	if err != nil {
		t.Fatalf("closing Transition() error = %v", err)
	}
	if !closed.Closed() {
		t.Fatal("no-effect reconciliation did not close the retained recovery")
	}
	if closed.Outcome() != RecoveryOutcomeNotApplied || closed.MetadataDisposition() != RecoveryMetadataAbsent {
		t.Fatal("closed recovery did not retain reconciliation facts")
	}
	if _, ok, err := fresh.FindOutstanding(ctx, reserved.RootID(), reserved.Source()); err != nil || ok {
		t.Fatalf("FindOutstanding() after close = (_, %t, %v), want not found", ok, err)
	}

	replacement, err := fresh.Reserve(ctx, action, planDigest)
	if err != nil {
		t.Fatalf("Reserve() after closed recovery error = %v", err)
	}
	if replacement.Token() == reserved.Token() {
		t.Fatal("new reservation reused a closed recovery token")
	}
}

func TestRecoveryLedgerPublicQueriesRemainBoundedAndExact(t *testing.T) {
	ctx := context.Background()
	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	first := testRecoveryAction(t, domain.ActionTrashPath)
	second := testRecoveryActionAtPath(t, domain.ActionQuarantinePath, [][]byte{[]byte("cache"), []byte("other")})
	planDigest := testRecoveryPlanDigest(t)
	if _, err := ledger.Reserve(ctx, first, planDigest); err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	if _, err := ledger.Reserve(ctx, second, planDigest); err != nil {
		t.Fatalf("second Reserve() error = %v", err)
	}

	if _, err := ledger.ListOutstanding(ctx, 1); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("bounded ListOutstanding() error = %v, want ErrRecoveryUnsupported", err)
	}
	missing, err := pathbytes.New([][]byte{[]byte("cache"), []byte("missing")})
	if err != nil {
		t.Fatalf("missing path: %v", err)
	}
	if _, found, err := ledger.FindOutstanding(ctx, first.Target.Filesystem.Root, missing); err != nil || found {
		t.Fatalf("FindOutstanding(missing) = (_, %t, %v), want not found", found, err)
	}
}

func TestRecoveryLedgerPublicPublicationInterruptionsReturnReloadableCandidates(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	action := testRecoveryAction(t, domain.ActionQuarantinePath)
	planDigest := testRecoveryPlanDigest(t)

	sessions.interruptNextPublish()
	reserved, err := ledger.Reserve(ctx, action, planDigest)
	if !errors.Is(err, linuxfs.ErrInterrupted) || reserved.Token() == "" {
		t.Fatalf("interrupted Reserve() = (%#v, %v), want candidate plus ErrInterrupted", reserved, err)
	}
	fresh := newTestRecoveryLedger(sessions)
	reloaded, err := fresh.Reload(ctx, reserved)
	if err != nil || reloaded.Token() != reserved.Token() {
		t.Fatalf("Reload() after interrupted reserve = (%#v, %v), want retained candidate", reloaded, err)
	}

	sessions.interruptNextPublish()
	updated, err := fresh.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if !errors.Is(err, linuxfs.ErrInterrupted) || updated.Event() != RecoveryEventMoveDispatchRecorded {
		t.Fatalf("interrupted Transition() = (%#v, %v), want move-dispatch candidate plus ErrInterrupted", updated, err)
	}
	reloaded, err = newTestRecoveryLedger(sessions).Reload(ctx, reserved)
	if err != nil || reloaded.Event() != RecoveryEventMoveDispatchRecorded || reloaded.Ordinal() != 1 {
		t.Fatalf("Reload() after interrupted transition = (%#v, %v), want retained move dispatch", reloaded, err)
	}
}

func TestRecoveryLedgerPublicReadersFailClosedWithoutRepairingCorruption(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	recovery, err := recoveryFromFrame(intent)
	if err != nil {
		t.Fatalf("recoveryFromFrame() error = %v", err)
	}
	id, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, 0)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	corrupt := []byte{0xff}
	sessions.setRecord(id, corrupt)

	for name, call := range map[string]func() error{
		"find": func() error {
			_, _, err := ledger.FindOutstanding(ctx, recovery.RootID(), recovery.Source())
			return err
		},
		"list": func() error {
			_, err := ledger.ListOutstanding(ctx, 1)
			return err
		},
		"reload": func() error {
			_, err := ledger.Reload(ctx, recovery)
			return err
		},
		"reserve": func() error {
			_, err := ledger.Reserve(ctx, testRecoveryAction(t, domain.ActionTrashPath), testRecoveryPlanDigest(t))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("%s error = %v, want ErrRecoveryCorrupt", name, err)
			}
			if retained := sessions.record(id); !bytes.Equal(retained, corrupt) {
				t.Fatalf("%s repaired corrupt record = %x, want %x", name, retained, corrupt)
			}
		})
	}
}

func TestRecoveryLedgerPublicGuardsAndProductionConstructor(t *testing.T) {
	if _, err := NewRecoveryLedger(nil); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("NewRecoveryLedger(nil) error = %v, want ErrRecoveryUnsupported", err)
	}
	production, err := NewRecoveryLedger(&linuxfs.PrivateDirectoryLease{})
	if err != nil {
		t.Fatalf("NewRecoveryLedger(zero lease) error = %v", err)
	}
	action := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)
	if _, err := production.Reserve(context.Background(), action, planDigest); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("Reserve() through unqualified production lease error = %v, want linuxfs.ErrUnsupported", err)
	}
	if _, _, err := production.FindOutstanding(context.Background(), action.Target.Filesystem.Root, action.Target.Filesystem.Path); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("FindOutstanding() through unqualified production lease error = %v, want linuxfs.ErrUnsupported", err)
	}

	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	if _, err := ledger.Reserve(nil, action, planDigest); !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("Reserve(nil context) error = %v, want ErrInterrupted", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.Reserve(cancelled, action, planDigest); !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("Reserve(cancelled context) error = %v, want ErrInterrupted", err)
	}
	if _, err := ledger.Reserve(context.Background(), action, domain.PlanDigest{}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(zero digest) error = %v, want ErrRecoveryUnsupported", err)
	}
	unsupportedAction := action.Clone()
	unsupportedAction.Kind = domain.ActionDeleteRecreatablePath
	if _, err := ledger.Reserve(context.Background(), unsupportedAction, planDigest); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(unsupported action) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, err := ledger.Transition(context.Background(), Recovery{}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Transition(zero recovery) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, _, err := ledger.FindOutstanding(context.Background(), "", action.Target.Filesystem.Path); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("FindOutstanding(zero root) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, _, err := ledger.FindOutstanding(context.Background(), action.Target.Filesystem.Root, pathbytes.BytePath{}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("FindOutstanding(empty path) error = %v, want ErrRecoveryUnsupported", err)
	}
	for _, limit := range []int{0, maximumRecoveryLedgerRecords + 1} {
		if _, err := ledger.ListOutstanding(context.Background(), limit); !errors.Is(err, ErrRecoveryUnsupported) {
			t.Fatalf("ListOutstanding(%d) error = %v, want ErrRecoveryUnsupported", limit, err)
		}
	}

	var nilLedger *RecoveryLedger
	if _, err := nilLedger.Reserve(context.Background(), action, planDigest); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("nil ledger Reserve() error = %v, want ErrRecoveryUnsupported", err)
	}
}

func testRecoveryActionAtPath(t *testing.T, kind domain.ActionKind, components [][]byte) domain.Action {
	t.Helper()
	action := testRecoveryAction(t, kind)
	path, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(action.Target.Filesystem.Root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	action.Target = target
	action.Evidence[0].Filesystem.Target = target
	action.Precondition.Filesystem.Target = target
	if err := action.Validate(); err != nil {
		t.Fatalf("test action with source validation error = %v", err)
	}
	return action
}

// testRecoveryLedgerSessions is a package-local test seam. It models only the
// opaque session contract used by state; it grants no path or descriptor
// authority and is not a production storage implementation.
type testRecoveryLedgerSessions struct {
	mu                      sync.Mutex
	records                 map[linuxfs.PrivateLedgerRecordID][]byte
	listErr                 error
	readErr                 error
	prePublishErr           error
	interruptAfterNextWrite bool
}

func newTestRecoveryLedgerSessions() *testRecoveryLedgerSessions {
	return &testRecoveryLedgerSessions{records: make(map[linuxfs.PrivateLedgerRecordID][]byte)}
}

func newTestRecoveryLedger(sessions *testRecoveryLedgerSessions) *RecoveryLedger {
	return &RecoveryLedger{sessions: sessions}
}

func (sessions *testRecoveryLedgerSessions) withRead(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return sessions.withSession(ctx, false, callback)
}

func (sessions *testRecoveryLedgerSessions) withWrite(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return sessions.withSession(ctx, true, callback)
}

func (sessions *testRecoveryLedgerSessions) withSession(ctx context.Context, writable bool, callback func(recoveryLedgerSession) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: test session context: %v", linuxfs.ErrInterrupted, err)
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return callback(testRecoveryLedgerSession{sessions: sessions, writable: writable})
}

func (sessions *testRecoveryLedgerSessions) interruptNextPublish() {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.interruptAfterNextWrite = true
}

func (sessions *testRecoveryLedgerSessions) failNextList(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.listErr = err
}

func (sessions *testRecoveryLedgerSessions) failNextRead(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.readErr = err
}

func (sessions *testRecoveryLedgerSessions) failNextPublishBeforeCreate(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.prePublishErr = err
}

func (sessions *testRecoveryLedgerSessions) setRecord(id linuxfs.PrivateLedgerRecordID, contents []byte) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.records[id] = append([]byte(nil), contents...)
}

func (sessions *testRecoveryLedgerSessions) record(id linuxfs.PrivateLedgerRecordID) []byte {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return append([]byte(nil), sessions.records[id]...)
}

func (sessions *testRecoveryLedgerSessions) count() int {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return len(sessions.records)
}

type testRecoveryLedgerSession struct {
	sessions *testRecoveryLedgerSessions
	writable bool
}

func (session testRecoveryLedgerSession) Publish(ctx context.Context, id linuxfs.PrivateLedgerRecordID, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: test publish context: %v", linuxfs.ErrInterrupted, err)
	}
	if !session.writable {
		return fmt.Errorf("%w: test read-only session cannot publish", linuxfs.ErrUnsupported)
	}
	if session.sessions.prePublishErr != nil {
		err := session.sessions.prePublishErr
		session.sessions.prePublishErr = nil
		return err
	}
	if _, exists := session.sessions.records[id]; exists {
		return fmt.Errorf("%w: test ledger record already exists", linuxfs.ErrUnsupported)
	}
	session.sessions.records[id] = append([]byte(nil), contents...)
	if session.sessions.interruptAfterNextWrite {
		session.sessions.interruptAfterNextWrite = false
		return fmt.Errorf("%w: injected post-create publication ambiguity", linuxfs.ErrInterrupted)
	}
	return nil
}

func (session testRecoveryLedgerSession) Read(ctx context.Context, id linuxfs.PrivateLedgerRecordID, maximumBytes int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: test read context: %v", linuxfs.ErrInterrupted, err)
	}
	if session.sessions.readErr != nil {
		err := session.sessions.readErr
		session.sessions.readErr = nil
		return nil, err
	}
	contents, found := session.sessions.records[id]
	if !found || len(contents) > maximumBytes {
		return nil, fmt.Errorf("%w: test ledger record is unavailable", linuxfs.ErrUnsupported)
	}
	return append([]byte(nil), contents...), nil
}

func (session testRecoveryLedgerSession) List(ctx context.Context, limit int) ([]linuxfs.PrivateLedgerRecordID, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: test list context: %v", linuxfs.ErrInterrupted, err)
	}
	if session.sessions.listErr != nil {
		err := session.sessions.listErr
		session.sessions.listErr = nil
		return nil, err
	}
	if limit <= 0 || len(session.sessions.records) > limit {
		return nil, fmt.Errorf("%w: test ledger list limit", linuxfs.ErrUnsupported)
	}
	ids := make([]linuxfs.PrivateLedgerRecordID, 0, len(session.sessions.records))
	for id := range session.sessions.records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		if ids[left].ActionBindingDigest() == ids[right].ActionBindingDigest() {
			if ids[left].Token() == ids[right].Token() {
				return ids[left].Ordinal() < ids[right].Ordinal()
			}
			return ids[left].Token() < ids[right].Token()
		}
		return ids[left].ActionBindingDigest() < ids[right].ActionBindingDigest()
	})
	return ids, nil
}

func testRecoveryLedgerRecord(t *testing.T, frame recoveryFrame) recoveryLedgerRecord {
	t.Helper()
	contents, err := encodeRecoveryFrame(frame)
	if err != nil {
		t.Fatalf("encodeRecoveryFrame() error = %v", err)
	}
	id, err := linuxfs.NewPrivateLedgerRecordID(frame.binding.actionBindingDigest.String(), frame.binding.token, frame.ordinal)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	return recoveryLedgerRecord{id: id, contents: contents}
}

func testRecoveryBinding(t *testing.T, kind domain.ActionKind) recoveryBinding {
	t.Helper()
	binding, err := newRecoveryBinding(testRecoveryAction(t, kind), testRecoveryPlanDigest(t), "ldc-"+string(bytes.Repeat([]byte{'a'}, 64)))
	if err != nil {
		t.Fatalf("newRecoveryBinding() error = %v", err)
	}
	return binding
}

func testRecoveryAction(t *testing.T, kind domain.ActionKind) domain.Action {
	t.Helper()
	root := mustRecoveryRoot(t, "state-test-root")
	path, err := pathbytes.New([][]byte{[]byte("cache"), []byte("item")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	snapshot := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 8},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 1},
		Inode:       domain.Uint64Fact{Known: true, Value: 42},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true, Value: 1000},
		GID:         domain.Uint32Fact{Known: true, Value: 1000},
		Mode:        domain.Uint32Fact{Known: true, Value: 0o600},
		LinkCount:   domain.Uint64Fact{Known: true, Value: 1},
		Size:        domain.Uint64Fact{Known: true, Value: 4096},
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		MountID:     domain.Uint64Fact{Known: true, Value: 17},
	}
	actionID, err := domain.NewActionID("state-ledger-action")
	if err != nil {
		t.Fatalf("NewActionID() error = %v", err)
	}
	capability, err := domain.NewCapabilityID("state-ledger-capability")
	if err != nil {
		t.Fatalf("NewCapabilityID() error = %v", err)
	}
	action := domain.Action{
		ID:     actionID,
		Kind:   kind,
		Target: target,
		Evidence: []domain.Evidence{{
			Kind:       domain.EvidenceFilesystemIdentity,
			Filesystem: &domain.FilesystemEvidence{Target: target, Snapshot: snapshot},
		}},
		Precondition: domain.Precondition{
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
				Snapshot: snapshot,
			},
		},
		Risk:                  domain.RiskLow,
		Reversibility:         domain.ReversibilityRecoverable,
		EstimatedEffect:       domain.SizeFacts{LinkCount: domain.Uint64Fact{Known: true, Value: 1}},
		RequiredCapability:    capability,
		ProviderGuarantee:     domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeReadOnlyInventory},
		ExpectedPostcondition: domain.PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("test action validation: %v", err)
	}
	return action
}

func testRecoveryPlanDigest(t *testing.T) domain.PlanDigest {
	t.Helper()
	digest, err := domain.NewPlanDigest(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	return digest
}

func mustRecoveryRoot(t *testing.T, value string) domain.TrustedRootID {
	t.Helper()
	root, err := domain.NewTrustedRootID(value)
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	return root
}
