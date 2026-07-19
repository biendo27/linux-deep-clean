package state

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestRecoveryLedgerPublicAdapterPropagatesSessionFailuresWithoutCreatingFacts(t *testing.T) {
	ctx := context.Background()
	action := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)

	t.Run("list failure", func(t *testing.T) {
		sessions := newTestRecoveryLedgerSessions()
		ledger := newTestRecoveryLedger(sessions)
		injected := errors.New("injected list failure")
		sessions.failNextList(injected)

		if recovery, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, injected) || recovery.Token() != "" {
			t.Fatalf("Reserve() = (%#v, %v), want no recovery plus injected list failure", recovery, err)
		}
		if records := sessions.count(); records != 0 {
			t.Fatalf("list failure created %d records, want none", records)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		sessions := newTestRecoveryLedgerSessions()
		ledger := newTestRecoveryLedger(sessions)
		reserved, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind))
		if err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		injected := errors.New("injected read failure")
		sessions.failNextRead(injected)

		if _, _, err := ledger.FindOutstanding(ctx, reserved.RootID(), reserved.Source()); !errors.Is(err, injected) {
			t.Fatalf("FindOutstanding() error = %v, want injected read failure", err)
		}
		if records := sessions.count(); records != 1 {
			t.Fatalf("read failure changed retained record count to %d, want 1", records)
		}
	})

	t.Run("pre-create publication failure", func(t *testing.T) {
		sessions := newTestRecoveryLedgerSessions()
		ledger := newTestRecoveryLedger(sessions)
		injected := errors.New("injected pre-create publication failure")
		sessions.failNextPublishBeforeCreate(injected)

		if recovery, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, injected) || recovery.Token() != "" {
			t.Fatalf("Reserve() = (%#v, %v), want no recovery plus injected publication failure", recovery, err)
		}
		if records := sessions.count(); records != 0 {
			t.Fatalf("pre-create failure created %d records, want none", records)
		}
	})
}

func TestRecoveryLedgerPublicAdapterRejectsMissingAndIllegalTransitionIdentities(t *testing.T) {
	ctx := context.Background()
	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	missing, err := recoveryFromFrame(intent)
	if err != nil {
		t.Fatalf("recoveryFromFrame() error = %v", err)
	}
	if _, err := ledger.Reload(ctx, missing); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("Reload(missing) error = %v, want ErrRecoveryConflict", err)
	}
	if _, err := ledger.Transition(ctx, missing, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("Transition(missing) error = %v, want ErrRecoveryConflict", err)
	}

	reserved, err := ledger.Reserve(ctx, testRecoveryAction(t, domain.ActionTrashPath), testRecoveryPlanDigest(t), testRecoveryReservation(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if _, err := ledger.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("Transition(skipping metadata) error = %v, want ErrRecoveryConflict", err)
	}
}

func TestRecoveryLedgerPublicReserveBlocksAtExactRetainedRecordCapacity(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	planDigest := testRecoveryPlanDigest(t)

	for index := 0; index < maximumRecoveryLedgerRecords; index++ {
		action := testRecoveryActionAtPath(t, domain.ActionQuarantinePath, [][]byte{
			[]byte("cache"),
			[]byte(fmt.Sprintf("capacity-%03d", index)),
		})
		binding, err := newRecoveryBinding(action, planDigest, fmt.Sprintf("ldc-%064x", index+1), testRecoveryReservation(t, action.Kind).TrashLayoutBinding)
		if err != nil {
			t.Fatalf("newRecoveryBinding(%d) error = %v", index, err)
		}
		intent, err := newIntentFrame(binding)
		if err != nil {
			t.Fatalf("newIntentFrame(%d) error = %v", index, err)
		}
		record := testRecoveryLedgerRecord(t, intent)
		sessions.setRecord(record.id, record.contents)
	}
	if records := sessions.count(); records != maximumRecoveryLedgerRecords {
		t.Fatalf("retained record count = %d, want %d", records, maximumRecoveryLedgerRecords)
	}
	if outstanding, err := ledger.ListOutstanding(ctx, maximumRecoveryLedgerRecords); err != nil || len(outstanding) != maximumRecoveryLedgerRecords {
		t.Fatalf("ListOutstanding(exact capacity) = (%d recoveries, %v), want all retained records", len(outstanding), err)
	}

	if _, err := ledger.Reserve(ctx, testRecoveryActionAtPath(t, domain.ActionQuarantinePath, [][]byte{[]byte("cache"), []byte("next")}), planDigest, testRecoveryReservation(t, domain.ActionQuarantinePath)); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("Reserve() at exact capacity error = %v, want ErrRecoveryConflict", err)
	}
	if records := sessions.count(); records != maximumRecoveryLedgerRecords {
		t.Fatalf("capacity rejection changed retained record count to %d, want %d", records, maximumRecoveryLedgerRecords)
	}
}

func TestRecoveryLedgerTransitionRejectsLegacyHistoryBeforeCapacity(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	planDigest := testRecoveryPlanDigest(t)

	legacyBinding := testLegacyRecoveryBinding(t, domain.ActionTrashPath)
	legacyIntent, err := newIntentFrameForSchema(legacyBinding, recoveryLedgerSchemaVersionV1)
	if err != nil {
		t.Fatalf("newIntentFrameForSchema(v1) error = %v", err)
	}
	legacy, err := recoveryFromFrame(legacyIntent)
	if err != nil {
		t.Fatalf("recoveryFromFrame(v1) error = %v", err)
	}
	legacyRecord := testRecoveryLedgerRecord(t, legacyIntent)
	sessions.setRecord(legacyRecord.id, legacyRecord.contents)

	for index := 1; index < maximumRecoveryLedgerRecords; index++ {
		action := testRecoveryActionAtPath(t, domain.ActionQuarantinePath, [][]byte{
			[]byte("cache"),
			[]byte(fmt.Sprintf("legacy-capacity-%03d", index)),
		})
		binding, err := newRecoveryBinding(action, planDigest, fmt.Sprintf("ldc-%064x", index), testRecoveryReservation(t, action.Kind).TrashLayoutBinding)
		if err != nil {
			t.Fatalf("newRecoveryBinding(%d) error = %v", index, err)
		}
		intent, err := newIntentFrame(binding)
		if err != nil {
			t.Fatalf("newIntentFrame(%d) error = %v", index, err)
		}
		record := testRecoveryLedgerRecord(t, intent)
		sessions.setRecord(record.id, record.contents)
	}
	if records := sessions.count(); records != maximumRecoveryLedgerRecords {
		t.Fatalf("retained record count = %d, want %d", records, maximumRecoveryLedgerRecords)
	}

	if _, err := ledger.Transition(ctx, legacy, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Transition(v1 at exact capacity) error = %v, want ErrRecoveryUnsupported", err)
	}
	if records := sessions.count(); records != maximumRecoveryLedgerRecords {
		t.Fatalf("legacy transition rejection changed retained record count to %d, want %d", records, maximumRecoveryLedgerRecords)
	}
}
