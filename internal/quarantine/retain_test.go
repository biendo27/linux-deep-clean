//go:build linux

package quarantine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

func TestRetainRecordsVerifiedFactsAroundOneFilesystemEffect(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if err != nil {
		t.Fatalf("retainWith() error = %v", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveVerified || ticket.Closed() {
		t.Fatalf("retainWith() ticket = %#v, want open move-verified ticket", ticket)
	}
	if len(ledger.reservations) != 1 || ledger.reservations[0] != (recoveryport.Reservation{}) {
		t.Fatalf("durable reservation = %#v, want exactly the zero Quarantine reservation", ledger.reservations)
	}
	assertQuarantineRetainTestLog(t, ledger.log,
		"reserve",
		"transition:move_dispatch_recorded",
		"retain",
		"transition:move_verified",
	)
}

func TestStoreWithRetainSuppliesConcretePrivateLeaseToBridge(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	retainerCalls := 0
	store := newQuarantineRetainTestStore(request.root, func(_ context.Context, _ *linuxfs.ParentLease, _ string, privateLease *linuxfs.PrivateDirectoryLease, _ string, _ domain.FilesystemPrecondition) (linuxfs.QuarantineRetainDisposition, error) {
		retainerCalls++
		if privateLease == nil {
			t.Fatal("Store bridge supplied a nil private lease to its retainer")
		}
		return linuxfs.QuarantineRetainVerified, nil
	})

	_, err := store.withRetain(func(privateLease *linuxfs.PrivateDirectoryLease, retainer quarantineRetainer) (recoveryport.Ticket, error) {
		if privateLease == nil || privateLease != store.privateLease {
			t.Fatalf("Store bridge private lease = %p, want its concrete non-nil lease %p", privateLease, store.privateLease)
		}
		if retainer == nil {
			t.Fatal("Store bridge supplied a nil LinuxFS retainer")
		}
		disposition, err := retainer(context.Background(), nil, "", privateLease, "", domain.FilesystemPrecondition{})
		if err != nil || disposition != linuxfs.QuarantineRetainVerified {
			t.Fatalf("Store bridge retainer result = (%d, %v), want (%d, nil)", disposition, err, linuxfs.QuarantineRetainVerified)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Store.withRetain() error = %v", err)
	}
	if retainerCalls != 1 {
		t.Fatalf("Store bridge retainer calls = %d, want one", retainerCalls)
	}
}

func TestRetainStopsBeforeFilesystemEffectWhenDispatchTransitionFails(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	dispatchErr := errors.New("dispatch ledger write interrupted")
	ledger.transitionErrors[recoveryport.EventMoveDispatchRecorded] = dispatchErr
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("retainWith() error = %v, want dispatch failure", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveDispatchRecorded || ticket.Closed() {
		t.Fatalf("retainWith() ticket = %#v, want returned dispatch ticket", ticket)
	}
	assertQuarantineRetainTestLog(t, ledger.log,
		"reserve",
		"transition:move_dispatch_recorded",
	)
}

func TestRetainLeavesEveryNonVerifiedPostDispatchOutcomeOutstanding(t *testing.T) {
	for _, test := range []struct {
		name        string
		disposition linuxfs.QuarantineRetainDisposition
		retainErr   error
	}{
		{
			name:        "known not applied",
			disposition: linuxfs.QuarantineRetainNotApplied,
		},
		{
			name:        "indeterminate",
			disposition: linuxfs.QuarantineRetainIndeterminate,
			retainErr:   fmt.Errorf("%w: simulated rename ambiguity", linuxfs.ErrInterrupted),
		},
		{
			name:        "verified disposition with error",
			disposition: linuxfs.QuarantineRetainVerified,
			retainErr:   fmt.Errorf("%w: verification could not complete", linuxfs.ErrInterrupted),
		},
		{
			name:        "unknown disposition",
			disposition: linuxfs.QuarantineRetainDisposition(99),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := newQuarantineRetainTestRequest(t)
			ledger := newQuarantineRetainTestLedger(request)
			operations := newQuarantineRetainTestOperations(ledger, test.disposition, test.retainErr)

			ticket, err := retainWith(context.Background(), ledger, request, operations)
			if test.retainErr != nil && !errors.Is(err, test.retainErr) {
				t.Fatalf("retainWith() error = %v, want retain error %v", err, test.retainErr)
			}
			if test.retainErr == nil && !errors.Is(err, linuxfs.ErrInterrupted) && !errors.Is(err, linuxfs.ErrDrifted) {
				t.Fatalf("retainWith() error = %v, want a fail-closed retain outcome", err)
			}
			if ticket == nil || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
				t.Fatalf("retainWith() ticket = %#v, want outstanding move-indeterminate ticket", ticket)
			}
			for _, transition := range ledger.transitions {
				if transition.Event == recoveryport.EventReconciliationResolved ||
					transition.Event == recoveryport.EventMetadataDispatchRecorded ||
					transition.Event == recoveryport.EventMetadataVerified ||
					transition.Event == recoveryport.EventMetadataIndeterminate {
					t.Fatalf("retainWith() recorded forbidden transition %#v", transition)
				}
			}
			assertQuarantineRetainTestLog(t, ledger.log,
				"reserve",
				"transition:move_dispatch_recorded",
				"retain",
				"transition:move_indeterminate",
			)
		})
	}
}

func TestRetainRejectsSubstitutedTransitionTicketBeforeFilesystemEffect(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	original := ledger.ticket
	ledger.substituteTransitionToken = "ldc-" + string(bytes.Repeat([]byte{'b'}, 64))
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("retainWith() error = %v, want ErrUnsupported", err)
	}
	if ticket != original {
		t.Fatalf("retainWith() ticket = %#v, want prior ticket %#v", ticket, original)
	}
	assertQuarantineRetainTestLog(t, ledger.log,
		"reserve",
		"transition:move_dispatch_recorded",
	)
}

func TestRetainRejectsClosedOrReconciledDispatchTicketBeforeFilesystemEffect(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*quarantineRetainTestTicket)
	}{
		{
			name: "closed",
			mutate: func(ticket *quarantineRetainTestTicket) {
				ticket.closed = true
			},
		},
		{
			name: "reconciliation outcome",
			mutate: func(ticket *quarantineRetainTestTicket) {
				ticket.outcome = recoveryport.OutcomeNotApplied
			},
		},
		{
			name: "metadata disposition",
			mutate: func(ticket *quarantineRetainTestTicket) {
				ticket.metadata = recoveryport.MetadataRetained
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := newQuarantineRetainTestRequest(t)
			ledger := newQuarantineRetainTestLedger(request)
			ledger.mutateTransitionTicket[recoveryport.EventMoveDispatchRecorded] = test.mutate
			operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

			_, err := retainWith(context.Background(), ledger, request, operations)
			if !errors.Is(err, linuxfs.ErrUnsupported) {
				t.Fatalf("retainWith() error = %v, want ErrUnsupported", err)
			}
			assertQuarantineRetainTestLog(t, ledger.log,
				"reserve",
				"transition:move_dispatch_recorded",
			)
		})
	}
}

func TestRetainRejectsMismatchedReservedTicketBeforeDispatch(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	ledger.ticket.trashLayoutBinding, _ = domain.NewTrashLayoutBinding(bytes.Repeat([]byte{3}, 32))
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("retainWith() error = %v, want ErrUnsupported", err)
	}
	if ticket == nil {
		t.Fatal("retainWith() discarded the invalid reserved ticket")
	}
	assertQuarantineRetainTestLog(t, ledger.log, "reserve")
}

func TestRetainRejectsNonCanonicalReservedTokenBeforeDispatch(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	ledger.ticket.token = "ldc-0123456789abcdef"
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("retainWith() error = %v, want ErrUnsupported", err)
	}
	if ticket == nil {
		t.Fatal("retainWith() discarded the invalid reserved ticket")
	}
	assertQuarantineRetainTestLog(t, ledger.log, "reserve")
}

func TestRetainPreservesDispatchTicketWhenVerifiedTransitionReturnsNoTicketAndError(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	writeErr := errors.New("move verification ledger write failed before publication")
	ledger.transitionErrors[recoveryport.EventMoveVerified] = writeErr
	ledger.nilTicketOnTransitionError[recoveryport.EventMoveVerified] = true
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, writeErr) {
		t.Fatalf("retainWith() error = %v, want durable failure", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveDispatchRecorded || ticket.Closed() {
		t.Fatalf("retainWith() ticket = %#v, want prior open move-dispatch ticket", ticket)
	}
	assertQuarantineRetainTestLog(t, ledger.log,
		"reserve",
		"transition:move_dispatch_recorded",
		"retain",
		"transition:move_verified",
	)
}

func TestRetainPreservesDispatchTicketWhenVerifiedTransitionReturnsNoTicket(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	ledger.nilTicketOnSuccessfulTransition[recoveryport.EventMoveVerified] = true
	operations := newQuarantineRetainTestOperations(ledger, linuxfs.QuarantineRetainVerified, nil)

	ticket, err := retainWith(context.Background(), ledger, request, operations)
	if !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("retainWith() error = %v, want ErrInterrupted", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventMoveDispatchRecorded || ticket.Closed() {
		t.Fatalf("retainWith() ticket = %#v, want prior open move-dispatch ticket", ticket)
	}
	assertQuarantineRetainTestLog(t, ledger.log,
		"reserve",
		"transition:move_dispatch_recorded",
		"retain",
		"transition:move_verified",
	)
}

func TestRetainRejectsInvalidInputsBeforeReservation(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	wrongRoot, err := domain.NewTrustedRootID("other-quarantine-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	wrongAction := request.action.Clone()
	wrongAction.Kind = domain.ActionTrashPath
	wrongMask := request.action.Clone()
	wrongMask.Precondition.Filesystem.Required = domain.FilesystemFieldDevice

	for _, test := range []struct {
		name       string
		ctx        context.Context
		ledger     recoveryport.Ledger
		store      *Store
		action     domain.Action
		planDigest domain.PlanDigest
		source     *linuxfs.ParentLease
		want       error
	}{
		{
			name:       "nil context",
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     request.action,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrInterrupted,
		},
		{
			name:       "missing ledger",
			ctx:        context.Background(),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     request.action,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:       "missing store",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			action:     request.action,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:       "missing source",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     request.action,
			planDigest: request.planDigest,
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:       "wrong action",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     wrongAction,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:   "zero plan digest",
			ctx:    context.Background(),
			ledger: newQuarantineRetainTestLedger(request),
			store:  newQuarantineRetainTestStore(request.root, nil),
			action: request.action,
			source: &linuxfs.ParentLease{},
			want:   linuxfs.ErrUnsupported,
		},
		{
			name:       "wrong action stat mask",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     wrongMask,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:       "store root mismatch",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(wrongRoot, nil),
			action:     request.action,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
		{
			name:       "source root mismatch",
			ctx:        context.Background(),
			ledger:     newQuarantineRetainTestLedger(request),
			store:      newQuarantineRetainTestStore(request.root, nil),
			action:     request.action,
			planDigest: request.planDigest,
			source:     &linuxfs.ParentLease{},
			want:       linuxfs.ErrUnsupported,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger, _ := test.ledger.(*quarantineRetainTestLedger)
			lowLevelCalls := 0
			if test.store != nil {
				test.store.retain = func(context.Context, *linuxfs.ParentLease, string, *linuxfs.PrivateDirectoryLease, string, domain.FilesystemPrecondition) (linuxfs.QuarantineRetainDisposition, error) {
					lowLevelCalls++
					return linuxfs.QuarantineRetainVerified, nil
				}
			}
			ticket, err := Retain(test.ctx, test.ledger, test.store, test.action, test.planDigest, test.source)
			if !errors.Is(err, test.want) {
				t.Fatalf("Retain() error = %v, want %v", err, test.want)
			}
			if ticket != nil {
				t.Fatalf("Retain() ticket = %#v, want nil on rejected input", ticket)
			}
			if ledger != nil && len(ledger.log) != 0 {
				t.Fatalf("Retain() called ledger for rejected input: %v", ledger.log)
			}
			if lowLevelCalls != 0 {
				t.Fatalf("Retain() made %d low-level calls for rejected input", lowLevelCalls)
			}
		})
	}
}

func TestRetainClosedStoreStopsBeforeReservationAndFilesystemEffect(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	ledger := newQuarantineRetainTestLedger(request)
	lowLevelCalls := 0
	store := newQuarantineRetainTestStore(request.root, func(context.Context, *linuxfs.ParentLease, string, *linuxfs.PrivateDirectoryLease, string, domain.FilesystemPrecondition) (linuxfs.QuarantineRetainDisposition, error) {
		lowLevelCalls++
		return linuxfs.QuarantineRetainVerified, nil
	})
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	ticket, err := Retain(context.Background(), ledger, store, request.action, request.planDigest, &linuxfs.ParentLease{})
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("Retain() error = %v, want ErrUnsupported", err)
	}
	if ticket != nil {
		t.Fatalf("Retain() ticket = %#v, want nil", ticket)
	}
	if len(ledger.log) != 0 {
		t.Fatalf("Retain() called ledger after Store.Close(): %v", ledger.log)
	}
	if lowLevelCalls != 0 {
		t.Fatalf("Retain() made %d low-level calls after Store.Close()", lowLevelCalls)
	}
}

func TestStoreCloseWaitsForInFlightRetainAndRevokesFutureUse(t *testing.T) {
	request := newQuarantineRetainTestRequest(t)
	store := newQuarantineRetainTestStore(request.root, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	retainDone := make(chan error, 1)
	go func() {
		_, err := store.withRetain(func(*linuxfs.PrivateDirectoryLease, quarantineRetainer) (recoveryport.Ticket, error) {
			close(started)
			<-release
			return nil, nil
		})
		retainDone <- err
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- store.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Store.Close() returned before the in-flight retain finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-retainDone; err != nil {
		t.Fatalf("in-flight Store retain error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	if _, err := store.withRetain(func(*linuxfs.PrivateDirectoryLease, quarantineRetainer) (recoveryport.Ticket, error) {
		t.Fatal("closed Store permitted a future retain")
		return nil, nil
	}); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("closed Store retain error = %v, want ErrUnsupported", err)
	}
}

func newQuarantineRetainTestRequest(t *testing.T) quarantineRetainRequest {
	t.Helper()
	root := quarantineTestRootID(t)
	path, err := pathbytes.New([][]byte{[]byte("cache"), []byte("item")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionQuarantinePath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	precondition := domain.FilesystemPrecondition{
		Target:   target,
		Required: required,
		Snapshot: quarantineRetainTestSnapshot(),
	}
	if err := precondition.Validate(); err != nil {
		t.Fatalf("filesystem precondition validation error = %v", err)
	}
	actionID, err := domain.NewActionID("quarantine-retain")
	if err != nil {
		t.Fatalf("NewActionID() error = %v", err)
	}
	capability, err := domain.NewCapabilityID("quarantine-retain")
	if err != nil {
		t.Fatalf("NewCapabilityID() error = %v", err)
	}
	action := domain.Action{
		ID:     actionID,
		Kind:   domain.ActionQuarantinePath,
		Target: target,
		Evidence: []domain.Evidence{{
			Kind:       domain.EvidenceFilesystemIdentity,
			Filesystem: &domain.FilesystemEvidence{Target: target, Snapshot: quarantineRetainTestSnapshot()},
		}},
		Precondition: domain.Precondition{
			Kind:       domain.PreconditionFilesystemIdentity,
			Filesystem: &precondition,
		},
		Risk:                  domain.RiskLow,
		Reversibility:         domain.ReversibilityRecoverable,
		EstimatedEffect:       domain.SizeFacts{LinkCount: domain.Uint64Fact{Known: true, Value: 1}},
		RequiredCapability:    capability,
		ProviderGuarantee:     domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeReadOnlyInventory},
		ExpectedPostcondition: domain.PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("action validation error = %v", err)
	}
	digest, err := domain.NewPlanDigest(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	return quarantineRetainRequest{
		action:       action,
		planDigest:   digest,
		root:         root,
		source:       path,
		basename:     "item",
		precondition: precondition,
	}
}

func quarantineRetainTestSnapshot() domain.FilesystemSnapshot {
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
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		MountID:     domain.Uint64Fact{Known: true, Value: 17},
	}
}

type quarantineRetainTestTicket struct {
	token              string
	root               domain.TrustedRootID
	source             pathbytes.BytePath
	actionID           domain.ActionID
	precondition       domain.FilesystemPrecondition
	trashLayoutBinding domain.TrashLayoutBinding
	event              recoveryport.Event
	outcome            recoveryport.Outcome
	metadata           recoveryport.MetadataDisposition
	closed             bool
}

func (ticket *quarantineRetainTestTicket) Token() string                { return ticket.token }
func (ticket *quarantineRetainTestTicket) RootID() domain.TrustedRootID { return ticket.root }
func (ticket *quarantineRetainTestTicket) Source() pathbytes.BytePath   { return ticket.source }
func (ticket *quarantineRetainTestTicket) ActionID() domain.ActionID    { return ticket.actionID }
func (ticket *quarantineRetainTestTicket) ActionKind() domain.ActionKind {
	return domain.ActionQuarantinePath
}
func (ticket *quarantineRetainTestTicket) Destination() recoveryport.Destination {
	return recoveryport.DestinationQuarantine
}
func (ticket *quarantineRetainTestTicket) Precondition() domain.FilesystemPrecondition {
	return ticket.precondition
}
func (ticket *quarantineRetainTestTicket) Event() recoveryport.Event     { return ticket.event }
func (ticket *quarantineRetainTestTicket) Outcome() recoveryport.Outcome { return ticket.outcome }
func (ticket *quarantineRetainTestTicket) MetadataDisposition() recoveryport.MetadataDisposition {
	return ticket.metadata
}
func (ticket *quarantineRetainTestTicket) TrashLayoutBinding() domain.TrashLayoutBinding {
	return ticket.trashLayoutBinding
}
func (ticket *quarantineRetainTestTicket) Closed() bool { return ticket.closed }

type quarantineRetainTestLedger struct {
	ticket                          *quarantineRetainTestTicket
	log                             []string
	reservations                    []recoveryport.Reservation
	transitions                     []recoveryport.Transition
	transitionErrors                map[recoveryport.Event]error
	nilTicketOnTransitionError      map[recoveryport.Event]bool
	nilTicketOnSuccessfulTransition map[recoveryport.Event]bool
	mutateTransitionTicket          map[recoveryport.Event]func(*quarantineRetainTestTicket)
	substituteTransitionToken       string
}

func newQuarantineRetainTestLedger(request quarantineRetainRequest) *quarantineRetainTestLedger {
	return &quarantineRetainTestLedger{
		ticket: &quarantineRetainTestTicket{
			token:        "ldc-" + string(bytes.Repeat([]byte{'a'}, 64)),
			root:         request.root,
			source:       request.source,
			actionID:     request.action.ID,
			precondition: request.precondition,
			event:        recoveryport.EventIntentReserved,
		},
		transitionErrors:                make(map[recoveryport.Event]error),
		nilTicketOnTransitionError:      make(map[recoveryport.Event]bool),
		nilTicketOnSuccessfulTransition: make(map[recoveryport.Event]bool),
		mutateTransitionTicket:          make(map[recoveryport.Event]func(*quarantineRetainTestTicket)),
	}
}

func (ledger *quarantineRetainTestLedger) Reserve(_ context.Context, _ domain.Action, _ domain.PlanDigest, reservation recoveryport.Reservation) (recoveryport.Ticket, error) {
	ledger.log = append(ledger.log, "reserve")
	ledger.reservations = append(ledger.reservations, reservation)
	return ledger.ticket, nil
}

func (ledger *quarantineRetainTestLedger) Transition(_ context.Context, _ recoveryport.Ticket, transition recoveryport.Transition) (recoveryport.Ticket, error) {
	ledger.log = append(ledger.log, "transition:"+string(transition.Event))
	ledger.transitions = append(ledger.transitions, transition)
	updated := *ledger.ticket
	updated.event = transition.Event
	updated.outcome = transition.ReconciliationOutcome
	updated.metadata = transition.MetadataDisposition
	updated.closed = transition.Event == recoveryport.EventReconciliationResolved && transition.ReconciliationOutcome != recoveryport.OutcomeMoveVerified
	if mutate := ledger.mutateTransitionTicket[transition.Event]; mutate != nil {
		mutate(&updated)
	}
	if ledger.substituteTransitionToken != "" {
		updated.token = ledger.substituteTransitionToken
		return &updated, nil
	}
	if ledger.nilTicketOnSuccessfulTransition[transition.Event] {
		return nil, nil
	}
	if err := ledger.transitionErrors[transition.Event]; err != nil && ledger.nilTicketOnTransitionError[transition.Event] {
		return nil, err
	}
	ledger.ticket = &updated
	return ledger.ticket, ledger.transitionErrors[transition.Event]
}

func (*quarantineRetainTestLedger) FindOutstanding(context.Context, domain.TrustedRootID, pathbytes.BytePath) (recoveryport.Ticket, bool, error) {
	return nil, false, nil
}

func (*quarantineRetainTestLedger) ListOutstanding(context.Context, int) ([]recoveryport.Ticket, error) {
	return nil, nil
}

func (ledger *quarantineRetainTestLedger) Reload(context.Context, recoveryport.Ticket) (recoveryport.Ticket, error) {
	return ledger.ticket, nil
}

func newQuarantineRetainTestOperations(
	ledger *quarantineRetainTestLedger,
	disposition linuxfs.QuarantineRetainDisposition,
	retainErr error,
) quarantineRetainOperations {
	return quarantineRetainOperations{
		retain: func(context.Context, *linuxfs.ParentLease, string, *linuxfs.PrivateDirectoryLease, string, domain.FilesystemPrecondition) (linuxfs.QuarantineRetainDisposition, error) {
			ledger.log = append(ledger.log, "retain")
			return disposition, retainErr
		},
	}
}

func newQuarantineRetainTestStore(root domain.TrustedRootID, retain quarantineRetainer) *Store {
	if retain == nil {
		retain = func(context.Context, *linuxfs.ParentLease, string, *linuxfs.PrivateDirectoryLease, string, domain.FilesystemPrecondition) (linuxfs.QuarantineRetainDisposition, error) {
			return linuxfs.QuarantineRetainNotApplied, linuxfs.ErrUnsupported
		}
	}
	return &Store{
		directory:    &testPrivateDirectory{rootID: root, kind: mounts.LayoutPrivateQuarantine},
		privateLease: &linuxfs.PrivateDirectoryLease{},
		rootID:       root,
		retain:       retain,
	}
}

func assertQuarantineRetainTestLog(t *testing.T, got []string, want ...string) {
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
