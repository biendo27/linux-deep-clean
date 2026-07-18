//go:build linux

package trash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

func TestMoveToTrashRecordsVerifiedFactsAroundFilesystemEffects(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveVerified, nil)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if err != nil {
		t.Fatalf("moveToTrashWith() error = %v", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveVerified || ticket.Closed() {
		t.Fatalf("moveToTrashWith() ticket = %#v, want open move-verified ticket", ticket)
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
		"select",
		"transition:move_dispatch_recorded",
		"move",
		"transition:move_verified",
		"close",
	)
}

func TestMoveToTrashStopsBeforeFilesystemMoveWhenDurableTransitionFails(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	ledger.transitionErrors[recoveryport.EventMetadataVerified] = errors.New("metadata verification ledger write interrupted")
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveVerified, nil)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, ledger.transitionErrors[recoveryport.EventMetadataVerified]) {
		t.Fatalf("moveToTrashWith() transition error = %v, want durable failure", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMetadataVerified {
		t.Fatalf("moveToTrashWith() ticket after failed metadata verification = %#v, want returned transition ticket", ticket)
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
	)
}

func TestMoveToTrashRetainsPriorTicketWhenPostMoveLedgerWriteReturnsNoTicket(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	ledger.transitionErrors[recoveryport.EventMoveVerified] = errors.New("move verification ledger write failed before publication")
	ledger.nilTicketOnTransitionError[recoveryport.EventMoveVerified] = true
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveVerified, nil)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, ledger.transitionErrors[recoveryport.EventMoveVerified]) {
		t.Fatalf("moveToTrashWith() transition error = %v, want durable failure", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveDispatchRecorded || ticket.Closed() {
		t.Fatalf("moveToTrashWith() ticket after unpublished move verification = %#v, want prior open move-dispatch ticket", ticket)
	}
	if ledger.ticket.Event() != recoveryport.EventMoveDispatchRecorded {
		t.Fatalf("ledger retained event after unpublished move verification = %q, want move dispatch", ledger.ticket.Event())
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
		"select",
		"transition:move_dispatch_recorded",
		"move",
		"transition:move_verified",
		"close",
	)
}

func TestMoveToTrashLeavesIndeterminateMoveOutstandingWithoutCleanup(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	moveErr := fmt.Errorf("%w: simulated rename ambiguity", linuxfs.ErrInterrupted)
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveIndeterminate, moveErr)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, moveErr) {
		t.Fatalf("moveToTrashWith() indeterminate error = %v, want move error", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
		t.Fatalf("moveToTrashWith() indeterminate ticket = %#v, want outstanding move-indeterminate ticket", ticket)
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
		"select",
		"transition:move_dispatch_recorded",
		"move",
		"transition:move_indeterminate",
		"close",
	)
}

func TestMoveToTrashReconcilesKnownNonMoveWithoutMetadataDisposition(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	moveErr := fmt.Errorf("%w: simulated collision", linuxfs.ErrDrifted)
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveNotApplied, moveErr)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, moveErr) {
		t.Fatalf("moveToTrashWith() known non-move error = %v, want move error", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved || ticket.Outcome() != recoveryport.OutcomeNotApplied || !ticket.Closed() {
		t.Fatalf("moveToTrashWith() known non-move ticket = %#v, want closed not-applied reconciliation", ticket)
	}
	if got := ledger.transitions[len(ledger.transitions)-1].MetadataDisposition; got != "" {
		t.Fatalf("known non-move reconciliation metadata disposition = %q, want empty for the move-indeterminate graph edge", got)
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
		"select",
		"transition:move_dispatch_recorded",
		"move",
		"transition:move_indeterminate",
		"transition:reconciliation_resolved",
		"close",
	)
}

func TestMoveToTrashReconcilesSelectorFailureAsMetadataRetained(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	selectorErr := fmt.Errorf("%w: topology changed", linuxfs.ErrDrifted)
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveVerified, nil)
	operations.selectDirectories = func() (*linuxfs.TrashDirectories, error) {
		ledger.log = append(ledger.log, "select")
		return nil, selectorErr
	}

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, selectorErr) {
		t.Fatalf("moveToTrashWith() selector error = %v, want selector failure", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved || ticket.MetadataDisposition() != recoveryport.MetadataRetained || !ticket.Closed() {
		t.Fatalf("moveToTrashWith() selector ticket = %#v, want closed metadata-retained reconciliation", ticket)
	}
	assertTrashMoveTestLog(t, ledger.log,
		"reserve",
		"transition:metadata_dispatch_recorded",
		"write",
		"transition:metadata_verified",
		"select",
		"transition:reconciliation_resolved",
	)
}

func TestMoveToTrashRejectsInvalidExternalRequestBeforeReservation(t *testing.T) {
	ledger := &trashMoveTestLedger{}
	ticket, err := MoveToTrash(context.Background(), ledger, domain.Action{}, domain.PlanDigest{}, nil, nil, time.Time{})
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("MoveToTrash() invalid request error = %v, want ErrUnsupported", err)
	}
	if ticket != nil {
		t.Fatalf("MoveToTrash() invalid request ticket = %#v, want nil", ticket)
	}
	if len(ledger.log) != 0 {
		t.Fatalf("MoveToTrash() invalid request called ledger: %v", ledger.log)
	}
}

func TestMoveToTrashRejectsMismatchedReservedTicketBeforeMetadataDispatch(t *testing.T) {
	request := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(request)
	ledger.ticket.root = mustTrashMoveRoot(t, "different-root")
	operations := newTrashMoveTestOperations(t, ledger, linuxfs.TrashMoveVerified, nil)

	ticket, err := moveToTrashWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("moveToTrashWith() mismatched ticket error = %v, want ErrUnsupported", err)
	}
	if ticket == nil {
		t.Fatal("moveToTrashWith() mismatched ticket discarded the durable reservation")
	}
	assertTrashMoveTestLog(t, ledger.log, "reserve")
}

func newTrashMoveTestRequest(t *testing.T) trashMoveRequest {
	t.Helper()
	root := mustTrashMoveRoot(t, "trash-move-root")
	path := mustTrashMovePath(t, "source", "item")
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	precondition := domain.FilesystemPrecondition{
		Target:   target,
		Required: required,
		Snapshot: trashMoveTestSnapshot(),
	}
	if err := precondition.Validate(); err != nil {
		t.Fatalf("precondition.Validate() error = %v", err)
	}
	actionID, err := domain.NewActionID("trash-move")
	if err != nil {
		t.Fatalf("NewActionID() error = %v", err)
	}
	return trashMoveRequest{
		action:       domain.Action{ID: actionID},
		planDigest:   mustTrashMovePlanDigest(t),
		root:         root,
		source:       path,
		basename:     "item",
		precondition: precondition,
		deletedAt:    time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC),
	}
}

func mustTrashMovePlanDigest(t *testing.T) domain.PlanDigest {
	t.Helper()
	digest, err := domain.NewPlanDigest(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	return digest
}

func mustTrashMoveRoot(t *testing.T, value string) domain.TrustedRootID {
	t.Helper()
	root, err := domain.NewTrustedRootID(value)
	if err != nil {
		t.Fatalf("NewTrustedRootID(%q) error = %v", value, err)
	}
	return root
}

func mustTrashMovePath(t *testing.T, components ...string) pathbytes.BytePath {
	t.Helper()
	values := make([][]byte, len(components))
	for index, component := range components {
		values[index] = []byte(component)
	}
	path, err := pathbytes.New(values)
	if err != nil {
		t.Fatalf("pathbytes.New(%q) error = %v", components, err)
	}
	return path
}

func trashMoveTestSnapshot() domain.FilesystemSnapshot {
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

type trashMoveTestTicket struct {
	token    string
	root     domain.TrustedRootID
	source   pathbytes.BytePath
	actionID domain.ActionID
	precond  domain.FilesystemPrecondition
	event    recoveryport.Event
	outcome  recoveryport.Outcome
	metadata recoveryport.MetadataDisposition
	closed   bool
}

func (ticket *trashMoveTestTicket) Token() string                 { return ticket.token }
func (ticket *trashMoveTestTicket) RootID() domain.TrustedRootID  { return ticket.root }
func (ticket *trashMoveTestTicket) Source() pathbytes.BytePath    { return ticket.source }
func (ticket *trashMoveTestTicket) ActionID() domain.ActionID     { return ticket.actionID }
func (ticket *trashMoveTestTicket) ActionKind() domain.ActionKind { return domain.ActionTrashPath }
func (ticket *trashMoveTestTicket) Destination() recoveryport.Destination {
	return recoveryport.DestinationTrash
}
func (ticket *trashMoveTestTicket) Precondition() domain.FilesystemPrecondition {
	return ticket.precond
}
func (ticket *trashMoveTestTicket) Event() recoveryport.Event     { return ticket.event }
func (ticket *trashMoveTestTicket) Outcome() recoveryport.Outcome { return ticket.outcome }
func (ticket *trashMoveTestTicket) MetadataDisposition() recoveryport.MetadataDisposition {
	return ticket.metadata
}
func (ticket *trashMoveTestTicket) Closed() bool { return ticket.closed }

type trashMoveTestLedger struct {
	ticket                     *trashMoveTestTicket
	log                        []string
	transitions                []recoveryport.Transition
	transitionErrors           map[recoveryport.Event]error
	nilTicketOnTransitionError map[recoveryport.Event]bool
}

func newTrashMoveTestLedger(request trashMoveRequest) *trashMoveTestLedger {
	return &trashMoveTestLedger{
		ticket: &trashMoveTestTicket{
			token:    "ldc-0123456789abcdef",
			root:     request.root,
			source:   request.source,
			actionID: request.action.ID,
			precond:  request.precondition,
			event:    recoveryport.EventIntentReserved,
		},
		transitionErrors:           make(map[recoveryport.Event]error),
		nilTicketOnTransitionError: make(map[recoveryport.Event]bool),
	}
}

func (ledger *trashMoveTestLedger) Reserve(context.Context, domain.Action, domain.PlanDigest) (recoveryport.Ticket, error) {
	ledger.log = append(ledger.log, "reserve")
	return ledger.ticket, nil
}

func (ledger *trashMoveTestLedger) Transition(_ context.Context, _ recoveryport.Ticket, transition recoveryport.Transition) (recoveryport.Ticket, error) {
	ledger.log = append(ledger.log, "transition:"+string(transition.Event))
	ledger.transitions = append(ledger.transitions, transition)
	updated := *ledger.ticket
	updated.event = transition.Event
	updated.outcome = transition.ReconciliationOutcome
	updated.metadata = transition.MetadataDisposition
	updated.closed = transition.Event == recoveryport.EventReconciliationResolved && transition.ReconciliationOutcome != recoveryport.OutcomeMoveVerified
	if err := ledger.transitionErrors[transition.Event]; err != nil && ledger.nilTicketOnTransitionError[transition.Event] {
		return nil, err
	}
	ledger.ticket = &updated
	return ledger.ticket, ledger.transitionErrors[transition.Event]
}

func (ledger *trashMoveTestLedger) FindOutstanding(context.Context, domain.TrustedRootID, pathbytes.BytePath) (recoveryport.Ticket, bool, error) {
	return nil, false, nil
}

func (ledger *trashMoveTestLedger) ListOutstanding(context.Context, int) ([]recoveryport.Ticket, error) {
	return nil, nil
}

func (ledger *trashMoveTestLedger) Reload(context.Context, recoveryport.Ticket) (recoveryport.Ticket, error) {
	return ledger.ticket, nil
}

func newTrashMoveTestOperations(t *testing.T, ledger *trashMoveTestLedger, disposition linuxfs.TrashMoveDisposition, moveErr error) trashMoveOperations {
	t.Helper()
	return trashMoveOperations{
		write: func(context.Context, string, pathbytes.BytePath, time.Time) (*MetadataPublication, error) {
			ledger.log = append(ledger.log, "write")
			return &MetadataPublication{rootID: ledger.ticket.root, token: ledger.ticket.token, publication: &linuxfs.TrashInfoPublication{}}, nil
		},
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			ledger.log = append(ledger.log, "select")
			return &linuxfs.TrashDirectories{}, nil
		},
		move: func(context.Context, *linuxfs.ParentLease, string, *linuxfs.TrashDirectories, *linuxfs.TrashInfoPublication, domain.FilesystemPrecondition) (linuxfs.TrashMoveDisposition, error) {
			ledger.log = append(ledger.log, "move")
			return disposition, moveErr
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error {
			ledger.log = append(ledger.log, "close")
			return nil
		},
	}
}

func assertTrashMoveTestLog(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("operation log = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("operation log = %v, want %v", got, want)
		}
	}
}
