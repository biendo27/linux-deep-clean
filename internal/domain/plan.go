package domain

import (
	"fmt"
	"sort"
	"time"
)

var (
	minimumUnixNanosecondTime = time.Unix(0, -1<<63).UTC()
	maximumUnixNanosecondTime = time.Unix(0, 1<<63-1).UTC()
)

// CallerEcho records the local caller facts that shaped a plan. It is an
// audit echo only: it is never an authorization credential.
type CallerEcho struct {
	RunID                RunID
	UID                  uint32
	Interactive          bool
	SelectedCandidateIDs []CandidateID
}

// CreationContext records the stable context in which a plan body was made.
// CreatedAt is normalized to UTC by NewPlan so it has one canonical value.
type CreationContext struct {
	CreatedAt   time.Time
	ToolVersion string
	HostProfile string
}

// PlanBody is the digest-excluded, schema-versioned portion of a plan. Its
// public fields are immutable by convention; NewPlan and CanonicalBody make
// defensive copies at the boundary.
type PlanBody struct {
	SchemaVersion uint16
	Command       Command
	Caller        CallerEcho
	Capabilities  CapabilitySnapshot
	ConfigDigest  ConfigDigest
	Actions       []Action
	Totals        SizeFacts
	Creation      CreationContext
}

// Plan binds a validated canonical body to its plan digest. The digest is
// binding audit evidence, not an authorization token. Byte-level canonical
// encoding and digest verification belong to planproto.
type Plan struct {
	body   PlanBody
	digest PlanDigest
}

// NewPlan validates and defensively copies a schema-v1 body. It canonicalizes
// only declared order-insensitive collections: selected candidate IDs and
// capability facts. Action and dependency order are intentional semantic plan
// data and are preserved exactly.
func NewPlan(body PlanBody, digest PlanDigest) (Plan, error) {
	canonical, err := canonicalPlanBody(body)
	if err != nil {
		return Plan{}, err
	}
	if err := digest.Validate(); err != nil {
		return Plan{}, fmt.Errorf("plan digest: %w", err)
	}
	return Plan{body: canonical, digest: digest}, nil
}

// Validate checks the stored canonical body and digest. It intentionally does
// not compare the digest with encoded bytes: only planproto can establish that
// relationship after strict canonical encoding is available.
func (plan Plan) Validate() error {
	if err := plan.body.Validate(); err != nil {
		return fmt.Errorf("plan body: %w", err)
	}
	if err := plan.digest.Validate(); err != nil {
		return fmt.Errorf("plan digest: %w", err)
	}
	return nil
}

// CanonicalBody returns a deep copy so consumers cannot mutate a plan's
// retained action dependencies, byte paths, or declared unordered facts.
func (plan Plan) CanonicalBody() PlanBody {
	return plan.body.Clone()
}

// Digest returns the immutable value digest associated with this plan.
func (plan Plan) Digest() PlanDigest {
	return plan.digest
}

// Validate requires a fully canonical plan body. Callers constructing plans
// should use NewPlan, which normalizes the fields that are order-insensitive
// before invoking this check.
func (body PlanBody) Validate() error {
	if body.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("unsupported plan schema version %d", body.SchemaVersion)
	}
	if err := body.Command.Validate(); err != nil {
		return err
	}
	if err := body.Caller.Validate(); err != nil {
		return fmt.Errorf("caller echo: %w", err)
	}
	if err := body.Capabilities.Validate(); err != nil {
		return fmt.Errorf("capability snapshot: %w", err)
	}
	if err := body.ConfigDigest.Validate(); err != nil {
		return fmt.Errorf("config digest: %w", err)
	}
	if err := body.Totals.Validate(); err != nil {
		return fmt.Errorf("plan totals: %w", err)
	}
	if err := body.Creation.Validate(); err != nil {
		return fmt.Errorf("creation context: %w", err)
	}
	return validatePlanActions(body.Actions)
}

// Clone returns a deep copy of the plan body. It does not canonicalize input;
// callers that need validation and canonicalization should use NewPlan.
func (body PlanBody) Clone() PlanBody {
	cloned := body
	cloned.Caller = body.Caller.Clone()
	cloned.Capabilities = body.Capabilities.Clone()
	cloned.Actions = cloneActions(body.Actions)
	cloned.Totals = body.Totals.Clone()
	cloned.Creation = body.Creation.Clone()
	return cloned
}

// Validate checks the caller value in its canonical form. Selected candidates
// are a set and must therefore be strictly ordered after construction.
func (caller CallerEcho) Validate() error {
	if err := caller.RunID.Validate(); err != nil {
		return err
	}
	for index, id := range caller.SelectedCandidateIDs {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("selected candidate %d: %w", index, err)
		}
		if index > 0 && caller.SelectedCandidateIDs[index-1] >= id {
			return fmt.Errorf("selected candidate IDs must be strictly sorted")
		}
	}
	return nil
}

// Clone returns a value whose selected-ID backing array is independent of the
// original caller echo.
func (caller CallerEcho) Clone() CallerEcho {
	cloned := caller
	cloned.SelectedCandidateIDs = append([]CandidateID(nil), caller.SelectedCandidateIDs...)
	return cloned
}

// Validate checks the canonical creation context. A zero time and a local or
// offset-specific representation would make the same instant encode multiple
// ways, so NewPlan normalizes accepted values to UTC before this check.
func (context CreationContext) Validate() error {
	if context.CreatedAt.IsZero() {
		return fmt.Errorf("creation time is required")
	}
	if context.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("creation time must use UTC")
	}
	if err := validateUnixNanosecondTime("creation time", context.CreatedAt); err != nil {
		return err
	}
	if err := validateStableVersion("tool version", context.ToolVersion); err != nil {
		return err
	}
	if err := validateStableID("host profile", context.HostProfile); err != nil {
		return err
	}
	return nil
}

func validateUnixNanosecondTime(name string, value time.Time) error {
	if value.Before(minimumUnixNanosecondTime) || value.After(maximumUnixNanosecondTime) {
		return fmt.Errorf("%s must be representable as an int64 Unix nanosecond value", name)
	}
	return nil
}

func validateStableVersion(name, value string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%s must contain 1 through 128 bytes", name)
	}
	for _, character := range []byte(value) {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '.', character == '-', character == '_', character == '+':
		default:
			return fmt.Errorf("%s contains unsupported byte %q", name, character)
		}
	}
	return nil
}

// Clone returns the canonical UTC representation of the recorded instant.
func (context CreationContext) Clone() CreationContext {
	cloned := context
	if !cloned.CreatedAt.IsZero() {
		cloned.CreatedAt = cloned.CreatedAt.UTC()
	}
	return cloned
}

func canonicalPlanBody(body PlanBody) (PlanBody, error) {
	canonical := body.Clone()
	sort.Slice(canonical.Caller.SelectedCandidateIDs, func(left, right int) bool {
		return canonical.Caller.SelectedCandidateIDs[left] < canonical.Caller.SelectedCandidateIDs[right]
	})

	capabilities, err := NewCapabilitySnapshot(canonical.Capabilities.Facts)
	if err != nil {
		return PlanBody{}, fmt.Errorf("capability snapshot: %w", err)
	}
	canonical.Capabilities = capabilities

	canonical.Creation = canonical.Creation.Clone()

	if err := canonical.Validate(); err != nil {
		return PlanBody{}, err
	}
	return canonical, nil
}

func validatePlanActions(actions []Action) error {
	known := make(map[ActionID]struct{}, len(actions))
	for index, action := range actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("action %d: %w", index, err)
		}
		if _, exists := known[action.ID]; exists {
			return fmt.Errorf("duplicate action ID %q", action.ID)
		}
		known[action.ID] = struct{}{}
	}

	dependencies := make(map[ActionID][]ActionID, len(actions))
	for index, action := range actions {
		seenDependencies := make(map[ActionID]struct{}, len(action.Dependencies))
		for dependencyIndex, dependency := range action.Dependencies {
			if err := dependency.Validate(); err != nil {
				return fmt.Errorf("action %d dependency %d: %w", index, dependencyIndex, err)
			}
			if dependency == action.ID {
				return fmt.Errorf("action %q depends on itself", action.ID)
			}
			if _, exists := seenDependencies[dependency]; exists {
				return fmt.Errorf("action %q lists duplicate dependency %q", action.ID, dependency)
			}
			seenDependencies[dependency] = struct{}{}
			if _, exists := known[dependency]; !exists {
				return fmt.Errorf("action %q depends on unknown action %q", action.ID, dependency)
			}
		}
		dependencies[action.ID] = action.Dependencies
	}

	states := make(map[ActionID]dependencyVisitState, len(actions))
	for _, action := range actions {
		if err := visitActionDependencies(action.ID, dependencies, states); err != nil {
			return err
		}
	}
	return nil
}

type dependencyVisitState uint8

const (
	dependencyUnvisited dependencyVisitState = iota
	dependencyVisiting
	dependencyVisited
)

func visitActionDependencies(id ActionID, dependencies map[ActionID][]ActionID, states map[ActionID]dependencyVisitState) error {
	switch states[id] {
	case dependencyVisited:
		return nil
	case dependencyVisiting:
		return fmt.Errorf("action dependency cycle includes %q", id)
	}

	states[id] = dependencyVisiting
	for _, dependency := range dependencies[id] {
		if err := visitActionDependencies(dependency, dependencies, states); err != nil {
			return err
		}
	}
	states[id] = dependencyVisited
	return nil
}

func cloneActions(actions []Action) []Action {
	if actions == nil {
		return nil
	}
	cloned := make([]Action, len(actions))
	for index := range actions {
		cloned[index] = actions[index].Clone()
	}
	return cloned
}
