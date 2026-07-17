package planproto

import (
	"bytes"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestActionBindingDigestBindsCanonicalActionAndPlanDigest(t *testing.T) {
	plan := testPlan(t)
	action := plan.CanonicalBody().Actions[0]
	action.Evidence = append(action.Evidence, domain.Evidence{
		Kind: domain.EvidenceCapability,
		Capability: &domain.CapabilityEvidence{
			Capability: action.RequiredCapability,
			State:      domain.CapabilitySupported,
			Version:    "1",
		},
	})
	if err := action.Validate(); err != nil {
		t.Fatalf("action validation: %v", err)
	}

	reordered := action.Clone()
	reordered.Evidence[0], reordered.Evidence[1] = reordered.Evidence[1], reordered.Evidence[0]
	if err := reordered.Validate(); err != nil {
		t.Fatalf("reordered action validation: %v", err)
	}

	digest, err := ActionBindingDigest(action, plan.Digest())
	if err != nil {
		t.Fatalf("ActionBindingDigest() error = %v", err)
	}
	reorderedDigest, err := ActionBindingDigest(reordered, plan.Digest())
	if err != nil {
		t.Fatalf("ActionBindingDigest(reordered) error = %v", err)
	}
	if digest != reorderedDigest {
		t.Fatal("canonical evidence ordering changed the action binding digest")
	}

	changedAction := action.Clone()
	changedAction.Risk = domain.RiskMedium
	changedActionDigest, err := ActionBindingDigest(changedAction, plan.Digest())
	if err != nil {
		t.Fatalf("ActionBindingDigest(changed action) error = %v", err)
	}
	if digest == changedActionDigest {
		t.Fatal("semantic action change did not change the action binding digest")
	}

	changedPlanDigest, err := domain.NewPlanDigest(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	changedPlanBinding, err := ActionBindingDigest(action, changedPlanDigest)
	if err != nil {
		t.Fatalf("ActionBindingDigest(changed plan digest) error = %v", err)
	}
	if digest == changedPlanBinding {
		t.Fatal("plan digest change did not change the action binding digest")
	}
}

func TestActionBindingDigestRejectsInvalidInputs(t *testing.T) {
	plan := testPlan(t)
	action := plan.CanonicalBody().Actions[0]

	invalidAction := action.Clone()
	invalidAction.ID = ""
	if _, err := ActionBindingDigest(invalidAction, plan.Digest()); err == nil {
		t.Fatal("ActionBindingDigest(invalid action) error = nil, want error")
	}
	if _, err := ActionBindingDigest(action, domain.PlanDigest{}); err == nil {
		t.Fatal("ActionBindingDigest(zero plan digest) error = nil, want error")
	}
}

func TestActionBindingPayloadRoundTripsAndVerifiesDigest(t *testing.T) {
	plan := testPlan(t)
	action := plan.CanonicalBody().Actions[0]

	payload, err := EncodeActionBinding(action, plan.Digest())
	if err != nil {
		t.Fatalf("EncodeActionBinding() error = %v", err)
	}
	decodedAction, decodedPlanDigest, err := DecodeActionBinding(payload, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodeActionBinding() error = %v", err)
	}
	if decodedPlanDigest != plan.Digest() {
		t.Fatalf("decoded plan digest = %s, want %s", decodedPlanDigest, plan.Digest())
	}
	decodedPayload, err := EncodeActionBinding(decodedAction, decodedPlanDigest)
	if err != nil {
		t.Fatalf("re-encode decoded binding: %v", err)
	}
	if !bytes.Equal(payload, decodedPayload) {
		t.Fatal("canonical action binding payload changed after decode/re-encode")
	}
	wantDigest, err := ActionBindingDigest(action, plan.Digest())
	if err != nil {
		t.Fatalf("ActionBindingDigest() error = %v", err)
	}
	if got := domain.ComputeActionBindingDigest(payload); got != wantDigest {
		t.Fatalf("payload digest = %s, want %s", got, wantDigest)
	}
}

func TestDecodeActionBindingRejectsNoncanonicalOrOversizedPayload(t *testing.T) {
	plan := testPlan(t)
	payload, err := EncodeActionBinding(plan.CanonicalBody().Actions[0], plan.Digest())
	if err != nil {
		t.Fatalf("EncodeActionBinding() error = %v", err)
	}
	if _, _, err := DecodeActionBinding(append(payload, 0), DefaultDecodeLimits()); err == nil {
		t.Fatal("DecodeActionBinding() accepted trailing bytes")
	}
	limits := DefaultDecodeLimits()
	limits.MaxFrameBytes = len(payload) - 1
	if _, _, err := DecodeActionBinding(payload, limits); err == nil {
		t.Fatal("DecodeActionBinding() accepted payload over caller frame limit")
	}
}
