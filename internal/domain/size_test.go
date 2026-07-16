package domain

import (
	"math"
	"testing"
)

func TestSizeFactsNeverOverclaimHardLinkSavings(t *testing.T) {
	facts := testSizeFacts()
	facts.LinkCount = Uint64Fact{Known: true, Value: 2}
	if err := facts.Validate(); err == nil {
		t.Fatal("SizeFacts.Validate() accepted allocated savings for a hard-linked entry")
	}

	facts.Estimated.Allocated = ByteQuantity{}
	facts.Verified.Allocated = ByteQuantity{}
	if err := facts.Validate(); err != nil {
		t.Fatalf("SizeFacts.Validate() error = %v, want unavailable allocated savings for hard link", err)
	}
}

func TestSizeFactsCheckedAdditionPreservesUnavailableAndRejectsOverflow(t *testing.T) {
	left := testSizeFacts()
	right := testSizeFacts()
	right.Verified.Allocated = ByteQuantity{}
	sum, err := left.Add(right)
	if err != nil {
		t.Fatalf("SizeFacts.Add() error = %v", err)
	}
	if sum.Verified.Allocated.Available {
		t.Fatal("SizeFacts.Add() inferred an unavailable verified allocation")
	}
	if !sum.Aggregate || sum.LinkCount.Known {
		t.Fatalf("SizeFacts.Add() aggregate identity = %+v, want aggregate without a link count", sum)
	}

	left.Apparent = ByteQuantity{Available: true, Bytes: math.MaxUint64}
	right.Apparent = ByteQuantity{Available: true, Bytes: 1}
	if _, err := left.Add(right); err == nil {
		t.Fatal("SizeFacts.Add() overflow error = nil, want error")
	}
}
