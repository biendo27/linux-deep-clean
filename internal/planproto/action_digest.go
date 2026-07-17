package planproto

import (
	"bytes"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

type actionBindingWire struct {
	SchemaVersion uint16     `cbor:"schema_version"`
	PlanDigest    []byte     `cbor:"plan_digest"`
	Action        actionWire `cbor:"action"`
}

// ActionBindingDigest canonically binds one validated action to a plan digest.
// It deliberately does not prove that the action is a member of a verified
// full plan; validating that relationship belongs to the later planner/apply
// boundary. The returned digest is audit/correlation evidence, never authority.
func ActionBindingDigest(action domain.Action, planDigest domain.PlanDigest) (domain.ActionBindingDigest, error) {
	encoded, err := EncodeActionBinding(action, planDigest)
	if err != nil {
		return domain.ActionBindingDigest{}, err
	}
	return domain.ComputeActionBindingDigest(encoded), nil
}

// EncodeActionBinding returns the canonical v1 CBOR preimage used by
// ActionBindingDigest. It preserves the complete validated action rather than
// only a later consumer's projection, so a durable state record can retain
// and independently verify the digest it names. The bytes are evidence, not
// executable authority.
func EncodeActionBinding(action domain.Action, planDigest domain.PlanDigest) ([]byte, error) {
	if err := action.Validate(); err != nil {
		return nil, fmt.Errorf("action validation: %w", err)
	}
	if err := planDigest.Validate(); err != nil {
		return nil, fmt.Errorf("plan digest validation: %w", err)
	}

	encoded, err := encodeCanonical(actionBindingWire{
		SchemaVersion: domain.SchemaVersionV1,
		PlanDigest:    planDigest.Bytes(),
		Action:        toActionWire(action),
	})
	if err != nil {
		return nil, fmt.Errorf("canonical action binding encode: %w", err)
	}
	return encoded, nil
}

// DecodeActionBinding accepts one complete bounded canonical v1
// action-binding preimage. It validates the embedded full action and plan
// digest, then requires canonical re-encoding equality before returning
// evidence values to a durable-state caller.
func DecodeActionBinding(encoded []byte, limits DecodeLimits) (domain.Action, domain.PlanDigest, error) {
	if err := scanCanonicalCBOR(encoded, limits); err != nil {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("action binding CBOR pre-scan: %w", err)
	}
	decoder, err := limits.decMode()
	if err != nil {
		return domain.Action{}, domain.PlanDigest{}, err
	}
	var wire actionBindingWire
	if err := decoder.Unmarshal(encoded, &wire); err != nil {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("action binding CBOR decode: %w", err)
	}
	if wire.SchemaVersion != domain.SchemaVersionV1 {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("unsupported action binding schema version %d", wire.SchemaVersion)
	}
	planDigest, err := domain.NewPlanDigest(wire.PlanDigest)
	if err != nil {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("action binding plan digest: %w", err)
	}
	action, err := fromActionWire(wire.Action, limits)
	if err != nil {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("action binding action: %w", err)
	}
	canonical, err := EncodeActionBinding(action, planDigest)
	if err != nil {
		return domain.Action{}, domain.PlanDigest{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return domain.Action{}, domain.PlanDigest{}, fmt.Errorf("action binding CBOR is not canonical v1 encoding")
	}
	return action, planDigest, nil
}
