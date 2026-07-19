//go:build linux

package quarantine

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

const (
	quarantineRecoveryTokenPrefix = "ldc-"
	quarantineRecoveryTokenHexLen = 64
)

// quarantineRetainer is the sole private Store-to-LinuxFS content bridge. It
// accepts only existing descriptor-rooted leases, one ledger-issued token,
// and an exact precondition; it cannot be used as a generic directory
// operation or to select a quarantine path.
type quarantineRetainer func(
	context.Context,
	*linuxfs.ParentLease,
	string,
	*linuxfs.PrivateDirectoryLease,
	string,
	domain.FilesystemPrecondition,
) (linuxfs.QuarantineRetainDisposition, error)

// Retain durably records and performs exactly one no-replace move into the
// already-qualified per-mount quarantine Store. It never retries the effect,
// scans a layout, publishes metadata, or performs reconciliation. A result
// that is not fully verified remains an open move-indeterminate ticket.
//
// The Store is held across the bounded operation so Close either revokes it
// before the reservation begins or waits for this one operation to finish.
func Retain(
	ctx context.Context,
	ledger recoveryport.Ledger,
	store *Store,
	action domain.Action,
	planDigest domain.PlanDigest,
	source *linuxfs.ParentLease,
) (recoveryport.Ticket, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: open per-mount quarantine Store is required", linuxfs.ErrUnsupported)
	}

	return store.withRetain(func(privateLease *linuxfs.PrivateDirectoryLease, retainer quarantineRetainer) (recoveryport.Ticket, error) {
		request, err := newQuarantineRetainRequest(ctx, ledger, store.rootID, action, planDigest, source)
		if err != nil {
			return nil, err
		}
		return retainWith(ctx, ledger, request, quarantineRetainOperations{
			quarantine: privateLease,
			retain:     retainer,
		})
	})
}

// withRetain owns the Store's short read-authority window. Holding its mutex
// through the ledger state machine and one LinuxFS call gives Close a clear
// boundary: a completed Close prevents a future reservation, while an
// operation that already entered this scope retains a live typed lease until
// its bounded result is recorded.
func (store *Store) withRetain(operation func(*linuxfs.PrivateDirectoryLease, quarantineRetainer) (recoveryport.Ticket, error)) (recoveryport.Ticket, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: open per-mount quarantine Store is required", linuxfs.ErrUnsupported)
	}
	if operation == nil {
		return nil, fmt.Errorf("%w: quarantine retain operation is required", linuxfs.ErrUnsupported)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.directory == nil || store.privateLease == nil || store.retain == nil {
		return nil, fmt.Errorf("%w: per-mount quarantine Store is closed or has no qualified LinuxFS bridge", linuxfs.ErrUnsupported)
	}
	return operation(store.privateLease, store.retain)
}

type quarantineRetainRequest struct {
	action       domain.Action
	planDigest   domain.PlanDigest
	sourceLease  *linuxfs.ParentLease
	root         domain.TrustedRootID
	source       pathbytes.BytePath
	basename     string
	precondition domain.FilesystemPrecondition
}

type quarantineRetainOperations struct {
	quarantine *linuxfs.PrivateDirectoryLease
	retain     quarantineRetainer
}

func newQuarantineRetainRequest(
	ctx context.Context,
	ledger recoveryport.Ledger,
	storeRoot domain.TrustedRootID,
	action domain.Action,
	planDigest domain.PlanDigest,
	source *linuxfs.ParentLease,
) (quarantineRetainRequest, error) {
	if err := quarantineRetainContextError(ctx); err != nil {
		return quarantineRetainRequest{}, err
	}
	if ledger == nil {
		return quarantineRetainRequest{}, fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if source == nil {
		return quarantineRetainRequest{}, fmt.Errorf("%w: resolved source parent is required", linuxfs.ErrUnsupported)
	}
	clonedAction := action.Clone()
	if err := clonedAction.Validate(); err != nil {
		return quarantineRetainRequest{}, fmt.Errorf("%w: invalid Quarantine action: %v", linuxfs.ErrUnsupported, err)
	}
	if clonedAction.Kind != domain.ActionQuarantinePath {
		return quarantineRetainRequest{}, fmt.Errorf("%w: action kind %q is not a Quarantine retain", linuxfs.ErrUnsupported, clonedAction.Kind)
	}
	if err := planDigest.Validate(); err != nil {
		return quarantineRetainRequest{}, fmt.Errorf("%w: invalid plan digest: %v", linuxfs.ErrUnsupported, err)
	}

	precondition, sourcePath, root, basename, err := validateQuarantineRetainAction(clonedAction)
	if err != nil {
		return quarantineRetainRequest{}, err
	}
	if err := storeRoot.Validate(); err != nil {
		return quarantineRetainRequest{}, fmt.Errorf("%w: Quarantine Store root identity: %v", linuxfs.ErrUnsupported, err)
	}
	if source.RootID() != root || storeRoot != root {
		return quarantineRetainRequest{}, fmt.Errorf("%w: action source, source parent, and Quarantine Store roots differ", linuxfs.ErrUnsupported)
	}
	if err := linuxfs.ValidateResolvedTarget(ctx, source, basename, precondition); err != nil {
		return quarantineRetainRequest{}, err
	}

	return quarantineRetainRequest{
		action:       clonedAction,
		planDigest:   planDigest,
		sourceLease:  source,
		root:         root,
		source:       sourcePath,
		basename:     basename,
		precondition: precondition,
	}, nil
}

func validateQuarantineRetainAction(action domain.Action) (domain.FilesystemPrecondition, pathbytes.BytePath, domain.TrustedRootID, string, error) {
	if action.Target.Kind != domain.TargetFilesystem || action.Target.Filesystem == nil ||
		action.Precondition.Kind != domain.PreconditionFilesystemIdentity || action.Precondition.Filesystem == nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Quarantine action requires an exact filesystem target and precondition", linuxfs.ErrUnsupported)
	}
	precondition := *action.Precondition.Filesystem
	if err := precondition.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Quarantine filesystem precondition: %v", linuxfs.ErrUnsupported, err)
	}
	if !sameQuarantineRetainTarget(action.Target, precondition.Target) {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Quarantine action and precondition targets differ", linuxfs.ErrUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionQuarantinePath)
	if err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", err
	}
	if precondition.Required != required {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Quarantine action precondition mask %b does not match %b", linuxfs.ErrUnsupported, precondition.Required, required)
	}
	root := action.Target.Filesystem.Root
	if err := root.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", fmt.Errorf("%w: Quarantine action root: %v", linuxfs.ErrUnsupported, err)
	}
	path, basename, err := quarantineRetainPathAndBasename(action.Target.Filesystem.Path)
	if err != nil {
		return domain.FilesystemPrecondition{}, pathbytes.BytePath{}, "", "", err
	}
	return precondition, path, root, basename, nil
}

func retainWith(ctx context.Context, ledger recoveryport.Ledger, request quarantineRetainRequest, operations quarantineRetainOperations) (recoveryport.Ticket, error) {
	if err := validateQuarantineRetainExecution(ctx, ledger, request, operations); err != nil {
		return nil, err
	}

	ticket, err := ledger.Reserve(ctx, request.action, request.planDigest, recoveryport.Reservation{})
	if err != nil {
		if ticket != nil {
			if validationErr := validateQuarantineRetainTicket(ticket, request); validationErr != nil {
				return ticket, errors.Join(err, validationErr)
			}
		}
		return ticket, err
	}
	if err := requireQuarantineRetainTicketEvent(ticket, request, recoveryport.EventIntentReserved); err != nil {
		return ticket, err
	}

	ticket, err = transitionQuarantineRetainTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveDispatchRecorded})
	if err != nil {
		return ticket, err
	}

	disposition, retainErr := operations.retain(ctx, request.sourceLease, request.basename, operations.quarantine, ticket.Token(), request.precondition)
	return retainWithOperationResult(ctx, ledger, ticket, request, disposition, retainErr)
}

func retainWithOperationResult(
	ctx context.Context,
	ledger recoveryport.Ledger,
	ticket recoveryport.Ticket,
	request quarantineRetainRequest,
	disposition linuxfs.QuarantineRetainDisposition,
	retainErr error,
) (recoveryport.Ticket, error) {
	if disposition == linuxfs.QuarantineRetainVerified && retainErr == nil {
		return transitionQuarantineRetainTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveVerified})
	}

	cause := retainErr
	if cause == nil {
		switch disposition {
		case linuxfs.QuarantineRetainNotApplied:
			cause = fmt.Errorf("%w: no-replace Quarantine retain did not verify an effect", linuxfs.ErrDrifted)
		case linuxfs.QuarantineRetainIndeterminate:
			cause = fmt.Errorf("%w: Quarantine retain requires later recovery handling", linuxfs.ErrInterrupted)
		default:
			cause = fmt.Errorf("%w: unknown Quarantine retain disposition %d", linuxfs.ErrInterrupted, disposition)
		}
	}
	return recordQuarantineRetainIndeterminate(ctx, ledger, ticket, request, cause)
}

func validateQuarantineRetainExecution(ctx context.Context, ledger recoveryport.Ledger, request quarantineRetainRequest, operations quarantineRetainOperations) error {
	if err := quarantineRetainContextError(ctx); err != nil {
		return err
	}
	if ledger == nil {
		return fmt.Errorf("%w: durable recovery ledger is required", linuxfs.ErrUnsupported)
	}
	if operations.retain == nil {
		return fmt.Errorf("%w: Quarantine retain operation is required", linuxfs.ErrUnsupported)
	}
	if err := request.action.Validate(); err != nil {
		return fmt.Errorf("%w: invalid Quarantine action: %v", linuxfs.ErrUnsupported, err)
	}
	if request.action.Kind != domain.ActionQuarantinePath {
		return fmt.Errorf("%w: action kind %q is not a Quarantine retain", linuxfs.ErrUnsupported, request.action.Kind)
	}
	if err := request.root.Validate(); err != nil {
		return fmt.Errorf("%w: Quarantine retain root: %v", linuxfs.ErrUnsupported, err)
	}
	if err := request.planDigest.Validate(); err != nil {
		return fmt.Errorf("%w: Quarantine retain plan digest: %v", linuxfs.ErrUnsupported, err)
	}
	path, basename, err := quarantineRetainPathAndBasename(request.source)
	if err != nil {
		return err
	}
	if basename != request.basename {
		return fmt.Errorf("%w: Quarantine retain source basename does not match its path", linuxfs.ErrUnsupported)
	}
	if err := request.precondition.Validate(); err != nil || !sameQuarantineRetainTarget(request.precondition.Target, filesystemQuarantineRetainTarget(request.root, path)) {
		if err != nil {
			return fmt.Errorf("%w: Quarantine filesystem precondition: %v", linuxfs.ErrUnsupported, err)
		}
		return fmt.Errorf("%w: Quarantine precondition does not bind its source", linuxfs.ErrUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(domain.ActionQuarantinePath)
	if err != nil {
		return err
	}
	if request.precondition.Required != required {
		return fmt.Errorf("%w: Quarantine retain precondition mask mismatch", linuxfs.ErrUnsupported)
	}
	return nil
}

func filesystemQuarantineRetainTarget(root domain.TrustedRootID, path pathbytes.BytePath) domain.Target {
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		return domain.Target{}
	}
	return target
}

func quarantineRetainPathAndBasename(path pathbytes.BytePath) (pathbytes.BytePath, string, error) {
	components := path.Components()
	if len(components) == 0 {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: Quarantine retain source path is empty", linuxfs.ErrUnsupported)
	}
	cloned, err := pathbytes.New(components)
	if err != nil {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: invalid Quarantine retain source path: %v", linuxfs.ErrUnsupported, err)
	}
	basename := string(components[len(components)-1])
	if basename == "" || basename == "." || basename == ".." {
		return pathbytes.BytePath{}, "", fmt.Errorf("%w: Quarantine retain basename must name one entry", linuxfs.ErrUnsupported)
	}
	for _, value := range []byte(basename) {
		if value == 0 || value == '/' {
			return pathbytes.BytePath{}, "", fmt.Errorf("%w: Quarantine retain basename must not contain NUL or slash", linuxfs.ErrUnsupported)
		}
	}
	return cloned, basename, nil
}

func recordQuarantineRetainIndeterminate(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request quarantineRetainRequest, cause error) (recoveryport.Ticket, error) {
	updated, transitionErr := transitionQuarantineRetainTicket(ctx, ledger, ticket, request, recoveryport.Transition{Event: recoveryport.EventMoveIndeterminate})
	if transitionErr != nil {
		return updated, errors.Join(cause, transitionErr)
	}
	return updated, cause
}

func transitionQuarantineRetainTicket(ctx context.Context, ledger recoveryport.Ledger, ticket recoveryport.Ticket, request quarantineRetainRequest, transition recoveryport.Transition) (recoveryport.Ticket, error) {
	updated, err := ledger.Transition(ctx, ticket, transition)
	if updated != nil {
		if ticket == nil || updated.Token() != ticket.Token() {
			return ticket, errors.Join(err, fmt.Errorf("%w: durable transition changed the Quarantine ticket identity", linuxfs.ErrUnsupported))
		}
		if validationErr := requireQuarantineRetainTicketEvent(updated, request, transition.Event); validationErr != nil {
			return ticket, errors.Join(err, validationErr)
		}
	}
	if err != nil {
		if updated == nil {
			return ticket, err
		}
		return updated, err
	}
	if updated == nil {
		return ticket, fmt.Errorf("%w: durable transition returned no ticket", linuxfs.ErrInterrupted)
	}
	return updated, nil
}

func requireQuarantineRetainTicketEvent(ticket recoveryport.Ticket, request quarantineRetainRequest, event recoveryport.Event) error {
	if err := validateQuarantineRetainTicket(ticket, request); err != nil {
		return err
	}
	if ticket.Event() != event {
		return fmt.Errorf("%w: durable ticket event %q, want %q", linuxfs.ErrUnsupported, ticket.Event(), event)
	}
	if ticket.Closed() {
		return fmt.Errorf("%w: durable ticket is already closed", linuxfs.ErrUnsupported)
	}
	if ticket.Outcome() != "" || ticket.MetadataDisposition() != "" {
		return fmt.Errorf("%w: ordinary Quarantine ticket carries reconciliation or metadata facts", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateQuarantineRetainTicket(ticket recoveryport.Ticket, request quarantineRetainRequest) error {
	if ticket == nil {
		return fmt.Errorf("%w: durable Quarantine ticket is required", linuxfs.ErrUnsupported)
	}
	if err := validateQuarantineRecoveryToken(ticket.Token()); err != nil {
		return fmt.Errorf("%w: durable Quarantine ticket token: %v", linuxfs.ErrUnsupported, err)
	}
	if ticket.RootID() != request.root || !ticket.Source().Equal(request.source) ||
		ticket.ActionID() != request.action.ID || ticket.ActionKind() != domain.ActionQuarantinePath ||
		ticket.Destination() != recoveryport.DestinationQuarantine {
		return fmt.Errorf("%w: durable Quarantine ticket does not bind this action source", linuxfs.ErrUnsupported)
	}
	if !ticket.TrashLayoutBinding().IsZero() {
		return fmt.Errorf("%w: durable Quarantine ticket must not carry a Trash layout binding", linuxfs.ErrUnsupported)
	}
	if !sameQuarantineRetainPrecondition(ticket.Precondition(), request.precondition) {
		return fmt.Errorf("%w: durable Quarantine ticket precondition does not bind this source", linuxfs.ErrUnsupported)
	}
	return nil
}

func validateQuarantineRecoveryToken(token string) error {
	if len(token) != len(quarantineRecoveryTokenPrefix)+quarantineRecoveryTokenHexLen || token[:len(quarantineRecoveryTokenPrefix)] != quarantineRecoveryTokenPrefix {
		return fmt.Errorf("Quarantine recovery token must be %q plus exactly %d lowercase hexadecimal bytes", quarantineRecoveryTokenPrefix, quarantineRecoveryTokenHexLen)
	}
	for _, value := range []byte(token[len(quarantineRecoveryTokenPrefix):]) {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return fmt.Errorf("Quarantine recovery token contains an unsupported byte %q", value)
		}
	}
	return nil
}

func sameQuarantineRetainPrecondition(left, right domain.FilesystemPrecondition) bool {
	return left.Required == right.Required && left.Snapshot == right.Snapshot && sameQuarantineRetainTarget(left.Target, right.Target)
}

func sameQuarantineRetainTarget(left, right domain.Target) bool {
	if left.Kind != domain.TargetFilesystem || right.Kind != domain.TargetFilesystem || left.Filesystem == nil || right.Filesystem == nil {
		return false
	}
	return left.Filesystem.Root == right.Filesystem.Root && left.Filesystem.Path.Equal(right.Filesystem.Path)
}

func quarantineRetainContextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", linuxfs.ErrInterrupted)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", linuxfs.ErrInterrupted, err)
	}
	return nil
}
