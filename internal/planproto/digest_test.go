package planproto

import (
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestDigestBindsOnlyCanonicalPlanBody(t *testing.T) {
	plan := testPlan(t)
	computed, err := ComputeDigest(plan.CanonicalBody())
	if err != nil {
		t.Fatalf("ComputeDigest() error = %v", err)
	}
	if computed != plan.Digest() {
		t.Fatal("computed digest does not bind the plan body")
	}
	if err := VerifyDigest(plan); err != nil {
		t.Fatalf("VerifyDigest() error = %v", err)
	}

	body := plan.CanonicalBody()
	body.Command = domain.CommandAnalyze
	changed, err := ComputeDigest(body)
	if err != nil {
		t.Fatalf("ComputeDigest(mutated body) error = %v", err)
	}
	if changed == plan.Digest() {
		t.Fatal("semantic body change did not change digest")
	}
}
