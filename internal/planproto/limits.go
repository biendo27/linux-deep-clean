// Package planproto defines the strict v1 plan and result serialization
// boundary. It owns canonical CBOR, explicit JSON DTOs, and digest binding;
// it does not authorize or execute any described action.
package planproto

import (
	"bytes"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/fxamacker/cbor/v2"
)

// DecodeLimits is an explicit upper bound for one untrusted plan or result
// frame. The scanner checks these limits before the CBOR decoder creates
// container backing storage. Callers must select a profile deliberately;
// DefaultDecodeLimits is the v1 ordinary-process profile, while a later
// privileged boundary may choose a stricter profile.
type DecodeLimits struct {
	MaxFrameBytes       int
	MaxDepth            int
	MaxActions          int
	MaxMapPairs         int
	MaxArrayItems       int
	MaxScalarBytes      int
	MaxPathComponents   int
	MaxEncodedPathBytes int
}

// fxamacker/cbor v2.9.1 permits these highest decoder values. The nested path
// unmarshaler uses them only after scanCanonicalCBOR has applied the caller's
// actual DecodeLimits to the complete frame.
const (
	cborMaximumNestedLevels  = 65535
	cborMaximumArrayElements = 2147483647
	cborMaximumMapPairs      = 2147483647
)

// DefaultDecodeLimits returns the documented v1 bounded frame profile.
func DefaultDecodeLimits() DecodeLimits {
	return DecodeLimits{
		MaxFrameBytes:       4 << 20,
		MaxDepth:            16,
		MaxActions:          1024,
		MaxMapPairs:         128,
		MaxArrayItems:       2048,
		MaxScalarBytes:      1 << 20,
		MaxPathComponents:   1024,
		MaxEncodedPathBytes: 256 << 10,
	}
}

// Validate rejects incomplete or nonsensical decode budgets. It does not
// silently fill zero values so a caller cannot accidentally opt into broader
// library defaults.
func (limits DecodeLimits) Validate() error {
	values := []struct {
		name  string
		value int
	}{
		{"maximum frame bytes", limits.MaxFrameBytes},
		{"maximum depth", limits.MaxDepth},
		{"maximum actions", limits.MaxActions},
		{"maximum map pairs", limits.MaxMapPairs},
		{"maximum array items", limits.MaxArrayItems},
		{"maximum scalar bytes", limits.MaxScalarBytes},
		{"maximum path components", limits.MaxPathComponents},
		{"maximum encoded path bytes", limits.MaxEncodedPathBytes},
	}
	for _, value := range values {
		if value.value <= 0 {
			return fmt.Errorf("%s must be positive", value.name)
		}
	}
	if limits.MaxActions > limits.MaxArrayItems {
		return fmt.Errorf("maximum actions cannot exceed maximum array items")
	}
	if limits.MaxPathComponents > limits.MaxArrayItems {
		return fmt.Errorf("maximum path components cannot exceed maximum array items")
	}
	if limits.MaxEncodedPathBytes > limits.MaxFrameBytes {
		return fmt.Errorf("maximum encoded path bytes cannot exceed maximum frame bytes")
	}
	return nil
}

func (limits DecodeLimits) decMode() (cbor.DecMode, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return strictDecMode(
		cborLimitAtLeast(limits.MaxDepth, 4),
		cborLimitAtLeast(limits.MaxArrayItems, 16),
		cborLimitAtLeast(limits.MaxMapPairs, 16),
	)
}

func strictDecMode(maxDepth, maxArrayItems, maxMapPairs int) (cbor.DecMode, error) {
	return cbor.DecOptions{
		DupMapKey:          cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:    maxDepth,
		MaxArrayElements:   maxArrayItems,
		MaxMapPairs:        maxMapPairs,
		IndefLength:        cbor.IndefLengthForbidden,
		TagsMd:             cbor.TagsForbidden,
		ExtraReturnErrors:  cbor.ExtraDecErrorUnknownField,
		UTF8:               cbor.UTF8RejectInvalid,
		FieldNameMatching:  cbor.FieldNameMatchingCaseSensitive,
		ByteStringToString: cbor.ByteStringToStringForbidden,
	}.DecMode()
}

func cborLimitAtLeast(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

// scanCanonicalCBOR validates the complete outer byte stream before the CBOR
// library decodes into DTO slices or maps. It intentionally accepts only the
// primitive CBOR types that schema v1 uses: integers, byte strings, UTF-8 text,
// arrays, maps, booleans, and null. Canonical DTO re-encoding performs the
// remaining semantic and key-order check after typed conversion.
func scanCanonicalCBOR(data []byte, limits DecodeLimits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("CBOR frame is empty")
	}
	if len(data) > limits.MaxFrameBytes {
		return fmt.Errorf("CBOR frame exceeds %d-byte limit", limits.MaxFrameBytes)
	}
	end, err := scanCBORItem(data, 0, 0, limits)
	if err != nil {
		return err
	}
	if end != len(data) {
		return fmt.Errorf("CBOR frame has trailing bytes")
	}
	return nil
}

func scanCBORItem(data []byte, offset, depth int, limits DecodeLimits) (int, error) {
	if offset >= len(data) {
		return 0, fmt.Errorf("CBOR item ends before its header")
	}
	major := data[offset] >> 5
	argument, next, err := scanCBORArgument(data, offset)
	if err != nil {
		return 0, err
	}

	switch major {
	case 0, 1:
		return next, nil
	case 2, 3:
		if argument > uint64(limits.MaxScalarBytes) {
			return 0, fmt.Errorf("CBOR scalar exceeds %d-byte limit", limits.MaxScalarBytes)
		}
		end, err := scanCBORAdvance(next, argument, len(data))
		if err != nil {
			return 0, err
		}
		if major == 3 && !utf8.Valid(data[next:end]) {
			return 0, fmt.Errorf("CBOR text string is not valid UTF-8")
		}
		return end, nil
	case 4:
		if depth+1 > limits.MaxDepth {
			return 0, fmt.Errorf("CBOR nesting exceeds %d-level limit", limits.MaxDepth)
		}
		if argument > uint64(limits.MaxArrayItems) {
			return 0, fmt.Errorf("CBOR array exceeds %d-item limit", limits.MaxArrayItems)
		}
		for index := uint64(0); index < argument; index++ {
			next, err = scanCBORItem(data, next, depth+1, limits)
			if err != nil {
				return 0, err
			}
		}
		return next, nil
	case 5:
		if depth+1 > limits.MaxDepth {
			return 0, fmt.Errorf("CBOR nesting exceeds %d-level limit", limits.MaxDepth)
		}
		if argument > uint64(limits.MaxMapPairs) {
			return 0, fmt.Errorf("CBOR map exceeds %d-pair limit", limits.MaxMapPairs)
		}
		mapOffset := offset
		hasComponentsMember := false
		previousKeyStart := -1
		previousKeyEnd := -1
		for index := uint64(0); index < argument; index++ {
			keyOffset := next
			if cborTextEquals(data, keyOffset, "components") {
				hasComponentsMember = true
			}
			next, err = scanCBORItem(data, next, depth+1, limits)
			if err != nil {
				return 0, err
			}
			if previousKeyStart >= 0 {
				comparison := bytes.Compare(data[previousKeyStart:previousKeyEnd], data[keyOffset:next])
				switch {
				case comparison == 0:
					return 0, fmt.Errorf("CBOR map has duplicate key")
				case comparison > 0:
					return 0, fmt.Errorf("CBOR map keys are not in deterministic bytewise order")
				}
			}
			previousKeyStart = keyOffset
			previousKeyEnd = next
			if cborTextEquals(data, keyOffset, "components") {
				if err := scanPathComponentsHeader(data, next, limits); err != nil {
					return 0, err
				}
			}
			if cborTextEquals(data, keyOffset, "actions") {
				if err := scanActionsHeader(data, next, limits); err != nil {
					return 0, err
				}
			}
			next, err = scanCBORItem(data, next, depth+1, limits)
			if err != nil {
				return 0, err
			}
		}
		if hasComponentsMember && next-mapOffset > limits.MaxEncodedPathBytes {
			return 0, fmt.Errorf("encoded path map exceeds %d-byte limit", limits.MaxEncodedPathBytes)
		}
		return next, nil
	case 6:
		return 0, fmt.Errorf("CBOR tags are forbidden")
	case 7:
		if argument == 20 || argument == 21 || argument == 22 {
			return next, nil
		}
		return 0, fmt.Errorf("CBOR simple values and floats are forbidden")
	default:
		return 0, fmt.Errorf("unsupported CBOR major type %d", major)
	}
}

// cborTextEquals recognizes a definite CBOR text key without allocating a Go
// string. The scanner calls it only after bounds-safe header parsing.
func cborTextEquals(data []byte, offset int, want string) bool {
	if offset >= len(data) || data[offset]>>5 != 3 {
		return false
	}
	length, contentOffset, err := scanCBORArgument(data, offset)
	if err != nil || length != uint64(len(want)) {
		return false
	}
	_, err = scanCBORAdvance(contentOffset, length, len(data))
	if err != nil {
		return false
	}
	for index := range want {
		if data[contentOffset+index] != want[index] {
			return false
		}
	}
	return true
}

// scanPathComponentsHeader recognizes the only authority-bearing array in a
// path DTO. It applies its path-specific count budget before the general CBOR
// decoder can allocate a slice for it. The enclosing path-map byte span is
// checked by scanCBORItem once all of its members have been scanned.
func scanPathComponentsHeader(data []byte, offset int, limits DecodeLimits) error {
	if offset >= len(data) || data[offset]>>5 != 4 {
		return fmt.Errorf("path components must be a CBOR array")
	}
	count, _, err := scanCBORArgument(data, offset)
	if err != nil {
		return err
	}
	if count > uint64(limits.MaxPathComponents) {
		return fmt.Errorf("path components exceed %d-item limit", limits.MaxPathComponents)
	}
	return nil
}

// scanActionsHeader applies the plan/result-specific action count before the
// decoder allocates its action slice. No other schema-v1 field uses this name,
// so treating any untrusted map member named actions as bounded is safely
// conservative.
func scanActionsHeader(data []byte, offset int, limits DecodeLimits) error {
	if offset >= len(data) || data[offset]>>5 != 4 {
		return fmt.Errorf("actions must be a CBOR array")
	}
	count, _, err := scanCBORArgument(data, offset)
	if err != nil {
		return err
	}
	if count > uint64(limits.MaxActions) {
		return fmt.Errorf("actions exceed %d-item limit", limits.MaxActions)
	}
	return nil
}

func scanCBORArgument(data []byte, offset int) (uint64, int, error) {
	additional := data[offset] & 0x1f
	switch {
	case additional < 24:
		return uint64(additional), offset + 1, nil
	case additional == 24:
		if offset+1 >= len(data) {
			return 0, 0, fmt.Errorf("CBOR argument ends before one-byte value")
		}
		value := uint64(data[offset+1])
		if value < 24 {
			return 0, 0, fmt.Errorf("CBOR argument is not minimally encoded")
		}
		return value, offset + 2, nil
	case additional == 25:
		if offset+2 >= len(data) {
			return 0, 0, fmt.Errorf("CBOR argument ends before two-byte value")
		}
		value := uint64(data[offset+1])<<8 | uint64(data[offset+2])
		if value <= math.MaxUint8 {
			return 0, 0, fmt.Errorf("CBOR argument is not minimally encoded")
		}
		return value, offset + 3, nil
	case additional == 26:
		if offset+4 >= len(data) {
			return 0, 0, fmt.Errorf("CBOR argument ends before four-byte value")
		}
		value := uint64(data[offset+1])<<24 | uint64(data[offset+2])<<16 |
			uint64(data[offset+3])<<8 | uint64(data[offset+4])
		if value <= math.MaxUint16 {
			return 0, 0, fmt.Errorf("CBOR argument is not minimally encoded")
		}
		return value, offset + 5, nil
	case additional == 27:
		if offset+8 >= len(data) {
			return 0, 0, fmt.Errorf("CBOR argument ends before eight-byte value")
		}
		var value uint64
		for index := 1; index <= 8; index++ {
			value = value<<8 | uint64(data[offset+index])
		}
		if value <= math.MaxUint32 {
			return 0, 0, fmt.Errorf("CBOR argument is not minimally encoded")
		}
		return value, offset + 9, nil
	case additional == 31:
		return 0, 0, fmt.Errorf("indefinite-length CBOR is forbidden")
	default:
		return 0, 0, fmt.Errorf("reserved CBOR additional information %d", additional)
	}
}

func scanCBORAdvance(offset int, count uint64, length int) (int, error) {
	if offset < 0 || offset > length {
		return 0, fmt.Errorf("CBOR item starts outside its frame")
	}
	if count > uint64(length-offset) {
		return 0, fmt.Errorf("CBOR item ends before declared content")
	}
	end := offset + int(count)
	if end < offset {
		return 0, fmt.Errorf("CBOR item length overflows")
	}
	return end, nil
}
