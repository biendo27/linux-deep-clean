// Package state owns durable, typed recovery facts. It does not discover
// layouts, resolve paths, or authorize content operations; LinuxFS retains all
// descriptor and on-disk authority behind an opaque private-ledger session.
package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/planproto"
	"github.com/fxamacker/cbor/v2"
)

const (
	recoveryLedgerSchemaVersion   uint16 = 1
	maximumRecoveryFrameBytes            = 64 << 10
	maximumRecoveryLedgerRecords         = 128
	maximumRecoveryPathComponents        = 128
	maximumRecoveryPathBytes             = 16 << 10
)

var (
	recoveryFrameDigestDomain = []byte("ldclean.recovery-frame.digest.v1\x00")
	recoveryFrameEncMode      = newRecoveryFrameEncMode()
	recoveryFrameDecMode      = newRecoveryFrameDecMode()
)

var (
	// ErrRecoveryConflict means retained facts already make a requested new
	// reservation or transition unsafe. Callers must reload/reconcile rather
	// than retrying with another token.
	ErrRecoveryConflict = errors.New("recovery ledger conflict")
	// ErrRecoveryCorrupt means a retained frame is malformed, noncanonical,
	// unsupported, or internally inconsistent. It is intentionally read-only.
	ErrRecoveryCorrupt = errors.New("recovery ledger is corrupt or unsupported")
	// ErrRecoveryUnsupported means an input is outside the closed Phase 4A
	// recovery vocabulary or its bounded encoding profile.
	ErrRecoveryUnsupported = errors.New("recovery ledger operation unsupported")
)

// RecoveryDestination is the closed content destination semantics recorded by
// an intent. It describes no destination path and grants no move authority.
type RecoveryDestination string

const (
	RecoveryDestinationTrash      RecoveryDestination = "trash"
	RecoveryDestinationQuarantine RecoveryDestination = "quarantine"
)

// RecoveryEvent is the closed event vocabulary for facts persisted around a
// later content operation. Recording an event never performs that operation.
type RecoveryEvent string

const (
	RecoveryEventIntentReserved           RecoveryEvent = "intent_reserved"
	RecoveryEventMetadataDispatchRecorded RecoveryEvent = "metadata_dispatch_recorded"
	RecoveryEventMetadataVerified         RecoveryEvent = "metadata_verified"
	RecoveryEventMetadataIndeterminate    RecoveryEvent = "metadata_indeterminate"
	RecoveryEventMoveDispatchRecorded     RecoveryEvent = "move_dispatch_recorded"
	RecoveryEventMoveVerified             RecoveryEvent = "move_verified"
	RecoveryEventMoveIndeterminate        RecoveryEvent = "move_indeterminate"
	RecoveryEventRestoreDispatchRecorded  RecoveryEvent = "restore_dispatch_recorded"
	RecoveryEventRestoreVerified          RecoveryEvent = "restore_verified"
	RecoveryEventRestoreIndeterminate     RecoveryEvent = "restore_indeterminate"
	RecoveryEventReconciliationResolved   RecoveryEvent = "reconciliation_resolved"
)

// RecoveryOutcome is meaningful only for a reconciliation-resolved event.
type RecoveryOutcome string

const (
	RecoveryOutcomeNotApplied      RecoveryOutcome = "not_applied"
	RecoveryOutcomeMoveVerified    RecoveryOutcome = "move_verified"
	RecoveryOutcomeRestoreVerified RecoveryOutcome = "restore_verified"
)

// RecoveryMetadataDisposition records the closed no-effect metadata fact
// required when a reservation or metadata dispatch is reconciled before any
// content dispatch. It is not a metadata path or cleanup instruction.
type RecoveryMetadataDisposition string

const (
	RecoveryMetadataAbsent   RecoveryMetadataDisposition = "absent"
	RecoveryMetadataRetained RecoveryMetadataDisposition = "retained"
)

// RecoveryTransition requests one closed fact transition for a recovery that
// was returned by this package. It has no fields for paths, tokens, cleanup,
// destination names, or content-side instructions.
type RecoveryTransition struct {
	Event                 RecoveryEvent
	ReconciliationOutcome RecoveryOutcome
	MetadataDisposition   RecoveryMetadataDisposition
}

type recoveryBinding struct {
	actionBindingDigest  domain.ActionBindingDigest
	actionBindingPayload []byte
	planDigest           domain.PlanDigest
	token                string
	root                 domain.TrustedRootID
	source               pathbytes.BytePath
	actionID             domain.ActionID
	actionKind           domain.ActionKind
	destination          RecoveryDestination
	precondition         domain.FilesystemPrecondition
}

type recoveryFrame struct {
	schemaVersion uint16
	binding       recoveryBinding
	event         RecoveryEvent
	ordinal       uint8
	predecessor   [sha256.Size]byte
	outcome       RecoveryOutcome
	metadata      RecoveryMetadataDisposition
}

// Recovery is a typed immutable-by-convention view of one replayed retained
// intent. Its unexported identity prevents callers from manufacturing a token
// or changing a binding before a later transition.
type Recovery struct {
	binding  recoveryBinding
	event    RecoveryEvent
	ordinal  uint8
	digest   [sha256.Size]byte
	outcome  RecoveryOutcome
	metadata RecoveryMetadataDisposition
}

func (recovery Recovery) ActionBindingDigest() domain.ActionBindingDigest {
	return recovery.binding.actionBindingDigest
}
func (recovery Recovery) PlanDigest() domain.PlanDigest    { return recovery.binding.planDigest }
func (recovery Recovery) Token() string                    { return recovery.binding.token }
func (recovery Recovery) RootID() domain.TrustedRootID     { return recovery.binding.root }
func (recovery Recovery) ActionID() domain.ActionID        { return recovery.binding.actionID }
func (recovery Recovery) ActionKind() domain.ActionKind    { return recovery.binding.actionKind }
func (recovery Recovery) Destination() RecoveryDestination { return recovery.binding.destination }
func (recovery Recovery) Event() RecoveryEvent             { return recovery.event }
func (recovery Recovery) Ordinal() uint8                   { return recovery.ordinal }
func (recovery Recovery) Outcome() RecoveryOutcome         { return recovery.outcome }
func (recovery Recovery) MetadataDisposition() RecoveryMetadataDisposition {
	return recovery.metadata
}

// Source returns a defensive copy of the exact relative raw-byte source path.
func (recovery Recovery) Source() pathbytes.BytePath {
	return cloneRecoveryPath(recovery.binding.source)
}

// Closed reports whether no further closed-graph transition is legal. A
// reconciliation that verified a retained move remains open for a later
// restore boundary; all other reconciliation outcomes are closed.
func (recovery Recovery) Closed() bool {
	if recovery.event == RecoveryEventRestoreVerified {
		return true
	}
	return recovery.event == RecoveryEventReconciliationResolved && recovery.outcome != RecoveryOutcomeMoveVerified
}

func (event RecoveryEvent) validate() error {
	switch event {
	case RecoveryEventIntentReserved,
		RecoveryEventMetadataDispatchRecorded,
		RecoveryEventMetadataVerified,
		RecoveryEventMetadataIndeterminate,
		RecoveryEventMoveDispatchRecorded,
		RecoveryEventMoveVerified,
		RecoveryEventMoveIndeterminate,
		RecoveryEventRestoreDispatchRecorded,
		RecoveryEventRestoreVerified,
		RecoveryEventRestoreIndeterminate,
		RecoveryEventReconciliationResolved:
		return nil
	default:
		return fmt.Errorf("%w: unknown recovery event %q", ErrRecoveryUnsupported, event)
	}
}

func (destination RecoveryDestination) validate() error {
	switch destination {
	case RecoveryDestinationTrash, RecoveryDestinationQuarantine:
		return nil
	default:
		return fmt.Errorf("%w: unknown recovery destination %q", ErrRecoveryUnsupported, destination)
	}
}

func (outcome RecoveryOutcome) validate() error {
	switch outcome {
	case RecoveryOutcomeNotApplied, RecoveryOutcomeMoveVerified, RecoveryOutcomeRestoreVerified:
		return nil
	default:
		return fmt.Errorf("%w: unknown reconciliation outcome %q", ErrRecoveryUnsupported, outcome)
	}
}

func (metadata RecoveryMetadataDisposition) validate() error {
	switch metadata {
	case RecoveryMetadataAbsent, RecoveryMetadataRetained:
		return nil
	default:
		return fmt.Errorf("%w: unknown metadata disposition %q", ErrRecoveryUnsupported, metadata)
	}
}

func newRecoveryBinding(action domain.Action, planDigest domain.PlanDigest, token string) (recoveryBinding, error) {
	cloned := action.Clone()
	if err := validateRecoveryAction(cloned); err != nil {
		return recoveryBinding{}, err
	}
	if err := planDigest.Validate(); err != nil {
		return recoveryBinding{}, fmt.Errorf("%w: plan digest: %v", ErrRecoveryUnsupported, err)
	}
	payload, err := planproto.EncodeActionBinding(cloned, planDigest)
	if err != nil {
		return recoveryBinding{}, fmt.Errorf("%w: action binding payload: %v", ErrRecoveryUnsupported, err)
	}
	digest := domain.ComputeActionBindingDigest(payload)
	if _, err := linuxfs.NewPrivateLedgerRecordID(digest.String(), token, 0); err != nil {
		return recoveryBinding{}, fmt.Errorf("%w: reservation token: %v", ErrRecoveryUnsupported, err)
	}
	filesystem := cloned.Precondition.Filesystem
	return recoveryBinding{
		actionBindingDigest:  digest,
		actionBindingPayload: append([]byte(nil), payload...),
		planDigest:           planDigest,
		token:                token,
		root:                 cloned.Target.Filesystem.Root,
		source:               cloneRecoveryPath(cloned.Target.Filesystem.Path),
		actionID:             cloned.ID,
		actionKind:           cloned.Kind,
		destination:          destinationForAction(cloned.Kind),
		precondition:         cloneFilesystemPrecondition(*filesystem),
	}, nil
}

func validateRecoveryAction(action domain.Action) error {
	if err := action.Validate(); err != nil {
		return fmt.Errorf("%w: action: %v", ErrRecoveryUnsupported, err)
	}
	if action.Kind != domain.ActionTrashPath && action.Kind != domain.ActionQuarantinePath {
		return fmt.Errorf("%w: action kind %q has no Phase 4A recovery ledger destination", ErrRecoveryUnsupported, action.Kind)
	}
	if action.Target.Kind != domain.TargetFilesystem || action.Target.Filesystem == nil {
		return fmt.Errorf("%w: recovery action requires a filesystem target", ErrRecoveryUnsupported)
	}
	if action.Precondition.Kind != domain.PreconditionFilesystemIdentity || action.Precondition.Filesystem == nil {
		return fmt.Errorf("%w: recovery action requires a filesystem precondition", ErrRecoveryUnsupported)
	}
	filesystem := action.Precondition.Filesystem
	if filesystem.Target.Kind != domain.TargetFilesystem || filesystem.Target.Filesystem == nil ||
		filesystem.Target.Filesystem.Root != action.Target.Filesystem.Root ||
		!filesystem.Target.Filesystem.Path.Equal(action.Target.Filesystem.Path) {
		return fmt.Errorf("%w: filesystem precondition does not exactly bind the recovery source", ErrRecoveryUnsupported)
	}
	required, err := linuxfs.RequiredStatMask(action.Kind)
	if err != nil {
		return fmt.Errorf("%w: action precondition policy: %v", ErrRecoveryUnsupported, err)
	}
	if filesystem.Required != required {
		return fmt.Errorf("%w: action precondition mask %b does not match %b", ErrRecoveryUnsupported, filesystem.Required, required)
	}
	return validateRecoveryPath(action.Target.Filesystem.Path)
}

func destinationForAction(kind domain.ActionKind) RecoveryDestination {
	if kind == domain.ActionQuarantinePath {
		return RecoveryDestinationQuarantine
	}
	return RecoveryDestinationTrash
}

func validateRecoveryBinding(binding recoveryBinding) error {
	if err := binding.actionBindingDigest.Validate(); err != nil {
		return fmt.Errorf("%w: action binding digest: %v", ErrRecoveryCorrupt, err)
	}
	if len(binding.actionBindingPayload) == 0 || len(binding.actionBindingPayload) > maximumRecoveryFrameBytes {
		return fmt.Errorf("%w: action binding payload is outside %d-byte bound", ErrRecoveryCorrupt, maximumRecoveryFrameBytes)
	}
	if err := binding.planDigest.Validate(); err != nil {
		return fmt.Errorf("%w: plan digest: %v", ErrRecoveryCorrupt, err)
	}
	if err := binding.root.Validate(); err != nil {
		return fmt.Errorf("%w: source root: %v", ErrRecoveryCorrupt, err)
	}
	if err := validateRecoveryPath(binding.source); err != nil {
		return fmt.Errorf("%w: source path: %v", ErrRecoveryCorrupt, err)
	}
	if err := binding.actionID.Validate(); err != nil {
		return fmt.Errorf("%w: action ID: %v", ErrRecoveryCorrupt, err)
	}
	if err := binding.actionKind.Validate(); err != nil {
		return fmt.Errorf("%w: action kind: %v", ErrRecoveryCorrupt, err)
	}
	if binding.actionKind != domain.ActionTrashPath && binding.actionKind != domain.ActionQuarantinePath {
		return fmt.Errorf("%w: action kind %q is not recoverable in Phase 4A", ErrRecoveryCorrupt, binding.actionKind)
	}
	if err := binding.destination.validate(); err != nil {
		return fmt.Errorf("%w: stored destination: %v", ErrRecoveryCorrupt, err)
	}
	if binding.destination != destinationForAction(binding.actionKind) {
		return fmt.Errorf("%w: destination does not match action kind", ErrRecoveryCorrupt)
	}
	if _, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, 0); err != nil {
		return fmt.Errorf("%w: reservation identifier: %v", ErrRecoveryCorrupt, err)
	}
	if binding.precondition.Target.Kind != domain.TargetFilesystem || binding.precondition.Target.Filesystem == nil ||
		binding.precondition.Target.Filesystem.Root != binding.root ||
		!binding.precondition.Target.Filesystem.Path.Equal(binding.source) {
		return fmt.Errorf("%w: stored filesystem precondition does not exactly bind source", ErrRecoveryCorrupt)
	}
	required, err := linuxfs.RequiredStatMask(binding.actionKind)
	if err != nil {
		return fmt.Errorf("%w: stored action policy: %v", ErrRecoveryCorrupt, err)
	}
	if binding.precondition.Required != required {
		return fmt.Errorf("%w: stored filesystem precondition mask mismatch", ErrRecoveryCorrupt)
	}
	if err := binding.precondition.Validate(); err != nil {
		return fmt.Errorf("%w: stored filesystem precondition: %v", ErrRecoveryCorrupt, err)
	}
	boundAction, boundPlanDigest, err := planproto.DecodeActionBinding(binding.actionBindingPayload, recoveryActionBindingDecodeLimits())
	if err != nil {
		return fmt.Errorf("%w: retained action binding payload: %v", ErrRecoveryCorrupt, err)
	}
	if boundPlanDigest != binding.planDigest || !binding.actionBindingDigest.Verify(binding.actionBindingPayload) {
		return fmt.Errorf("%w: retained action binding digest does not match its payload", ErrRecoveryCorrupt)
	}
	expected, err := newRecoveryBinding(boundAction, boundPlanDigest, binding.token)
	if err != nil {
		return fmt.Errorf("%w: retained action binding projection: %v", ErrRecoveryCorrupt, err)
	}
	if !sameRecoveryBinding(expected, binding) {
		return fmt.Errorf("%w: retained action binding payload does not match its projected facts", ErrRecoveryCorrupt)
	}
	return nil
}

func recoveryActionBindingDecodeLimits() planproto.DecodeLimits {
	return planproto.DecodeLimits{
		MaxFrameBytes:       maximumRecoveryFrameBytes,
		MaxDepth:            16,
		MaxActions:          1,
		MaxMapPairs:         128,
		MaxArrayItems:       2048,
		MaxScalarBytes:      maximumRecoveryFrameBytes,
		MaxPathComponents:   maximumRecoveryPathComponents,
		MaxEncodedPathBytes: maximumRecoveryPathBytes,
	}
}

func validateRecoveryPath(path pathbytes.BytePath) error {
	components := path.Components()
	if len(components) == 0 || len(components) > maximumRecoveryPathComponents {
		return fmt.Errorf("path has %d components, limit is %d", len(components), maximumRecoveryPathComponents)
	}
	decodedBytes := 0
	for _, component := range components {
		decodedBytes += len(component)
		if decodedBytes > maximumRecoveryPathBytes {
			return fmt.Errorf("path exceeds %d raw bytes", maximumRecoveryPathBytes)
		}
	}
	_, err := pathbytes.New(components)
	return err
}

func cloneRecoveryPath(path pathbytes.BytePath) pathbytes.BytePath {
	cloned, err := pathbytes.New(path.Components())
	if err != nil {
		return pathbytes.BytePath{}
	}
	return cloned
}

func cloneFilesystemPrecondition(precondition domain.FilesystemPrecondition) domain.FilesystemPrecondition {
	return domain.FilesystemPrecondition{
		Target:   precondition.Target.Clone(),
		Required: precondition.Required,
		Snapshot: precondition.Snapshot,
	}
}

func newIntentFrame(binding recoveryBinding) (recoveryFrame, error) {
	frame := recoveryFrame{
		schemaVersion: recoveryLedgerSchemaVersion,
		binding:       binding,
		event:         RecoveryEventIntentReserved,
	}
	if err := validateRecoveryFrame(frame); err != nil {
		return recoveryFrame{}, err
	}
	return frame, nil
}

func appendRecoveryTransition(history []recoveryFrame, transition RecoveryTransition) (recoveryFrame, error) {
	recovery, err := replayRecoveryFrames(history)
	if err != nil {
		return recoveryFrame{}, err
	}
	if len(history) == 0 || history[len(history)-1].ordinal >= linuxfsMaximumLedgerOrdinal() {
		return recoveryFrame{}, fmt.Errorf("%w: recovery transition ordinal capacity reached", ErrRecoveryConflict)
	}
	if err := validateRecoveryTransition(recovery, transition); err != nil {
		return recoveryFrame{}, err
	}
	predecessor, err := recoveryFrameDigest(history[len(history)-1])
	if err != nil {
		return recoveryFrame{}, err
	}
	frame := recoveryFrame{
		schemaVersion: recoveryLedgerSchemaVersion,
		binding:       recovery.binding,
		event:         transition.Event,
		ordinal:       recovery.ordinal + 1,
		predecessor:   predecessor,
		outcome:       transition.ReconciliationOutcome,
		metadata:      transition.MetadataDisposition,
	}
	if err := validateRecoveryFrame(frame); err != nil {
		return recoveryFrame{}, err
	}
	return frame, nil
}

// linuxfsMaximumLedgerOrdinal keeps the state-machine bound derived from the
// opaque record-ID constructor without exposing a filename or raw filesystem
// detail. The v1 transition graph has eleven states, well within this limit.
func linuxfsMaximumLedgerOrdinal() uint8 { return 15 }

func replayRecoveryFrames(frames []recoveryFrame) (Recovery, error) {
	if len(frames) == 0 || len(frames) > int(linuxfsMaximumLedgerOrdinal())+1 {
		return Recovery{}, fmt.Errorf("%w: recovery frame count %d is outside the v1 bound", ErrRecoveryCorrupt, len(frames))
	}
	var recovery Recovery
	for index, frame := range frames {
		if err := validateRecoveryFrame(frame); err != nil {
			return Recovery{}, err
		}
		if frame.ordinal != uint8(index) {
			return Recovery{}, fmt.Errorf("%w: frame ordinal %d does not match replay position %d", ErrRecoveryCorrupt, frame.ordinal, index)
		}
		if index == 0 {
			if frame.event != RecoveryEventIntentReserved || frame.predecessor != ([sha256.Size]byte{}) {
				return Recovery{}, fmt.Errorf("%w: first frame is not a canonical intent reservation", ErrRecoveryCorrupt)
			}
			var err error
			recovery, err = recoveryFromFrame(frame)
			if err != nil {
				return Recovery{}, err
			}
			continue
		}
		if !sameRecoveryBinding(recovery.binding, frame.binding) {
			return Recovery{}, fmt.Errorf("%w: frame %d changed immutable reservation binding", ErrRecoveryCorrupt, index)
		}
		previousDigest, err := recoveryFrameDigest(frames[index-1])
		if err != nil {
			return Recovery{}, err
		}
		if frame.predecessor != previousDigest {
			return Recovery{}, fmt.Errorf("%w: frame %d predecessor digest does not bind prior frame", ErrRecoveryCorrupt, index)
		}
		transition := RecoveryTransition{
			Event:                 frame.event,
			ReconciliationOutcome: frame.outcome,
			MetadataDisposition:   frame.metadata,
		}
		if err := validateRecoveryTransition(recovery, transition); err != nil {
			return Recovery{}, fmt.Errorf("%w: frame %d transition: %v", ErrRecoveryCorrupt, index, err)
		}
		if err := recovery.apply(frame); err != nil {
			return Recovery{}, err
		}
	}
	return recovery, nil
}

func recoveryFromFrame(frame recoveryFrame) (Recovery, error) {
	digest, err := recoveryFrameDigest(frame)
	if err != nil {
		return Recovery{}, err
	}
	return Recovery{
		binding:  frame.binding,
		event:    frame.event,
		ordinal:  frame.ordinal,
		digest:   digest,
		outcome:  frame.outcome,
		metadata: frame.metadata,
	}, nil
}

func (recovery *Recovery) apply(frame recoveryFrame) error {
	digest, err := recoveryFrameDigest(frame)
	if err != nil {
		return err
	}
	recovery.event = frame.event
	recovery.ordinal = frame.ordinal
	recovery.digest = digest
	recovery.outcome = frame.outcome
	recovery.metadata = frame.metadata
	return nil
}

func validateRecoveryTransition(recovery Recovery, transition RecoveryTransition) error {
	if err := validateRecoveryBinding(recovery.binding); err != nil {
		return err
	}
	if recovery.Closed() {
		return fmt.Errorf("%w: recovery is already closed", ErrRecoveryConflict)
	}
	if err := transition.Event.validate(); err != nil || transition.Event == RecoveryEventIntentReserved {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: intent reservation cannot be appended", ErrRecoveryUnsupported)
	}

	stage := recovery.event
	if stage == RecoveryEventReconciliationResolved && recovery.outcome == RecoveryOutcomeMoveVerified {
		stage = RecoveryEventMoveVerified
	}
	switch stage {
	case RecoveryEventIntentReserved:
		if transition.Event == RecoveryEventReconciliationResolved {
			return validateNoEffectReconciliation(recovery.binding.destination, transition)
		}
		if recovery.binding.destination == RecoveryDestinationTrash && transition.Event == RecoveryEventMetadataDispatchRecorded {
			return validateOrdinaryTransition(transition)
		}
		if recovery.binding.destination == RecoveryDestinationQuarantine && transition.Event == RecoveryEventMoveDispatchRecorded {
			return validateOrdinaryTransition(transition)
		}
	case RecoveryEventMetadataDispatchRecorded:
		if transition.Event == RecoveryEventMetadataVerified || transition.Event == RecoveryEventMetadataIndeterminate {
			return validateOrdinaryTransition(transition)
		}
	case RecoveryEventMetadataVerified:
		if transition.Event == RecoveryEventMoveDispatchRecorded {
			return validateOrdinaryTransition(transition)
		}
		if transition.Event == RecoveryEventReconciliationResolved {
			return validateNoEffectReconciliation(recovery.binding.destination, transition)
		}
	case RecoveryEventMetadataIndeterminate:
		if transition.Event == RecoveryEventReconciliationResolved {
			if transition.ReconciliationOutcome != RecoveryOutcomeNotApplied {
				return fmt.Errorf("%w: metadata indeterminate can resolve only as not applied", ErrRecoveryConflict)
			}
			return validateNoEffectReconciliation(recovery.binding.destination, transition)
		}
	case RecoveryEventMoveDispatchRecorded:
		if transition.Event == RecoveryEventMoveVerified || transition.Event == RecoveryEventMoveIndeterminate {
			return validateOrdinaryTransition(transition)
		}
	case RecoveryEventMoveVerified:
		if transition.Event == RecoveryEventRestoreDispatchRecorded {
			return validateOrdinaryTransition(transition)
		}
	case RecoveryEventRestoreDispatchRecorded:
		if transition.Event == RecoveryEventRestoreVerified || transition.Event == RecoveryEventRestoreIndeterminate {
			return validateOrdinaryTransition(transition)
		}
	case RecoveryEventMoveIndeterminate:
		if transition.Event == RecoveryEventReconciliationResolved {
			return validateMoveIndeterminateReconciliation(transition)
		}
	case RecoveryEventRestoreIndeterminate:
		if transition.Event == RecoveryEventReconciliationResolved {
			return validateRestoreIndeterminateReconciliation(transition)
		}
	}
	return fmt.Errorf("%w: event %q cannot follow %q", ErrRecoveryConflict, transition.Event, recovery.event)
}

func validateOrdinaryTransition(transition RecoveryTransition) error {
	if transition.ReconciliationOutcome != "" || transition.MetadataDisposition != "" {
		return fmt.Errorf("%w: event %q cannot carry reconciliation or metadata facts", ErrRecoveryUnsupported, transition.Event)
	}
	return nil
}

func validateNoEffectReconciliation(destination RecoveryDestination, transition RecoveryTransition) error {
	if transition.Event != RecoveryEventReconciliationResolved || transition.ReconciliationOutcome != RecoveryOutcomeNotApplied {
		return fmt.Errorf("%w: no-effect reconciliation must resolve not applied", ErrRecoveryConflict)
	}
	if err := transition.MetadataDisposition.validate(); err != nil {
		return err
	}
	if destination == RecoveryDestinationQuarantine && transition.MetadataDisposition != RecoveryMetadataAbsent {
		return fmt.Errorf("%w: quarantine has no retained Trash metadata", ErrRecoveryConflict)
	}
	return nil
}

func validateMoveIndeterminateReconciliation(transition RecoveryTransition) error {
	if transition.Event != RecoveryEventReconciliationResolved {
		return fmt.Errorf("%w: indeterminate move requires reconciliation", ErrRecoveryConflict)
	}
	switch transition.ReconciliationOutcome {
	case RecoveryOutcomeNotApplied, RecoveryOutcomeMoveVerified:
	default:
		return fmt.Errorf("%w: indeterminate move cannot reconcile as %q", ErrRecoveryConflict, transition.ReconciliationOutcome)
	}
	if transition.MetadataDisposition != "" {
		return fmt.Errorf("%w: move reconciliation cannot carry a no-effect metadata fact", ErrRecoveryUnsupported)
	}
	return nil
}

func validateRestoreIndeterminateReconciliation(transition RecoveryTransition) error {
	if transition.Event != RecoveryEventReconciliationResolved {
		return fmt.Errorf("%w: indeterminate restore requires reconciliation", ErrRecoveryConflict)
	}
	switch transition.ReconciliationOutcome {
	case RecoveryOutcomeMoveVerified, RecoveryOutcomeRestoreVerified:
	default:
		return fmt.Errorf("%w: indeterminate restore cannot reconcile as %q", ErrRecoveryConflict, transition.ReconciliationOutcome)
	}
	if transition.MetadataDisposition != "" {
		return fmt.Errorf("%w: restore reconciliation cannot carry a no-effect metadata fact", ErrRecoveryUnsupported)
	}
	return nil
}

func validateRecoveryFrame(frame recoveryFrame) error {
	if frame.schemaVersion != recoveryLedgerSchemaVersion {
		return fmt.Errorf("%w: unsupported recovery schema version %d", ErrRecoveryCorrupt, frame.schemaVersion)
	}
	if err := validateRecoveryBinding(frame.binding); err != nil {
		return err
	}
	if err := frame.event.validate(); err != nil {
		return fmt.Errorf("%w: stored event: %v", ErrRecoveryCorrupt, err)
	}
	if frame.ordinal > linuxfsMaximumLedgerOrdinal() {
		return fmt.Errorf("%w: frame ordinal %d exceeds v1 bound", ErrRecoveryCorrupt, frame.ordinal)
	}
	if frame.ordinal == 0 {
		if frame.event != RecoveryEventIntentReserved || frame.predecessor != ([sha256.Size]byte{}) || frame.outcome != "" || frame.metadata != "" {
			return fmt.Errorf("%w: intent reservation frame has noncanonical transition fields", ErrRecoveryCorrupt)
		}
		return nil
	}
	if frame.event == RecoveryEventIntentReserved || frame.predecessor == ([sha256.Size]byte{}) {
		return fmt.Errorf("%w: non-initial frame has invalid event or predecessor", ErrRecoveryCorrupt)
	}
	if frame.event == RecoveryEventReconciliationResolved {
		if err := frame.outcome.validate(); err != nil {
			return fmt.Errorf("%w: stored reconciliation outcome: %v", ErrRecoveryCorrupt, err)
		}
		if frame.metadata != "" {
			if err := frame.metadata.validate(); err != nil {
				return fmt.Errorf("%w: stored metadata disposition: %v", ErrRecoveryCorrupt, err)
			}
		}
		return nil
	}
	if err := validateOrdinaryTransition(RecoveryTransition{Event: frame.event, ReconciliationOutcome: frame.outcome, MetadataDisposition: frame.metadata}); err != nil {
		return fmt.Errorf("%w: stored ordinary transition: %v", ErrRecoveryCorrupt, err)
	}
	return nil
}

func sameRecoveryBinding(left, right recoveryBinding) bool {
	return left.actionBindingDigest == right.actionBindingDigest &&
		bytes.Equal(left.actionBindingPayload, right.actionBindingPayload) &&
		left.planDigest == right.planDigest &&
		left.token == right.token &&
		left.root == right.root &&
		left.source.Equal(right.source) &&
		left.actionID == right.actionID &&
		left.actionKind == right.actionKind &&
		left.destination == right.destination &&
		sameFilesystemPrecondition(left.precondition, right.precondition)
}

func sameFilesystemPrecondition(left, right domain.FilesystemPrecondition) bool {
	return left.Required == right.Required &&
		left.Snapshot == right.Snapshot &&
		left.Target.Kind == right.Target.Kind &&
		left.Target.Filesystem != nil && right.Target.Filesystem != nil &&
		left.Target.Filesystem.Root == right.Target.Filesystem.Root &&
		left.Target.Filesystem.Path.Equal(right.Target.Filesystem.Path)
}

func newRecoveryFrameEncMode() cbor.EncMode {
	options := cbor.CoreDetEncOptions()
	options.IndefLength = cbor.IndefLengthForbidden
	options.TagsMd = cbor.TagsForbidden
	mode, err := options.EncMode()
	if err != nil {
		panic(fmt.Sprintf("recovery ledger canonical CBOR mode: %v", err))
	}
	return mode
}

func newRecoveryFrameDecMode() cbor.DecMode {
	mode, err := cbor.DecOptions{
		DupMapKey:          cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:    16,
		MaxArrayElements:   maximumRecoveryPathComponents,
		MaxMapPairs:        32,
		IndefLength:        cbor.IndefLengthForbidden,
		TagsMd:             cbor.TagsForbidden,
		ExtraReturnErrors:  cbor.ExtraDecErrorUnknownField,
		UTF8:               cbor.UTF8RejectInvalid,
		FieldNameMatching:  cbor.FieldNameMatchingCaseSensitive,
		ByteStringToString: cbor.ByteStringToStringForbidden,
	}.DecMode()
	if err != nil {
		panic(fmt.Sprintf("recovery ledger strict CBOR mode: %v", err))
	}
	return mode
}

type recoveryFrameWire struct {
	SchemaVersion         uint16                     `cbor:"schema_version"`
	ActionBindingDigest   []byte                     `cbor:"action_binding_digest"`
	ActionBindingPayload  []byte                     `cbor:"action_binding_payload"`
	PlanDigest            []byte                     `cbor:"plan_digest"`
	Token                 string                     `cbor:"token"`
	Root                  string                     `cbor:"root"`
	Source                [][]byte                   `cbor:"source"`
	ActionID              string                     `cbor:"action_id"`
	ActionKind            string                     `cbor:"action_kind"`
	Destination           string                     `cbor:"destination"`
	Precondition          filesystemPreconditionWire `cbor:"precondition"`
	Event                 string                     `cbor:"event"`
	Ordinal               uint8                      `cbor:"ordinal"`
	PredecessorDigest     []byte                     `cbor:"predecessor_digest"`
	ReconciliationOutcome string                     `cbor:"reconciliation_outcome"`
	MetadataDisposition   string                     `cbor:"metadata_disposition"`
}

type filesystemPreconditionWire struct {
	Required    uint32         `cbor:"required"`
	DeviceMajor uint32FactWire `cbor:"device_major"`
	DeviceMinor uint32FactWire `cbor:"device_minor"`
	Inode       uint64FactWire `cbor:"inode"`
	Type        string         `cbor:"type"`
	UID         uint32FactWire `cbor:"uid"`
	GID         uint32FactWire `cbor:"gid"`
	Mode        uint32FactWire `cbor:"mode"`
	LinkCount   uint64FactWire `cbor:"link_count"`
	Size        uint64FactWire `cbor:"size"`
	ModifiedAt  int64FactWire  `cbor:"modified_at"`
	ChangedAt   int64FactWire  `cbor:"changed_at"`
	MountID     uint64FactWire `cbor:"mount_id"`
}

type uint32FactWire struct {
	Known bool   `cbor:"known"`
	Value uint32 `cbor:"value"`
}

type uint64FactWire struct {
	Known bool   `cbor:"known"`
	Value uint64 `cbor:"value"`
}

type int64FactWire struct {
	Known bool  `cbor:"known"`
	Value int64 `cbor:"value"`
}

func encodeRecoveryFrame(frame recoveryFrame) ([]byte, error) {
	if err := validateRecoveryFrame(frame); err != nil {
		return nil, err
	}
	encoded, err := recoveryFrameEncMode.Marshal(toRecoveryFrameWire(frame))
	if err != nil {
		return nil, fmt.Errorf("%w: canonical recovery frame encode: %v", ErrRecoveryCorrupt, err)
	}
	if len(encoded) == 0 || len(encoded) > maximumRecoveryFrameBytes {
		return nil, fmt.Errorf("%w: recovery frame is outside %d-byte bound", ErrRecoveryUnsupported, maximumRecoveryFrameBytes)
	}
	return encoded, nil
}

func decodeRecoveryFrame(encoded []byte) (recoveryFrame, error) {
	if len(encoded) == 0 || len(encoded) > maximumRecoveryFrameBytes {
		return recoveryFrame{}, fmt.Errorf("%w: recovery frame is outside %d-byte bound", ErrRecoveryCorrupt, maximumRecoveryFrameBytes)
	}
	var wire recoveryFrameWire
	if err := recoveryFrameDecMode.Unmarshal(encoded, &wire); err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: recovery frame decode: %v", ErrRecoveryCorrupt, err)
	}
	frame, err := fromRecoveryFrameWire(wire)
	if err != nil {
		return recoveryFrame{}, err
	}
	canonical, err := encodeRecoveryFrame(frame)
	if err != nil {
		return recoveryFrame{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return recoveryFrame{}, fmt.Errorf("%w: recovery frame is not canonical v1 CBOR", ErrRecoveryCorrupt)
	}
	return frame, nil
}

func recoveryFrameDigest(frame recoveryFrame) ([sha256.Size]byte, error) {
	encoded, err := encodeRecoveryFrame(frame)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write(recoveryFrameDigestDomain)
	_, _ = hash.Write(encoded)
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func toRecoveryFrameWire(frame recoveryFrame) recoveryFrameWire {
	return recoveryFrameWire{
		SchemaVersion:         frame.schemaVersion,
		ActionBindingDigest:   frame.binding.actionBindingDigest.Bytes(),
		ActionBindingPayload:  append([]byte(nil), frame.binding.actionBindingPayload...),
		PlanDigest:            frame.binding.planDigest.Bytes(),
		Token:                 frame.binding.token,
		Root:                  frame.binding.root.String(),
		Source:                frame.binding.source.Components(),
		ActionID:              frame.binding.actionID.String(),
		ActionKind:            string(frame.binding.actionKind),
		Destination:           string(frame.binding.destination),
		Precondition:          toFilesystemPreconditionWire(frame.binding.precondition),
		Event:                 string(frame.event),
		Ordinal:               frame.ordinal,
		PredecessorDigest:     append([]byte(nil), frame.predecessor[:]...),
		ReconciliationOutcome: string(frame.outcome),
		MetadataDisposition:   string(frame.metadata),
	}
}

func fromRecoveryFrameWire(wire recoveryFrameWire) (recoveryFrame, error) {
	bindingDigest, err := domain.NewActionBindingDigest(wire.ActionBindingDigest)
	if err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: action binding digest: %v", ErrRecoveryCorrupt, err)
	}
	planDigest, err := domain.NewPlanDigest(wire.PlanDigest)
	if err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: plan digest: %v", ErrRecoveryCorrupt, err)
	}
	root, err := domain.NewTrustedRootID(wire.Root)
	if err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: source root: %v", ErrRecoveryCorrupt, err)
	}
	source, err := newBoundedRecoveryPath(wire.Source)
	if err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: source path: %v", ErrRecoveryCorrupt, err)
	}
	actionID, err := domain.NewActionID(wire.ActionID)
	if err != nil {
		return recoveryFrame{}, fmt.Errorf("%w: action ID: %v", ErrRecoveryCorrupt, err)
	}
	precondition, err := fromFilesystemPreconditionWire(root, source, wire.Precondition)
	if err != nil {
		return recoveryFrame{}, err
	}
	if len(wire.PredecessorDigest) != sha256.Size {
		return recoveryFrame{}, fmt.Errorf("%w: predecessor digest is not %d bytes", ErrRecoveryCorrupt, sha256.Size)
	}
	var predecessor [sha256.Size]byte
	copy(predecessor[:], wire.PredecessorDigest)
	frame := recoveryFrame{
		schemaVersion: wire.SchemaVersion,
		binding: recoveryBinding{
			actionBindingDigest:  bindingDigest,
			actionBindingPayload: append([]byte(nil), wire.ActionBindingPayload...),
			planDigest:           planDigest,
			token:                wire.Token,
			root:                 root,
			source:               source,
			actionID:             actionID,
			actionKind:           domain.ActionKind(wire.ActionKind),
			destination:          RecoveryDestination(wire.Destination),
			precondition:         precondition,
		},
		event:       RecoveryEvent(wire.Event),
		ordinal:     wire.Ordinal,
		predecessor: predecessor,
		outcome:     RecoveryOutcome(wire.ReconciliationOutcome),
		metadata:    RecoveryMetadataDisposition(wire.MetadataDisposition),
	}
	if err := validateRecoveryFrame(frame); err != nil {
		return recoveryFrame{}, err
	}
	return frame, nil
}

func newBoundedRecoveryPath(components [][]byte) (pathbytes.BytePath, error) {
	if len(components) == 0 || len(components) > maximumRecoveryPathComponents {
		return pathbytes.BytePath{}, fmt.Errorf("path has %d components, limit is %d", len(components), maximumRecoveryPathComponents)
	}
	decodedBytes := 0
	for _, component := range components {
		decodedBytes += len(component)
		if decodedBytes > maximumRecoveryPathBytes {
			return pathbytes.BytePath{}, fmt.Errorf("path exceeds %d raw bytes", maximumRecoveryPathBytes)
		}
	}
	return pathbytes.New(components)
}

func toFilesystemPreconditionWire(precondition domain.FilesystemPrecondition) filesystemPreconditionWire {
	snapshot := precondition.Snapshot
	return filesystemPreconditionWire{
		Required:    uint32(precondition.Required),
		DeviceMajor: uint32FactToWire(snapshot.DeviceMajor),
		DeviceMinor: uint32FactToWire(snapshot.DeviceMinor),
		Inode:       uint64FactToWire(snapshot.Inode),
		Type:        string(snapshot.Type),
		UID:         uint32FactToWire(snapshot.UID),
		GID:         uint32FactToWire(snapshot.GID),
		Mode:        uint32FactToWire(snapshot.Mode),
		LinkCount:   uint64FactToWire(snapshot.LinkCount),
		Size:        uint64FactToWire(snapshot.Size),
		ModifiedAt:  int64FactToWire(snapshot.ModifiedAt),
		ChangedAt:   int64FactToWire(snapshot.ChangedAt),
		MountID:     uint64FactToWire(snapshot.MountID),
	}
}

func fromFilesystemPreconditionWire(root domain.TrustedRootID, source pathbytes.BytePath, wire filesystemPreconditionWire) (domain.FilesystemPrecondition, error) {
	target, err := domain.NewFilesystemTarget(root, source)
	if err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: precondition target: %v", ErrRecoveryCorrupt, err)
	}
	return domain.FilesystemPrecondition{
		Target:   target,
		Required: domain.FilesystemFieldMask(wire.Required),
		Snapshot: domain.FilesystemSnapshot{
			DeviceMajor: uint32FactFromWire(wire.DeviceMajor),
			DeviceMinor: uint32FactFromWire(wire.DeviceMinor),
			Inode:       uint64FactFromWire(wire.Inode),
			Type:        domain.FileType(wire.Type),
			UID:         uint32FactFromWire(wire.UID),
			GID:         uint32FactFromWire(wire.GID),
			Mode:        uint32FactFromWire(wire.Mode),
			LinkCount:   uint64FactFromWire(wire.LinkCount),
			Size:        uint64FactFromWire(wire.Size),
			ModifiedAt:  int64FactFromWire(wire.ModifiedAt),
			ChangedAt:   int64FactFromWire(wire.ChangedAt),
			MountID:     uint64FactFromWire(wire.MountID),
		},
	}, nil
}

func uint32FactToWire(fact domain.Uint32Fact) uint32FactWire {
	return uint32FactWire{Known: fact.Known, Value: fact.Value}
}

func uint64FactToWire(fact domain.Uint64Fact) uint64FactWire {
	return uint64FactWire{Known: fact.Known, Value: fact.Value}
}

func int64FactToWire(fact domain.Int64Fact) int64FactWire {
	return int64FactWire{Known: fact.Known, Value: fact.Value}
}

func uint32FactFromWire(wire uint32FactWire) domain.Uint32Fact {
	return domain.Uint32Fact{Known: wire.Known, Value: wire.Value}
}

func uint64FactFromWire(wire uint64FactWire) domain.Uint64Fact {
	return domain.Uint64Fact{Known: wire.Known, Value: wire.Value}
}

func int64FactFromWire(wire int64FactWire) domain.Int64Fact {
	return domain.Int64Fact{Known: wire.Known, Value: wire.Value}
}

func newRecoveryToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("%w: generate reservation token: %v", ErrRecoveryUnsupported, err)
	}
	return "ldc-" + hex.EncodeToString(bytes), nil
}

// recoveryLedgerSession carries only the bounded opaque ledger operations
// State requires while an already-held LinuxFS session remains live. It
// deliberately does not expose a path, descriptor, or mutable record store.
type recoveryLedgerSession interface {
	Publish(context.Context, linuxfs.PrivateLedgerRecordID, []byte) error
	Read(context.Context, linuxfs.PrivateLedgerRecordID, int) ([]byte, error)
	List(context.Context, int) ([]linuxfs.PrivateLedgerRecordID, error)
}

// recoveryLedgerSessions keeps the critical-section ownership inside LinuxFS.
// It is package-private solely so state tests can exercise public adapter
// orchestration against retained-frame fixtures without creating cross-package
// path authority or an alternate production backend.
type recoveryLedgerSessions interface {
	withRead(context.Context, func(recoveryLedgerSession) error) error
	withWrite(context.Context, func(recoveryLedgerSession) error) error
}

type linuxfsRecoveryLedgerSessions struct {
	directory *linuxfs.PrivateDirectoryLease
}

var _ recoveryLedgerSessions = linuxfsRecoveryLedgerSessions{}

func (sessions linuxfsRecoveryLedgerSessions) withRead(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return linuxfs.WithPrivateLedgerReadSession(ctx, sessions.directory, func(session *linuxfs.PrivateLedgerSession) error {
		return callback(session)
	})
}

func (sessions linuxfsRecoveryLedgerSessions) withWrite(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return linuxfs.WithPrivateLedgerWriteSession(ctx, sessions.directory, func(session *linuxfs.PrivateLedgerSession) error {
		return callback(session)
	})
}

// RecoveryLedger is the only production state adapter for the private durable
// recovery ledger. It owns neither a path nor a descriptor: LinuxFS retains
// those capabilities behind a qualified private-state lease and its opaque
// locked sessions.
type RecoveryLedger struct {
	sessions recoveryLedgerSessions
}

// NewRecoveryLedger accepts only an already qualified private directory
// lease. It deliberately has no path, file-descriptor, backend, or memory
// constructor. LinuxFS rechecks that the lease remains private state at each
// actual session operation.
func NewRecoveryLedger(directory *linuxfs.PrivateDirectoryLease) (*RecoveryLedger, error) {
	if directory == nil {
		return nil, fmt.Errorf("%w: qualified private-state directory is required", ErrRecoveryUnsupported)
	}
	return &RecoveryLedger{sessions: linuxfsRecoveryLedgerSessions{directory: directory}}, nil
}

// Reserve creates the first immutable intent frame for one validated Trash or
// quarantine action. It never selects a destination path or performs a
// content operation. If publication becomes ambiguous after creation, it
// returns the candidate opaque recovery identity together with ErrInterrupted
// so a caller can reload rather than mint a new token.
func (ledger *RecoveryLedger) Reserve(ctx context.Context, action domain.Action, planDigest domain.PlanDigest) (Recovery, error) {
	if err := ledger.validate(); err != nil {
		return Recovery{}, err
	}
	if err := validateRecoveryContext(ctx); err != nil {
		return Recovery{}, err
	}
	clonedAction := action.Clone()
	if err := validateRecoveryAction(clonedAction); err != nil {
		return Recovery{}, err
	}
	if err := planDigest.Validate(); err != nil {
		return Recovery{}, fmt.Errorf("%w: plan digest: %v", ErrRecoveryUnsupported, err)
	}

	var reserved Recovery
	err := ledger.sessions.withWrite(ctx, func(session recoveryLedgerSession) error {
		token, err := newRecoveryToken()
		if err != nil {
			return err
		}
		binding, err := newRecoveryBinding(clonedAction, planDigest, token)
		if err != nil {
			return err
		}
		histories, err := readRecoveryHistories(ctx, session)
		if err != nil {
			return err
		}
		if err := requireRecoveryLedgerPublicationCapacity(histories); err != nil {
			return err
		}
		if existing, err := findOutstandingRecovery(histories, binding.root, binding.source); err != nil {
			return err
		} else if existing.Token() != "" {
			return fmt.Errorf("%w: source %q under root %q already has outstanding recovery %q", ErrRecoveryConflict, binding.source.Display(), binding.root, existing.Token())
		}

		intent, err := newIntentFrame(binding)
		if err != nil {
			return err
		}
		candidate, err := recoveryFromFrame(intent)
		if err != nil {
			return err
		}
		id, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, intent.ordinal)
		if err != nil {
			return fmt.Errorf("%w: reservation record identity: %v", ErrRecoveryUnsupported, err)
		}
		contents, err := encodeRecoveryFrame(intent)
		if err != nil {
			return err
		}
		if err := session.Publish(ctx, id, contents); err != nil {
			if errors.Is(err, linuxfs.ErrInterrupted) {
				reserved = candidate
			}
			return err
		}
		reserved = candidate
		return nil
	})
	if err != nil {
		if reserved.Token() != "" && errors.Is(err, linuxfs.ErrInterrupted) {
			return reserved, err
		}
		return Recovery{}, err
	}
	return reserved, nil
}

// Transition appends one fact from the closed recovery graph. It reloads and
// validates the retained history under the same write lock, so a stale caller
// view cannot choose an ordinal, predecessor, or content-side outcome.
func (ledger *RecoveryLedger) Transition(ctx context.Context, recovery Recovery, transition RecoveryTransition) (Recovery, error) {
	if err := ledger.validate(); err != nil {
		return Recovery{}, err
	}
	if err := validateRecoveryContext(ctx); err != nil {
		return Recovery{}, err
	}
	if err := validateRecoveryIdentity(recovery); err != nil {
		return Recovery{}, err
	}

	var updated Recovery
	err := ledger.sessions.withWrite(ctx, func(session recoveryLedgerSession) error {
		histories, err := readRecoveryHistories(ctx, session)
		if err != nil {
			return err
		}
		if err := requireRecoveryLedgerPublicationCapacity(histories); err != nil {
			return err
		}
		history, err := findRecoveryHistory(histories, recovery)
		if err != nil {
			return err
		}
		next, err := appendRecoveryTransition(history.frames, transition)
		if err != nil {
			return err
		}
		candidate := history.recovery
		if err := candidate.apply(next); err != nil {
			return err
		}
		id, err := linuxfs.NewPrivateLedgerRecordID(candidate.ActionBindingDigest().String(), candidate.Token(), next.ordinal)
		if err != nil {
			return fmt.Errorf("%w: transition record identity: %v", ErrRecoveryCorrupt, err)
		}
		contents, err := encodeRecoveryFrame(next)
		if err != nil {
			return err
		}
		if err := session.Publish(ctx, id, contents); err != nil {
			if errors.Is(err, linuxfs.ErrInterrupted) {
				updated = candidate
			}
			return err
		}
		updated = candidate
		return nil
	})
	if err != nil {
		if updated.Token() != "" && errors.Is(err, linuxfs.ErrInterrupted) {
			return updated, err
		}
		return Recovery{}, err
	}
	return updated, nil
}

// FindOutstanding returns the sole open intent for an exact root/source pair.
// It returns found=false when no intent is retained and fails closed if two
// retained open intents make that source ambiguous.
func (ledger *RecoveryLedger) FindOutstanding(ctx context.Context, root domain.TrustedRootID, source pathbytes.BytePath) (Recovery, bool, error) {
	if err := ledger.validate(); err != nil {
		return Recovery{}, false, err
	}
	if err := validateRecoveryContext(ctx); err != nil {
		return Recovery{}, false, err
	}
	if err := root.Validate(); err != nil {
		return Recovery{}, false, fmt.Errorf("%w: source root: %v", ErrRecoveryUnsupported, err)
	}
	if err := validateRecoveryPath(source); err != nil {
		return Recovery{}, false, fmt.Errorf("%w: source path: %v", ErrRecoveryUnsupported, err)
	}

	var found Recovery
	err := ledger.sessions.withRead(ctx, func(session recoveryLedgerSession) error {
		histories, err := readRecoveryHistories(ctx, session)
		if err != nil {
			return err
		}
		found, err = findOutstandingRecovery(histories, root, source)
		return err
	})
	if err != nil {
		return Recovery{}, false, err
	}
	return found, found.Token() != "", nil
}

// ListOutstanding returns a deterministic bounded inventory of all open
// recovery facts. It never truncates an over-limit result or repairs an
// invalid retained record.
func (ledger *RecoveryLedger) ListOutstanding(ctx context.Context, limit int) ([]Recovery, error) {
	if err := ledger.validate(); err != nil {
		return nil, err
	}
	if err := validateRecoveryContext(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maximumRecoveryLedgerRecords {
		return nil, fmt.Errorf("%w: outstanding recovery limit must be between 1 and %d", ErrRecoveryUnsupported, maximumRecoveryLedgerRecords)
	}

	var recoveries []Recovery
	err := ledger.sessions.withRead(ctx, func(session recoveryLedgerSession) error {
		histories, err := readRecoveryHistories(ctx, session)
		if err != nil {
			return err
		}
		recoveries, err = outstandingRecoveries(histories, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return recoveries, nil
}

// Reload reconstructs current facts for an opaque recovery identity returned
// by this ledger. It does not mutate state and deliberately ignores any stale
// caller event/ordinal fields in favor of the retained frame chain.
func (ledger *RecoveryLedger) Reload(ctx context.Context, recovery Recovery) (Recovery, error) {
	if err := ledger.validate(); err != nil {
		return Recovery{}, err
	}
	if err := validateRecoveryContext(ctx); err != nil {
		return Recovery{}, err
	}
	if err := validateRecoveryIdentity(recovery); err != nil {
		return Recovery{}, err
	}

	var reloaded Recovery
	err := ledger.sessions.withRead(ctx, func(session recoveryLedgerSession) error {
		histories, err := readRecoveryHistories(ctx, session)
		if err != nil {
			return err
		}
		history, err := findRecoveryHistory(histories, recovery)
		if err != nil {
			return err
		}
		reloaded = history.recovery
		return nil
	})
	if err != nil {
		return Recovery{}, err
	}
	return reloaded, nil
}

func (ledger *RecoveryLedger) validate() error {
	if ledger == nil || ledger.sessions == nil {
		return fmt.Errorf("%w: initialized recovery ledger is required", ErrRecoveryUnsupported)
	}
	return nil
}

func validateRecoveryContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", linuxfs.ErrInterrupted)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", linuxfs.ErrInterrupted, err)
	}
	return nil
}

func validateRecoveryIdentity(recovery Recovery) error {
	if err := recovery.binding.actionBindingDigest.Validate(); err != nil {
		return fmt.Errorf("%w: recovery identity digest: %v", ErrRecoveryUnsupported, err)
	}
	if _, err := linuxfs.NewPrivateLedgerRecordID(recovery.binding.actionBindingDigest.String(), recovery.binding.token, 0); err != nil {
		return fmt.Errorf("%w: recovery identity token: %v", ErrRecoveryUnsupported, err)
	}
	return nil
}

type recoveryLedgerRecord struct {
	id       linuxfs.PrivateLedgerRecordID
	contents []byte
}

type recoveryHistory struct {
	frames   []recoveryFrame
	recovery Recovery
}

// readRecoveryHistories is deliberately the only state-side adapter from an
// opaque LinuxFS session to immutable recovery facts. LinuxFS controls the
// directory enumeration/read boundary; state only decodes the already-bounded
// records and rejects every malformed frame rather than skipping a tail.
func readRecoveryHistories(ctx context.Context, session recoveryLedgerSession) ([]recoveryHistory, error) {
	ids, err := session.List(ctx, maximumRecoveryLedgerRecords)
	if err != nil {
		return nil, err
	}
	records := make([]recoveryLedgerRecord, 0, len(ids))
	for _, id := range ids {
		contents, err := session.Read(ctx, id, maximumRecoveryFrameBytes)
		if err != nil {
			return nil, err
		}
		records = append(records, recoveryLedgerRecord{id: id, contents: contents})
	}
	return replayRecoveryLedgerRecords(records)
}

// requireRecoveryLedgerPublicationCapacity refuses a new immutable frame
// before LinuxFS creates it. Read paths deliberately remain available at the
// exact record limit so retained recovery facts can still be inspected.
func requireRecoveryLedgerPublicationCapacity(histories []recoveryHistory) error {
	count := 0
	for _, history := range histories {
		count += len(history.frames)
		if count >= maximumRecoveryLedgerRecords {
			return fmt.Errorf("%w: recovery ledger record capacity %d reached", ErrRecoveryConflict, maximumRecoveryLedgerRecords)
		}
	}
	return nil
}

func replayRecoveryLedgerRecords(records []recoveryLedgerRecord) ([]recoveryHistory, error) {
	if len(records) > maximumRecoveryLedgerRecords {
		return nil, fmt.Errorf("%w: recovery ledger contains %d records, limit is %d", ErrRecoveryUnsupported, len(records), maximumRecoveryLedgerRecords)
	}
	type key struct {
		digest string
		token  string
	}
	groups := make(map[key][]recoveryFrame, len(records))
	seenOrdinals := make(map[key]map[uint8]struct{}, len(records))
	for _, record := range records {
		id, err := linuxfs.NewPrivateLedgerRecordID(record.id.ActionBindingDigest(), record.id.Token(), record.id.Ordinal())
		if err != nil {
			return nil, fmt.Errorf("%w: retained record identity: %v", ErrRecoveryCorrupt, err)
		}
		frame, err := decodeRecoveryFrame(record.contents)
		if err != nil {
			return nil, err
		}
		if frame.binding.actionBindingDigest.String() != id.ActionBindingDigest() || frame.binding.token != id.Token() || frame.ordinal != id.Ordinal() {
			return nil, fmt.Errorf("%w: retained record identity does not match its immutable frame", ErrRecoveryCorrupt)
		}
		groupKey := key{digest: id.ActionBindingDigest(), token: id.Token()}
		if seenOrdinals[groupKey] == nil {
			seenOrdinals[groupKey] = make(map[uint8]struct{})
		}
		if _, duplicate := seenOrdinals[groupKey][id.Ordinal()]; duplicate {
			return nil, fmt.Errorf("%w: duplicate retained transition ordinal", ErrRecoveryCorrupt)
		}
		seenOrdinals[groupKey][id.Ordinal()] = struct{}{}
		groups[groupKey] = append(groups[groupKey], frame)
	}

	keys := make([]key, 0, len(groups))
	for groupKey := range groups {
		keys = append(keys, groupKey)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].digest == keys[right].digest {
			return keys[left].token < keys[right].token
		}
		return keys[left].digest < keys[right].digest
	})
	histories := make([]recoveryHistory, 0, len(keys))
	for _, groupKey := range keys {
		frames := groups[groupKey]
		sort.Slice(frames, func(left, right int) bool { return frames[left].ordinal < frames[right].ordinal })
		recovery, err := replayRecoveryFrames(frames)
		if err != nil {
			return nil, err
		}
		histories = append(histories, recoveryHistory{frames: frames, recovery: recovery})
	}
	if err := ensureUniqueOutstandingSources(histories); err != nil {
		return nil, err
	}
	return histories, nil
}

func ensureUniqueOutstandingSources(histories []recoveryHistory) error {
	seen := make(map[string]struct{}, len(histories))
	for _, history := range histories {
		if history.recovery.Closed() {
			continue
		}
		key := recoverySourceKey(history.recovery.binding)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: multiple outstanding recoveries retain source %q under root %q", ErrRecoveryConflict, history.recovery.binding.source.Display(), history.recovery.binding.root)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func recoverySourceKey(binding recoveryBinding) string {
	components := binding.source.Components()
	capacity := len(binding.root) + 8
	for _, component := range components {
		capacity += 4 + len(component)
	}
	encoded := make([]byte, 0, capacity)
	encoded = appendRecoveryKeyPart(encoded, []byte(binding.root))
	for _, component := range components {
		encoded = appendRecoveryKeyPart(encoded, component)
	}
	return string(encoded)
}

func appendRecoveryKeyPart(encoded, value []byte) []byte {
	length := len(value)
	encoded = append(encoded, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	return append(encoded, value...)
}

func findOutstandingRecovery(histories []recoveryHistory, root domain.TrustedRootID, source pathbytes.BytePath) (Recovery, error) {
	var found Recovery
	for _, history := range histories {
		candidate := history.recovery
		if candidate.Closed() || candidate.binding.root != root || !candidate.binding.source.Equal(source) {
			continue
		}
		if found.Token() != "" {
			return Recovery{}, fmt.Errorf("%w: multiple outstanding recoveries retain source %q under root %q", ErrRecoveryConflict, source.Display(), root)
		}
		found = candidate
	}
	return found, nil
}

func outstandingRecoveries(histories []recoveryHistory, limit int) ([]Recovery, error) {
	if limit <= 0 || limit > maximumRecoveryLedgerRecords {
		return nil, fmt.Errorf("%w: outstanding recovery limit must be between 1 and %d", ErrRecoveryUnsupported, maximumRecoveryLedgerRecords)
	}
	recoveries := make([]Recovery, 0, min(limit, len(histories)))
	for _, history := range histories {
		if history.recovery.Closed() {
			continue
		}
		if len(recoveries) == limit {
			return nil, fmt.Errorf("%w: outstanding recovery result exceeds requested %d-entry bound", ErrRecoveryUnsupported, limit)
		}
		recoveries = append(recoveries, history.recovery)
	}
	return recoveries, nil
}

func findRecoveryHistory(histories []recoveryHistory, recovery Recovery) (recoveryHistory, error) {
	for _, history := range histories {
		if history.recovery.ActionBindingDigest() == recovery.ActionBindingDigest() && history.recovery.Token() == recovery.Token() {
			return history, nil
		}
	}
	return recoveryHistory{}, fmt.Errorf("%w: recovery identity is not retained", ErrRecoveryConflict)
}
