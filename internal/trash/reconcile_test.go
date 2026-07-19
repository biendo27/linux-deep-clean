//go:build linux

package trash

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

func TestReconcileIndeterminateTrashMetadataRecordsRetainedFactOnlyAfterExactProbe(t *testing.T) {
	request, ledger := newTrashMetadataReconcileTestRequest(t)
	contents := mustTrashMetadataReconcileInfo(t, ledger.ticket.token, request.basis, request.metadataPath)
	operations := newTrashMetadataReconcileTestOperations(t, ledger, contents, true, nil)

	ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
	if err != nil {
		t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved ||
		ticket.Outcome() != recoveryport.OutcomeNotApplied ||
		ticket.MetadataDisposition() != recoveryport.MetadataRetained || !ticket.Closed() {
		t.Fatalf("reconciled ticket = %#v, want closed not-applied metadata-retained reconciliation", ticket)
	}
	if len(ledger.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want exactly one", len(ledger.transitions))
	}
	assertTrashMetadataReconcileTransition(t, ledger.transitions[0], recoveryport.MetadataRetained)
	assertTrashMoveTestLog(t, ledger.log,
		"select",
		"probe",
		"transition:reconciliation_resolved",
		"close",
	)
}

func TestReconcileIndeterminateTrashMetadataRecordsAbsentFactOnlyAfterExactProbe(t *testing.T) {
	request, ledger := newTrashMetadataReconcileTestRequest(t)
	operations := newTrashMetadataReconcileTestOperations(t, ledger, nil, false, nil)

	ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
	if err != nil {
		t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v", err)
	}
	if ticket == nil || ticket.MetadataDisposition() != recoveryport.MetadataAbsent || !ticket.Closed() {
		t.Fatalf("reconciled ticket = %#v, want closed metadata-absent reconciliation", ticket)
	}
	if len(ledger.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want exactly one", len(ledger.transitions))
	}
	assertTrashMetadataReconcileTransition(t, ledger.transitions[0], recoveryport.MetadataAbsent)
	assertTrashMoveTestLog(t, ledger.log,
		"select",
		"probe",
		"transition:reconciliation_resolved",
		"close",
	)
}

func TestReconcileIndeterminateTrashMetadataRetainsMalformedOrMismatchedRecordWithoutTransition(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents func(t *testing.T, request trashMetadataReconcileRequest, token string) []byte
	}{
		{
			name: "malformed",
			contents: func(_ *testing.T, _ trashMetadataReconcileRequest, _ string) []byte {
				return []byte("not LDC metadata")
			},
		},
		{
			name: "mismatched metadata path",
			contents: func(t *testing.T, request trashMetadataReconcileRequest, token string) []byte {
				t.Helper()
				return mustTrashMetadataReconcileInfo(t, token, request.basis, mustTrashMetadataReconcilePath(t, "other", "item"))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, ledger := newTrashMetadataReconcileTestRequest(t)
			contents := test.contents(t, request, ledger.ticket.token)
			originalContents := append([]byte(nil), contents...)
			operations := newTrashMetadataReconcileTestOperations(t, ledger, contents, true, nil)

			ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
			if !errors.Is(err, linuxfs.ErrDrifted) {
				t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want ErrDrifted", err)
			}
			if ticket != ledger.ticket {
				t.Fatalf("ticket after rejected record = %#v, want original outstanding ticket %#v", ticket, ledger.ticket)
			}
			if ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() {
				t.Fatalf("ticket after rejected record = %#v, want open metadata-indeterminate ticket", ticket)
			}
			if len(ledger.transitions) != 0 {
				t.Fatalf("malformed or mismatched record caused transitions: %#v", ledger.transitions)
			}
			if !bytes.Equal(contents, originalContents) {
				t.Fatal("reconciliation mutated the probed metadata bytes")
			}
			assertTrashMoveTestLog(t, ledger.log, "select", "probe", "close")
		})
	}
}

func TestReconcileIndeterminateTrashMetadataLeavesTicketOutstandingWhenProbeCannotProveFact(t *testing.T) {
	request, ledger := newTrashMetadataReconcileTestRequest(t)
	probeErr := errors.New("metadata identity changed while probing")
	operations := newTrashMetadataReconcileTestOperations(t, ledger, nil, false, probeErr)

	ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
	if !errors.Is(err, probeErr) {
		t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want probe error", err)
	}
	if ticket != ledger.ticket || ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() {
		t.Fatalf("ticket after failed probe = %#v, want original outstanding ticket", ticket)
	}
	if len(ledger.transitions) != 0 {
		t.Fatalf("failed probe caused transitions: %#v", ledger.transitions)
	}
	assertTrashMoveTestLog(t, ledger.log, "select", "probe", "close")
}

func TestReconcileIndeterminateTrashMetadataReloadsCurrentTicketBeforeDescriptorSelection(t *testing.T) {
	reloadInterrupted := errors.New("ticket reload interrupted")
	for _, test := range []struct {
		name      string
		configure func(*trashMoveTestLedger) (recoveryport.Ticket, error)
		wantError error
		wantFresh bool
	}{
		{
			name: "ledger cannot reload ticket",
			configure: func(*trashMoveTestLedger) (recoveryport.Ticket, error) {
				return nil, reloadInterrupted
			},
			wantError: reloadInterrupted,
		},
		{
			name: "ticket advanced after caller observation",
			configure: func(ledger *trashMoveTestLedger) (recoveryport.Ticket, error) {
				advanced := *ledger.ticket
				advanced.event = recoveryport.EventMetadataVerified
				return &advanced, nil
			},
			wantError: linuxfs.ErrUnsupported,
			wantFresh: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, base := newTrashMetadataReconcileTestRequest(t)
			reloaded, reloadErr := test.configure(base)
			ledger := &trashMetadataReloadLedger{
				trashMoveTestLedger: base,
				reloaded:            reloaded,
				reloadErr:           reloadErr,
			}
			selected := false
			operations := trashMetadataReconcileOperations{
				selectDirectories: func() (*linuxfs.TrashDirectories, error) {
					selected = true
					return nil, nil
				},
				probe: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
					t.Fatal("unreloaded ticket reached metadata probe")
					return nil, false, nil
				},
				closeDirectories: func(*linuxfs.TrashDirectories) error { return nil },
			}

			ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, base.ticket, request, operations)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want %v", err, test.wantError)
			}
			if ledger.reloads != 1 {
				t.Fatalf("ledger reloads = %d, want exactly one before descriptor selection", ledger.reloads)
			}
			if selected {
				t.Fatal("unreloaded or advanced ticket reached descriptor selection")
			}
			if len(base.transitions) != 0 {
				t.Fatalf("unreloaded or advanced ticket caused transitions: %#v", base.transitions)
			}
			if test.wantFresh {
				if ticket != reloaded {
					t.Fatalf("advanced ticket result = %#v, want current ticket %#v", ticket, reloaded)
				}
			} else if ticket != base.ticket {
				t.Fatalf("reload failure ticket = %#v, want original ticket %#v", ticket, base.ticket)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMetadataReloadsBeforeLeaseDerivedRequestMapping(t *testing.T) {
	_, base := newTrashMetadataReconcileTestRequest(t)
	reloadInterrupted := errors.New("ticket reload interrupted before metadata mapping")
	ledger := &trashMetadataReloadLedger{
		trashMoveTestLedger: base,
		reloadErr:           reloadInterrupted,
	}

	ticket, err := ReconcileIndeterminateTrashMetadata(context.Background(), ledger, base.ticket, &mounts.TrashLease{})
	if !errors.Is(err, reloadInterrupted) {
		t.Fatalf("ReconcileIndeterminateTrashMetadata() error = %v, want reload error %v", err, reloadInterrupted)
	}
	if ticket != base.ticket {
		t.Fatalf("ReconcileIndeterminateTrashMetadata() ticket = %#v, want original outstanding ticket %#v", ticket, base.ticket)
	}
	if ledger.reloads != 1 {
		t.Fatalf("ledger reloads = %d, want exactly one before lease-derived request mapping", ledger.reloads)
	}
	if len(base.transitions) != 0 || len(base.log) != 0 {
		t.Fatalf("reload failure before lease mapping caused ledger activity: transitions=%v log=%v", base.transitions, base.log)
	}
}

func TestReconcileIndeterminateTrashMetadataRejectsSameRootDifferentLayoutBeforeMappingOrProbe(t *testing.T) {
	request, base := newTrashMetadataReconcileTestRequest(t)
	ledger := &trashMetadataReloadLedger{
		trashMoveTestLedger: base,
		reloaded:            base.ticket,
	}
	otherBinding, err := domain.NewTrashLayoutBinding(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatalf("NewTrashLayoutBinding() error = %v", err)
	}
	authority := &trashMetadataReconcileTestAuthority{
		root:    request.root,
		anchor:  mounts.TrashAnchorFilesystemTop,
		binding: otherBinding,
		path:    request.metadataPath,
		basis:   mounts.TrashMetadataBasisTopRelative,
	}
	selected := false
	probed := false
	operations := trashMetadataReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			selected = true
			return nil, nil
		},
		probe: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
			probed = true
			return nil, false, nil
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error {
			t.Fatal("layout mismatch reached directory close")
			return nil
		},
	}

	ticket, err := reconcileIndeterminateTrashMetadataWithAuthority(context.Background(), ledger, base.ticket, authority, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("reconcileIndeterminateTrashMetadataWithAuthority() error = %v, want ErrUnsupported", err)
	}
	if ticket != base.ticket || ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() || ticket.Outcome() != "" || ticket.MetadataDisposition() != "" {
		t.Fatalf("layout mismatch result = %#v, want original open metadata-indeterminate ticket", ticket)
	}
	if ledger.reloads != 1 {
		t.Fatalf("ledger reloads = %d, want exactly one before authority mapping", ledger.reloads)
	}
	if authority.identityCalls != 1 || authority.pathCalls != 0 || authority.basisCalls != 0 {
		t.Fatalf("authority calls = identity:%d path:%d basis:%d, want identity only", authority.identityCalls, authority.pathCalls, authority.basisCalls)
	}
	if selected || probed {
		t.Fatalf("layout mismatch reached descriptor selection/probe: selected=%t probed=%t", selected, probed)
	}
	if len(base.transitions) != 0 || len(base.log) != 0 {
		t.Fatalf("layout mismatch caused durable activity: transitions=%v log=%v", base.transitions, base.log)
	}
}

func TestReconcileIndeterminateTrashMetadataRejectsUnboundTicketBeforeAuthorityMapping(t *testing.T) {
	request, base := newTrashMetadataReconcileTestRequest(t)
	request.trashLayoutBinding = domain.TrashLayoutBinding{}
	base.ticket.trashLayoutBinding = domain.TrashLayoutBinding{}
	ledger := &trashMetadataReloadLedger{
		trashMoveTestLedger: base,
		reloaded:            base.ticket,
	}
	authority := &trashMetadataReconcileTestAuthority{
		root:    request.root,
		anchor:  mounts.TrashAnchorFilesystemTop,
		binding: request.trashLayoutBinding,
		path:    request.metadataPath,
		basis:   mounts.TrashMetadataBasisTopRelative,
	}
	selected := false
	probed := false
	operations := trashMetadataReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			selected = true
			return nil, nil
		},
		probe: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
			probed = true
			return nil, false, nil
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error {
			t.Fatal("unbound ticket reached directory close")
			return nil
		},
	}

	ticket, err := reconcileIndeterminateTrashMetadataWithAuthority(context.Background(), ledger, base.ticket, authority, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("reconcileIndeterminateTrashMetadataWithAuthority() error = %v, want ErrUnsupported", err)
	}
	if ticket != base.ticket || ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() {
		t.Fatalf("unbound ticket result = %#v, want original open metadata-indeterminate ticket", ticket)
	}
	if ledger.reloads != 1 {
		t.Fatalf("ledger reloads = %d, want exactly one before authority mapping", ledger.reloads)
	}
	if authority.identityCalls != 0 || authority.pathCalls != 0 || authority.basisCalls != 0 {
		t.Fatalf("authority calls = identity:%d path:%d basis:%d, want none", authority.identityCalls, authority.pathCalls, authority.basisCalls)
	}
	if selected || probed {
		t.Fatalf("unbound ticket reached descriptor selection/probe: selected=%t probed=%t", selected, probed)
	}
	if len(base.transitions) != 0 || len(base.log) != 0 {
		t.Fatalf("unbound ticket caused durable activity: transitions=%v log=%v", base.transitions, base.log)
	}
}

func TestTransitionTrashMetadataReconcileTicketRejectsSubstitutedTicketIdentity(t *testing.T) {
	request, base := newTrashMetadataReconcileTestRequest(t)
	original := base.ticket
	ledger := &trashTokenSubstitutionLedger{
		trashMoveTestLedger: base,
		token:               "ldc-" + string(bytes.Repeat([]byte{'c'}, 64)),
	}

	ticket, err := transitionTrashMetadataReconcileTicket(context.Background(), ledger, original, request, recoveryport.MetadataAbsent)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("transitionTrashMetadataReconcileTicket() error = %v, want ErrUnsupported", err)
	}
	if ticket != original {
		t.Fatalf("transitionTrashMetadataReconcileTicket() ticket = %#v, want original ticket %#v", ticket, original)
	}
	if len(base.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want one attempted transition", len(base.transitions))
	}
}

func TestReconcileIndeterminateTrashMetadataPreservesCorrectTicketOnLedgerPublicationAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name         string
		nilSuccessor bool
	}{
		{name: "no successor", nilSuccessor: true},
		{name: "returned successor", nilSuccessor: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, ledger := newTrashMetadataReconcileTestRequest(t)
			transitionErr := errors.New("reconciliation ledger publication interrupted")
			ledger.transitionErrors[recoveryport.EventReconciliationResolved] = transitionErr
			ledger.nilTicketOnTransitionError[recoveryport.EventReconciliationResolved] = test.nilSuccessor
			operations := newTrashMetadataReconcileTestOperations(t, ledger, nil, false, nil)

			ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
			if !errors.Is(err, transitionErr) {
				t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want transition error", err)
			}
			if len(ledger.transitions) != 1 {
				t.Fatalf("ledger transitions = %d, want exactly one without retry", len(ledger.transitions))
			}
			if test.nilSuccessor {
				if ticket != ledger.ticket || ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() {
					t.Fatalf("ticket after unpublished transition = %#v, want original outstanding ticket", ticket)
				}
			} else if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved || !ticket.Closed() {
				t.Fatalf("ticket after published transition = %#v, want returned closed successor", ticket)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMetadataTreatsNilSuccessfulTransitionAsIndeterminate(t *testing.T) {
	request, base := newTrashMetadataReconcileTestRequest(t)
	ledger := &trashMetadataNilTransitionLedger{trashMoveTestLedger: base}
	operations := newTrashMetadataReconcileTestOperations(t, base, nil, false, nil)
	original := base.ticket

	ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, original, request, operations)
	if !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want ErrInterrupted", err)
	}
	if ticket != original || ticket.Event() != recoveryport.EventMetadataIndeterminate || ticket.Closed() {
		t.Fatalf("ticket after nil successful transition = %#v, want original outstanding ticket", ticket)
	}
	if len(base.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want exactly one without retry", len(base.transitions))
	}
	assertTrashMoveTestLog(t, base.log,
		"select",
		"probe",
		"transition:reconciliation_resolved",
		"close",
	)
}

func TestReconcileIndeterminateTrashMetadataRejectsOtherTicketStatesBeforeProbe(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*trashMoveTestTicket)
	}{
		{
			name: "metadata verified",
			mutate: func(ticket *trashMoveTestTicket) {
				ticket.event = recoveryport.EventMetadataVerified
			},
		},
		{
			name: "closed indeterminate ticket",
			mutate: func(ticket *trashMoveTestTicket) {
				ticket.closed = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, ledger := newTrashMetadataReconcileTestRequest(t)
			test.mutate(ledger.ticket)
			operations := trashMetadataReconcileOperations{
				selectDirectories: func() (*linuxfs.TrashDirectories, error) {
					t.Fatal("invalid ticket reached descriptor selection")
					return nil, nil
				},
				probe: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
					t.Fatal("invalid ticket reached metadata probe")
					return nil, false, nil
				},
				closeDirectories: func(*linuxfs.TrashDirectories) error { return nil },
			}

			ticket, err := reconcileIndeterminateTrashMetadataWith(context.Background(), ledger, ledger.ticket, request, operations)
			if !errors.Is(err, linuxfs.ErrUnsupported) {
				t.Fatalf("reconcileIndeterminateTrashMetadataWith() error = %v, want ErrUnsupported", err)
			}
			if ticket != ledger.ticket {
				t.Fatalf("invalid ticket changed to %#v, want original %#v", ticket, ledger.ticket)
			}
			if len(ledger.transitions) != 0 || len(ledger.log) != 0 {
				t.Fatalf("invalid ticket caused ledger activity: transitions=%v log=%v", ledger.transitions, ledger.log)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMetadataRejectsMissingTrustedLeaseWithoutTransition(t *testing.T) {
	_, ledger := newTrashMetadataReconcileTestRequest(t)

	ticket, err := ReconcileIndeterminateTrashMetadata(context.Background(), ledger, ledger.ticket, nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("ReconcileIndeterminateTrashMetadata(nil lease) error = %v, want ErrUnsupported", err)
	}
	if ticket != ledger.ticket {
		t.Fatalf("ReconcileIndeterminateTrashMetadata(nil lease) ticket = %#v, want original outstanding ticket %#v", ticket, ledger.ticket)
	}
	if len(ledger.transitions) != 0 || len(ledger.log) != 0 {
		t.Fatalf("nil lease caused ledger activity: transitions=%v log=%v", ledger.transitions, ledger.log)
	}
}

func newTrashMetadataReconcileTestRequest(t *testing.T) (trashMetadataReconcileRequest, *trashMoveTestLedger) {
	t.Helper()
	move := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(move)
	ledger.ticket.event = recoveryport.EventMetadataIndeterminate
	return trashMetadataReconcileRequest{
		root:               move.root,
		source:             move.source,
		actionID:           move.action.ID,
		precondition:       move.precondition,
		trashLayoutBinding: move.trashLayoutBinding,
		metadataPath:       move.source,
		basis:              trashPathBasisTopDirectoryRelative,
	}, ledger
}

func newTrashMetadataReconcileTestOperations(
	t *testing.T,
	ledger *trashMoveTestLedger,
	contents []byte,
	present bool,
	probeErr error,
) trashMetadataReconcileOperations {
	t.Helper()
	return trashMetadataReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			ledger.log = append(ledger.log, "select")
			return &linuxfs.TrashDirectories{}, nil
		},
		probe: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
			ledger.log = append(ledger.log, "probe")
			return contents, present, probeErr
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error {
			ledger.log = append(ledger.log, "close")
			return nil
		},
	}
}

type trashMetadataReloadLedger struct {
	*trashMoveTestLedger
	reloaded  recoveryport.Ticket
	reloadErr error
	reloads   int
}

type trashMetadataReconcileTestAuthority struct {
	root          domain.TrustedRootID
	anchor        mounts.TrashAnchorKind
	binding       domain.TrashLayoutBinding
	path          pathbytes.BytePath
	basis         mounts.TrashMetadataBasis
	identityCalls int
	pathCalls     int
	basisCalls    int
}

func (authority *trashMetadataReconcileTestAuthority) RootID() domain.TrustedRootID {
	return authority.root
}

func (authority *trashMetadataReconcileTestAuthority) AnchorKind() mounts.TrashAnchorKind {
	return authority.anchor
}

func (authority *trashMetadataReconcileTestAuthority) MetadataReconciliationIdentity() (domain.TrashLayoutBinding, error) {
	authority.identityCalls++
	return authority.binding, nil
}

func (authority *trashMetadataReconcileTestAuthority) MetadataPathFor(pathbytes.BytePath) (pathbytes.BytePath, error) {
	authority.pathCalls++
	return authority.path, nil
}

func (authority *trashMetadataReconcileTestAuthority) MetadataBasis() mounts.TrashMetadataBasis {
	authority.basisCalls++
	return authority.basis
}

func (ledger *trashMetadataReloadLedger) Reload(context.Context, recoveryport.Ticket) (recoveryport.Ticket, error) {
	ledger.reloads++
	return ledger.reloaded, ledger.reloadErr
}

type trashMetadataNilTransitionLedger struct {
	*trashMoveTestLedger
}

func (ledger *trashMetadataNilTransitionLedger) Transition(
	ctx context.Context,
	ticket recoveryport.Ticket,
	transition recoveryport.Transition,
) (recoveryport.Ticket, error) {
	if _, err := ledger.trashMoveTestLedger.Transition(ctx, ticket, transition); err != nil {
		return nil, err
	}
	return nil, nil
}

func mustTrashMetadataReconcileInfo(t *testing.T, token string, basis trashPathBasis, metadataPath pathbytes.BytePath) []byte {
	t.Helper()
	info, err := newTrashInfo(token, basis, metadataPath, time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("newTrashInfo() error = %v", err)
	}
	contents, err := info.marshal()
	if err != nil {
		t.Fatalf("trashInfo.marshal() error = %v", err)
	}
	return contents
}

func mustTrashMetadataReconcilePath(t *testing.T, components ...string) pathbytes.BytePath {
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

func assertTrashMetadataReconcileTransition(t *testing.T, transition recoveryport.Transition, disposition recoveryport.MetadataDisposition) {
	t.Helper()
	if transition.Event != recoveryport.EventReconciliationResolved ||
		transition.ReconciliationOutcome != recoveryport.OutcomeNotApplied ||
		transition.MetadataDisposition != disposition {
		t.Fatalf("reconciliation transition = %#v, want resolved/not-applied/%q", transition, disposition)
	}
}
