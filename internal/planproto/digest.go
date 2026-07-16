package planproto

import (
	"bytes"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

// ComputeDigest canonicalizes a v1 plan body and computes its fixed
// domain-separated SHA-256 binding. The returned digest is integrity and audit
// evidence only; callers must not treat it as a MAC or authorization grant.
func ComputeDigest(body domain.PlanBody) (domain.PlanDigest, error) {
	canonical, err := canonicalPlanBody(body)
	if err != nil {
		return domain.PlanDigest{}, err
	}
	encoded, err := encodeCanonical(toPlanBodyWire(canonical))
	if err != nil {
		return domain.PlanDigest{}, fmt.Errorf("canonical plan body encode: %w", err)
	}
	return domain.ComputePlanDigest(encoded), nil
}

// BuildPlan computes a v1 body digest and constructs the corresponding
// immutable domain plan. It is the normal local construction path for callers
// that do not already carry a digest from a strict decoder.
func BuildPlan(body domain.PlanBody) (domain.Plan, error) {
	digest, err := ComputeDigest(body)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.NewPlan(body, digest)
}

// VerifyDigest reports an error when a plan's stored digest differs from the
// digest of its canonical body. It deliberately performs no authorization.
func VerifyDigest(plan domain.Plan) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("plan validation: %w", err)
	}
	computed, err := ComputeDigest(plan.CanonicalBody())
	if err != nil {
		return err
	}
	if !bytes.Equal(computed.Bytes(), plan.Digest().Bytes()) {
		return fmt.Errorf("plan digest does not bind canonical plan body")
	}
	return nil
}

func canonicalPlanBody(body domain.PlanBody) (domain.PlanBody, error) {
	// Domain keeps canonicalization behind NewPlan. A nonzero placeholder is
	// safe here because this helper uses the constructor only to normalize and
	// validate the digest-excluded body before replacing it with its real hash.
	placeholder, err := domain.NewPlanDigest(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		return domain.PlanBody{}, err
	}
	plan, err := domain.NewPlan(body, placeholder)
	if err != nil {
		return domain.PlanBody{}, err
	}
	return plan.CanonicalBody(), nil
}
