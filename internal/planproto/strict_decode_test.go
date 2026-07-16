package planproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestJSONStrictDecoderRejectsAmbiguityAndUnknownData(t *testing.T) {
	plan := testPlan(t)
	canonical, err := EncodePlanJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	withoutClose := strings.TrimSuffix(string(canonical), "}")
	cases := map[string][]byte{
		"unknown field":   []byte(withoutClose + `,"unknown":true}`),
		"duplicate field": []byte(withoutClose + `,"schema_version":1}`),
		"trailing value":  append(append([]byte(nil), canonical...), []byte(` {}`)...),
		"invalid utf8":    append(append([]byte(nil), canonical...), 0xff),
		"float":           []byte(`{"schema_version":1.0}`),
		"exponent":        []byte(`{"schema_version":1e0}`),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePlanJSON(encoded, DefaultDecodeLimits()); err == nil {
				t.Fatalf("DecodePlanJSON accepted %s", name)
			}
		})
	}
}

func TestJSONScannerEnforcesContainerDepthBeforeOpeningNestedValues(t *testing.T) {
	limits := DefaultDecodeLimits()
	limits.MaxDepth = 1

	for _, encoded := range [][]byte{
		[]byte(`{}`),
		[]byte(`[]`),
		[]byte(`{"scalar":1}`),
	} {
		if err := scanJSON(encoded, limits); err != nil {
			t.Fatalf("scanJSON(%s) rejected a one-container root: %v", encoded, err)
		}
	}
	for _, encoded := range [][]byte{
		[]byte(`{"nested":{}}`),
		[]byte(`{"nested":[]}`),
		[]byte(`[[]]`),
	} {
		if err := scanJSON(encoded, limits); err == nil {
			t.Fatalf("scanJSON(%s) accepted nested containers at MaxDepth=1", encoded)
		}
	}
}

func TestJSONScannerRejectsMalformedSurrogateEscapesInEveryString(t *testing.T) {
	limits := DefaultDecodeLimits()
	for _, encoded := range [][]byte{
		[]byte(`{"label":"\uD800"}`),
		[]byte(`{"label":"\uDC00"}`),
		[]byte(`{"label":"\uD800\u0061"}`),
		[]byte(`{"\uD800":"value"}`),
	} {
		if err := scanJSON(encoded, limits); err == nil {
			t.Fatalf("scanJSON(%s) accepted a malformed surrogate escape", encoded)
		}
	}
	if err := scanJSON([]byte(`{"label":"\uD83D\uDE00"}`), limits); err != nil {
		t.Fatalf("scanJSON() rejected a valid surrogate pair: %v", err)
	}
}

func TestJSONPathExactFormRejectsNoncanonicalAuthority(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"dual form", `{"display":"cache","components":[{"utf8":"cache","base64":"Y2FjaGU="}]}`},
		{"inactive utf8 null", `{"display":"\\xFF","components":[{"utf8":null,"base64":"/w=="}]}`},
		{"inactive base64 null", `{"display":"cache","components":[{"utf8":"cache","base64":null}]}`},
		{"missing form", `{"display":"cache","components":[{}]}`},
		{"noncanonical base64", `{"display":"\\xFF","components":[{"base64":"_w=="}]}`},
		{"valid utf8 as base64", `{"display":"cache","components":[{"base64":"Y2FjaGU="}]}`},
		{"unknown component field", `{"display":"cache","components":[{"utf8":"cache","other":true}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var wire pathWire
			if err := json.Unmarshal([]byte(test.json), &wire); err == nil {
				if _, err := fromPathWire(wire, DefaultDecodeLimits()); err == nil {
					t.Fatal("accepted ambiguous exact JSON path")
				}
			}
		})
	}

	valid := toPathWire(mustPath(t, "cache"))
	if _, err := fromPathWire(valid, DefaultDecodeLimits()); err != nil {
		t.Fatalf("valid exact JSON path error = %v", err)
	}
	invalidDisplay := valid
	invalidDisplay.Display = "not-authority"
	if _, err := fromPathWire(invalidDisplay, DefaultDecodeLimits()); err == nil {
		t.Fatal("accepted noncanonical display as path authority")
	}
}

func TestJSONPathUTF8ComponentsRejectMalformedSurrogateEscapes(t *testing.T) {
	cases := []struct {
		name    string
		encoded string
	}{
		{"unpaired high surrogate", `{"utf8":"\uD800"}`},
		{"unpaired low surrogate", `{"utf8":"\uDC00"}`},
		{"high surrogate followed by text", `{"utf8":"\uD800x"}`},
		{"high surrogate followed by non-low escape", `{"utf8":"\uD800\u0061"}`},
		{"high surrogate followed by high surrogate", `{"utf8":"\uD800\uD801"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var component jsonPathComponentWire
			if err := json.Unmarshal([]byte(test.encoded), &component); err == nil {
				t.Fatal("json.Unmarshal() accepted a malformed Unicode surrogate escape")
			}
		})
	}
}

func TestJSONPathUTF8ComponentPreservesEscapedSurrogatePairBytes(t *testing.T) {
	encoded := []byte(`{"display":"\\xF0\\x9F\\x98\\x80","components":[{"utf8":"\uD83D\uDE00"}]}`)
	var wire pathWire
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	path, err := fromPathWire(wire, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("fromPathWire() error = %v", err)
	}
	components := path.Components()
	if len(components) != 1 || !bytes.Equal(components[0], []byte{0xf0, 0x9f, 0x98, 0x80}) {
		t.Fatalf("escaped surrogate pair decoded to %x, want f09f9880", components)
	}
}

func TestStrictCBORScannerCoversPrimitiveAndBudgetBranches(t *testing.T) {
	limits := DefaultDecodeLimits()
	for _, encoded := range [][]byte{
		{0xf4}, // false
		{0xf5}, // true
		{0xf6}, // null
		{0x18, 0x18},
		{0x19, 0x01, 0x00},
		{0x1a, 0x00, 0x01, 0x00, 0x00},
		{0x1b, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00},
	} {
		if err := scanCanonicalCBOR(encoded, limits); err != nil {
			t.Fatalf("scanCanonicalCBOR(%x) error = %v", encoded, err)
		}
	}
	for _, encoded := range [][]byte{
		{}, {0x1c}, {0x18}, {0x19, 0}, {0x1a, 0, 0, 0}, {0x1b, 0, 0, 0, 0},
		{0xf7}, {0xfc}, {0x9f, 0xff}, {0x5a, 0, 0, 0, 1}, {0x61, 0xff},
	} {
		if err := scanCanonicalCBOR(encoded, limits); err == nil {
			t.Fatalf("scanCanonicalCBOR(%x) unexpectedly succeeded", encoded)
		}
	}

	shallow := DefaultDecodeLimits()
	shallow.MaxDepth = 1
	if err := scanCanonicalCBOR([]byte{0x81, 0x81, 0x00}, shallow); err == nil {
		t.Fatal("accepted over-depth CBOR")
	}
	shortScalar := DefaultDecodeLimits()
	shortScalar.MaxScalarBytes = 1
	if err := scanCanonicalCBOR([]byte{0x42, 0, 1}, shortScalar); err == nil {
		t.Fatal("accepted over-budget scalar")
	}
	shortMap := DefaultDecodeLimits()
	shortMap.MaxMapPairs = 1
	if err := scanCanonicalCBOR([]byte{0xa2, 0, 0, 1, 1}, shortMap); err == nil {
		t.Fatal("accepted over-budget map")
	}
	shortPath := DefaultDecodeLimits()
	shortPath.MaxPathComponents = 1
	if err := scanCanonicalCBOR([]byte{0xa1, 0x6a, 'c', 'o', 'm', 'p', 'o', 'n', 'e', 'n', 't', 's', 0x82, 0x41, 'a', 0x41, 'b'}, shortPath); err == nil {
		t.Fatal("accepted over-budget path components")
	}
	shortActions := DefaultDecodeLimits()
	shortActions.MaxActions = 1
	if err := scanCanonicalCBOR([]byte{0xa1, 0x67, 'a', 'c', 't', 'i', 'o', 'n', 's', 0x82, 0, 0}, shortActions); err == nil {
		t.Fatal("accepted over-budget actions")
	}
}

func TestStrictCBORScannerRejectsDuplicateAndOutOfOrderMapKeys(t *testing.T) {
	limits := DefaultDecodeLimits()
	for name, encoded := range map[string][]byte{
		"duplicate key":     {0xa2, 0x61, 'a', 0x00, 0x61, 'a', 0x00},
		"out of order keys": {0xa2, 0x61, 'z', 0x00, 0x61, 'a', 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			if err := scanCanonicalCBOR(encoded, limits); err == nil {
				t.Fatalf("scanCanonicalCBOR(%x) unexpectedly succeeded", encoded)
			}
		})
	}
}

func TestScanCBORAdvanceRejectsOffsetsBeyondTheFrame(t *testing.T) {
	if _, err := scanCBORAdvance(2, 0, 1); err == nil {
		t.Fatal("scanCBORAdvance accepted an offset past the frame")
	}
}

func TestCanonicalCBORRejectsWrongDigestSummaryAndNoncanonicalWire(t *testing.T) {
	plan := testPlan(t)
	planWire := toPlanWire(plan)
	planWire.Command = "analyze"
	badDigest, err := encodeCanonical(planWire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePlan(badDigest, DefaultDecodeLimits()); err == nil {
		t.Fatal("accepted plan whose digest binds a different body")
	}

	result := testResult(t, plan)
	resultWire := toResultWire(result)
	resultWire.Summary.Total++
	badSummary, err := encodeCanonical(resultWire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeResult(badSummary, DefaultDecodeLimits()); err == nil {
		t.Fatal("accepted result with caller-supplied false summary")
	}

	noncanonical, err := cbor.Marshal(toPlanWire(plan))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(noncanonical, canonical) {
		t.Skip("library default happened to match deterministic order on this version")
	}
	if _, err := DecodePlan(noncanonical, DefaultDecodeLimits()); err == nil {
		t.Fatal("accepted noncanonical CBOR map ordering")
	}
}

func TestDecodeLimitValidationAndJSONContainerBudgets(t *testing.T) {
	invalid := DefaultDecodeLimits()
	invalid.MaxActions = invalid.MaxArrayItems + 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted invalid action limit")
	}
	invalid = DefaultDecodeLimits()
	invalid.MaxPathComponents = invalid.MaxArrayItems + 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted invalid path limit")
	}
	invalid = DefaultDecodeLimits()
	invalid.MaxEncodedPathBytes = invalid.MaxFrameBytes + 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted invalid path byte limit")
	}
	invalid = DefaultDecodeLimits()
	invalid.MaxFrameBytes = 1
	if err := scanJSON([]byte(`{}`), invalid); err == nil {
		t.Fatal("accepted oversized JSON frame")
	}

	limits := DefaultDecodeLimits()
	limits.MaxActions = 1
	if err := scanJSON([]byte(`{"actions":[{},{}]}`), limits); err == nil {
		t.Fatal("accepted over-budget JSON actions")
	}
	limits = DefaultDecodeLimits()
	limits.MaxPathComponents = 1
	if err := scanJSON([]byte(`{"components":[{},{}]}`), limits); err == nil {
		t.Fatal("accepted over-budget JSON components")
	}
	limits = DefaultDecodeLimits()
	limits.MaxMapPairs = 1
	if err := scanJSON([]byte(`{"a":1,"b":2}`), limits); err == nil {
		t.Fatal("accepted over-budget JSON object")
	}
}

func TestDecodePlanHonorsBroaderNestedPathCBORLimit(t *testing.T) {
	const componentCount = 2049
	components := make([][]byte, componentCount)
	for index := range components {
		components[index] = []byte("x")
	}

	encoded, err := EncodePlan(testPlan(t, components...))
	if err != nil {
		t.Fatalf("EncodePlan() error = %v", err)
	}
	if _, err := DecodePlan(encoded, DefaultDecodeLimits()); err == nil {
		t.Fatal("default path component limit accepted an over-budget plan")
	}

	limits := DefaultDecodeLimits()
	limits.MaxArrayItems = componentCount
	limits.MaxPathComponents = componentCount
	if _, err := DecodePlan(encoded, limits); err != nil {
		t.Fatalf("DecodePlan() with deliberately broader limits error = %v", err)
	}
}
