package domain

import (
	"fmt"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// Outcome is the terminal state of one attempted plan action. It deliberately
// has no generic "other" value: adding an executor-visible outcome requires a
// schema decision.
type Outcome string

const (
	OutcomeSuccess       Outcome = "success"
	OutcomeSkipped       Outcome = "skipped"
	OutcomeDrifted       Outcome = "drifted"
	OutcomeDenied        Outcome = "denied"
	OutcomeUnsupported   Outcome = "unsupported"
	OutcomeFailed        Outcome = "failed"
	OutcomeInterrupted   Outcome = "interrupted"
	OutcomeIndeterminate Outcome = "indeterminate"
)

// ReconciliationState records whether a terminal result can safely be left
// alone. An indeterminate result is not a retryable failure: it has an
// explicit, typed reconciliation requirement instead.
type ReconciliationState string

const (
	ReconciliationNotRequired ReconciliationState = "not_required"
	ReconciliationRequired    ReconciliationState = "reconciliation_required"
)

// ReconciliationProbeKind identifies the bounded, read-only fact needed to
// determine the effect of an indeterminate action.
type ReconciliationProbeKind string

const (
	ReconciliationProbeFilesystemTarget   ReconciliationProbeKind = "filesystem_target"
	ReconciliationProbePackageTransaction ReconciliationProbeKind = "package_transaction"
	ReconciliationProbeManagerObject      ReconciliationProbeKind = "manager_object"
	ReconciliationProbeStateRecord        ReconciliationProbeKind = "state_record"
)

// StateRecordProbe identifies a durable local record without carrying a
// caller-provided state path.
type StateRecordProbe struct {
	RunID    RunID
	ActionID ActionID
}

// ReconciliationProbe is a closed typed union. Target is authoritative only
// through its already-validated TrustedRootID + BytePath or manager object
// target; Graph is the shared, typed manager graph rather than command text.
type ReconciliationProbe struct {
	Kind        ReconciliationProbeKind
	Target      Target
	Graph       TransactionGraph
	StateRecord *StateRecordProbe
}

// RecoveryHandleKind names a recovery mechanism that is already owned by a
// trusted root. It is not a filesystem path or an arbitrary restore command.
type RecoveryHandleKind string

const (
	RecoveryHandleTrash      RecoveryHandleKind = "trash"
	RecoveryHandleQuarantine RecoveryHandleKind = "quarantine"
)

// RecoveryDisposition records the observable result of recovery-oriented
// filesystem work. It lets callers distinguish a successful restore from a
// safely retained object without treating either as a generic rollback claim.
type RecoveryDisposition string

const (
	RecoveryNotApplicable RecoveryDisposition = "not_applicable"
	RecoveryRetained      RecoveryDisposition = "retained"
	RecoveryRestored      RecoveryDisposition = "restored"
)

// RecoveryHandle identifies recoverable content using only a compiled trusted
// root, an opaque constrained token, and an exact relative BytePath.
type RecoveryHandle struct {
	Kind         RecoveryHandleKind
	Root         TrustedRootID
	Token        string
	OriginalPath pathbytes.BytePath
	RecordedAt   time.Time
}

// ActionResult records one terminal action state. VerifiedEffect is evidence,
// not an estimate, and is permitted only after confirmed success. A recovery
// handle does not claim that an irreversible action can be rolled back.
type ActionResult struct {
	ActionID            ActionID
	Kind                ActionKind
	Outcome             Outcome
	Attempted           bool
	Reconciliation      ReconciliationState
	ReconciliationProbe *ReconciliationProbe
	Recovery            RecoveryDisposition
	RecoveryHandle      *RecoveryHandle
	VerifiedEffect      SizeFacts
}

// ExitSummary is derived from action terminals; callers do not choose it.
// Count fields keep aggregate presentation from collapsing failure and unknown
// states into a misleading single "success" result.
type ExitSummary struct {
	Total                  uint32
	SuccessCount           uint32
	SkippedCount           uint32
	DriftedCount           uint32
	DeniedCount            uint32
	UnsupportedCount       uint32
	FailedCount            uint32
	InterruptedCount       uint32
	IndeterminateCount     uint32
	Partial                bool
	ReconciliationRequired bool
}

// Result is the immutable-by-convention terminal record for a plan run.
// Construct it through NewResult so its summary cannot be caller supplied.
type Result struct {
	SchemaVersion uint16
	PlanDigest    PlanDigest
	RunID         RunID
	Actions       []ActionResult
	Summary       ExitSummary
}

func (outcome Outcome) Validate() error {
	switch outcome {
	case OutcomeSuccess, OutcomeSkipped, OutcomeDrifted, OutcomeDenied,
		OutcomeUnsupported, OutcomeFailed, OutcomeInterrupted, OutcomeIndeterminate:
		return nil
	default:
		return fmt.Errorf("unknown outcome %q", outcome)
	}
}

func (state ReconciliationState) Validate() error {
	switch state {
	case ReconciliationNotRequired, ReconciliationRequired:
		return nil
	default:
		return fmt.Errorf("unknown reconciliation state %q", state)
	}
}

func (kind RecoveryHandleKind) Validate() error {
	switch kind {
	case RecoveryHandleTrash, RecoveryHandleQuarantine:
		return nil
	default:
		return fmt.Errorf("unknown recovery handle kind %q", kind)
	}
}

func (disposition RecoveryDisposition) Validate() error {
	switch disposition {
	case RecoveryNotApplicable, RecoveryRetained, RecoveryRestored:
		return nil
	default:
		return fmt.Errorf("unknown recovery disposition %q", disposition)
	}
}

// Validate checks the closed typed probe variant and rejects fields belonging
// to another variant. Reconciliation is read-only by definition; this model
// contains no retry instruction or executable authority.
func (probe ReconciliationProbe) Validate() error {
	switch probe.Kind {
	case ReconciliationProbeFilesystemTarget:
		if err := probe.Target.Validate(); err != nil {
			return fmt.Errorf("filesystem reconciliation target: %w", err)
		}
		if probe.Target.Kind != TargetFilesystem {
			return fmt.Errorf("filesystem reconciliation probe requires a filesystem target")
		}
		if !isZeroTransactionGraph(probe.Graph) || probe.StateRecord != nil {
			return fmt.Errorf("filesystem reconciliation probe contains another variant")
		}
		return nil
	case ReconciliationProbePackageTransaction:
		if !isZeroTarget(probe.Target) || probe.StateRecord != nil {
			return fmt.Errorf("package transaction reconciliation probe contains another variant")
		}
		if err := probe.Graph.Validate(); err != nil {
			return fmt.Errorf("package transaction reconciliation graph: %w", err)
		}
		return nil
	case ReconciliationProbeManagerObject:
		if err := probe.Target.Validate(); err != nil {
			return fmt.Errorf("manager-object reconciliation target: %w", err)
		}
		if probe.Target.Kind != TargetManagerObject {
			return fmt.Errorf("manager-object reconciliation probe requires a manager-object target")
		}
		if !isZeroTransactionGraph(probe.Graph) || probe.StateRecord != nil {
			return fmt.Errorf("manager-object reconciliation probe contains another variant")
		}
		return nil
	case ReconciliationProbeStateRecord:
		if !isZeroTarget(probe.Target) || !isZeroTransactionGraph(probe.Graph) {
			return fmt.Errorf("state-record reconciliation probe contains another variant")
		}
		if probe.StateRecord == nil {
			return fmt.Errorf("state-record reconciliation probe requires a record reference")
		}
		return probe.StateRecord.Validate()
	default:
		return fmt.Errorf("unknown reconciliation probe kind %q", probe.Kind)
	}
}

func (probe StateRecordProbe) Validate() error {
	if err := probe.RunID.Validate(); err != nil {
		return fmt.Errorf("state record run ID: %w", err)
	}
	if err := probe.ActionID.Validate(); err != nil {
		return fmt.Errorf("state record action ID: %w", err)
	}
	return nil
}

// Validate proves that the handle contains only typed relative authority.
func (handle RecoveryHandle) Validate() error {
	if err := handle.Kind.Validate(); err != nil {
		return err
	}
	if err := handle.Root.Validate(); err != nil {
		return fmt.Errorf("recovery root: %w", err)
	}
	if err := validateStableID("recovery token", handle.Token); err != nil {
		return err
	}
	if _, err := pathbytes.New(handle.OriginalPath.Components()); err != nil {
		return fmt.Errorf("recovery original path: %w", err)
	}
	if handle.RecordedAt.IsZero() {
		return fmt.Errorf("recovery handle requires a recorded time")
	}
	if handle.RecordedAt.Location() != time.UTC {
		return fmt.Errorf("recovery handle recorded time must be UTC")
	}
	if err := validateUnixNanosecondTime("recovery handle recorded time", handle.RecordedAt); err != nil {
		return err
	}
	return nil
}

// Validate checks state-machine invariants which prevent unknown completion
// from being disguised as a normal failure or retryable interruption.
func (result ActionResult) Validate() error {
	if err := result.ActionID.Validate(); err != nil {
		return fmt.Errorf("result action ID: %w", err)
	}
	if err := result.Kind.Validate(); err != nil {
		return fmt.Errorf("result action kind: %w", err)
	}
	if err := result.Outcome.Validate(); err != nil {
		return err
	}
	if err := result.reconciliationState().Validate(); err != nil {
		return err
	}
	if err := result.recoveryDisposition().Validate(); err != nil {
		return err
	}
	if result.ReconciliationProbe != nil {
		if err := result.ReconciliationProbe.Validate(); err != nil {
			return fmt.Errorf("reconciliation probe: %w", err)
		}
	}
	if result.RecoveryHandle != nil {
		if err := result.RecoveryHandle.Validate(); err != nil {
			return fmt.Errorf("recovery handle: %w", err)
		}
	}
	if err := result.VerifiedEffect.Validate(); err != nil {
		return fmt.Errorf("verified effect: %w", err)
	}

	switch result.Outcome {
	case OutcomeSuccess:
		if !result.Attempted {
			return fmt.Errorf("successful action result must be attempted")
		}
		if err := result.requireNoReconciliation(); err != nil {
			return err
		}
	case OutcomeFailed:
		if !result.Attempted {
			return fmt.Errorf("failed action result must be attempted")
		}
		if err := result.requireNoReconciliation(); err != nil {
			return err
		}
	case OutcomeIndeterminate:
		if !result.Attempted {
			return fmt.Errorf("indeterminate action result must record attempted dispatch")
		}
		if result.reconciliationState() != ReconciliationRequired || result.ReconciliationProbe == nil {
			return fmt.Errorf("indeterminate action result requires reconciliation_required and a typed probe")
		}
	case OutcomeDrifted:
		if result.Attempted {
			switch result.recoveryDisposition() {
			case RecoveryRetained, RecoveryRestored:
				// A filesystem action can conclusively detect a mismatch only after
				// staging. Its safety recovery is known, so this is drift rather
				// than an indeterminate dispatch result.
			default:
				return fmt.Errorf("attempted drifted action result requires retained or restored recovery")
			}
		}
		if err := result.requireNoReconciliation(); err != nil {
			return err
		}
	case OutcomeSkipped, OutcomeDenied, OutcomeUnsupported, OutcomeInterrupted:
		if result.Attempted {
			return fmt.Errorf("%s action result must become indeterminate after dispatch", result.Outcome)
		}
		if err := result.requireNoReconciliation(); err != nil {
			return err
		}
	}

	if result.Outcome != OutcomeSuccess {
		recoveryHandleAllowed := result.Outcome == OutcomeDrifted && result.Attempted && result.recoveryDisposition() == RecoveryRetained
		if result.RecoveryHandle != nil && !recoveryHandleAllowed {
			return fmt.Errorf("only successful actions or retained post-stage drift may carry recovery handles")
		}
		if !isZeroSizeFacts(result.VerifiedEffect) {
			return fmt.Errorf("only successful action results may claim verified effects")
		}
	}
	if err := result.validateRecoveryDisposition(); err != nil {
		return err
	}
	return nil
}

func (result ActionResult) requireNoReconciliation() error {
	if result.reconciliationState() != ReconciliationNotRequired {
		return fmt.Errorf("%s action result must not require reconciliation", result.Outcome)
	}
	if result.ReconciliationProbe != nil {
		return fmt.Errorf("%s action result must not carry a reconciliation probe", result.Outcome)
	}
	return nil
}

func (result ActionResult) reconciliationState() ReconciliationState {
	// As with Recovery, constructors normalize an omitted ordinary state to its
	// explicit canonical value. Non-empty unknown values still fail closed.
	if result.Reconciliation == "" {
		return ReconciliationNotRequired
	}
	return result.Reconciliation
}

func (result ActionResult) recoveryDisposition() RecoveryDisposition {
	// The zero value is accepted at the Go construction boundary and normalized
	// by NewResult before protocol encoding. It has the same safe meaning as an
	// explicitly non-recovery action, never as a hidden recovery claim.
	if result.Recovery == "" {
		return RecoveryNotApplicable
	}
	return result.Recovery
}

func (result ActionResult) validateRecoveryDisposition() error {
	switch result.recoveryDisposition() {
	case RecoveryNotApplicable:
		if result.RecoveryHandle != nil {
			return fmt.Errorf("non-recovery action result must not carry a recovery handle")
		}
	case RecoveryRetained:
		if !result.isKnownRecoveryOutcome() || result.RecoveryHandle == nil {
			return fmt.Errorf("retained recovery result requires a known success or attempted drift and a recovery handle")
		}
		if !isZeroSizeFacts(result.VerifiedEffect) {
			return fmt.Errorf("retained recovery result must not claim freed space")
		}
	case RecoveryRestored:
		if !result.isKnownRecoveryOutcome() {
			return fmt.Errorf("restored recovery result requires a known success or attempted drift")
		}
		if result.RecoveryHandle != nil {
			return fmt.Errorf("restored recovery result must not retain a recovery handle")
		}
		if !isZeroSizeFacts(result.VerifiedEffect) {
			return fmt.Errorf("restored recovery result must not claim freed space")
		}
	}
	return nil
}

func (result ActionResult) isKnownRecoveryOutcome() bool {
	return result.Outcome == OutcomeSuccess || (result.Outcome == OutcomeDrifted && result.Attempted)
}

// NewResult creates an isolated result value and derives its summary from the
// typed action terminal states.
func NewResult(result Result) (Result, error) {
	cloned := result.Clone()
	for index := range cloned.Actions {
		if cloned.Actions[index].Reconciliation == "" {
			cloned.Actions[index].Reconciliation = ReconciliationNotRequired
		}
		if cloned.Actions[index].Recovery == "" {
			cloned.Actions[index].Recovery = RecoveryNotApplicable
		}
	}
	cloned.Summary = deriveExitSummary(cloned.Actions)
	if err := cloned.Validate(); err != nil {
		return Result{}, err
	}
	return cloned, nil
}

// Validate checks result identity, unique action records, terminal invariants,
// and verifies that Summary has not been supplied independently of Actions.
func (result Result) Validate() error {
	if result.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("unsupported result schema version %d", result.SchemaVersion)
	}
	if err := result.PlanDigest.Validate(); err != nil {
		return fmt.Errorf("result plan digest: %w", err)
	}
	if err := result.RunID.Validate(); err != nil {
		return fmt.Errorf("result run ID: %w", err)
	}

	seenActions := make(map[ActionID]struct{}, len(result.Actions))
	for index, action := range result.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("result action %d: %w", index, err)
		}
		if err := validateStateRecordProbeBinding(result.RunID, action); err != nil {
			return fmt.Errorf("result action %d: %w", index, err)
		}
		if _, exists := seenActions[action.ActionID]; exists {
			return fmt.Errorf("result contains duplicate action ID %q", action.ActionID)
		}
		seenActions[action.ActionID] = struct{}{}
	}

	if expected := deriveExitSummary(result.Actions); result.Summary != expected {
		return fmt.Errorf("result summary does not match action outcomes")
	}
	return nil
}

func validateStateRecordProbeBinding(runID RunID, action ActionResult) error {
	probe := action.ReconciliationProbe
	if probe == nil || probe.Kind != ReconciliationProbeStateRecord || probe.StateRecord == nil {
		return nil
	}
	if probe.StateRecord.RunID != runID {
		return fmt.Errorf("state-record reconciliation probe run ID does not bind the result")
	}
	if probe.StateRecord.ActionID != action.ActionID {
		return fmt.Errorf("state-record reconciliation probe action ID does not bind the result action")
	}
	return nil
}

// Clone returns a deep copy of every slice and pointer reachable through a
// result, preserving the plan's ordered action-result sequence.
func (result Result) Clone() Result {
	cloned := result
	if result.Actions == nil {
		return cloned
	}
	cloned.Actions = make([]ActionResult, len(result.Actions))
	for index := range result.Actions {
		cloned.Actions[index] = result.Actions[index].Clone()
	}
	return cloned
}

// Clone returns an isolated action terminal record.
func (result ActionResult) Clone() ActionResult {
	cloned := result
	if result.ReconciliationProbe != nil {
		probe := result.ReconciliationProbe.Clone()
		cloned.ReconciliationProbe = &probe
	}
	if result.RecoveryHandle != nil {
		handle := result.RecoveryHandle.Clone()
		cloned.RecoveryHandle = &handle
	}
	return cloned
}

// Clone returns an isolated typed reconciliation request.
func (probe ReconciliationProbe) Clone() ReconciliationProbe {
	cloned := probe
	cloned.Target = probe.Target.Clone()
	cloned.Graph = probe.Graph.Clone()
	if probe.StateRecord != nil {
		stateRecord := *probe.StateRecord
		cloned.StateRecord = &stateRecord
	}
	return cloned
}

// Clone returns an isolated recovery handle, including a defensive copy of
// its raw-byte relative path.
func (handle RecoveryHandle) Clone() RecoveryHandle {
	cloned := handle
	cloned.OriginalPath = cloneBytePath(handle.OriginalPath)
	return cloned
}

func deriveExitSummary(actions []ActionResult) ExitSummary {
	summary := ExitSummary{Total: uint32(len(actions))}
	anySuccess := false
	anyNonSuccess := false
	for _, action := range actions {
		switch action.Outcome {
		case OutcomeSuccess:
			summary.SuccessCount++
			anySuccess = true
		case OutcomeSkipped:
			summary.SkippedCount++
		case OutcomeDrifted:
			summary.DriftedCount++
			anyNonSuccess = true
		case OutcomeDenied:
			summary.DeniedCount++
			anyNonSuccess = true
		case OutcomeUnsupported:
			summary.UnsupportedCount++
			anyNonSuccess = true
		case OutcomeFailed:
			summary.FailedCount++
			anyNonSuccess = true
		case OutcomeInterrupted:
			summary.InterruptedCount++
			anyNonSuccess = true
		case OutcomeIndeterminate:
			summary.IndeterminateCount++
			summary.Partial = true
			anyNonSuccess = true
		}
		if action.reconciliationState() == ReconciliationRequired {
			summary.ReconciliationRequired = true
		}
	}
	if anySuccess && anyNonSuccess {
		summary.Partial = true
	}
	return summary
}

func isZeroTarget(target Target) bool {
	return target == (Target{})
}

func isZeroTransactionGraph(graph TransactionGraph) bool {
	return graph.Provider == "" &&
		len(graph.Nodes) == 0 &&
		len(graph.Edges) == 0 &&
		graph.ProviderEvidenceDigest == (EvidenceDigest{}) &&
		graph.Guarantee.Kind == "" &&
		len(graph.Guarantee.CoveredObjects) == 0
}

func isZeroSizeFacts(facts SizeFacts) bool {
	return facts == (SizeFacts{})
}
