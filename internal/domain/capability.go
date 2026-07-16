package domain

import (
	"fmt"
	"sort"
)

// CapabilityState preserves support facts without treating absence as success.
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnavailable CapabilityState = "unavailable"
)

// CapabilityFact is one stable probe result. Version is a provider-defined
// probe revision or observed version, not a shell command.
type CapabilityFact struct {
	ID      CapabilityID
	State   CapabilityState
	Version string
}

// CapabilitySnapshot is an immutable-by-convention canonical set of facts.
// Facts are order-insensitive and are sorted by ID during construction.
type CapabilitySnapshot struct {
	Facts []CapabilityFact
}

func (state CapabilityState) Validate() error {
	switch state {
	case CapabilitySupported, CapabilityUnsupported, CapabilityUnavailable:
		return nil
	default:
		return fmt.Errorf("unknown capability state %q", state)
	}
}

func (fact CapabilityFact) Validate() error {
	if err := fact.ID.Validate(); err != nil {
		return err
	}
	if err := fact.State.Validate(); err != nil {
		return err
	}
	if fact.Version == "" {
		return fmt.Errorf("capability fact %q requires a probe revision", fact.ID)
	}
	return nil
}

func NewCapabilitySnapshot(facts []CapabilityFact) (CapabilitySnapshot, error) {
	cloned := CapabilitySnapshot{Facts: append([]CapabilityFact(nil), facts...)}
	sort.Slice(cloned.Facts, func(left, right int) bool {
		return cloned.Facts[left].ID < cloned.Facts[right].ID
	})
	if err := cloned.Validate(); err != nil {
		return CapabilitySnapshot{}, err
	}
	return cloned, nil
}

func (snapshot CapabilitySnapshot) Validate() error {
	for index, fact := range snapshot.Facts {
		if err := fact.Validate(); err != nil {
			return fmt.Errorf("capability fact %d: %w", index, err)
		}
		if index > 0 && snapshot.Facts[index-1].ID >= fact.ID {
			return fmt.Errorf("capability facts must be strictly sorted by ID")
		}
	}
	return nil
}

func (snapshot CapabilitySnapshot) Clone() CapabilitySnapshot {
	return CapabilitySnapshot{Facts: append([]CapabilityFact(nil), snapshot.Facts...)}
}
