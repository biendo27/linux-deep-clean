package state

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

type contentRecoveryTicketFact struct {
	event    recoveryport.Event
	outcome  recoveryport.Outcome
	metadata recoveryport.MetadataDisposition
	closed   bool
}

func TestContentRecoveryLedgerBridgesOpaqueRecoveryFacts(t *testing.T) {
	ctx := context.Background()
	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	content, err := NewRecoveryLedgerPort(ledger)
	if err != nil {
		t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
	}

	action := testRecoveryAction(t, domain.ActionTrashPath)
	reservation := testRecoveryPortReservation(t, action.Kind)
	reserved, err := content.Reserve(ctx, action, testRecoveryPlanDigest(t), reservation)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if reserved == nil {
		t.Fatal("Reserve() returned no recovery record")
	}
	if reserved.Destination() != recoveryport.DestinationTrash {
		t.Fatalf("reserved destination = %q, want trash", reserved.Destination())
	}
	if reserved.Event() != recoveryport.EventIntentReserved {
		t.Fatalf("reserved event = %q, want intent reserved", reserved.Event())
	}
	if reserved.RootID() != action.Target.Filesystem.Root || !reserved.Source().Equal(action.Target.Filesystem.Path) {
		t.Fatalf("reserved source = (%q, %q), want action source", reserved.RootID(), reserved.Source().Display())
	}
	if reserved.ActionID() != action.ID || reserved.ActionKind() != action.Kind {
		t.Fatal("reserved record changed immutable action facts")
	}
	if !reserved.TrashLayoutBinding().Equal(reservation.TrashLayoutBinding) {
		t.Fatal("reserved ticket did not expose its immutable Trash layout binding")
	}

	precondition := reserved.Precondition()
	if err := precondition.Validate(); err != nil {
		t.Fatalf("reserved precondition = %v", err)
	}
	precondition.Target.Filesystem.Path = mustContentRecoveryPath(t, "changed")
	if reserved.Precondition().Target.Filesystem.Path.Equal(precondition.Target.Filesystem.Path) {
		t.Fatal("Precondition() leaked mutable recovery binding state")
	}

	metadataDispatch, err := content.Transition(ctx, reserved, recoveryport.Transition{
		Event: recoveryport.EventMetadataDispatchRecorded,
	})
	if err != nil {
		t.Fatalf("metadata dispatch transition error = %v", err)
	}
	metadataVerified, err := content.Transition(ctx, metadataDispatch, recoveryport.Transition{
		Event: recoveryport.EventMetadataVerified,
	})
	if err != nil {
		t.Fatalf("metadata verified transition error = %v", err)
	}
	moveDispatch, err := content.Transition(ctx, metadataVerified, recoveryport.Transition{
		Event: recoveryport.EventMoveDispatchRecorded,
	})
	if err != nil {
		t.Fatalf("move dispatch transition error = %v", err)
	}
	moveVerified, err := content.Transition(ctx, moveDispatch, recoveryport.Transition{
		Event: recoveryport.EventMoveVerified,
	})
	if err != nil {
		t.Fatalf("move verified transition error = %v", err)
	}
	if moveVerified.Event() != recoveryport.EventMoveVerified || moveVerified.Closed() {
		t.Fatalf("move-verified record = event %q, closed %t; want open move-verified", moveVerified.Event(), moveVerified.Closed())
	}

	retained, found, err := content.FindOutstanding(ctx, action.Target.Filesystem.Root, action.Target.Filesystem.Path)
	if err != nil {
		t.Fatalf("FindOutstanding() error = %v", err)
	}
	if !found || retained == nil || retained.Token() != moveVerified.Token() {
		t.Fatalf("FindOutstanding() = (%#v, %t), want retained move record", retained, found)
	}
	if retained.Event() != recoveryport.EventMoveVerified {
		t.Fatalf("retained event = %q, want move verified", retained.Event())
	}
}

func TestContentRecoveryLedgerMapsEveryKnownPortEnum(t *testing.T) {
	ctx := context.Background()

	for _, test := range []struct {
		name        string
		actionKind  domain.ActionKind
		destination recoveryport.Destination
		steps       []recoveryport.Transition
		want        []contentRecoveryTicketFact
	}{
		{
			name:        "trash verified move and restore",
			actionKind:  domain.ActionTrashPath,
			destination: recoveryport.DestinationTrash,
			steps: []recoveryport.Transition{
				{Event: recoveryport.EventMetadataDispatchRecorded},
				{Event: recoveryport.EventMetadataVerified},
				{Event: recoveryport.EventMoveDispatchRecorded},
				{Event: recoveryport.EventMoveVerified},
				{Event: recoveryport.EventRestoreDispatchRecorded},
				{Event: recoveryport.EventRestoreVerified},
			},
			want: []contentRecoveryTicketFact{
				{event: recoveryport.EventIntentReserved},
				{event: recoveryport.EventMetadataDispatchRecorded},
				{event: recoveryport.EventMetadataVerified},
				{event: recoveryport.EventMoveDispatchRecorded},
				{event: recoveryport.EventMoveVerified},
				{event: recoveryport.EventRestoreDispatchRecorded},
				{event: recoveryport.EventRestoreVerified, closed: true},
			},
		},
		{
			name:        "trash metadata indeterminate resolves absent",
			actionKind:  domain.ActionTrashPath,
			destination: recoveryport.DestinationTrash,
			steps: []recoveryport.Transition{
				{Event: recoveryport.EventMetadataDispatchRecorded},
				{Event: recoveryport.EventMetadataIndeterminate},
				{
					Event:                 recoveryport.EventReconciliationResolved,
					ReconciliationOutcome: recoveryport.OutcomeNotApplied,
					MetadataDisposition:   recoveryport.MetadataAbsent,
				},
			},
			want: []contentRecoveryTicketFact{
				{event: recoveryport.EventIntentReserved},
				{event: recoveryport.EventMetadataDispatchRecorded},
				{event: recoveryport.EventMetadataIndeterminate},
				{
					event:    recoveryport.EventReconciliationResolved,
					outcome:  recoveryport.OutcomeNotApplied,
					metadata: recoveryport.MetadataAbsent,
					closed:   true,
				},
			},
		},
		{
			name:        "trash no effect retains metadata",
			actionKind:  domain.ActionTrashPath,
			destination: recoveryport.DestinationTrash,
			steps: []recoveryport.Transition{
				{Event: recoveryport.EventMetadataDispatchRecorded},
				{Event: recoveryport.EventMetadataVerified},
				{
					Event:                 recoveryport.EventReconciliationResolved,
					ReconciliationOutcome: recoveryport.OutcomeNotApplied,
					MetadataDisposition:   recoveryport.MetadataRetained,
				},
			},
			want: []contentRecoveryTicketFact{
				{event: recoveryport.EventIntentReserved},
				{event: recoveryport.EventMetadataDispatchRecorded},
				{event: recoveryport.EventMetadataVerified},
				{
					event:    recoveryport.EventReconciliationResolved,
					outcome:  recoveryport.OutcomeNotApplied,
					metadata: recoveryport.MetadataRetained,
					closed:   true,
				},
			},
		},
		{
			name:        "quarantine indeterminate move remains restorable",
			actionKind:  domain.ActionQuarantinePath,
			destination: recoveryport.DestinationQuarantine,
			steps: []recoveryport.Transition{
				{Event: recoveryport.EventMoveDispatchRecorded},
				{Event: recoveryport.EventMoveIndeterminate},
				{
					Event:                 recoveryport.EventReconciliationResolved,
					ReconciliationOutcome: recoveryport.OutcomeMoveVerified,
				},
			},
			want: []contentRecoveryTicketFact{
				{event: recoveryport.EventIntentReserved},
				{event: recoveryport.EventMoveDispatchRecorded},
				{event: recoveryport.EventMoveIndeterminate},
				{
					event:   recoveryport.EventReconciliationResolved,
					outcome: recoveryport.OutcomeMoveVerified,
				},
			},
		},
		{
			name:        "quarantine indeterminate restore resolves restored",
			actionKind:  domain.ActionQuarantinePath,
			destination: recoveryport.DestinationQuarantine,
			steps: []recoveryport.Transition{
				{Event: recoveryport.EventMoveDispatchRecorded},
				{Event: recoveryport.EventMoveVerified},
				{Event: recoveryport.EventRestoreDispatchRecorded},
				{Event: recoveryport.EventRestoreIndeterminate},
				{
					Event:                 recoveryport.EventReconciliationResolved,
					ReconciliationOutcome: recoveryport.OutcomeRestoreVerified,
				},
			},
			want: []contentRecoveryTicketFact{
				{event: recoveryport.EventIntentReserved},
				{event: recoveryport.EventMoveDispatchRecorded},
				{event: recoveryport.EventMoveVerified},
				{event: recoveryport.EventRestoreDispatchRecorded},
				{event: recoveryport.EventRestoreIndeterminate},
				{
					event:   recoveryport.EventReconciliationResolved,
					outcome: recoveryport.OutcomeRestoreVerified,
					closed:  true,
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := NewRecoveryLedgerPort(newTestRecoveryLedger(newTestRecoveryLedgerSessions()))
			if err != nil {
				t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
			}

			ticket, err := content.Reserve(ctx, testRecoveryAction(t, test.actionKind), testRecoveryPlanDigest(t), testRecoveryPortReservation(t, test.actionKind))
			if err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}
			if ticket.Destination() != test.destination {
				t.Fatalf("Reserve() destination = %q, want %q", ticket.Destination(), test.destination)
			}
			assertContentRecoveryTicketFact(t, ticket, test.want[0])

			for index, transition := range test.steps {
				ticket, err = content.Transition(ctx, ticket, transition)
				if err != nil {
					t.Fatalf("Transition(%q) error = %v", transition.Event, err)
				}
				assertContentRecoveryTicketFact(t, ticket, test.want[index+1])
			}
		})
	}
}

func TestContentRecoveryLedgerRejectsForeignRecoveryRecord(t *testing.T) {
	ctx := context.Background()
	content, err := NewRecoveryLedgerPort(newTestRecoveryLedger(newTestRecoveryLedgerSessions()))
	if err != nil {
		t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
	}

	foreign := foreignRecoveryRecord{
		root:   mustRecoveryRoot(t, "foreign-root"),
		source: mustContentRecoveryPath(t, "foreign"),
	}
	if _, err := content.Transition(ctx, foreign, recoveryport.Transition{Event: recoveryport.EventMoveDispatchRecorded}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Transition(foreign record) error = %v, want ErrRecoveryUnsupported", err)
	}
}

func TestContentRecoveryLedgerRejectsNilLedger(t *testing.T) {
	if content, err := NewRecoveryLedgerPort(nil); !errors.Is(err, ErrRecoveryUnsupported) || content != nil {
		t.Fatalf("NewRecoveryLedgerPort(nil) = (%#v, %v), want nil + ErrRecoveryUnsupported", content, err)
	}
}

func TestContentRecoveryLedgerReloadsBoundedTicketsAndRejectsOtherPorts(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	content, err := NewRecoveryLedgerPort(newTestRecoveryLedger(sessions))
	if err != nil {
		t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
	}
	other, err := NewRecoveryLedgerPort(newTestRecoveryLedger(sessions))
	if err != nil {
		t.Fatalf("NewRecoveryLedgerPort(other) error = %v", err)
	}

	action := testRecoveryAction(t, domain.ActionQuarantinePath)
	reserved, err := content.Reserve(ctx, action, testRecoveryPlanDigest(t), testRecoveryPortReservation(t, action.Kind))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if _, err := other.Reload(ctx, reserved); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("other Reload(ticket) error = %v, want ErrRecoveryUnsupported", err)
	}

	components := reserved.Source().Components()
	components[0][0] = 'X'
	if !reserved.Source().Equal(action.Target.Filesystem.Path) {
		t.Fatal("Source() leaked mutable recovery binding state")
	}

	dispatch, err := content.Transition(ctx, reserved, recoveryport.Transition{
		Event: recoveryport.EventMoveDispatchRecorded,
	})
	if err != nil {
		t.Fatalf("Transition(move dispatch) error = %v", err)
	}
	reloaded, err := content.Reload(ctx, reserved)
	if err != nil {
		t.Fatalf("Reload(stale ticket) error = %v", err)
	}
	if reloaded.Event() != recoveryport.EventMoveDispatchRecorded || reloaded.Token() != dispatch.Token() {
		t.Fatalf("Reload(stale ticket) = (%q, %q), want move dispatch and current token", reloaded.Event(), reloaded.Token())
	}

	outstanding, err := content.ListOutstanding(ctx, 1)
	if err != nil {
		t.Fatalf("ListOutstanding() error = %v", err)
	}
	if len(outstanding) != 1 || outstanding[0].Token() != reserved.Token() {
		t.Fatalf("ListOutstanding() = %#v, want the reserved ticket", outstanding)
	}
}

func TestContentRecoveryLedgerPreservesInterruptedTicketsForReload(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	content, err := NewRecoveryLedgerPort(newTestRecoveryLedger(sessions))
	if err != nil {
		t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
	}

	sessions.interruptNextPublish()
	reserved, err := content.Reserve(ctx, testRecoveryAction(t, domain.ActionQuarantinePath), testRecoveryPlanDigest(t), testRecoveryPortReservation(t, domain.ActionQuarantinePath))
	if !errors.Is(err, linuxfs.ErrInterrupted) || reserved == nil || reserved.Token() == "" {
		t.Fatalf("interrupted Reserve() = (%#v, %v), want reloadable ticket + ErrInterrupted", reserved, err)
	}
	reloaded, err := content.Reload(ctx, reserved)
	if err != nil || reloaded.Token() != reserved.Token() {
		t.Fatalf("Reload(interrupted reserve) = (%#v, %v), want retained ticket", reloaded, err)
	}

	sessions.interruptNextPublish()
	dispatch, err := content.Transition(ctx, reserved, recoveryport.Transition{
		Event: recoveryport.EventMoveDispatchRecorded,
	})
	if !errors.Is(err, linuxfs.ErrInterrupted) || dispatch == nil || dispatch.Event() != recoveryport.EventMoveDispatchRecorded {
		t.Fatalf("interrupted Transition() = (%#v, %v), want reloadable move-dispatch ticket + ErrInterrupted", dispatch, err)
	}
	reloaded, err = content.Reload(ctx, reserved)
	if err != nil || reloaded.Event() != recoveryport.EventMoveDispatchRecorded {
		t.Fatalf("Reload(interrupted transition) = (%#v, %v), want retained move-dispatch ticket", reloaded, err)
	}
}

func TestContentRecoveryLedgerRejectsInvalidPortEnumsBeforeMutation(t *testing.T) {
	ctx := context.Background()

	for _, test := range []struct {
		name       string
		transition recoveryport.Transition
		wantErr    error
	}{
		{
			name:       "unknown event",
			transition: recoveryport.Transition{Event: recoveryport.Event("unknown")},
			wantErr:    ErrRecoveryUnsupported,
		},
		{
			name: "unknown outcome",
			transition: recoveryport.Transition{
				Event:                 recoveryport.EventMoveDispatchRecorded,
				ReconciliationOutcome: recoveryport.Outcome("unknown"),
			},
			wantErr: ErrRecoveryUnsupported,
		},
		{
			name: "unknown metadata disposition",
			transition: recoveryport.Transition{
				Event:               recoveryport.EventMoveDispatchRecorded,
				MetadataDisposition: recoveryport.MetadataDisposition("unknown"),
			},
			wantErr: ErrRecoveryUnsupported,
		},
		{
			name:       "reserved event cannot be appended",
			transition: recoveryport.Transition{Event: recoveryport.EventIntentReserved},
			wantErr:    ErrRecoveryUnsupported,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := NewRecoveryLedgerPort(newTestRecoveryLedger(newTestRecoveryLedgerSessions()))
			if err != nil {
				t.Fatalf("NewRecoveryLedgerPort() error = %v", err)
			}
			reserved, err := content.Reserve(ctx, testRecoveryAction(t, domain.ActionQuarantinePath), testRecoveryPlanDigest(t), testRecoveryPortReservation(t, domain.ActionQuarantinePath))
			if err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}

			if ticket, err := content.Transition(ctx, reserved, test.transition); ticket != nil || !errors.Is(err, test.wantErr) {
				t.Fatalf("Transition(%#v) = (%#v, %v), want no ticket + %v", test.transition, ticket, err, test.wantErr)
			}
			reloaded, err := content.Reload(ctx, reserved)
			if err != nil {
				t.Fatalf("Reload() error = %v", err)
			}
			if reloaded.Event() != recoveryport.EventIntentReserved || reloaded.Outcome() != "" || reloaded.MetadataDisposition() != "" {
				t.Fatalf("rejected transition changed retained ticket to (%q, %q, %q)", reloaded.Event(), reloaded.Outcome(), reloaded.MetadataDisposition())
			}
		})
	}
}

func assertContentRecoveryTicketFact(t *testing.T, ticket recoveryport.Ticket, want contentRecoveryTicketFact) {
	t.Helper()

	if ticket == nil {
		t.Fatal("operation returned no recovery ticket")
	}
	if ticket.Event() != want.event || ticket.Outcome() != want.outcome || ticket.MetadataDisposition() != want.metadata || ticket.Closed() != want.closed {
		t.Fatalf(
			"ticket facts = (event %q, outcome %q, metadata %q, closed %t), want (%q, %q, %q, %t)",
			ticket.Event(),
			ticket.Outcome(),
			ticket.MetadataDisposition(),
			ticket.Closed(),
			want.event,
			want.outcome,
			want.metadata,
			want.closed,
		)
	}
}

type foreignRecoveryRecord struct {
	root   domain.TrustedRootID
	source pathbytes.BytePath
}

func (record foreignRecoveryRecord) Token() string { return "ldc-0123456789abcdef" }
func (record foreignRecoveryRecord) RootID() domain.TrustedRootID {
	return record.root
}
func (record foreignRecoveryRecord) Source() pathbytes.BytePath { return record.source }
func (foreignRecoveryRecord) ActionID() domain.ActionID {
	return mustRecoveryActionIDForForeignRecord()
}
func (foreignRecoveryRecord) ActionKind() domain.ActionKind { return domain.ActionTrashPath }
func (foreignRecoveryRecord) Destination() recoveryport.Destination {
	return recoveryport.DestinationTrash
}
func (record foreignRecoveryRecord) Precondition() domain.FilesystemPrecondition {
	return domain.FilesystemPrecondition{
		Target: domain.Target{
			Kind: domain.TargetFilesystem,
			Filesystem: &domain.FilesystemTarget{
				Root: record.root,
				Path: record.source,
			},
		},
		Required: domain.FilesystemFieldDevice,
		Snapshot: domain.FilesystemSnapshot{
			DeviceMajor: domain.Uint32Fact{Known: true, Value: 1},
		},
	}
}
func (foreignRecoveryRecord) Event() recoveryport.Event { return recoveryport.EventIntentReserved }
func (foreignRecoveryRecord) Outcome() recoveryport.Outcome {
	return ""
}
func (foreignRecoveryRecord) MetadataDisposition() recoveryport.MetadataDisposition {
	return ""
}
func (foreignRecoveryRecord) TrashLayoutBinding() domain.TrashLayoutBinding {
	return domain.TrashLayoutBinding{}
}
func (foreignRecoveryRecord) Closed() bool { return false }

func mustRecoveryActionIDForForeignRecord() domain.ActionID { return domain.ActionID("foreign-record") }

func mustContentRecoveryPath(t *testing.T, components ...string) pathbytes.BytePath {
	t.Helper()

	rawComponents := make([][]byte, 0, len(components))
	for _, component := range components {
		rawComponents = append(rawComponents, []byte(component))
	}
	path, err := pathbytes.New(rawComponents)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	return path
}
