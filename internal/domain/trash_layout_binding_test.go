package domain

import (
	"bytes"
	"testing"
)

const trashLayoutBindingLength = 32

func TestTrashLayoutBindingRejectsMalformedAndZeroValues(t *testing.T) {
	for _, value := range [][]byte{
		nil,
		make([]byte, trashLayoutBindingLength-1),
		make([]byte, trashLayoutBindingLength),
		make([]byte, trashLayoutBindingLength+1),
	} {
		if _, err := NewTrashLayoutBinding(value); err == nil {
			t.Errorf("NewTrashLayoutBinding(%d bytes) error = nil, want error", len(value))
		}
	}

	var zero TrashLayoutBinding
	if !zero.IsZero() {
		t.Fatal("zero TrashLayoutBinding is not zero")
	}
	if err := zero.Validate(); err == nil {
		t.Fatal("zero TrashLayoutBinding.Validate() error = nil, want error")
	}
}

func TestTrashLayoutBindingIsImmutableAndComparable(t *testing.T) {
	input := bytes.Repeat([]byte{0x7a}, trashLayoutBindingLength)
	binding, err := NewTrashLayoutBinding(input)
	if err != nil {
		t.Fatalf("NewTrashLayoutBinding() error = %v", err)
	}
	if got := binding.Bytes(); len(got) != trashLayoutBindingLength || !bytes.Equal(got, input) {
		t.Fatalf("TrashLayoutBinding.Bytes() = %x (%d bytes), want %x (%d bytes)", got, len(got), input, trashLayoutBindingLength)
	}
	if binding.IsZero() {
		t.Fatal("nonzero TrashLayoutBinding is zero")
	}
	if err := binding.Validate(); err != nil {
		t.Fatalf("TrashLayoutBinding.Validate() error = %v", err)
	}

	same, err := NewTrashLayoutBinding(append([]byte(nil), input...))
	if err != nil {
		t.Fatalf("NewTrashLayoutBinding(same) error = %v", err)
	}
	if binding != same || !binding.Equal(same) {
		t.Fatal("equal TrashLayoutBinding values compare different")
	}
	differentInput := append([]byte(nil), input...)
	differentInput[0] ^= 0xff
	different, err := NewTrashLayoutBinding(differentInput)
	if err != nil {
		t.Fatalf("NewTrashLayoutBinding(different) error = %v", err)
	}
	if binding == different || binding.Equal(different) {
		t.Fatal("different nonzero TrashLayoutBinding values compare equal")
	}
	if binding.Equal(TrashLayoutBinding{}) {
		t.Fatal("nonzero TrashLayoutBinding equals zero value")
	}
	if !(TrashLayoutBinding{}).Equal(TrashLayoutBinding{}) {
		t.Fatal("zero TrashLayoutBinding values compare different")
	}

	input[0] ^= 0xff
	if bytes.Equal(binding.Bytes(), input) {
		t.Fatal("NewTrashLayoutBinding() retained caller storage")
	}
	exported := binding.Bytes()
	exported[0] ^= 0xff
	if bytes.Equal(exported, binding.Bytes()) {
		t.Fatal("TrashLayoutBinding.Bytes() leaked mutable backing storage")
	}
	if len(binding.String()) != trashLayoutBindingLength*2 {
		t.Fatalf("TrashLayoutBinding.String() length = %d, want %d", len(binding.String()), trashLayoutBindingLength*2)
	}

	canonical := []byte("canonical-trash-layout-binding")
	computed := ComputeTrashLayoutBinding(canonical)
	if !computed.Verify(canonical) {
		t.Fatal("computed TrashLayoutBinding did not verify its canonical binding")
	}
	if computed.Verify([]byte("changed-trash-layout-binding")) {
		t.Fatal("computed TrashLayoutBinding verified changed canonical binding")
	}
}
