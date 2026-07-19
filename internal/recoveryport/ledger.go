// Package recoveryport defines the narrow, data-only recovery boundary shared
// by durable state and descriptor-rooted content operations. It carries no
// filesystem descriptor, layout, or content-mutation authority.
package recoveryport

import (
	"context"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// Destination is the closed retained-content location selected by a durable
// source-bound reservation. It is descriptive only and cannot select a
// directory or authorize an operation.
type Destination string

const (
	DestinationTrash      Destination = "trash"
	DestinationQuarantine Destination = "quarantine"
)

// Event is the closed vocabulary for durable facts recorded before and after
// a content operation. Recording an event never performs that operation.
type Event string

const (
	EventIntentReserved           Event = "intent_reserved"
	EventMetadataDispatchRecorded Event = "metadata_dispatch_recorded"
	EventMetadataVerified         Event = "metadata_verified"
	EventMetadataIndeterminate    Event = "metadata_indeterminate"
	EventMoveDispatchRecorded     Event = "move_dispatch_recorded"
	EventMoveVerified             Event = "move_verified"
	EventMoveIndeterminate        Event = "move_indeterminate"
	EventRestoreDispatchRecorded  Event = "restore_dispatch_recorded"
	EventRestoreVerified          Event = "restore_verified"
	EventRestoreIndeterminate     Event = "restore_indeterminate"
	EventReconciliationResolved   Event = "reconciliation_resolved"
)

// Outcome is meaningful only for a reconciliation-resolved event.
type Outcome string

const (
	OutcomeNotApplied      Outcome = "not_applied"
	OutcomeMoveVerified    Outcome = "move_verified"
	OutcomeRestoreVerified Outcome = "restore_verified"
)

// MetadataDisposition records the closed metadata fact needed to resolve a
// Trash intent before content dispatch. Quarantine reservations have none.
type MetadataDisposition string

const (
	MetadataAbsent   MetadataDisposition = "absent"
	MetadataRetained MetadataDisposition = "retained"
)

// Reservation carries immutable, data-only facts that must be durably bound
// before a content operation begins. It never contains a layout lease,
// descriptor, path, or operation authority.
//
// TrashPath reservations require a valid nonzero TrashLayoutBinding;
// QuarantinePath reservations require its zero value. The ledger validates
// that policy against the action kind before it records an intent.
type Reservation struct {
	TrashLayoutBinding domain.TrashLayoutBinding
}

// Transition requests one durable closed-graph fact. It contains no token,
// cleanup instruction, or content-side destination name.
type Transition struct {
	Event                 Event
	ReconciliationOutcome Outcome
	MetadataDisposition   MetadataDisposition
}

// Ticket is an opaque source-bound recovery identity returned by a Ledger.
// Its accessors expose immutable data facts only. Implementations must return
// defensive copies for the raw-byte source and filesystem precondition.
type Ticket interface {
	Token() string
	RootID() domain.TrustedRootID
	Source() pathbytes.BytePath
	ActionID() domain.ActionID
	ActionKind() domain.ActionKind
	Destination() Destination
	Precondition() domain.FilesystemPrecondition
	Event() Event
	Outcome() Outcome
	MetadataDisposition() MetadataDisposition
	TrashLayoutBinding() domain.TrashLayoutBinding
	Closed() bool
}

// Ledger is the opaque durable-facts port used by content-policy code. Its
// implementation owns reservation generation, replay, and transition
// validation; callers cannot choose durable storage or construct a ticket.
type Ledger interface {
	Reserve(context.Context, domain.Action, domain.PlanDigest, Reservation) (Ticket, error)
	Transition(context.Context, Ticket, Transition) (Ticket, error)
	FindOutstanding(context.Context, domain.TrustedRootID, pathbytes.BytePath) (Ticket, bool, error)
	ListOutstanding(context.Context, int) ([]Ticket, error)
	Reload(context.Context, Ticket) (Ticket, error)
}
