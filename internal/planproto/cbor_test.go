package planproto

import (
	"bytes"
	"testing"
)

func TestPlanAndResultCanonicalCBORRoundTrip(t *testing.T) {
	plan := testPlan(t)
	first, err := EncodePlan(plan)
	if err != nil {
		t.Fatalf("EncodePlan() error = %v", err)
	}
	second, err := EncodePlan(plan)
	if err != nil {
		t.Fatalf("EncodePlan() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical plan encoding changed between equal values")
	}

	decoded, err := DecodePlan(first, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlan() error = %v", err)
	}
	reencoded, err := EncodePlan(decoded)
	if err != nil {
		t.Fatalf("EncodePlan(decoded) error = %v", err)
	}
	if !bytes.Equal(first, reencoded) {
		t.Fatal("decode/re-encode changed canonical plan bytes")
	}
	requirePlanEqual(t, plan, decoded)

	result := testResult(t, plan)
	resultBytes, err := EncodeResult(result)
	if err != nil {
		t.Fatalf("EncodeResult() error = %v", err)
	}
	decodedResult, err := DecodeResult(resultBytes, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodeResult() error = %v", err)
	}
	requireResultEqual(t, result, decodedResult)
}

func TestCanonicalPlanUsesCBORByteStringsForRawComponents(t *testing.T) {
	plan := testPlan(t, []byte("cache"), []byte{0xff, 0xfe})
	encoded, err := EncodePlan(plan)
	if err != nil {
		t.Fatalf("EncodePlan() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte{0x42, 0xff, 0xfe}) {
		t.Fatalf("canonical CBOR did not contain raw invalid-UTF-8 component as a byte string: %x", encoded)
	}
	if !bytes.Contains(encoded, []byte{0x45, 'c', 'a', 'c', 'h', 'e'}) {
		t.Fatalf("canonical CBOR did not contain valid UTF-8 component as a byte string: %x", encoded)
	}
}

func TestStrictDecoderRejectsNonCanonicalCorpus(t *testing.T) {
	plan := testPlan(t)
	canonical, err := EncodePlan(plan)
	if err != nil {
		t.Fatalf("EncodePlan() error = %v", err)
	}
	cases := map[string][]byte{
		"trailing item":          append(append([]byte(nil), canonical...), 0x00),
		"indefinite map":         {0xbf, 0xff},
		"tag":                    {0xc0, 0xf6},
		"float":                  {0xf9, 0x00, 0x00},
		"bignum":                 {0xc2, 0x40},
		"invalid utf8 text":      {0x61, 0xff},
		"nonminimal integer":     {0x18, 0x00},
		"unknown field":          {0xa1, 0x67, 'u', 'n', 'k', 'n', 'o', 'w', 'n', 0x00},
		"duplicate field":        {0xa2, 0x66, 'd', 'i', 'g', 'e', 's', 't', 0x40, 0x66, 'd', 'i', 'g', 'e', 's', 't', 0x40},
		"indefinite byte string": {0x5f, 0xff},
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePlan(encoded, DefaultDecodeLimits()); err == nil {
				t.Fatalf("DecodePlan(%x) accepted forbidden input", encoded)
			}
			if _, err := DecodeResult(encoded, DefaultDecodeLimits()); err == nil {
				t.Fatalf("DecodeResult(%x) accepted forbidden input", encoded)
			}
		})
	}
}

func TestDecodeLimitsAreCheckedBeforeDecodeAllocation(t *testing.T) {
	plan := testPlan(t)
	encoded, err := EncodePlan(plan)
	if err != nil {
		t.Fatalf("EncodePlan() error = %v", err)
	}
	limits := DefaultDecodeLimits()
	limits.MaxFrameBytes = len(encoded) - 1
	if _, err := DecodePlan(encoded, limits); err == nil {
		t.Fatal("DecodePlan accepted frame larger than explicit budget")
	}

	limits = DefaultDecodeLimits()
	limits.MaxArrayItems = 16
	if _, err := DecodePlan([]byte{0x98, 0x11}, limits); err == nil {
		t.Fatal("DecodePlan accepted an over-budget array header")
	}
}
