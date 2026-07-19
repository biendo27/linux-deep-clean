//go:build linux

package trash

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

func TestReconcileIndeterminateTrashMoveRecordsVerifiedRetainedPairOnly(t *testing.T) {
	request, ledger := newTrashMoveReconcileTestRequest(t)
	info := mustTrashMetadataReconcileInfo(t, ledger.ticket.token, request.basis, request.metadataPath)
	operations := newTrashMoveReconcileTestOperations(t, ledger, []trashMoveReconcileProbe{
		{contents: info, metadataPresent: true, sourceAbsent: true, contentPresent: true},
		{contents: info, metadataPresent: true, sourceAbsent: true},
	})

	ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, ledger.ticket, request, operations)
	if err != nil {
		t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v", err)
	}
	if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved ||
		ticket.Outcome() != recoveryport.OutcomeMoveVerified ||
		ticket.MetadataDisposition() != "" || ticket.Closed() {
		t.Fatalf("reconciled ticket = %#v, want open move-verified reconciliation", ticket)
	}
	if len(ledger.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want exactly one", len(ledger.transitions))
	}
	assertTrashMoveReconcileTransition(t, ledger.transitions[0])
	assertTrashMoveTestLog(t, ledger.log,
		"select",
		"probe-metadata",
		"probe-source-absence",
		"probe-content",
		"probe-source-absence",
		"probe-metadata",
		"transition:reconciliation_resolved",
		"close",
	)
}

func TestReconcileIndeterminateTrashMoveLeavesAllNonpositiveEvidenceOutstanding(t *testing.T) {
	for _, test := range []struct {
		name   string
		probes func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe
	}{
		{
			name: "metadata absent",
			probes: func(_ *testing.T, _ trashMoveReconcileRequest, _ string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{{metadataPresent: false}}
			},
		},
		{
			name: "content absent",
			probes: func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{{
					contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath),
					metadataPresent: true,
					sourceAbsent:    true,
					contentPresent:  false,
				}}
			},
		},
		{
			name: "source remains present initially",
			probes: func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{{
					contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath),
					metadataPresent: true,
					sourceAbsent:    false,
				}}
			},
		},
		{
			name: "source becomes present before second absence proof",
			probes: func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{
					{
						contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath),
						metadataPresent: true,
						sourceAbsent:    true,
						contentPresent:  true,
					},
					{sourceAbsent: false},
				}
			},
		},
		{
			name: "malformed metadata",
			probes: func(_ *testing.T, _ trashMoveReconcileRequest, _ string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{{contents: []byte("not owned Trash metadata"), metadataPresent: true}}
			},
		},
		{
			name: "mismatched metadata path",
			probes: func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{{
					contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, mustTrashMetadataReconcilePath(t, "other", "item")),
					metadataPresent: true,
				}}
			},
		},
		{
			name: "metadata changed around content evidence",
			probes: func(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
				return []trashMoveReconcileProbe{
					{
						contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath),
						metadataPresent: true,
						sourceAbsent:    true,
						contentPresent:  true,
					},
					{
						contents:        mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath),
						metadataPresent: true,
						sourceAbsent:    true,
					},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, ledger := newTrashMoveReconcileTestRequest(t)
			probes := test.probes(t, request, ledger.ticket.token)
			if test.name == "metadata changed around content evidence" {
				probes[1].contents = bytes.Replace(
					probes[1].contents,
					[]byte("DeletionDate=2026-07-18"),
					[]byte("DeletionDate=2026-07-19"),
					1,
				)
			}
			operations := newTrashMoveReconcileTestOperations(t, ledger, probes)

			ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, ledger.ticket, request, operations)
			if !errors.Is(err, linuxfs.ErrDrifted) {
				t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want ErrDrifted", err)
			}
			if ticket != ledger.ticket || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
				t.Fatalf("ticket after nonpositive evidence = %#v, want original outstanding ticket", ticket)
			}
			if len(ledger.transitions) != 0 {
				t.Fatalf("nonpositive evidence caused transitions: %#v", ledger.transitions)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMoveStopsOnProbeFailureWithoutTransition(t *testing.T) {
	request, ledger := newTrashMoveReconcileTestRequest(t)
	probeErr := errors.New("files token changed while probing")
	operations := newTrashMoveReconcileTestOperations(t, ledger, []trashMoveReconcileProbe{{
		metadataErr: probeErr,
	}})

	ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, ledger.ticket, request, operations)
	if !errors.Is(err, probeErr) {
		t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want probe error", err)
	}
	if ticket != ledger.ticket || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
		t.Fatalf("ticket after failed probe = %#v, want original outstanding ticket", ticket)
	}
	if len(ledger.transitions) != 0 {
		t.Fatalf("failed probe caused transitions: %#v", ledger.transitions)
	}
	assertTrashMoveTestLog(t, ledger.log, "select", "probe-metadata", "close")
}

func TestReconcileIndeterminateTrashMoveStopsOnSourceAbsenceFailureWithoutTransition(t *testing.T) {
	request, ledger := newTrashMoveReconcileTestRequest(t)
	probeErr := errors.New("source name observation interrupted")
	operations := newTrashMoveReconcileTestOperations(t, ledger, []trashMoveReconcileProbe{{
		contents:        mustTrashMetadataReconcileInfo(t, ledger.ticket.token, request.basis, request.metadataPath),
		metadataPresent: true,
		sourceErr:       probeErr,
	}})

	ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, ledger.ticket, request, operations)
	if !errors.Is(err, probeErr) {
		t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want source probe error", err)
	}
	if ticket != ledger.ticket || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
		t.Fatalf("ticket after failed source probe = %#v, want original outstanding ticket", ticket)
	}
	if len(ledger.transitions) != 0 {
		t.Fatalf("failed source probe caused transitions: %#v", ledger.transitions)
	}
	assertTrashMoveTestLog(t, ledger.log, "select", "probe-metadata", "probe-source-absence", "close")
}

func TestReconcileIndeterminateTrashMoveRejectsDifferentOrUnboundLayoutBeforeMappingOrProbe(t *testing.T) {
	for _, test := range []struct {
		name          string
		mutateTicket  func(*trashMoveTestTicket)
		binding       func(t *testing.T, request trashMoveReconcileRequest) domain.TrashLayoutBinding
		wantIdentity  int
		wantPathCalls int
	}{
		{
			name: "same root different layout",
			binding: func(t *testing.T, _ trashMoveReconcileRequest) domain.TrashLayoutBinding {
				t.Helper()
				binding, err := domain.NewTrashLayoutBinding(bytes.Repeat([]byte{3}, 32))
				if err != nil {
					t.Fatalf("NewTrashLayoutBinding() error = %v", err)
				}
				return binding
			},
			wantIdentity: 1,
		},
		{
			name: "unbound legacy ticket",
			mutateTicket: func(ticket *trashMoveTestTicket) {
				ticket.trashLayoutBinding = domain.TrashLayoutBinding{}
			},
			binding: func(_ *testing.T, request trashMoveReconcileRequest) domain.TrashLayoutBinding {
				return request.trashLayoutBinding
			},
			wantIdentity: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, base := newTrashMoveReconcileTestRequest(t)
			if test.mutateTicket != nil {
				test.mutateTicket(base.ticket)
				request.trashLayoutBinding = base.ticket.trashLayoutBinding
			}
			authority := &trashMetadataReconcileTestAuthority{
				root:    request.root,
				anchor:  mounts.TrashAnchorFilesystemTop,
				binding: test.binding(t, request),
				path:    request.metadataPath,
				basis:   mounts.TrashMetadataBasisTopRelative,
			}
			operations := trashMoveReconcileOperations{
				selectDirectories: func() (*linuxfs.TrashDirectories, error) {
					t.Fatal("layout mismatch reached descriptor selection")
					return nil, nil
				},
				probeMetadata: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
					t.Fatal("layout mismatch reached metadata probe")
					return nil, false, nil
				},
				probeContent: func(context.Context, *linuxfs.TrashDirectories, string, domain.FilesystemPrecondition) (bool, error) {
					t.Fatal("layout mismatch reached content probe")
					return false, nil
				},
				probeSourceAbsence: func(context.Context, *linuxfs.ParentLease, string, domain.FilesystemPrecondition) (bool, error) {
					t.Fatal("layout mismatch reached source absence probe")
					return false, nil
				},
				closeDirectories: func(*linuxfs.TrashDirectories) error {
					t.Fatal("layout mismatch reached directory close")
					return nil
				},
			}

			ticket, err := reconcileIndeterminateTrashMoveWithAuthority(context.Background(), base, base.ticket, nil, authority, operations)
			if !errors.Is(err, linuxfs.ErrUnsupported) {
				t.Fatalf("reconcileIndeterminateTrashMoveWithAuthority() error = %v, want ErrUnsupported", err)
			}
			if ticket != base.ticket || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
				t.Fatalf("layout mismatch result = %#v, want original outstanding ticket", ticket)
			}
			if authority.identityCalls != test.wantIdentity || authority.pathCalls != test.wantPathCalls || authority.basisCalls != 0 {
				t.Fatalf("authority calls = identity:%d path:%d basis:%d, want identity:%d path:%d basis:0", authority.identityCalls, authority.pathCalls, authority.basisCalls, test.wantIdentity, test.wantPathCalls)
			}
			if len(base.transitions) != 0 || len(base.log) != 0 {
				t.Fatalf("layout mismatch caused durable activity: transitions=%v log=%v", base.transitions, base.log)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMoveReloadsCurrentTicketBeforeDescriptorSelection(t *testing.T) {
	request, base := newTrashMoveReconcileTestRequest(t)
	advanced := *base.ticket
	advanced.event = recoveryport.EventMoveVerified
	ledger := &trashMetadataReloadLedger{
		trashMoveTestLedger: base,
		reloaded:            &advanced,
	}
	operations := trashMoveReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			t.Fatal("advanced ticket reached descriptor selection")
			return nil, nil
		},
		probeMetadata: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
			t.Fatal("advanced ticket reached metadata probe")
			return nil, false, nil
		},
		probeContent: func(context.Context, *linuxfs.TrashDirectories, string, domain.FilesystemPrecondition) (bool, error) {
			t.Fatal("advanced ticket reached content probe")
			return false, nil
		},
		probeSourceAbsence: func(context.Context, *linuxfs.ParentLease, string, domain.FilesystemPrecondition) (bool, error) {
			t.Fatal("advanced ticket reached source absence probe")
			return false, nil
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error { return nil },
	}

	ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, base.ticket, request, operations)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want ErrUnsupported", err)
	}
	if ledger.reloads != 1 {
		t.Fatalf("ledger reloads = %d, want exactly one before descriptor selection", ledger.reloads)
	}
	if ticket != &advanced {
		t.Fatalf("advanced ticket result = %#v, want current ticket %#v", ticket, &advanced)
	}
	if len(base.transitions) != 0 || len(base.log) != 0 {
		t.Fatalf("advanced ticket caused durable activity: transitions=%v log=%v", base.transitions, base.log)
	}
}

func TestReconcileIndeterminateTrashMoveRejectsMissingSourceOrLeaseWithoutTransition(t *testing.T) {
	for _, test := range []struct {
		name   string
		source *linuxfs.ParentLease
		lease  *mounts.TrashLease
	}{
		{name: "missing source", lease: &mounts.TrashLease{}},
		{name: "missing lease", source: &linuxfs.ParentLease{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, ledger := newTrashMoveReconcileTestRequest(t)

			ticket, err := ReconcileIndeterminateTrashMove(context.Background(), ledger, ledger.ticket, test.source, test.lease)
			if !errors.Is(err, linuxfs.ErrUnsupported) {
				t.Fatalf("ReconcileIndeterminateTrashMove() error = %v, want ErrUnsupported", err)
			}
			if ticket != ledger.ticket {
				t.Fatalf("ReconcileIndeterminateTrashMove() ticket = %#v, want original outstanding ticket %#v", ticket, ledger.ticket)
			}
			if len(ledger.transitions) != 0 || len(ledger.log) != 0 {
				t.Fatalf("missing authority caused ledger activity: transitions=%v log=%v", ledger.transitions, ledger.log)
			}
		})
	}
}

func TestReloadTrashMoveReconcileTicketRejectsSubstitutedTicketIdentity(t *testing.T) {
	_, base := newTrashMoveReconcileTestRequest(t)
	foreign := *base.ticket
	foreign.token = "ldc-" + string(bytes.Repeat([]byte{'e'}, 64))
	ledger := &trashMetadataReloadLedger{
		trashMoveTestLedger: base,
		reloaded:            &foreign,
	}

	ticket, err := reloadTrashMoveReconcileTicket(context.Background(), ledger, base.ticket)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("reloadTrashMoveReconcileTicket() error = %v, want ErrUnsupported", err)
	}
	if ticket != base.ticket {
		t.Fatalf("reloadTrashMoveReconcileTicket() ticket = %#v, want original ticket %#v", ticket, base.ticket)
	}
	if ledger.reloads != 1 {
		t.Fatalf("ledger reloads = %d, want one", ledger.reloads)
	}
}

func TestReconcileIndeterminateTrashMovePreservesCorrectTicketOnLedgerPublicationAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name         string
		nilSuccessor bool
	}{
		{name: "no successor", nilSuccessor: true},
		{name: "returned successor", nilSuccessor: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, ledger := newTrashMoveReconcileTestRequest(t)
			transitionErr := errors.New("move reconciliation ledger publication interrupted")
			ledger.transitionErrors[recoveryport.EventReconciliationResolved] = transitionErr
			ledger.nilTicketOnTransitionError[recoveryport.EventReconciliationResolved] = test.nilSuccessor
			operations := newTrashMoveReconcileTestOperations(t, ledger, successfulTrashMoveReconcileProbes(t, request, ledger.ticket.token))

			ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, ledger.ticket, request, operations)
			if !errors.Is(err, transitionErr) {
				t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want transition error", err)
			}
			if len(ledger.transitions) != 1 {
				t.Fatalf("ledger transitions = %d, want exactly one without retry", len(ledger.transitions))
			}
			if test.nilSuccessor {
				if ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
					t.Fatalf("ticket after unpublished transition = %#v, want original outstanding ticket", ticket)
				}
			} else if ticket == nil || ticket.Event() != recoveryport.EventReconciliationResolved ||
				ticket.Outcome() != recoveryport.OutcomeMoveVerified || ticket.Closed() {
				t.Fatalf("ticket after published transition = %#v, want returned open move-verified successor", ticket)
			}
		})
	}
}

func TestReconcileIndeterminateTrashMoveTreatsNilSuccessfulTransitionAsIndeterminate(t *testing.T) {
	request, base := newTrashMoveReconcileTestRequest(t)
	ledger := &trashMetadataNilTransitionLedger{trashMoveTestLedger: base}
	operations := newTrashMoveReconcileTestOperations(t, base, successfulTrashMoveReconcileProbes(t, request, base.ticket.token))
	original := base.ticket

	ticket, err := reconcileIndeterminateTrashMoveWith(context.Background(), ledger, original, request, operations)
	if !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("reconcileIndeterminateTrashMoveWith() error = %v, want ErrInterrupted", err)
	}
	if ticket != original || ticket.Event() != recoveryport.EventMoveIndeterminate || ticket.Closed() {
		t.Fatalf("ticket after nil successful transition = %#v, want original outstanding ticket", ticket)
	}
	if len(base.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want one without retry", len(base.transitions))
	}
}

func TestTransitionTrashMoveReconcileTicketRejectsSubstitutedIdentity(t *testing.T) {
	request, base := newTrashMoveReconcileTestRequest(t)
	original := base.ticket
	ledger := &trashTokenSubstitutionLedger{
		trashMoveTestLedger: base,
		token:               "ldc-" + string(bytes.Repeat([]byte{'d'}, 64)),
	}

	ticket, err := transitionTrashMoveReconcileTicket(context.Background(), ledger, original, request)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("transitionTrashMoveReconcileTicket() error = %v, want ErrUnsupported", err)
	}
	if ticket != original {
		t.Fatalf("transitionTrashMoveReconcileTicket() ticket = %#v, want original ticket %#v", ticket, original)
	}
	if len(base.transitions) != 1 {
		t.Fatalf("ledger transitions = %d, want one attempted transition", len(base.transitions))
	}
}

func newTrashMoveReconcileTestRequest(t *testing.T) (trashMoveReconcileRequest, *trashMoveTestLedger) {
	t.Helper()
	move := newTrashMoveTestRequest(t)
	ledger := newTrashMoveTestLedger(move)
	ledger.ticket.event = recoveryport.EventMoveIndeterminate
	return trashMoveReconcileRequest{
		root:               move.root,
		source:             move.source,
		actionID:           move.action.ID,
		precondition:       move.precondition,
		trashLayoutBinding: move.trashLayoutBinding,
		metadataPath:       move.source,
		basis:              trashPathBasisTopDirectoryRelative,
	}, ledger
}

type trashMoveReconcileProbe struct {
	contents        []byte
	metadataPresent bool
	sourceAbsent    bool
	contentPresent  bool
	metadataErr     error
	sourceErr       error
	contentErr      error
}

func newTrashMoveReconcileTestOperations(
	t *testing.T,
	ledger *trashMoveTestLedger,
	probes []trashMoveReconcileProbe,
) trashMoveReconcileOperations {
	t.Helper()
	probeIndex := 0
	return trashMoveReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			ledger.log = append(ledger.log, "select")
			return &linuxfs.TrashDirectories{}, nil
		},
		probeMetadata: func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error) {
			ledger.log = append(ledger.log, "probe-metadata")
			if probeIndex >= len(probes) {
				t.Fatal("metadata probe exceeded configured evidence")
			}
			probe := probes[probeIndex]
			if probe.metadataErr != nil || !probe.metadataPresent {
				return probe.contents, probe.metadataPresent, probe.metadataErr
			}
			return append([]byte(nil), probe.contents...), true, nil
		},
		probeContent: func(context.Context, *linuxfs.TrashDirectories, string, domain.FilesystemPrecondition) (bool, error) {
			ledger.log = append(ledger.log, "probe-content")
			if probeIndex >= len(probes) {
				t.Fatal("content probe exceeded configured evidence")
			}
			probe := probes[probeIndex]
			probeIndex++
			return probe.contentPresent, probe.contentErr
		},
		probeSourceAbsence: func(context.Context, *linuxfs.ParentLease, string, domain.FilesystemPrecondition) (bool, error) {
			ledger.log = append(ledger.log, "probe-source-absence")
			if probeIndex >= len(probes) {
				t.Fatal("source absence probe exceeded configured evidence")
			}
			probe := probes[probeIndex]
			return probe.sourceAbsent, probe.sourceErr
		},
		closeDirectories: func(*linuxfs.TrashDirectories) error {
			ledger.log = append(ledger.log, "close")
			return nil
		},
	}
}

func successfulTrashMoveReconcileProbes(t *testing.T, request trashMoveReconcileRequest, token string) []trashMoveReconcileProbe {
	t.Helper()
	info := mustTrashMetadataReconcileInfo(t, token, request.basis, request.metadataPath)
	return []trashMoveReconcileProbe{
		{contents: info, metadataPresent: true, sourceAbsent: true, contentPresent: true},
		{contents: info, metadataPresent: true, sourceAbsent: true},
	}
}

func assertTrashMoveReconcileTransition(t *testing.T, transition recoveryport.Transition) {
	t.Helper()
	if transition.Event != recoveryport.EventReconciliationResolved ||
		transition.ReconciliationOutcome != recoveryport.OutcomeMoveVerified ||
		transition.MetadataDisposition != "" {
		t.Fatalf("reconciliation transition = %#v, want resolved/move-verified without metadata fact", transition)
	}
}
