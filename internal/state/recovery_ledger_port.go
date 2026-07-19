package state

import (
	"context"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

// RecoveryLedgerPort adapts the concrete durable ledger to the narrow
// recoveryport contract used by content operations. It owns no layout,
// descriptor, source traversal, or content mutation authority.
type RecoveryLedgerPort struct {
	ledger *RecoveryLedger
}

var _ recoveryport.Ledger = (*RecoveryLedgerPort)(nil)

// NewRecoveryLedgerPort binds an initialized concrete durable ledger to the
// opaque content-facing port. It accepts no path, descriptor, memory backend,
// or alternate storage implementation.
func NewRecoveryLedgerPort(ledger *RecoveryLedger) (*RecoveryLedgerPort, error) {
	if ledger == nil {
		return nil, fmt.Errorf("%w: initialized recovery ledger is required", ErrRecoveryUnsupported)
	}
	if err := ledger.validate(); err != nil {
		return nil, err
	}
	return &RecoveryLedgerPort{ledger: ledger}, nil
}

// Reserve persists one source-bound intent through the concrete ledger before
// any content operation. A returned ticket remains opaque to the caller.
func (port *RecoveryLedgerPort) Reserve(ctx context.Context, action domain.Action, planDigest domain.PlanDigest, reservation recoveryport.Reservation) (recoveryport.Ticket, error) {
	if err := port.validate(); err != nil {
		return nil, err
	}
	recovery, err := port.ledger.Reserve(ctx, action, planDigest, RecoveryReservation{
		TrashLayoutBinding: reservation.TrashLayoutBinding,
	})
	return port.ticketResult(recovery, err)
}

// Transition persists one closed durable fact for a ticket returned by this
// port. Foreign or cross-port tickets fail closed before the concrete ledger
// can observe them.
func (port *RecoveryLedgerPort) Transition(ctx context.Context, ticket recoveryport.Ticket, transition recoveryport.Transition) (recoveryport.Ticket, error) {
	if err := port.validate(); err != nil {
		return nil, err
	}
	recovery, err := port.unwrap(ticket)
	if err != nil {
		return nil, err
	}
	stateTransition, err := recoveryTransitionFromPort(transition)
	if err != nil {
		return nil, err
	}
	updated, err := port.ledger.Transition(ctx, recovery, stateTransition)
	return port.ticketResult(updated, err)
}

// FindOutstanding maps the concrete ledger's bounded exact-source lookup to
// the content-facing ticket contract. It never creates or mutates a fact.
func (port *RecoveryLedgerPort) FindOutstanding(ctx context.Context, root domain.TrustedRootID, source pathbytes.BytePath) (recoveryport.Ticket, bool, error) {
	if err := port.validate(); err != nil {
		return nil, false, err
	}
	recovery, found, err := port.ledger.FindOutstanding(ctx, root, source)
	if err != nil || !found {
		return nil, found, err
	}
	ticket, err := port.newTicket(recovery)
	if err != nil {
		return nil, false, err
	}
	return ticket, true, nil
}

// ListOutstanding preserves the concrete ledger's bounded deterministic
// inventory while exposing only opaque, data-only tickets.
func (port *RecoveryLedgerPort) ListOutstanding(ctx context.Context, limit int) ([]recoveryport.Ticket, error) {
	if err := port.validate(); err != nil {
		return nil, err
	}
	recoveries, err := port.ledger.ListOutstanding(ctx, limit)
	if err != nil {
		return nil, err
	}
	tickets := make([]recoveryport.Ticket, 0, len(recoveries))
	for _, recovery := range recoveries {
		ticket, err := port.newTicket(recovery)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

// Reload refreshes facts for a ticket returned by this port without changing
// the retained record history.
func (port *RecoveryLedgerPort) Reload(ctx context.Context, ticket recoveryport.Ticket) (recoveryport.Ticket, error) {
	if err := port.validate(); err != nil {
		return nil, err
	}
	recovery, err := port.unwrap(ticket)
	if err != nil {
		return nil, err
	}
	reloaded, err := port.ledger.Reload(ctx, recovery)
	return port.ticketResult(reloaded, err)
}

func (port *RecoveryLedgerPort) validate() error {
	if port == nil || port.ledger == nil {
		return fmt.Errorf("%w: initialized recovery ledger port is required", ErrRecoveryUnsupported)
	}
	return port.ledger.validate()
}

func (port *RecoveryLedgerPort) ticketResult(recovery Recovery, operationErr error) (recoveryport.Ticket, error) {
	if recovery.Token() == "" {
		if operationErr == nil {
			return nil, fmt.Errorf("%w: durable recovery operation returned no ticket", ErrRecoveryCorrupt)
		}
		return nil, operationErr
	}
	ticket, err := port.newTicket(recovery)
	if err != nil {
		return nil, err
	}
	return ticket, operationErr
}

func (port *RecoveryLedgerPort) newTicket(recovery Recovery) (*recoveryTicket, error) {
	if err := validateRecoveryIdentity(recovery); err != nil {
		return nil, fmt.Errorf("%w: recovery ticket identity: %v", ErrRecoveryCorrupt, err)
	}
	destination, err := recoveryDestinationToPort(recovery.Destination())
	if err != nil {
		return nil, err
	}
	event, err := recoveryEventToPort(recovery.Event())
	if err != nil {
		return nil, err
	}
	outcome, err := recoveryOutcomeToPort(recovery.Outcome())
	if err != nil {
		return nil, err
	}
	metadata, err := recoveryMetadataDispositionToPort(recovery.MetadataDisposition())
	if err != nil {
		return nil, err
	}
	return &recoveryTicket{
		port:         port,
		recovery:     recovery,
		destination:  destination,
		event:        event,
		outcome:      outcome,
		metadata:     metadata,
		precondition: cloneFilesystemPrecondition(recovery.binding.precondition),
		source:       cloneRecoveryPath(recovery.Source()),
	}, nil
}

func (port *RecoveryLedgerPort) unwrap(ticket recoveryport.Ticket) (Recovery, error) {
	owned, ok := ticket.(*recoveryTicket)
	if !ok || owned == nil || owned.port != port {
		return Recovery{}, fmt.Errorf("%w: recovery ticket was not issued by this ledger port", ErrRecoveryUnsupported)
	}
	return owned.recovery, nil
}

type recoveryTicket struct {
	port         *RecoveryLedgerPort
	recovery     Recovery
	destination  recoveryport.Destination
	event        recoveryport.Event
	outcome      recoveryport.Outcome
	metadata     recoveryport.MetadataDisposition
	precondition domain.FilesystemPrecondition
	source       pathbytes.BytePath
}

var _ recoveryport.Ticket = (*recoveryTicket)(nil)

func (ticket *recoveryTicket) Token() string {
	if ticket == nil {
		return ""
	}
	return ticket.recovery.Token()
}

func (ticket *recoveryTicket) RootID() domain.TrustedRootID {
	if ticket == nil {
		return ""
	}
	return ticket.recovery.RootID()
}

func (ticket *recoveryTicket) Source() pathbytes.BytePath {
	if ticket == nil {
		return pathbytes.BytePath{}
	}
	return cloneRecoveryPath(ticket.source)
}

func (ticket *recoveryTicket) ActionID() domain.ActionID {
	if ticket == nil {
		return ""
	}
	return ticket.recovery.ActionID()
}

func (ticket *recoveryTicket) ActionKind() domain.ActionKind {
	if ticket == nil {
		return ""
	}
	return ticket.recovery.ActionKind()
}

func (ticket *recoveryTicket) Destination() recoveryport.Destination {
	if ticket == nil {
		return ""
	}
	return ticket.destination
}

func (ticket *recoveryTicket) Precondition() domain.FilesystemPrecondition {
	if ticket == nil {
		return domain.FilesystemPrecondition{}
	}
	return cloneFilesystemPrecondition(ticket.precondition)
}

func (ticket *recoveryTicket) Event() recoveryport.Event {
	if ticket == nil {
		return ""
	}
	return ticket.event
}

func (ticket *recoveryTicket) Outcome() recoveryport.Outcome {
	if ticket == nil {
		return ""
	}
	return ticket.outcome
}

func (ticket *recoveryTicket) MetadataDisposition() recoveryport.MetadataDisposition {
	if ticket == nil {
		return ""
	}
	return ticket.metadata
}

func (ticket *recoveryTicket) TrashLayoutBinding() domain.TrashLayoutBinding {
	if ticket == nil {
		return domain.TrashLayoutBinding{}
	}
	return ticket.recovery.TrashLayoutBinding()
}

func (ticket *recoveryTicket) Closed() bool {
	return ticket != nil && ticket.recovery.Closed()
}

func recoveryDestinationToPort(destination RecoveryDestination) (recoveryport.Destination, error) {
	switch destination {
	case RecoveryDestinationTrash:
		return recoveryport.DestinationTrash, nil
	case RecoveryDestinationQuarantine:
		return recoveryport.DestinationQuarantine, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery destination %q", ErrRecoveryCorrupt, destination)
	}
}

func recoveryEventToPort(event RecoveryEvent) (recoveryport.Event, error) {
	switch event {
	case RecoveryEventIntentReserved:
		return recoveryport.EventIntentReserved, nil
	case RecoveryEventMetadataDispatchRecorded:
		return recoveryport.EventMetadataDispatchRecorded, nil
	case RecoveryEventMetadataVerified:
		return recoveryport.EventMetadataVerified, nil
	case RecoveryEventMetadataIndeterminate:
		return recoveryport.EventMetadataIndeterminate, nil
	case RecoveryEventMoveDispatchRecorded:
		return recoveryport.EventMoveDispatchRecorded, nil
	case RecoveryEventMoveVerified:
		return recoveryport.EventMoveVerified, nil
	case RecoveryEventMoveIndeterminate:
		return recoveryport.EventMoveIndeterminate, nil
	case RecoveryEventRestoreDispatchRecorded:
		return recoveryport.EventRestoreDispatchRecorded, nil
	case RecoveryEventRestoreVerified:
		return recoveryport.EventRestoreVerified, nil
	case RecoveryEventRestoreIndeterminate:
		return recoveryport.EventRestoreIndeterminate, nil
	case RecoveryEventReconciliationResolved:
		return recoveryport.EventReconciliationResolved, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery event %q", ErrRecoveryCorrupt, event)
	}
}

func recoveryOutcomeToPort(outcome RecoveryOutcome) (recoveryport.Outcome, error) {
	switch outcome {
	case "":
		return "", nil
	case RecoveryOutcomeNotApplied:
		return recoveryport.OutcomeNotApplied, nil
	case RecoveryOutcomeMoveVerified:
		return recoveryport.OutcomeMoveVerified, nil
	case RecoveryOutcomeRestoreVerified:
		return recoveryport.OutcomeRestoreVerified, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery outcome %q", ErrRecoveryCorrupt, outcome)
	}
}

func recoveryMetadataDispositionToPort(metadata RecoveryMetadataDisposition) (recoveryport.MetadataDisposition, error) {
	switch metadata {
	case "":
		return "", nil
	case RecoveryMetadataAbsent:
		return recoveryport.MetadataAbsent, nil
	case RecoveryMetadataRetained:
		return recoveryport.MetadataRetained, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery metadata disposition %q", ErrRecoveryCorrupt, metadata)
	}
}

func recoveryTransitionFromPort(transition recoveryport.Transition) (RecoveryTransition, error) {
	event, err := recoveryEventFromPort(transition.Event)
	if err != nil {
		return RecoveryTransition{}, err
	}
	outcome, err := recoveryOutcomeFromPort(transition.ReconciliationOutcome)
	if err != nil {
		return RecoveryTransition{}, err
	}
	metadata, err := recoveryMetadataDispositionFromPort(transition.MetadataDisposition)
	if err != nil {
		return RecoveryTransition{}, err
	}
	return RecoveryTransition{
		Event:                 event,
		ReconciliationOutcome: outcome,
		MetadataDisposition:   metadata,
	}, nil
}

func recoveryEventFromPort(event recoveryport.Event) (RecoveryEvent, error) {
	switch event {
	case recoveryport.EventIntentReserved:
		return RecoveryEventIntentReserved, nil
	case recoveryport.EventMetadataDispatchRecorded:
		return RecoveryEventMetadataDispatchRecorded, nil
	case recoveryport.EventMetadataVerified:
		return RecoveryEventMetadataVerified, nil
	case recoveryport.EventMetadataIndeterminate:
		return RecoveryEventMetadataIndeterminate, nil
	case recoveryport.EventMoveDispatchRecorded:
		return RecoveryEventMoveDispatchRecorded, nil
	case recoveryport.EventMoveVerified:
		return RecoveryEventMoveVerified, nil
	case recoveryport.EventMoveIndeterminate:
		return RecoveryEventMoveIndeterminate, nil
	case recoveryport.EventRestoreDispatchRecorded:
		return RecoveryEventRestoreDispatchRecorded, nil
	case recoveryport.EventRestoreVerified:
		return RecoveryEventRestoreVerified, nil
	case recoveryport.EventRestoreIndeterminate:
		return RecoveryEventRestoreIndeterminate, nil
	case recoveryport.EventReconciliationResolved:
		return RecoveryEventReconciliationResolved, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery-port event %q", ErrRecoveryUnsupported, event)
	}
}

func recoveryOutcomeFromPort(outcome recoveryport.Outcome) (RecoveryOutcome, error) {
	switch outcome {
	case "":
		return "", nil
	case recoveryport.OutcomeNotApplied:
		return RecoveryOutcomeNotApplied, nil
	case recoveryport.OutcomeMoveVerified:
		return RecoveryOutcomeMoveVerified, nil
	case recoveryport.OutcomeRestoreVerified:
		return RecoveryOutcomeRestoreVerified, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery-port outcome %q", ErrRecoveryUnsupported, outcome)
	}
}

func recoveryMetadataDispositionFromPort(metadata recoveryport.MetadataDisposition) (RecoveryMetadataDisposition, error) {
	switch metadata {
	case "":
		return "", nil
	case recoveryport.MetadataAbsent:
		return RecoveryMetadataAbsent, nil
	case recoveryport.MetadataRetained:
		return RecoveryMetadataRetained, nil
	default:
		return "", fmt.Errorf("%w: unknown recovery-port metadata disposition %q", ErrRecoveryUnsupported, metadata)
	}
}
