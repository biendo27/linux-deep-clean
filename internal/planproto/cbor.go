package planproto

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/fxamacker/cbor/v2"
)

var canonicalEncMode = newCanonicalEncMode()

func newCanonicalEncMode() cbor.EncMode {
	options := cbor.CoreDetEncOptions()
	options.IndefLength = cbor.IndefLengthForbidden
	options.TagsMd = cbor.TagsForbidden
	mode, err := options.EncMode()
	if err != nil {
		panic(fmt.Sprintf("planproto canonical CBOR mode: %v", err))
	}
	return mode
}

func encodeCanonical(value any) ([]byte, error) {
	return canonicalEncMode.Marshal(value)
}

// EncodePlan returns the deterministic RFC 8949 CBOR representation of a
// validated v1 plan. It refuses a plan whose stored digest does not bind the
// canonical digest-excluded body. A digest remains audit evidence, never an
// authorization mechanism.
func EncodePlan(plan domain.Plan) ([]byte, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("plan validation: %w", err)
	}
	if err := VerifyDigest(plan); err != nil {
		return nil, err
	}
	return encodeCanonical(toPlanWire(plan))
}

// DecodePlan accepts only one complete, bounded, canonical v1 CBOR plan.
// It pre-scans before allocating decoder containers, rejects forbidden CBOR
// forms and schema fields, validates closed domain values, verifies digest
// binding, then requires re-encoding to reproduce the original bytes.
func DecodePlan(encoded []byte, limits DecodeLimits) (domain.Plan, error) {
	if err := scanCanonicalCBOR(encoded, limits); err != nil {
		return domain.Plan{}, fmt.Errorf("plan CBOR pre-scan: %w", err)
	}
	decoder, err := limits.decMode()
	if err != nil {
		return domain.Plan{}, err
	}
	var wire planWire
	if err := decoder.Unmarshal(encoded, &wire); err != nil {
		return domain.Plan{}, fmt.Errorf("plan CBOR decode: %w", err)
	}
	plan, err := fromPlanWire(wire, limits)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("plan domain conversion: %w", err)
	}
	if err := VerifyDigest(plan); err != nil {
		return domain.Plan{}, err
	}
	canonical, err := EncodePlan(plan)
	if err != nil {
		return domain.Plan{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return domain.Plan{}, fmt.Errorf("plan CBOR is not canonical v1 encoding")
	}
	return plan, nil
}

// EncodeResult returns the deterministic RFC 8949 CBOR representation of a
// validated v1 result. It canonicalizes accepted ordinary zero states through
// domain.NewResult before writing the stable DTO.
func EncodeResult(result domain.Result) ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("result validation: %w", err)
	}
	normalized, err := domain.NewResult(result)
	if err != nil {
		return nil, fmt.Errorf("result normalization: %w", err)
	}
	return encodeCanonical(toResultWire(normalized))
}

// DecodeResult accepts only one complete, bounded, canonical v1 CBOR result.
// The same strict structural scan and typed re-encode equality check used for
// plans prevents alternate encodings from changing result meaning.
func DecodeResult(encoded []byte, limits DecodeLimits) (domain.Result, error) {
	if err := scanCanonicalCBOR(encoded, limits); err != nil {
		return domain.Result{}, fmt.Errorf("result CBOR pre-scan: %w", err)
	}
	decoder, err := limits.decMode()
	if err != nil {
		return domain.Result{}, err
	}
	var wire resultWire
	if err := decoder.Unmarshal(encoded, &wire); err != nil {
		return domain.Result{}, fmt.Errorf("result CBOR decode: %w", err)
	}
	result, err := fromResultWire(wire, limits)
	if err != nil {
		return domain.Result{}, fmt.Errorf("result domain conversion: %w", err)
	}
	canonical, err := EncodeResult(result)
	if err != nil {
		return domain.Result{}, err
	}
	if !bytes.Equal(encoded, canonical) {
		return domain.Result{}, fmt.Errorf("result CBOR is not canonical v1 encoding")
	}
	return result, nil
}

func canonicalEvidenceWires(evidence []evidenceWire) []evidenceWire {
	cloned := append([]evidenceWire(nil), evidence...)
	sort.SliceStable(cloned, func(left, right int) bool {
		leftBytes, err := encodeCanonical(cloned[left])
		if err != nil {
			panic(fmt.Sprintf("valid evidence could not encode: %v", err))
		}
		rightBytes, err := encodeCanonical(cloned[right])
		if err != nil {
			panic(fmt.Sprintf("valid evidence could not encode: %v", err))
		}
		return bytes.Compare(leftBytes, rightBytes) < 0
	})
	return cloned
}
