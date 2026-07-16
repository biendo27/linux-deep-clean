package domain

import (
	"testing"
	"time"
)

func TestCreationContextRequiresUnixNanosecondRepresentableTime(t *testing.T) {
	context := CreationContext{
		ToolVersion: "1.0.0",
		HostProfile: "ubuntu-24.04",
	}

	minimum := time.Unix(0, -1<<63).UTC()
	maximum := time.Unix(0, 1<<63-1).UTC()
	for _, test := range []struct {
		name  string
		at    time.Time
		valid bool
	}{
		{name: "minimum boundary", at: minimum, valid: true},
		{name: "maximum boundary", at: maximum, valid: true},
		{name: "before minimum", at: minimum.Add(-time.Nanosecond)},
		{name: "after maximum", at: maximum.Add(time.Nanosecond)},
	} {
		t.Run(test.name, func(t *testing.T) {
			context.CreatedAt = test.at
			err := context.Validate()
			if test.valid && err != nil {
				t.Fatalf("CreationContext.Validate() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("CreationContext.Validate() accepted a time outside the Unix-nanosecond range")
			}
		})
	}
}

func TestPlanRejectsDuplicateUnknownAndCyclicDependencies(t *testing.T) {
	first := testAction(t, "trash-first")
	second := testAction(t, "trash-second", first.ID)
	body := testPlanBody(t, []Action{first, second})
	if _, err := NewPlan(body, testDigest(t, 5)); err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*PlanBody)
	}{
		{
			name: "duplicate dependency",
			mutate: func(candidate *PlanBody) {
				candidate.Actions[1].Dependencies = []ActionID{first.ID, first.ID}
			},
		},
		{
			name: "unknown dependency",
			mutate: func(candidate *PlanBody) {
				candidate.Actions[1].Dependencies = []ActionID{testActionID(t, "missing-action")}
			},
		},
		{
			name: "action dependency cycle",
			mutate: func(candidate *PlanBody) {
				candidate.Actions[0].Dependencies = []ActionID{second.ID}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := body.Clone()
			tt.mutate(&candidate)
			if _, err := NewPlan(candidate, testDigest(t, 5)); err == nil {
				t.Fatal("NewPlan() error = nil, want error")
			}
		})
	}
}

func TestPlanCanonicalBodyIsDefensiveAndStableAcrossDeclaredUnorderedInputs(t *testing.T) {
	first := testAction(t, "trash-first")
	second := testAction(t, "trash-second", first.ID)
	body := testPlanBody(t, []Action{first, second})
	plan, err := NewPlan(body, testDigest(t, 6))
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	permuted := testPlanBody(t, []Action{first.Clone(), second.Clone()})

	body.Actions[1].Dependencies[0] = testActionID(t, "mutated-action")
	canonical := plan.CanonicalBody()
	if got := canonical.Actions[1].Dependencies[0]; got != first.ID {
		t.Fatalf("plan retained mutable dependency %q, want %q", got, first.ID)
	}

	permuted.Capabilities.Facts = append([]CapabilityFact(nil), permuted.Capabilities.Facts...)
	permuted.Capabilities.Facts = append(permuted.Capabilities.Facts, CapabilityFact{
		ID:      testCapabilityID(t, "read-only"),
		State:   CapabilityUnsupported,
		Version: "1",
	})
	stable, err := NewPlan(permuted, testDigest(t, 7))
	if err != nil {
		t.Fatalf("NewPlan(permuted) error = %v", err)
	}
	gotActions := plan.CanonicalBody().Actions
	wantActions := stable.CanonicalBody().Actions
	if len(gotActions) != len(wantActions) {
		t.Fatalf("canonical action count = %d, want %d", len(gotActions), len(wantActions))
	}
	for index := range gotActions {
		if gotActions[index].ID != wantActions[index].ID {
			t.Fatalf("canonical action %d ID = %q, want %q", index, gotActions[index].ID, wantActions[index].ID)
		}
		if len(gotActions[index].Dependencies) != len(wantActions[index].Dependencies) {
			t.Fatalf("canonical action %q dependency count changed", gotActions[index].ID)
		}
		for dependencyIndex, dependency := range gotActions[index].Dependencies {
			if dependency != wantActions[index].Dependencies[dependencyIndex] {
				t.Fatalf("canonical action %q dependency %d = %q, want %q", gotActions[index].ID, dependencyIndex, dependency, wantActions[index].Dependencies[dependencyIndex])
			}
		}
	}
}
