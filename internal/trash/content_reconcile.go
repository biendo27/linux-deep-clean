//go:build linux

package trash

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

// ReconcileIndeterminateTrashMove performs the positive-only, read-only
// recovery step for one outstanding Trash move. It accepts only a current v2
// move-indeterminate ticket, binds it to the exact topology-qualified layout
// that created its metadata, and proves both the owned metadata and the
// files/<token> entry still match the ticket's immutable source facts while
// stable descriptor-relative observations prove the original source absent.
//
// A matching pair records OutcomeMoveVerified, which deliberately remains
// open for a later restoration boundary. Absence, malformed metadata, identity
// drift, or any uncertain probe result records no new durable fact and leaves
// the ticket outstanding. This function never scans, creates, moves,
// restores, removes, or cleans up Trash entries.
func ReconcileIndeterminateTrashMove(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	source *linuxfs.ParentLease,
	lease *mounts.TrashLease,
) (recoveryport.Ticket, error) {
	if source == nil || lease == nil {
		return ticket, fmt.Errorf("%w: source parent and topology-qualified Trash lease are required", linuxfs.ErrUnsupported)
	}
	return reconcileIndeterminateTrashMoveWithAuthority(
		ctx,
		ledger,
		ticket,
		source,
		lease,
		productionTrashMoveReconcileOperations(lease),
	)
}

// trashMoveReconcileRequest is the immutable data-only portion of an
// outstanding Trash move. It intentionally does not retain a descriptor,
// recovery handle, or filesystem authority.
type trashMoveReconcileRequest struct {
	root               domain.TrustedRootID
	source             pathbytes.BytePath
	actionID           domain.ActionID
	precondition       domain.FilesystemPrecondition
	trashLayoutBinding domain.TrashLayoutBinding
	metadataPath       pathbytes.BytePath
	basis              trashPathBasis
}

type trashMoveReconcileOperations struct {
	selectDirectories  func() (*linuxfs.TrashDirectories, error)
	probeMetadata      func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error)
	probeContent       func(context.Context, *linuxfs.TrashDirectories, string, domain.FilesystemPrecondition) (bool, error)
	probeSourceAbsence func(context.Context, *linuxfs.ParentLease, string, domain.FilesystemPrecondition) (bool, error)
	closeDirectories   func(*linuxfs.TrashDirectories) error
}

func productionTrashMoveReconcileOperations(lease *mounts.TrashLease) trashMoveReconcileOperations {
	return trashMoveReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			return SelectTrashRoot(lease)
		},
		probeMetadata:      linuxfs.ProbeOwnedTrashInfo,
		probeContent:       linuxfs.ProbeOwnedTrashContent,
		probeSourceAbsence: linuxfs.ProbeResolvedTargetAbsence,
		closeDirectories: func(directories *linuxfs.TrashDirectories) error {
			if directories == nil {
				return nil
			}
			return directories.Close()
		},
	}
}

// reconcileIndeterminateTrashMoveWithAuthority preserves the production
// authority boundary at its public entry point while keeping the ordering
// contract testable with data-only layout facts. The private authority is the
// same narrow shape used by metadata reconciliation; no test path can supply
// a descriptor or resolve an arbitrary filesystem location.
func reconcileIndeterminateTrashMoveWithAuthority(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	source *linuxfs.ParentLease,
	authority trashMetadataReconcileAuthority,
	operations trashMoveReconcileOperations,
) (recoveryport.Ticket, error) {
	if authority == nil {
		return ticket, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	current, err := reloadTrashMoveReconcileTicket(ctx, ledger, ticket)
	if err != nil {
		return current, err
	}
	request, err := newTrashMoveReconcileCoreRequest(current)
	if err != nil {
		return current, err
	}
	request, err = completeTrashMoveReconcileRequest(current, request, authority)
	if err != nil {
		return current, err
	}
	return reconcileIndeterminateTrashMoveWithSource(ctx, ledger, current, source, request, operations)
}

func newTrashMoveReconcileCoreRequest(ticket recoveryport.Ticket) (trashMoveReconcileRequest, error) {
	if ticket == nil {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: durable move-indeterminate Trash ticket is required", linuxfs.ErrUnsupported)
	}
	source, _, err := trashMovePathAndBasename(ticket.Source())
	if err != nil {
		return trashMoveReconcileRequest{}, err
	}
	request := trashMoveReconcileRequest{
		root:               ticket.RootID(),
		source:             source,
		actionID:           ticket.ActionID(),
		precondition:       ticket.Precondition(),
		trashLayoutBinding: ticket.TrashLayoutBinding(),
	}
	if err := validateTrashMoveReconcileCore(request); err != nil {
		return trashMoveReconcileRequest{}, err
	}
	return request, nil
}

func completeTrashMoveReconcileRequest(
	ticket recoveryport.Ticket,
	request trashMoveReconcileRequest,
	authority trashMetadataReconcileAuthority,
) (trashMoveReconcileRequest, error) {
	if err := requireTrashMoveIndeterminateTicket(ticket, request); err != nil {
		return trashMoveReconcileRequest{}, err
	}
	if authority == nil {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	if authority.RootID() != request.root {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: durable ticket and trusted Trash lease roots differ", linuxfs.ErrUnsupported)
	}
	if authority.AnchorKind() == "" {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: move reconciliation requires a topology-qualified Trash lease", linuxfs.ErrUnsupported)
	}
	if err := request.trashLayoutBinding.Validate(); err != nil {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: durable Trash ticket layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	candidateBinding, err := authority.MetadataReconciliationIdentity()
	if err != nil {
		return trashMoveReconcileRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	if err := candidateBinding.Validate(); err != nil {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: supplied Trash layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	if !request.trashLayoutBinding.Equal(candidateBinding) {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: durable Trash ticket does not bind the supplied layout authority", linuxfs.ErrUnsupported)
	}

	metadataPath, err := authority.MetadataPathFor(request.source)
	if err != nil {
		return trashMoveReconcileRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	metadataPath, err = pathbytes.New(metadataPath.Components())
	if err != nil {
		return trashMoveReconcileRequest{}, fmt.Errorf("%w: trusted Trash metadata path: %v", linuxfs.ErrUnsupported, err)
	}
	basis, err := trashPathBasisFromMounts(authority.MetadataBasis())
	if err != nil {
		return trashMoveReconcileRequest{}, err
	}
	request.metadataPath = metadataPath
	request.basis = basis
	if err := validateTrashMoveReconcileRequest(request); err != nil {
		return trashMoveReconcileRequest{}, err
	}
	return request, nil
}

func reconcileIndeterminateTrashMoveWith(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMoveReconcileRequest,
	operations trashMoveReconcileOperations,
) (recoveryport.Ticket, error) {
	return reconcileIndeterminateTrashMoveWithSource(ctx, ledger, ticket, nil, request, operations)
}

func reconcileIndeterminateTrashMoveWithSource(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	source *linuxfs.ParentLease,
	request trashMoveReconcileRequest,
	operations trashMoveReconcileOperations,
) (recoveryport.Ticket, error) {
	if err := validateTrashMoveReconcileExecution(ctx, ledger, ticket, request, operations); err != nil {
		return ticket, err
	}
	current, err := reloadTrashMoveReconcileTicket(ctx, ledger, ticket)
	if err != nil {
		return current, err
	}
	if err := validateTrashMoveReconcileExecution(ctx, ledger, current, request, operations); err != nil {
		return current, err
	}
	directories, err := operations.selectDirectories()
	if err != nil {
		return current, err
	}
	if directories == nil {
		return current, fmt.Errorf("%w: topology-qualified Trash selector returned no directories", linuxfs.ErrUnsupported)
	}
	defer func() { _ = operations.closeDirectories(directories) }()

	metadata, err := probeTrashMoveReconcileMetadata(ctx, operations.probeMetadata, directories, current.Token(), request)
	if err != nil {
		return current, err
	}
	if err := probeTrashMoveReconcileSourceAbsence(ctx, operations.probeSourceAbsence, source, request); err != nil {
		return current, err
	}
	contentPresent, err := operations.probeContent(ctx, directories, current.Token(), request.precondition)
	if err != nil {
		return current, err
	}
	if !contentPresent {
		return current, fmt.Errorf("%w: owned Trash content is absent", linuxfs.ErrDrifted)
	}
	if err := probeTrashMoveReconcileSourceAbsence(ctx, operations.probeSourceAbsence, source, request); err != nil {
		return current, err
	}
	finalMetadata, err := probeTrashMoveReconcileMetadata(ctx, operations.probeMetadata, directories, current.Token(), request)
	if err != nil {
		return current, err
	}
	if !bytes.Equal(metadata, finalMetadata) {
		return current, fmt.Errorf("%w: owned Trash metadata changed while proving retained content", linuxfs.ErrDrifted)
	}
	return transitionTrashMoveReconcileTicket(ctx, ledger, current, request)
}

func probeTrashMoveReconcileSourceAbsence(
	ctx context.Context,
	probe func(context.Context, *linuxfs.ParentLease, string, domain.FilesystemPrecondition) (bool, error),
	source *linuxfs.ParentLease,
	request trashMoveReconcileRequest,
) error {
	_, basename, err := trashMovePathAndBasename(request.source)
	if err != nil {
		return err
	}
	absent, err := probe(ctx, source, basename, request.precondition)
	if err != nil {
		return err
	}
	if !absent {
		return fmt.Errorf("%w: durable Trash source remains present", linuxfs.ErrDrifted)
	}
	return nil
}

func probeTrashMoveReconcileMetadata(
	ctx context.Context,
	probe func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error),
	directories *linuxfs.TrashDirectories,
	token string,
	request trashMoveReconcileRequest,
) ([]byte, error) {
	contents, present, err := probe(ctx, directories, token)
	if err != nil {
		return nil, err
	}
	if !present {
		if contents != nil {
			return nil, fmt.Errorf("%w: metadata probe reported absent with retained bytes", linuxfs.ErrDrifted)
		}
		return nil, fmt.Errorf("%w: owned Trash metadata is absent", linuxfs.ErrDrifted)
	}
	info, err := parseTrashInfo(contents, token, request.basis)
	if err != nil {
		return nil, fmt.Errorf("%w: owned Trash metadata is malformed: %v", linuxfs.ErrDrifted, err)
	}
	if info.token != token || info.basis != request.basis || !info.metadataPath.Equal(request.metadataPath) {
		return nil, fmt.Errorf("%w: owned Trash metadata does not bind the durable ticket source", linuxfs.ErrDrifted)
	}
	return append([]byte(nil), contents...), nil
}

func reloadTrashMoveReconcileTicket(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
) (recoveryport.Ticket, error) {
	if err := validateTrashMoveReconcileReloadInput(ctx, ledger, ticket); err != nil {
		return ticket, err
	}
	current, err := ledger.Reload(ctx, ticket)
	if current == nil {
		if err != nil {
			return ticket, err
		}
		return ticket, fmt.Errorf("%w: durable move reconciliation reload returned no ticket", linuxfs.ErrInterrupted)
	}
	if current.Token() != ticket.Token() {
		return ticket, errors.Join(err, fmt.Errorf("%w: durable move reconciliation reload changed ticket identity", linuxfs.ErrUnsupported))
	}
	if err != nil {
		return current, err
	}
	return current, nil
}

func validateTrashMoveReconcileReloadInput(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket) error {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if ticket == nil {
		return fmt.Errorf("%w: durable move-indeterminate Trash ticket is required", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateTrashMoveReconcileExecution(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMoveReconcileRequest,
	operations trashMoveReconcileOperations,
) error {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if operations.selectDirectories == nil || operations.probeMetadata == nil || operations.probeContent == nil ||
		operations.probeSourceAbsence == nil || operations.closeDirectories == nil {
		return fmt.Errorf("%w: complete Trash move reconciliation operations are required", linuxfs.ErrUnsupported)
	}
	if err := validateTrashMoveReconcileRequest(request); err != nil {
		return err
	}
	return requireTrashMoveIndeterminateTicket(ticket, request)
}

func validateTrashMoveReconcileRequest(request trashMoveReconcileRequest) error {
	if err := validateTrashMoveReconcileCore(request); err != nil {
		return err
	}
	if err := request.trashLayoutBinding.Validate(); err != nil {
		return fmt.Errorf("%w: durable Trash ticket layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	if _, err := pathbytes.New(request.metadataPath.Components()); err != nil {
		return fmt.Errorf("%w: trusted Trash metadata path: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.basis.validate(); err != nil {
		return fmt.Errorf("%w: trusted Trash metadata basis: %v", linuxfs.ErrUnsupported, err)
	}
	return nil
}

func validateTrashMoveReconcileCore(request trashMoveReconcileRequest) error {
	if err := request.root.Validate(); err != nil {
		return fmt.Errorf("%w: durable Trash ticket root: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.actionID.Validate(); err != nil {
		return fmt.Errorf("%w: durable Trash ticket action identity: %v", linuxfs.ErrUnsupported, err)
	}
	source, _, err := trashMovePathAndBasename(request.source)
	if err != nil {
		return err
	}
	if !source.Equal(request.source) {
		return fmt.Errorf("%w: durable Trash ticket source is not canonical", linuxfs.ErrUnsupported)
	}
	if err := request.precondition.Validate(); err != nil {
		return fmt.Errorf("%w: durable Trash ticket precondition: %v", linuxfs.ErrUnsupported, err)
	}
	if !sameTrashMoveTarget(request.precondition.Target, filesystemTrashMoveTarget(request.root, source)) {
		return fmt.Errorf("%w: durable Trash ticket precondition does not bind its source", linuxfs.ErrUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		return err
	}
	if request.precondition.Required != required {
		return fmt.Errorf("%w: durable Trash ticket precondition mask mismatch", linuxfs.ErrUnsupported)
	}
	return nil
}

func requireTrashMoveIndeterminateTicket(ticket recoveryport.Ticket, request trashMoveReconcileRequest) error {
	if err := validateTrashMoveReconcileTicket(ticket, request); err != nil {
		return err
	}
	if ticket.Event() != recoveryport.EventMoveIndeterminate {
		return fmt.Errorf("%w: durable Trash ticket event %q is not move-indeterminate", linuxfs.ErrUnsupported, ticket.Event())
	}
	if ticket.Closed() {
		return fmt.Errorf("%w: durable move-indeterminate Trash ticket is already closed", linuxfs.ErrUnsupported)
	}
	if ticket.Outcome() != "" || ticket.MetadataDisposition() != "" {
		return fmt.Errorf("%w: move-indeterminate Trash ticket carries reconciliation facts", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateTrashMoveReconcileTicket(ticket recoveryport.Ticket, request trashMoveReconcileRequest) error {
	if ticket == nil {
		return fmt.Errorf("%w: durable Trash ticket is required", linuxfs.ErrUnsupported)
	}
	if err := validateTrashToken(ticket.Token()); err != nil {
		return fmt.Errorf("%w: durable Trash ticket token: %v", linuxfs.ErrUnsupported, err)
	}
	if ticket.RootID() != request.root || !ticket.Source().Equal(request.source) ||
		ticket.ActionID() != request.actionID || ticket.ActionKind() != domain.ActionTrashPath ||
		ticket.Destination() != recoveryport.DestinationTrash {
		return fmt.Errorf("%w: durable Trash ticket does not bind this action source", linuxfs.ErrUnsupported)
	}
	if !ticket.TrashLayoutBinding().Equal(request.trashLayoutBinding) {
		return fmt.Errorf("%w: durable Trash ticket layout binding does not match this reconciliation", linuxfs.ErrUnsupported)
	}
	if !sameTrashMovePrecondition(ticket.Precondition(), request.precondition) {
		return fmt.Errorf("%w: durable Trash ticket precondition does not bind this source", linuxfs.ErrUnsupported)
	}
	return nil
}

func transitionTrashMoveReconcileTicket(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMoveReconcileRequest,
) (recoveryport.Ticket, error) {
	transition := recoveryport.Transition{
		Event:                 recoveryport.EventReconciliationResolved,
		ReconciliationOutcome: recoveryport.OutcomeMoveVerified,
	}
	updated, err := ledger.Transition(ctx, ticket, transition)
	if updated != nil {
		if ticket == nil || updated.Token() != ticket.Token() {
			return ticket, errors.Join(err, fmt.Errorf("%w: durable move reconciliation transition changed ticket identity", linuxfs.ErrUnsupported))
		}
		if validationErr := validateTrashMoveReconcileTicket(updated, request); validationErr != nil {
			return updated, errors.Join(err, validationErr)
		}
		if updated.Event() != transition.Event || updated.Outcome() != transition.ReconciliationOutcome ||
			updated.MetadataDisposition() != "" || updated.Closed() {
			return updated, errors.Join(err, fmt.Errorf("%w: durable move reconciliation transition returned inconsistent facts", linuxfs.ErrUnsupported))
		}
	}
	if err != nil {
		if updated == nil {
			return ticket, err
		}
		return updated, err
	}
	if updated == nil {
		return ticket, fmt.Errorf("%w: durable move reconciliation transition returned no ticket", linuxfs.ErrInterrupted)
	}
	return updated, nil
}
