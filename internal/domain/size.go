package domain

import (
	"fmt"
	"math"
)

// ByteQuantity distinguishes a measured zero from a value that is unavailable.
// Unavailable values must carry zero bytes so callers cannot accidentally turn
// absence of evidence into a savings claim.
type ByteQuantity struct {
	Available bool
	Bytes     uint64
}

// SizeEffect records apparent and allocated effects separately.
type SizeEffect struct {
	Apparent  ByteQuantity
	Allocated ByteQuantity
}

// Uint64Fact, Uint32Fact, and Int64Fact make the presence of filesystem facts
// explicit. Numeric zero can be an observed value, so zero alone cannot mean
// that a stat field was available.
type Uint64Fact struct {
	Known bool
	Value uint64
}

type Uint32Fact struct {
	Known bool
	Value uint32
}

type Int64Fact struct {
	Known bool
	Value int64
}

// Validate rejects stale payload values when a fact is unavailable. Without
// this invariant the same absent observation would have multiple canonical
// encodings and an untrusted decoder could smuggle meaningless values through
// a later availability transition.
func (fact Uint64Fact) Validate() error {
	if !fact.Known && fact.Value != 0 {
		return fmt.Errorf("unknown uint64 fact must be zero")
	}
	return nil
}

// Validate rejects stale payload values when a fact is unavailable.
func (fact Uint32Fact) Validate() error {
	if !fact.Known && fact.Value != 0 {
		return fmt.Errorf("unknown uint32 fact must be zero")
	}
	return nil
}

// Validate rejects stale payload values when a fact is unavailable.
func (fact Int64Fact) Validate() error {
	if !fact.Known && fact.Value != 0 {
		return fmt.Errorf("unknown int64 fact must be zero")
	}
	return nil
}

// SizeFacts keeps source sizes, estimated effects, and verified effects apart.
// It never infers allocated savings from an entry that still has another hard
// link (or whose link count is unknown).
type SizeFacts struct {
	Apparent  ByteQuantity
	Allocated ByteQuantity
	Estimated SizeEffect
	Verified  SizeEffect
	LinkCount Uint64Fact
	// Aggregate distinguishes a total made from validated entries from facts
	// about one filesystem object. Aggregate facts cannot state one object's
	// link count or claim allocated effects: this public value carries no
	// per-entry proof ledger for exclusive allocation ownership.
	Aggregate bool
}

func (quantity ByteQuantity) Validate() error {
	if !quantity.Available && quantity.Bytes != 0 {
		return fmt.Errorf("unavailable byte quantity must be zero")
	}
	return nil
}

func (effect SizeEffect) Validate() error {
	if err := effect.Apparent.Validate(); err != nil {
		return fmt.Errorf("apparent effect: %w", err)
	}
	if err := effect.Allocated.Validate(); err != nil {
		return fmt.Errorf("allocated effect: %w", err)
	}
	return nil
}

func (facts SizeFacts) Validate() error {
	if err := facts.Apparent.Validate(); err != nil {
		return fmt.Errorf("apparent size: %w", err)
	}
	if err := facts.Allocated.Validate(); err != nil {
		return fmt.Errorf("allocated size: %w", err)
	}
	if err := facts.Estimated.Validate(); err != nil {
		return fmt.Errorf("estimated effect: %w", err)
	}
	if err := facts.Verified.Validate(); err != nil {
		return fmt.Errorf("verified effect: %w", err)
	}
	if err := facts.LinkCount.Validate(); err != nil {
		return fmt.Errorf("link count: %w", err)
	}
	if facts.Aggregate {
		if facts.LinkCount.Known {
			return fmt.Errorf("aggregate size facts must not claim one link count")
		}
		if facts.Estimated.Allocated.Available || facts.Verified.Allocated.Available {
			return fmt.Errorf("aggregate allocated effects are unavailable without per-entry proof")
		}
		return nil
	}
	if facts.LinkCount.Known && facts.LinkCount.Value == 0 {
		return fmt.Errorf("known link count must be at least one")
	}
	if !facts.LinkCount.Known || facts.LinkCount.Value > 1 {
		if facts.Estimated.Allocated.Available || facts.Verified.Allocated.Available {
			return fmt.Errorf("allocated effect is unavailable when hard-link ownership is not exclusive")
		}
	}
	return nil
}

// Add combines aggregate size facts using checked arithmetic. Any unavailable
// input remains unavailable in the aggregate rather than being guessed.
func (facts SizeFacts) Add(other SizeFacts) (SizeFacts, error) {
	if err := facts.Validate(); err != nil {
		return SizeFacts{}, err
	}
	if err := other.Validate(); err != nil {
		return SizeFacts{}, err
	}

	var err error
	result := SizeFacts{}
	if result.Apparent, err = addByteQuantity(facts.Apparent, other.Apparent); err != nil {
		return SizeFacts{}, fmt.Errorf("apparent size: %w", err)
	}
	if result.Allocated, err = addByteQuantity(facts.Allocated, other.Allocated); err != nil {
		return SizeFacts{}, fmt.Errorf("allocated size: %w", err)
	}
	if result.Estimated.Apparent, err = addByteQuantity(facts.Estimated.Apparent, other.Estimated.Apparent); err != nil {
		return SizeFacts{}, fmt.Errorf("estimated apparent effect: %w", err)
	}
	// Aggregation has no durable proof that every component's allocation was
	// exclusive. Keep allocated effects unavailable rather than allowing a
	// public Aggregate value to assert hard-link savings it cannot establish.
	result.Estimated.Allocated = ByteQuantity{}
	if result.Verified.Apparent, err = addByteQuantity(facts.Verified.Apparent, other.Verified.Apparent); err != nil {
		return SizeFacts{}, fmt.Errorf("verified apparent effect: %w", err)
	}
	result.Verified.Allocated = ByteQuantity{}
	// A plan aggregate does not make a meaningful single-file hard-link claim
	// or preserve allocated savings without a per-entry proof ledger.
	result.LinkCount = Uint64Fact{}
	result.Aggregate = true
	if err := result.Validate(); err != nil {
		return SizeFacts{}, err
	}
	return result, nil
}

func (facts SizeFacts) Clone() SizeFacts { return facts }

func addByteQuantity(left, right ByteQuantity) (ByteQuantity, error) {
	if !left.Available || !right.Available {
		return ByteQuantity{}, nil
	}
	if left.Bytes > math.MaxUint64-right.Bytes {
		return ByteQuantity{}, fmt.Errorf("byte quantity overflow")
	}
	return ByteQuantity{Available: true, Bytes: left.Bytes + right.Bytes}, nil
}
