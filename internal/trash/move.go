//go:build linux

package trash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

// MoveToTrash durably records and performs one receipt-bound, no-replace
// Trash move. The source parent and Trash lease are already-qualified
// descriptor authorities; this function never resolves an apply-time path or
// accepts a caller-selected destination/token.
//
// On a successful move it returns the open move-verified ticket. On an
// ambiguous effect it returns an outstanding ticket and an error. Callers must
// reconcile that ticket rather than retrying or cleaning up metadata.
func MoveToTrash(
	ctx context.Context,
	ledger recoveryport.Ledger,
	action domain.Action,
	planDigest domain.PlanDigest,
	source *linuxfs.ParentLease,
	trashLease *mounts.TrashLease,
	deletedAt time.Time,
) (recoveryport.Ticket, error) {
	request, err := newTrashMoveRequest(ctx, ledger, action, planDigest, source, trashLease, deletedAt)
	if err != nil {
		return nil, err
	}
	return moveToTrashWith(ctx, ledger, request, productionTrashMoveOperations(trashLease))
}

type trashMoveRequest struct {
	action             domain.Action
	planDigest         domain.PlanDigest
	sourceLease        *linuxfs.ParentLease
	root               domain.TrustedRootID
	source             pathbytes.BytePath
	basename           string
	precondition       domain.FilesystemPrecondition
	trashLayoutBinding domain.TrashLayoutBinding
	deletedAt          time.Time
}

type trashMoveOperations struct {
	write func(context.Context, string, pathbytes.BytePath, time.Time) (*MetadataPublication, error)

	selectDirectories func() (*linuxfs.TrashDirectories, error)
	move              func(context.Context, *linuxfs.ParentLease, string, *linuxfs.TrashDirectories, *linuxfs.TrashInfoPublication, domain.FilesystemPrecondition) (linuxfs.TrashMoveDisposition, error)
	closeDirectories  func(*linuxfs.TrashDirectories) error
}

func productionTrashMoveOperations(lease *mounts.TrashLease) trashMoveOperations {
	return trashMoveOperations{
		write: func(ctx context.Context, token string, source pathbytes.BytePath, deletedAt time.Time) (*MetadataPublication, error) {
			return WriteTrashInfoDurable(ctx, lease, token, source, deletedAt)
		},
		selectDirectories: func() (*linuxfs.TrashDirectories, error) {
			return SelectTrashRoot(lease)
		},
		move: linuxfs.MovePublishedTrashNoReplace,
		closeDirectories: func(directories *linuxfs.TrashDirectories) error {
			if directories == nil {
				return nil
			}
			return directories.Close()
		},
	}
}

func newTrashMoveRequest(
	ctx context.Context,
	ledger recoveryport.Ledger,
	action domain.Action,
	planDigest domain.PlanDigest,
	source *linuxfs.ParentLease,
	trashLease *mounts.TrashLease,
	deletedAt time.Time,
) (trashMoveRequest, error) {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return trashMoveRequest{}, err
	}
	if ledger == nil {
		return trashMoveRequest{}, fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}

	clonedAction := action.Clone()
	if err := clonedAction.Validate(); err != nil {
		return trashMoveRequest{}, fmt.Errorf("%w: invalid Trash action: %v", linuxfs.ErrUnsupported, err)
	}
	if clonedAction.Kind != domain.ActionTrashPath {
		return trashMoveRequest{}, fmt.Errorf("%w: action kind %q is not a Trash move", linuxfs.ErrUnsupported, clonedAction.Kind)
	}
	if err := planDigest.Validate(); err != nil {
		return trashMoveRequest{}, fmt.Errorf("%w: invalid plan digest: %v", linuxfs.ErrUnsupported, err)
	}
	if source == nil || trashLease == nil {
		return trashMoveRequest{}, fmt.Errorf("%w: source parent and topology-qualified Trash lease are required", linuxfs.ErrUnsupported)
	}
	if err := validateTrashDeletionDate(deletedAt); err != nil {
		return trashMoveRequest{}, fmt.Errorf("%w: Trash deletion date: %v", linuxfs.ErrUnsupported, err)
	}

	precondition, sourcePath, root, basename, err := validateTrashMoveAction(clonedAction)
	if err != nil {
		return trashMoveRequest{}, err
	}
	if source.RootID() != root || trashLease.RootID() != root {
		return trashMoveRequest{}, fmt.Errorf("%w: action source, parent lease, and Trash lease roots differ", linuxfs.ErrUnsupported)
	}
	trashLayoutBinding, err := trashLease.MetadataReconciliationIdentity()
	if err != nil {
		return trashMoveRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	if err := trashLayoutBinding.Validate(); err != nil {
		return trashMoveRequest{}, fmt.Errorf("%w: topology-qualified Trash layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	if _, err := trashLease.MetadataPathFor(sourcePath); err != nil {
		return trashMoveRequest{}, classifyTrashMetadataAuthorityFailure(err)
	}
	if err := validateTrashMoveSource(ctx, source, basename, precondition); err != nil {
		return trashMoveRequest{}, err
	}

	return trashMoveRequest{
		action:             clonedAction,
		planDigest:         planDigest,
		sourceLease:        source,
		root:               root,
		source:             sourcePath,
		basename:           basename,
		precondition:       precondition,
		trashLayoutBinding: trashLayoutBinding,
		deletedAt:          deletedAt,
	}, nil
}

func validateTrashMoveAction(action domain.Action) (domain.FilesystemPrecondition, pathbytes.BytePath, domain.TrustedRootID, string, error) {
	if action.Target.Kind != domain.TargetFilesystem || action.Target.Filesystem == nil ||
		action.Precondition.Kind != domain.PreconditionFilesystemIdentity || action.Precondition.Filesystem == nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Trash action requires an exact filesystem target and precondition", linuxfs.ErrUnsupported)
	}
	precondition := *action.Precondition.Filesystem
	if err := precondition.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Trash filesystem precondition: %v", linuxfs.ErrUnsupported, err)
	}
	if !sameTrashMoveTarget(action.Target, precondition.Target) {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Trash action and precondition targets differ", linuxfs.ErrUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", err
	}
	if precondition.Required != required {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Trash action precondition mask %b does not match %b", linuxfs.ErrUnsupported, precondition.Required, required)
	}
	root := action.Target.Filesystem.Root
	if err := root.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Trash action root: %v", linuxfs.ErrUnsupported, err)
	}
	path, basename, err := trashMovePathAndBasename(action.Target.Filesystem.Path)
	if err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", err
	}
	return precondition, path, root, basename, nil
}

func validateTrashMoveSource(ctx context.Context, source *linuxfs.ParentLease, basename string, expected domain.FilesystemPrecondition) error {
	return linuxfs.ValidateResolvedTarget(ctx, source, basename, expected)
}

func moveToTrashWith(ctx context.Context, ledger recoveryport.Ledger, request trashMoveRequest, operations trashMoveOperations) (recoveryport.Ticket, error) {
	if err := validateTrashMoveExecution(ctx, ledger, request, operations); err != nil {
		return nil, err
	}

	ticket, err := ledger.Reserve(ctx, request.action, request.planDigest, recoveryport.Reservation{
		TrashLayoutBinding: request.trashLayoutBinding,
	})
	if err != nil {
		if ticket != nil {
			if validationErr := validateTrashMoveTicket(ticket, request); validationErr != nil {
				return ticket, errors.Join(err, validationErr)
			}
		}
		return ticket, err
	}
	if err := requireTrashMoveTicketEvent(ticket, request, recoveryport.EventIntentReserved); err != nil {
		return ticket, err
	}

	ticket, err = transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMetadataDispatchRecorded})
	if err != nil {
		return ticket, err
	}
	publication, writeErr := operations.write(ctx, ticket.Token(), request.source, request.deletedAt)
	if writeErr != nil {
		return recordTrashMetadataIndeterminate(ctx, ledger, ticket, request, writeErr)
	}
	if err := validateTrashMetadataPublication(publication, request, ticket); err != nil {
		return recordTrashMetadataIndeterminate(ctx, ledger, ticket, request, err)
	}

	ticket, err = transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMetadataVerified})
	if err != nil {
		return ticket, err
	}
	directories, selectErr := operations.selectDirectories()
	if selectErr != nil {
		return reconcileTrashMetadataRetained(ctx, ledger, ticket, request, selectErr)
	}
	if directories == nil {
		return reconcileTrashMetadataRetained(ctx, ledger, ticket, request, fmt.Errorf("%w: topology-qualified Trash selector returned no directories", linuxfs.ErrUnsupported))
	}
	defer func() { _ = operations.closeDirectories(directories) }()

	ticket, err = transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveDispatchRecorded})
	if err != nil {
		return ticket, err
	}
	disposition, moveErr := operations.move(ctx, request.sourceLease, request.basename, directories, publication.publication, request.precondition)
	switch disposition {
	case linuxfs.TrashMoveVerified:
		if moveErr != nil {
			return recordTrashMoveIndeterminate(ctx, ledger, ticket, request, moveErr)
		}
		return transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveVerified})
	case linuxfs.TrashMoveIndeterminate:
		if moveErr == nil {
			moveErr = fmt.Errorf("%w: Trash move requires reconciliation", linuxfs.ErrInterrupted)
		}
		return recordTrashMoveIndeterminate(ctx, ledger, ticket, request, moveErr)
	case linuxfs.TrashMoveNotApplied:
		if moveErr == nil {
			moveErr = fmt.Errorf("%w: no-replace Trash move did not verify an effect", linuxfs.ErrDrifted)
		}
		indeterminate, transitionErr := transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveIndeterminate})
		if transitionErr != nil {
			return indeterminate, errors.Join(moveErr, transitionErr)
		}
		reconciled, reconciliationErr := transitionTrashMoveTicket(ctx, ledger, indeterminate, request, recoveryport.Transition{
			Event:                 recoveryport.EventReconciliationResolved,
			ReconciliationOutcome: recoveryport.OutcomeNotApplied,
		})
		if reconciliationErr != nil {
			return reconciled, errors.Join(moveErr, reconciliationErr)
		}
		return reconciled, moveErr
	default:
		return recordTrashMoveIndeterminate(ctx, ledger, ticket, request, fmt.Errorf("%w: unknown Trash move disposition %d", linuxfs.ErrInterrupted, disposition))
	}
}

func validateTrashMoveExecution(ctx context.Context, ledger recoveryport.Ledger, request trashMoveRequest, operations trashMoveOperations) error {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if operations.write == nil || operations.selectDirectories == nil || operations.move == nil || operations.closeDirectories == nil {
		return fmt.Errorf("%w: complete Trash move operations are required", linuxfs.ErrUnsupported)
	}
	if err := request.root.Validate(); err != nil {
		return fmt.Errorf("%w: Trash move root: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.action.ID.Validate(); err != nil {
		return fmt.Errorf("%w: Trash move action identity: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.planDigest.Validate(); err != nil {
		return fmt.Errorf("%w: Trash move plan digest: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.trashLayoutBinding.Validate(); err != nil {
		return fmt.Errorf("%w: topology-qualified Trash layout binding: %v", linuxfs.ErrUnsupported, err)
	}
	if err := validateTrashDeletionDate(request.deletedAt); err != nil {
		return fmt.Errorf("%w: Trash deletion date: %v", linuxfs.ErrUnsupported, err)
	}
	path, basename, err := trashMovePathAndBasename(request.source)
	if err != nil {
		return err
	}
	if basename != request.basename {
		return fmt.Errorf("%w: Trash move source basename does not match its path", linuxfs.ErrUnsupported)
	}
	if err := request.precondition.Validate(); err != nil || !sameTrashMoveTarget(request.precondition.Target, filesystemTrashMoveTarget(request.root, path)) {
		if err != nil {
			return fmt.Errorf("%w: Trash move filesystem precondition: %v", linuxfs.ErrUnsupported, err)
		}
		return fmt.Errorf("%w: Trash move precondition does not bind its source", linuxfs.ErrUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		return err
	}
	if request.precondition.Required != required {
		return fmt.Errorf("%w: Trash move precondition mask mismatch", linuxfs.ErrUnsupported)
	}
	return nil
}

func filesystemTrashMoveTarget(root domain.TrustedRootID, path pathbytes.BytePath) domain.Target {
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		return domain.Target{}
	}
	return target
}

func trashMovePathAndBasename(path pathbytes.BytePath) (pathbytes.BytePath, string, error) {
	components := path.Components()
	if len(components) == 0 {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: Trash move source path is empty", linuxfs.ErrUnsupported)
	}
	cloned, err := pathbytes.New(components)
	if err != nil {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: invalid Trash move source path: %v", linuxfs.ErrUnsupported, err)
	}
	basename := string(components[len(components)-1])
	if basename == "" || basename == "." || basename == ".." {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: Trash move basename must name one entry", linuxfs.ErrUnsupported)
	}
	for _, value := range []byte(basename) {
		if value == 0 || value == '/' {
			return pathbytes.BytePath{}, "", fmt.Errorf("%w: Trash move basename must not contain NUL or slash", linuxfs.ErrUnsupported)
		}
	}
	return cloned, basename, nil
}

func validateTrashMetadataPublication(publication *MetadataPublication, request trashMoveRequest, ticket recoveryport.Ticket) error {
	if publication == nil || publication.publication == nil {
		return fmt.Errorf("%w: metadata publication returned no durable receipt", linuxfs.ErrInterrupted)
	}
	if publication.RootID() != request.root || publication.Token() != ticket.Token() {
		return fmt.Errorf("%w: metadata publication does not bind the reserved Trash intent", linuxfs.ErrDrifted)
	}
	return nil
}

func recordTrashMetadataIndeterminate(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request trashMoveRequest, cause error) (recoveryport.Ticket, error) {
	updated, transitionErr := transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMetadataIndeterminate})
	if transitionErr != nil {
		return updated, errors.Join(cause, transitionErr)
	}
	return updated, cause
}

func reconcileTrashMetadataRetained(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request trashMoveRequest, cause error) (recoveryport.Ticket, error) {
	updated, transitionErr := transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{
		Event:                 recoveryport.EventReconciliationResolved,
		ReconciliationOutcome: recoveryport.OutcomeNotApplied,
		MetadataDisposition:   recoveryport.MetadataRetained,
	})
	if transitionErr != nil {
		return updated, errors.Join(cause, transitionErr)
	}
	return updated, cause
}

func recordTrashMoveIndeterminate(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request trashMoveRequest, cause error) (recoveryport.Ticket, error) {
	updated, transitionErr := transitionTrashMoveTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveIndeterminate})
	if transitionErr != nil {
		return updated, errors.Join(cause, transitionErr)
	}
	return updated, cause
}

func transitionTrashMoveTicket(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request trashMoveRequest, transition recoveryport.Transition) (recoveryport.Ticket, error) {
	updated, err := ledger.Transition(ctx, ticket, transition)
	if updated != nil {
		if ticket == nil || updated.Token() != ticket.Token() {
			return ticket, errors.Join(err, fmt.Errorf("%w: durable transition changed the Trash ticket identity", linuxfs.ErrUnsupported))
		}
		if validationErr := validateTrashMoveTicket(updated, request); validationErr != nil {
			return updated, errors.Join(err, validationErr)
		}
		if updated.Event() != transition.Event {
			return updated, errors.Join(err, fmt.Errorf("%w: durable transition returned event %q, want %q", linuxfs.ErrUnsupported, updated.Event(), transition.Event))
		}
	}
	if err != nil {
		if updated == nil {
			// A failed durable append may return no successor ticket. Preserve
			// the caller's last known durable fact so recovery remains possible.
			return ticket, err
		}
		return updated, err
	}
	if updated == nil {
		return nil, fmt.Errorf("%w: durable transition returned no ticket", linuxfs.ErrInterrupted)
	}
	return updated, nil
}

func requireTrashMoveTicketEvent(ticket recoveryport.Ticket, request trashMoveRequest, event recoveryport.Event) error {
	if err := validateTrashMoveTicket(ticket, request); err != nil {
		return err
	}
	if ticket.Event() != event {
		return fmt.Errorf("%w: durable ticket event %q, want %q", linuxfs.ErrUnsupported, ticket.Event(), event)
	}
	if ticket.Closed() {
		return fmt.Errorf("%w: durable ticket is already closed", linuxfs.ErrUnsupported)
	}
	if event != recoveryport.EventReconciliationResolved && (ticket.Outcome() != "" || ticket.MetadataDisposition() != "") {
		return fmt.Errorf("%w: ordinary durable ticket carries reconciliation facts", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateTrashMoveTicket(ticket recoveryport.Ticket, request trashMoveRequest) error {
	if ticket == nil {
		return fmt.Errorf("%w: durable Trash ticket is required", linuxfs.ErrUnsupported)
	}
	if err := validateTrashToken(ticket.Token()); err != nil {
		return fmt.Errorf("%w: durable Trash ticket token: %v", linuxfs.ErrUnsupported, err)
	}
	if ticket.RootID() != request.root || !ticket.Source().Equal(request.source) ||
		ticket.ActionID() != request.action.ID || ticket.ActionKind() != domain.ActionTrashPath ||
		ticket.Destination() != recoveryport.DestinationTrash {
		return fmt.Errorf("%w: durable Trash ticket does not bind this action source", linuxfs.ErrUnsupported)
	}
	if !ticket.TrashLayoutBinding().Equal(request.trashLayoutBinding) {
		return fmt.Errorf("%w: durable Trash ticket layout binding does not match this move", linuxfs.ErrUnsupported)
	}
	if !sameTrashMovePrecondition(ticket.Precondition(), request.precondition) {
		return fmt.Errorf("%w: durable Trash ticket precondition does not bind this source", linuxfs.ErrUnsupported)
	}
	return nil
}

func sameTrashMovePrecondition(left, right domain.FilesystemPrecondition) bool {
	return left.Required == right.Required && left.Snapshot == right.Snapshot && sameTrashMoveTarget(left.Target, right.Target)
}

func sameTrashMoveTarget(left, right domain.Target) bool {
	if left.Kind != domain.TargetFilesystem || right.Kind != domain.TargetFilesystem || left.Filesystem == nil || right.Filesystem == nil {
		return false
	}
	return left.Filesystem.Root == right.Filesystem.Root && left.Filesystem.Path.Equal(right.Filesystem.Path)
}
