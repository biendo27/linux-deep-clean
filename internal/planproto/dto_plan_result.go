package planproto

import (
	"bytes"
	"fmt"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func toPlanBodyWire(body domain.PlanBody) planBodyWire {
	actions := make([]actionWire, len(body.Actions))
	for index, action := range body.Actions {
		actions[index] = toActionWire(action)
	}
	return planBodyWire{
		SchemaVersion: body.SchemaVersion,
		Command:       string(body.Command),
		Caller:        toCallerWire(body.Caller),
		Capabilities:  toCapabilitySnapshotWire(body.Capabilities),
		ConfigDigest:  body.ConfigDigest.Bytes(),
		Actions:       actions,
		Totals:        toSizeFactsWire(body.Totals),
		Creation:      toCreationWire(body.Creation),
	}
}

func fromPlanBodyWire(wire planBodyWire, limits DecodeLimits) (domain.PlanBody, error) {
	if len(wire.Actions) > limits.MaxActions {
		return domain.PlanBody{}, fmt.Errorf("plan has more than %d actions", limits.MaxActions)
	}
	caller, err := fromCallerWire(wire.Caller)
	if err != nil {
		return domain.PlanBody{}, fmt.Errorf("plan caller: %w", err)
	}
	capabilities, err := fromCapabilitySnapshotWire(wire.Capabilities)
	if err != nil {
		return domain.PlanBody{}, fmt.Errorf("plan capabilities: %w", err)
	}
	configDigest, err := domain.NewConfigDigest(wire.ConfigDigest)
	if err != nil {
		return domain.PlanBody{}, fmt.Errorf("plan config digest: %w", err)
	}
	actions := make([]domain.Action, len(wire.Actions))
	for index, action := range wire.Actions {
		decoded, err := fromActionWire(action, limits)
		if err != nil {
			return domain.PlanBody{}, fmt.Errorf("plan action %d: %w", index, err)
		}
		actions[index] = decoded
	}
	body := domain.PlanBody{
		SchemaVersion: wire.SchemaVersion,
		Command:       domain.Command(wire.Command),
		Caller:        caller,
		Capabilities:  capabilities,
		ConfigDigest:  configDigest,
		Actions:       actions,
		Totals:        fromSizeFactsWire(wire.Totals),
		Creation:      fromCreationWire(wire.Creation),
	}
	if err := body.Validate(); err != nil {
		return domain.PlanBody{}, err
	}
	return body, nil
}

func toPlanWire(plan domain.Plan) planWire {
	body := toPlanBodyWire(plan.CanonicalBody())
	return planWire{
		SchemaVersion: body.SchemaVersion,
		Command:       body.Command,
		Caller:        body.Caller,
		Capabilities:  body.Capabilities,
		ConfigDigest:  body.ConfigDigest,
		Actions:       body.Actions,
		Totals:        body.Totals,
		Creation:      body.Creation,
		Digest:        plan.Digest().Bytes(),
	}
}

func fromPlanWire(wire planWire, limits DecodeLimits) (domain.Plan, error) {
	digest, err := domain.NewPlanDigest(wire.Digest)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("plan digest: %w", err)
	}
	body, err := fromPlanBodyWire(planBodyWire{
		SchemaVersion: wire.SchemaVersion,
		Command:       wire.Command,
		Caller:        wire.Caller,
		Capabilities:  wire.Capabilities,
		ConfigDigest:  wire.ConfigDigest,
		Actions:       wire.Actions,
		Totals:        wire.Totals,
		Creation:      wire.Creation,
	}, limits)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.NewPlan(body, digest)
}

func toActionResultWire(result domain.ActionResult) actionResultWire {
	wire := actionResultWire{
		ActionID:       result.ActionID.String(),
		Kind:           string(result.Kind),
		Outcome:        string(result.Outcome),
		Attempted:      result.Attempted,
		Reconciliation: string(result.Reconciliation),
		Recovery:       string(result.Recovery),
		VerifiedEffect: toSizeFactsWire(result.VerifiedEffect),
	}
	if result.ReconciliationProbe != nil {
		probe := toReconciliationProbeWire(*result.ReconciliationProbe)
		wire.ReconciliationProbe = &probe
	}
	if result.RecoveryHandle != nil {
		handle := toRecoveryHandleWire(*result.RecoveryHandle)
		wire.RecoveryHandle = &handle
	}
	return wire
}

func fromActionResultWire(wire actionResultWire, limits DecodeLimits) (domain.ActionResult, error) {
	id, err := domain.NewActionID(wire.ActionID)
	if err != nil {
		return domain.ActionResult{}, fmt.Errorf("result action ID: %w", err)
	}
	result := domain.ActionResult{
		ActionID:       id,
		Kind:           domain.ActionKind(wire.Kind),
		Outcome:        domain.Outcome(wire.Outcome),
		Attempted:      wire.Attempted,
		Reconciliation: domain.ReconciliationState(wire.Reconciliation),
		Recovery:       domain.RecoveryDisposition(wire.Recovery),
		VerifiedEffect: fromSizeFactsWire(wire.VerifiedEffect),
	}
	if wire.ReconciliationProbe != nil {
		probe, err := fromReconciliationProbeWire(*wire.ReconciliationProbe, limits)
		if err != nil {
			return domain.ActionResult{}, fmt.Errorf("reconciliation probe: %w", err)
		}
		result.ReconciliationProbe = &probe
	}
	if wire.RecoveryHandle != nil {
		handle, err := fromRecoveryHandleWire(*wire.RecoveryHandle, limits)
		if err != nil {
			return domain.ActionResult{}, fmt.Errorf("recovery handle: %w", err)
		}
		result.RecoveryHandle = &handle
	}
	if err := result.Validate(); err != nil {
		return domain.ActionResult{}, err
	}
	return result, nil
}

func toReconciliationProbeWire(probe domain.ReconciliationProbe) reconciliationProbeWire {
	wire := reconciliationProbeWire{Kind: string(probe.Kind)}
	switch probe.Kind {
	case domain.ReconciliationProbeFilesystemTarget, domain.ReconciliationProbeManagerObject:
		target := toTargetWire(probe.Target)
		wire.Target = &target
	case domain.ReconciliationProbePackageTransaction:
		graph := toTransactionGraphWire(probe.Graph)
		wire.Graph = &graph
	case domain.ReconciliationProbeStateRecord:
		if probe.StateRecord != nil {
			wire.StateRecord = &stateRecordProbeWire{RunID: probe.StateRecord.RunID.String(), ActionID: probe.StateRecord.ActionID.String()}
		}
	}
	return wire
}

func fromReconciliationProbeWire(wire reconciliationProbeWire, limits DecodeLimits) (domain.ReconciliationProbe, error) {
	probe := domain.ReconciliationProbe{Kind: domain.ReconciliationProbeKind(wire.Kind)}
	switch probe.Kind {
	case domain.ReconciliationProbeFilesystemTarget, domain.ReconciliationProbeManagerObject:
		if wire.Target == nil || wire.Graph != nil || wire.StateRecord != nil {
			return domain.ReconciliationProbe{}, fmt.Errorf("target reconciliation probe has invalid variants")
		}
		target, err := fromTargetWire(*wire.Target, limits)
		if err != nil {
			return domain.ReconciliationProbe{}, err
		}
		probe.Target = target
	case domain.ReconciliationProbePackageTransaction:
		if wire.Target != nil || wire.Graph == nil || wire.StateRecord != nil {
			return domain.ReconciliationProbe{}, fmt.Errorf("package reconciliation probe has invalid variants")
		}
		graph, err := fromTransactionGraphWire(*wire.Graph)
		if err != nil {
			return domain.ReconciliationProbe{}, err
		}
		probe.Graph = graph
	case domain.ReconciliationProbeStateRecord:
		if wire.Target != nil || wire.Graph != nil || wire.StateRecord == nil {
			return domain.ReconciliationProbe{}, fmt.Errorf("state-record reconciliation probe has invalid variants")
		}
		runID, err := domain.NewRunID(wire.StateRecord.RunID)
		if err != nil {
			return domain.ReconciliationProbe{}, fmt.Errorf("state record run ID: %w", err)
		}
		actionID, err := domain.NewActionID(wire.StateRecord.ActionID)
		if err != nil {
			return domain.ReconciliationProbe{}, fmt.Errorf("state record action ID: %w", err)
		}
		probe.StateRecord = &domain.StateRecordProbe{RunID: runID, ActionID: actionID}
	default:
		return domain.ReconciliationProbe{}, fmt.Errorf("unknown reconciliation probe kind %q", wire.Kind)
	}
	if err := probe.Validate(); err != nil {
		return domain.ReconciliationProbe{}, err
	}
	return probe, nil
}

func toRecoveryHandleWire(handle domain.RecoveryHandle) recoveryHandleWire {
	return recoveryHandleWire{
		Kind:         string(handle.Kind),
		Root:         handle.Root.String(),
		Token:        handle.Token,
		OriginalPath: toPathWire(handle.OriginalPath),
		RecordedAtNS: handle.RecordedAt.UnixNano(),
	}
}

func fromRecoveryHandleWire(wire recoveryHandleWire, limits DecodeLimits) (domain.RecoveryHandle, error) {
	root, err := domain.NewTrustedRootID(wire.Root)
	if err != nil {
		return domain.RecoveryHandle{}, fmt.Errorf("recovery root: %w", err)
	}
	path, err := fromPathWire(wire.OriginalPath, limits)
	if err != nil {
		return domain.RecoveryHandle{}, fmt.Errorf("recovery original path: %w", err)
	}
	handle := domain.RecoveryHandle{
		Kind:         domain.RecoveryHandleKind(wire.Kind),
		Root:         root,
		Token:        wire.Token,
		OriginalPath: path,
		RecordedAt:   time.Unix(0, wire.RecordedAtNS).UTC(),
	}
	if err := handle.Validate(); err != nil {
		return domain.RecoveryHandle{}, err
	}
	return handle, nil
}

func toExitSummaryWire(summary domain.ExitSummary) exitSummaryWire {
	return exitSummaryWire{
		Total:                  summary.Total,
		SuccessCount:           summary.SuccessCount,
		SkippedCount:           summary.SkippedCount,
		DriftedCount:           summary.DriftedCount,
		DeniedCount:            summary.DeniedCount,
		UnsupportedCount:       summary.UnsupportedCount,
		FailedCount:            summary.FailedCount,
		InterruptedCount:       summary.InterruptedCount,
		IndeterminateCount:     summary.IndeterminateCount,
		Partial:                summary.Partial,
		ReconciliationRequired: summary.ReconciliationRequired,
	}
}

func fromExitSummaryWire(wire exitSummaryWire) domain.ExitSummary {
	return domain.ExitSummary{
		Total:                  wire.Total,
		SuccessCount:           wire.SuccessCount,
		SkippedCount:           wire.SkippedCount,
		DriftedCount:           wire.DriftedCount,
		DeniedCount:            wire.DeniedCount,
		UnsupportedCount:       wire.UnsupportedCount,
		FailedCount:            wire.FailedCount,
		InterruptedCount:       wire.InterruptedCount,
		IndeterminateCount:     wire.IndeterminateCount,
		Partial:                wire.Partial,
		ReconciliationRequired: wire.ReconciliationRequired,
	}
}

func toResultWire(result domain.Result) resultWire {
	actions := make([]actionResultWire, len(result.Actions))
	for index, action := range result.Actions {
		actions[index] = toActionResultWire(action)
	}
	return resultWire{
		SchemaVersion: result.SchemaVersion,
		PlanDigest:    result.PlanDigest.Bytes(),
		RunID:         result.RunID.String(),
		Actions:       actions,
		Summary:       toExitSummaryWire(result.Summary),
	}
}

func fromResultWire(wire resultWire, limits DecodeLimits) (domain.Result, error) {
	if len(wire.Actions) > limits.MaxActions {
		return domain.Result{}, fmt.Errorf("result has more than %d actions", limits.MaxActions)
	}
	digest, err := domain.NewPlanDigest(wire.PlanDigest)
	if err != nil {
		return domain.Result{}, fmt.Errorf("result plan digest: %w", err)
	}
	runID, err := domain.NewRunID(wire.RunID)
	if err != nil {
		return domain.Result{}, fmt.Errorf("result run ID: %w", err)
	}
	actions := make([]domain.ActionResult, len(wire.Actions))
	for index, action := range wire.Actions {
		decoded, err := fromActionResultWire(action, limits)
		if err != nil {
			return domain.Result{}, fmt.Errorf("result action %d: %w", index, err)
		}
		actions[index] = decoded
	}
	result := domain.Result{
		SchemaVersion: wire.SchemaVersion,
		PlanDigest:    digest,
		RunID:         runID,
		Actions:       actions,
		Summary:       fromExitSummaryWire(wire.Summary),
	}
	normalized, err := domain.NewResult(result)
	if err != nil {
		return domain.Result{}, err
	}
	expectedWire, err := encodeCanonical(toResultWire(normalized))
	if err != nil {
		return domain.Result{}, fmt.Errorf("canonical normalized result: %w", err)
	}
	actualWire, err := encodeCanonical(wire)
	if err != nil {
		return domain.Result{}, fmt.Errorf("canonical decoded result: %w", err)
	}
	if !bytes.Equal(expectedWire, actualWire) {
		return domain.Result{}, fmt.Errorf("result summary or normalized terminal fields are not exact v1 values")
	}
	return normalized, nil
}
