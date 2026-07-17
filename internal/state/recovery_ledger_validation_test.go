package state

import (
	"bytes"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestRecoveryBindingValidatorFailsClosedForEveryStoredProjectionClass(t *testing.T) {
	for name, mutate := range map[string]func(*recoveryBinding){
		"zero action digest": func(binding *recoveryBinding) {
			binding.actionBindingDigest = domain.ActionBindingDigest{}
		},
		"empty action payload": func(binding *recoveryBinding) {
			binding.actionBindingPayload = nil
		},
		"zero plan digest": func(binding *recoveryBinding) {
			binding.planDigest = domain.PlanDigest{}
		},
		"zero root": func(binding *recoveryBinding) {
			binding.root = ""
		},
		"empty source": func(binding *recoveryBinding) {
			binding.source = pathbytes.BytePath{}
		},
		"zero action ID": func(binding *recoveryBinding) {
			binding.actionID = ""
		},
		"unknown action kind": func(binding *recoveryBinding) {
			binding.actionKind = domain.ActionKind("future")
		},
		"unrecoverable action kind": func(binding *recoveryBinding) {
			binding.actionKind = domain.ActionDeleteRecreatablePath
		},
		"unknown destination": func(binding *recoveryBinding) {
			binding.destination = RecoveryDestination("future")
		},
		"destination mismatch": func(binding *recoveryBinding) {
			binding.destination = RecoveryDestinationQuarantine
		},
		"invalid token": func(binding *recoveryBinding) {
			binding.token = "caller-chosen-token"
		},
		"precondition target mismatch": func(binding *recoveryBinding) {
			binding.precondition.Target = domain.Target{}
		},
		"precondition root mismatch": func(binding *recoveryBinding) {
			binding.precondition.Target.Filesystem.Root = mustRecoveryRoot(t, "other-root")
		},
		"precondition mask mismatch": func(binding *recoveryBinding) {
			binding.precondition.Required = 0
		},
		"precondition snapshot invalid": func(binding *recoveryBinding) {
			binding.precondition.Snapshot.Inode.Known = false
		},
		"payload digest mismatch": func(binding *recoveryBinding) {
			binding.actionBindingPayload = append(append([]byte(nil), binding.actionBindingPayload...), 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			binding := testRecoveryBinding(t, domain.ActionTrashPath)
			mutate(&binding)
			if err := validateRecoveryBinding(binding); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("validateRecoveryBinding() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}
}

func TestRecoveryFrameAndReplayValidatorsRejectInvalidStoredState(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	dispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("appendRecoveryTransition() error = %v", err)
	}

	for name, frame := range map[string]recoveryFrame{
		"future schema": func() recoveryFrame { value := intent; value.schemaVersion++; return value }(),
		"future event":  func() recoveryFrame { value := dispatch; value.event = RecoveryEvent("future"); return value }(),
		"ordinal overflow": func() recoveryFrame {
			value := dispatch
			value.ordinal = linuxfsMaximumLedgerOrdinal() + 1
			return value
		}(),
		"intent carries outcome":         func() recoveryFrame { value := intent; value.outcome = RecoveryOutcomeNotApplied; return value }(),
		"intent carries metadata":        func() recoveryFrame { value := intent; value.metadata = RecoveryMetadataAbsent; return value }(),
		"noninitial intent event":        func() recoveryFrame { value := dispatch; value.event = RecoveryEventIntentReserved; return value }(),
		"noninitial missing predecessor": func() recoveryFrame { value := dispatch; value.predecessor = [32]byte{}; return value }(),
		"reconciliation unknown outcome": func() recoveryFrame {
			value := dispatch
			value.event = RecoveryEventReconciliationResolved
			value.outcome = RecoveryOutcome("future")
			return value
		}(),
		"ordinary carries reconciliation outcome": func() recoveryFrame { value := dispatch; value.outcome = RecoveryOutcomeMoveVerified; return value }(),
	} {
		t.Run(name, func(t *testing.T) {
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
	wrongOrdinal := dispatch
	wrongOrdinal.ordinal++
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, wrongOrdinal}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(wrong ordinal) error = %v, want ErrRecoveryCorrupt", err)
	}

	secondBinding := testRecoveryBinding(t, domain.ActionTrashPath)
	secondBinding.token = "ldc-" + string(bytes.Repeat([]byte{'b'}, 64))
	secondIntent, err := newIntentFrame(secondBinding)
	if err != nil {
		t.Fatalf("new second intent: %v", err)
	}
	changedBinding, err := appendRecoveryTransition([]recoveryFrame{secondIntent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("append second transition: %v", err)
	}
	changedBinding.predecessor, err = recoveryFrameDigest(intent)
	if err != nil {
		t.Fatalf("recoveryFrameDigest(intent) error = %v", err)
	}
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, changedBinding}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(changed immutable binding) error = %v, want ErrRecoveryCorrupt", err)
	}
}

func TestRecoveryTransitionValidationRejectsClosedOutcomeAndMetadataCombinations(t *testing.T) {
	if err := validateOrdinaryTransition(RecoveryTransition{Event: RecoveryEventMoveVerified, ReconciliationOutcome: RecoveryOutcomeMoveVerified}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("validateOrdinaryTransition(outcome) error = %v, want ErrRecoveryUnsupported", err)
	}
	if err := validateNoEffectReconciliation(RecoveryDestinationTrash, RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeMoveVerified}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("validateNoEffectReconciliation(wrong outcome) error = %v, want ErrRecoveryConflict", err)
	}
	if err := validateNoEffectReconciliation(RecoveryDestinationQuarantine, RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied, MetadataDisposition: RecoveryMetadataRetained}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("validateNoEffectReconciliation(quarantine metadata) error = %v, want ErrRecoveryConflict", err)
	}
	if err := validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeRestoreVerified}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("validateMoveIndeterminateReconciliation(wrong outcome) error = %v, want ErrRecoveryConflict", err)
	}
	if err := validateMoveIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeMoveVerified, MetadataDisposition: RecoveryMetadataAbsent}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("validateMoveIndeterminateReconciliation(metadata) error = %v, want ErrRecoveryUnsupported", err)
	}
	if err := validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeNotApplied}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("validateRestoreIndeterminateReconciliation(wrong outcome) error = %v, want ErrRecoveryConflict", err)
	}
	if err := validateRestoreIndeterminateReconciliation(RecoveryTransition{Event: RecoveryEventReconciliationResolved, ReconciliationOutcome: RecoveryOutcomeRestoreVerified, MetadataDisposition: RecoveryMetadataAbsent}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("validateRestoreIndeterminateReconciliation(metadata) error = %v, want ErrRecoveryUnsupported", err)
	}
}

func TestRecoveryPathAndCloneBounds(t *testing.T) {
	if err := validateRecoveryPath(pathbytes.BytePath{}); err == nil {
		t.Fatal("validateRecoveryPath(empty) unexpectedly succeeded")
	}
	components := make([][]byte, maximumRecoveryPathComponents+1)
	for index := range components {
		components[index] = []byte("a")
	}
	tooMany, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New(too many) error = %v", err)
	}
	if err := validateRecoveryPath(tooMany); err == nil {
		t.Fatal("validateRecoveryPath(too many components) unexpectedly succeeded")
	}
	tooLarge, err := pathbytes.New([][]byte{bytes.Repeat([]byte{'a'}, maximumRecoveryPathBytes+1)})
	if err != nil {
		t.Fatalf("pathbytes.New(too large) error = %v", err)
	}
	if err := validateRecoveryPath(tooLarge); err == nil {
		t.Fatal("validateRecoveryPath(too many bytes) unexpectedly succeeded")
	}
	if cloned := cloneRecoveryPath(pathbytes.BytePath{}); len(cloned.Components()) != 0 {
		t.Fatalf("cloneRecoveryPath(empty) = %#v, want empty path", cloned)
	}
}
