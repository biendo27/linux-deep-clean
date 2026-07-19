//go:build linux

package trash

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

// ReconcileIndeterminateTrashMetadata performs the narrow metadata-only
// recovery step for one outstanding Trash ticket. It accepts only a durable
// metadata-indeterminate Trash ticket, probes only that ticket's owned
// <token>.trashinfo name, and records either an absent or retained metadata
// fact. It never scans a Trash directory, moves/restores content, removes
// metadata, or retries an uncertain effect.
func ReconcileIndeterminateTrashMetadata(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	lease *mounts.TrashLease,
) (recoveryport.Ticket, error) {
	if lease == nil {
		return ticket, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	return reconcileIndeterminateTrashMetadataWithAuthority(
		ctx,
		ledger,
		ticket,
		lease,
		productionTrashMetadataReconcileOperations(lease),
	)
}

// trashMetadataReconcileAuthority exposes only the data facts needed to
// bind an outstanding metadata ticket to the exact authority-selected Trash
// layout before any metadata mapping or descriptor selection. The public
// entry point still accepts only a *mounts.TrashLease; this private seam
// keeps the ordering contract testable without creating another authority
// path.
type trashMetadataReconcileAuthority interface {
	RootID() domain.TrustedRootID
	AnchorKind() mounts.TrashAnchorKind
	MetadataReconciliationIdentity() (domain.TrashLayoutBinding, error)
	MetadataBasis() mounts.TrashMetadataBasis
	MetadataPathFor(pathbytes.BytePath) (pathbytes.BytePath, error)
}

func reconcileIndeterminateTrashMetadataWithAuthority(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	authority trashMetadataReconcileAuthority,
	operations trashMetadataReconcileOperations,
) (recoveryport.Ticket, error) {
	if authority == nil {
		return ticket, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	current, err := reloadTrashMetadataReconcileTicket(ctx, ledger, ticket)
	if err != nil {
		return current, err
	}
	request, err := newTrashMetadataReconcileCoreRequest(current)
	if err != nil {
		return current, err
	}
	request, err = completeTrashMetadataReconcileRequest(current, request, authority)
	if err != nil {
		return current, err
	}
	return reconcileIndeterminateTrashMetadataWith(ctx, ledger, current, request, operations)
}

type trashMetadataReconcileRequest struct {
	root               domain.TrustedRootID
	source             pathbytes.BytePath
	actionID           domain.ActionID
	precondition       domain.FilesystemPrecondition
	trashLayoutBinding domain.TrashLayoutBinding
	metadataPath       pathbytes.BytePath
	basis              trashPathBasis
}

type trashMetadataReconcileOperations struct {
	selectDirectories func() (*linuxfs.TrashDirectories, error)
	probe             func(context.Context, *linuxfs.TrashDirectories, string) ([]byte, bool, error)
	closeDirectories  func(*linuxfs.TrashDirectories) error
}

func productionTrashMetadataReconcileOperations(lease *mounts.TrashLease) trashMetadataReconcileOperations {
	return trashMetadataReconcileOperations{
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			return SelectTrashRoot(lease)
		},
		probe: linuxfs.ProbeOwnedTrashInfo,
		closeDirectories: func(directories *linuxfs.TrashDirectories) error {
			if directories == nil {
				return nil
			}
			return directories.Close()
		},
	}
}

func newTrashMetadataReconcileCoreRequest(ticket recoveryport.Ticket) (trashMetadataReconcileRequest, error) {
	if ticket == nil {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: durable metadata-indeterminate Trash ticket is required", linuxfs.ErrUnsupported)
	}

	source, _, err := trashMovePathAndBasename(ticket.Source())
	if err != nil {
		return trashMetadataReconcileRequest{}, err
	}
	request := trashMetadataReconcileRequest{
		root:               ticket.RootID(),
		source:             source,
		actionID:           ticket.ActionID(),
		precondition:       ticket.Precondition(),
		trashLayoutBinding: ticket.TrashLayoutBinding(),
	}
	if err := validateTrashMetadataReconcileCore(request); err != nil {
		return trashMetadataReconcileRequest{}, err
	}
	return request, nil
}

func completeTrashMetadataReconcileRequest(
	ticket recoveryport.Ticket,
	request trashMetadataReconcileRequest,
	authority trashMetadataReconcileAuthority,
) (trashMetadataReconcileRequest, error) {
	if err := requireTrashMetadataIndeterminateTicket(ticket, request); err != nil {
		return trashMetadataReconcileRequest{}, err
	}
	if authority == nil {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	if authority.RootID() != request.root {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: durable ticket and trusted Trash lease roots differ", linuxfs.ErrUnsupported)
	}
	if authority.AnchorKind() == "" {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: metadata reconciliation requires a topology-qualified Trash lease", linuxfs.ErrUnsupported)
	}
	if err := request.trashLayoutBinding.Validate(); err != nil {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: durable Trash ticket layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	candidateBinding, err := authority.MetadataReconciliationIdentity()
	if err != nil {
		return trashMetadataReconcileRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	if err := candidateBinding.Validate(); err != nil {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: supplied Trash layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	if !request.trashLayoutBinding.Equal(candidateBinding) {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: durable Trash ticket does not bind the supplied layout authority", linuxfs.ErrUnsupported)
	}

	metadataPath, err := authority.MetadataPathFor(request.source)
	if err != nil {
		return trashMetadataReconcileRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	metadataPath, err = pathbytes.New(metadataPath.Components())
	if err != nil {
		return trashMetadataReconcileRequest{}, fmt.Errorf("%w: trusted Trash metadata path: %v", linuxfs.ErrUnsupported, err)
	}
	basis, err := trashPathBasisFromMounts(authority.MetadataBasis())
	if err != nil {
		return trashMetadataReconcileRequest{}, err
	}
	request.metadataPath = metadataPath
	request.basis = basis
	if err := validateTrashMetadataReconcileRequest(request); err != nil {
		return trashMetadataReconcileRequest{}, err
	}
	return request, nil
}

func reconcileIndeterminateTrashMetadataWith(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMetadataReconcileRequest,
	operations trashMetadataReconcileOperations,
) (recoveryport.Ticket, error) {
	if err := validateTrashMetadataReconcileExecution(ctx, ledger, ticket, request, operations); err != nil {
		return ticket, err
	}
	current, err := reloadTrashMetadataReconcileTicket(ctx, ledger, ticket)
	if err != nil {
		return current, err
	}
	if err := validateTrashMetadataReconcileExecution(ctx, ledger, current, request, operations); err != nil {
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

	contents, present, err := operations.probe(ctx, directories, current.Token())
	if err != nil {
		return current, err
	}
	if !present && contents != nil {
		return current, fmt.Errorf("%w: metadata probe reported absent with retained bytes", linuxfs.ErrDrifted)
	}

	disposition := recoveryport.MetadataAbsent
	if present {
		info, err := parseTrashInfo(contents, current.Token(), request.basis)
		if err != nil {
			return current, fmt.Errorf("%w: owned Trash metadata is malformed: %v", linuxfs.ErrDrifted, err)
		}
		if info.token != current.Token() || info.basis != request.basis || !info.metadataPath.Equal(request.metadataPath) {
			return current, fmt.Errorf("%w: owned Trash metadata does not bind the durable ticket source", linuxfs.ErrDrifted)
		}
		disposition = recoveryport.MetadataRetained
	}
	return transitionTrashMetadataReconcileTicket(ctx, ledger, current, request, disposition)
}

func reloadTrashMetadataReconcileTicket(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
) (recoveryport.Ticket, error) {
	if err := validateTrashMetadataReconcileReloadInput(ctx, ledger, ticket); err != nil {
		return ticket, err
	}
	current, err := ledger.Reload(ctx, ticket)
	if current == nil {
		if err != nil {
			return ticket, err
		}
		return ticket, fmt.Errorf("%w: durable metadata reconciliation reload returned no ticket", linuxfs.ErrInterrupted)
	}
	if current.Token() != ticket.Token() {
		return current, errors.Join(err, fmt.Errorf("%w: durable metadata reconciliation reload changed ticket identity", linuxfs.ErrUnsupported))
	}
	if err != nil {
		return current, err
	}
	return current, nil
}

func validateTrashMetadataReconcileReloadInput(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
) error {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if ticket == nil {
		return fmt.Errorf("%w: durable metadata-indeterminate Trash ticket is required", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateTrashMetadataReconcileExecution(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMetadataReconcileRequest,
	operations trashMetadataReconcileOperations,
) error {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if operations.selectDirectories == nil || operations.probe == nil || operations.closeDirectories == nil {
		return fmt.Errorf("%w: complete Trash metadata reconciliation operations are required", linuxfs.ErrUnsupported)
	}
	if err := validateTrashMetadataReconcileRequest(request); err != nil {
		return err
	}
	return requireTrashMetadataIndeterminateTicket(ticket, request)
}

func validateTrashMetadataReconcileRequest(request trashMetadataReconcileRequest) error {
	if err := validateTrashMetadataReconcileCore(request); err != nil {
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

func validateTrashMetadataReconcileCore(request trashMetadataReconcileRequest) error {
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

func requireTrashMetadataIndeterminateTicket(ticket recoveryport.Ticket, request trashMetadataReconcileRequest) error {
	if err := validateTrashMetadataReconcileTicket(ticket, request); err != nil {
		return err
	}
	if ticket.Event() != recoveryport.EventMetadataIndeterminate {
		return fmt.Errorf("%w: durable Trash ticket event %q is not metadata-indeterminate", linuxfs.ErrUnsupported, ticket.Event())
	}
	if ticket.Closed() {
		return fmt.Errorf("%w: durable metadata-indeterminate Trash ticket is already closed", linuxfs.ErrUnsupported)
	}
	if ticket.Outcome() != "" || ticket.MetadataDisposition() != "" {
		return fmt.Errorf("%w: metadata-indeterminate Trash ticket carries reconciliation facts", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateTrashMetadataReconcileTicket(ticket recoveryport.Ticket, request trashMetadataReconcileRequest) error {
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

func transitionTrashMetadataReconcileTicket(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request trashMetadataReconcileRequest,
	disposition recoveryport.MetadataDisposition,
) (recoveryport.Ticket, error) {
	transition := recoveryport.Transition{
		Event:                 recoveryport.EventReconciliationResolved,
		ReconciliationOutcome: recoveryport.OutcomeNotApplied,
		MetadataDisposition:   disposition,
	}
	updated, err := ledger.Transition(ctx, ticket, transition)
	if updated != nil {
		if ticket == nil || updated.Token() != ticket.Token() {
			return ticket, errors.Join(err, fmt.Errorf("%w: durable metadata reconciliation transition changed ticket identity", linuxfs.ErrUnsupported))
		}
		if validationErr := validateTrashMetadataReconcileTicket(updated, request); validationErr != nil {
			return updated, errors.Join(err, validationErr)
		}
		if updated.Event() != transition.Event ||
			updated.Outcome() != transition.ReconciliationOutcome ||
			updated.MetadataDisposition() != transition.MetadataDisposition || !updated.Closed() {
			return updated, errors.Join(err, fmt.Errorf("%w: durable metadata reconciliation transition returned inconsistent facts", linuxfs.ErrUnsupported))
		}
	}
	if err != nil {
		if updated == nil {
			return ticket, err
		}
		return updated, err
	}
	if updated == nil {
		return ticket, fmt.Errorf("%w: durable metadata reconciliation transition returned no ticket", linuxfs.ErrInterrupted)
	}
	return updated, nil
}
