package planproto

import (
	"bytes"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestPathWireRejectsAmbiguousAndNoncanonicalExactForms(t *testing.T) {
	utf8Component := "cache"
	validPath := toPathWire(mustPath(t, "cache"))
	invalidPath, err := pathbytes.New([][]byte{{0xff, 0xfe}})
	if err != nil {
		t.Fatal(err)
	}
	invalidWire := toPathWire(invalidPath)

	tests := map[string]func() error{
		"marshal dual forms": func() error {
			_, err := (pathWire{Display: "cache", Components: []pathComponentWire{{UTF8: &utf8Component, Base64: []byte{0xff}}}}).MarshalCBOR()
			return err
		},
		"marshal missing exact form": func() error {
			_, err := (pathWire{Display: "cache", Components: []pathComponentWire{{}}}).MarshalCBOR()
			return err
		},
		"marshal valid utf8 as bytes": func() error {
			_, err := (pathWire{Display: "cache", Components: []pathComponentWire{{Base64: []byte("cache")}}}).MarshalCBOR()
			return err
		},
		"conversion empty path": func() error {
			_, err := fromPathWire(pathWire{}, DefaultDecodeLimits())
			return err
		},
		"conversion dual forms": func() error {
			_, err := fromPathWire(pathWire{Display: "cache", Components: []pathComponentWire{{UTF8: &utf8Component, Base64: []byte{0xff}}}}, DefaultDecodeLimits())
			return err
		},
		"conversion valid utf8 as bytes": func() error {
			_, err := fromPathWire(pathWire{Display: "cache", Components: []pathComponentWire{{Base64: []byte("cache")}}}, DefaultDecodeLimits())
			return err
		},
		"conversion noncanonical display": func() error {
			wire := validPath
			wire.Display = "not-authoritative"
			_, err := fromPathWire(wire, DefaultDecodeLimits())
			return err
		},
	}
	for name, execute := range tests {
		t.Run(name, func(t *testing.T) {
			if err := execute(); err == nil {
				t.Fatal("accepted a noncanonical or ambiguous path wire")
			}
		})
	}

	encoded, err := invalidWire.MarshalCBOR()
	if err != nil {
		t.Fatalf("MarshalCBOR() valid raw path error = %v", err)
	}
	var decoded pathWire
	if err := decoded.UnmarshalCBOR(encoded); err != nil {
		t.Fatalf("UnmarshalCBOR() valid raw path error = %v", err)
	}
	decodedPath, err := fromPathWire(decoded, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("fromPathWire() valid raw path error = %v", err)
	}
	if !decodedPath.Equal(invalidPath) {
		t.Fatalf("raw path changed across CBOR path wire conversion\nwant: %q\n got: %q", invalidPath.Components(), decodedPath.Components())
	}

	if err := decoded.UnmarshalCBOR([]byte{0xa1, 0x67, 'u', 'n', 'k', 'n', 'o', 'w', 'n', 0xf6}); err == nil {
		t.Fatal("UnmarshalCBOR accepted an unknown path field")
	}
}

func TestProtocolBoundaryHelpersRejectImpossibleFrames(t *testing.T) {
	limits := DefaultDecodeLimits()
	if got := cborLimitAtLeast(1, 4); got != 4 {
		t.Fatalf("cborLimitAtLeast() = %d, want 4", got)
	}
	if got := cborLimitAtLeast(8, 4); got != 8 {
		t.Fatalf("cborLimitAtLeast() = %d, want 8", got)
	}
	if !cborTextEquals([]byte{0x61, 'a'}, 0, "a") {
		t.Fatal("cborTextEquals did not recognize a valid text key")
	}
	if cborTextEquals([]byte{0x41, 'a'}, 0, "a") || cborTextEquals([]byte{0x61, 'a'}, 0, "b") {
		t.Fatal("cborTextEquals accepted a nonmatching key")
	}
	if err := scanPathComponentsHeader([]byte{0x61, 'a'}, 0, limits); err == nil {
		t.Fatal("accepted a non-array path components member")
	}
	if err := scanActionsHeader([]byte{0x61, 'a'}, 0, limits); err == nil {
		t.Fatal("accepted a non-array actions member")
	}
	if _, err := scanCBORAdvance(-1, 0, 1); err == nil {
		t.Fatal("accepted a negative CBOR content offset")
	}
}

func TestJSONScannerCoversScalarAndNestingBoundaries(t *testing.T) {
	for _, encoded := range [][]byte{
		[]byte(`"text"`), []byte(`1`), []byte(`true`), []byte(`null`), []byte(`[]`), []byte(`{}`),
	} {
		if err := scanJSON(encoded, DefaultDecodeLimits()); err != nil {
			t.Fatalf("scanJSON(%s) error = %v", encoded, err)
		}
	}

	shortScalar := DefaultDecodeLimits()
	shortScalar.MaxScalarBytes = 1
	for _, encoded := range [][]byte{[]byte(`"ab"`), []byte(`10`)} {
		if err := scanJSON(encoded, shortScalar); err == nil {
			t.Fatalf("scanJSON(%s) accepted an oversized scalar", encoded)
		}
	}
	shallow := DefaultDecodeLimits()
	shallow.MaxDepth = 1
	if err := scanJSON([]byte(`{"outer":{"inner":0}}`), shallow); err == nil {
		t.Fatal("scanJSON accepted nesting over the configured limit")
	}
}

func TestJSONScannerMeasuresRawEncodedPathObjectSpanExactly(t *testing.T) {
	for _, encoded := range [][]byte{
		[]byte(`{"display":"a","components":[{"utf8":"a"}]}`),
		[]byte(`{"display":"\u0061","components":[{"base64":"\u002fw=="}]}`),
	} {
		limits := DefaultDecodeLimits()
		limits.MaxEncodedPathBytes = len(encoded)
		if err := scanJSON(encoded, limits); err != nil {
			t.Fatalf("scanJSON(%s) rejected a path at its exact raw-byte limit: %v", encoded, err)
		}

		limits.MaxEncodedPathBytes = len(encoded) - 1
		if err := scanJSON(encoded, limits); err == nil {
			t.Fatalf("scanJSON(%s) accepted a path one byte over its raw-byte limit", encoded)
		}
	}
}

func TestProtocolConstructionAndSortingHelpersFailClosed(t *testing.T) {
	plan := testPlan(t)
	built, err := BuildPlan(plan.CanonicalBody())
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	requirePlanEqual(t, plan, built)

	invalidBody := plan.CanonicalBody()
	invalidBody.Command = domain.Command("not-a-v1-command")
	if _, err := ComputeDigest(invalidBody); err == nil {
		t.Fatal("ComputeDigest accepted an invalid plan body")
	}
	if _, err := BuildPlan(invalidBody); err == nil {
		t.Fatal("BuildPlan accepted an invalid plan body")
	}

	for name, execute := range map[string]func() error{
		"EncodePlan":       func() error { _, err := EncodePlan(domain.Plan{}); return err },
		"EncodeResult":     func() error { _, err := EncodeResult(domain.Result{}); return err },
		"EncodePlanJSON":   func() error { _, err := EncodePlanJSON(domain.Plan{}); return err },
		"EncodeResultJSON": func() error { _, err := EncodeResultJSON(domain.Result{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := execute(); err == nil {
				t.Fatal("accepted an invalid public value")
			}
		})
	}

	if err := decodeJSONDTO([]byte(`{} {}`), &struct{}{}); err == nil {
		t.Fatal("decodeJSONDTO accepted trailing JSON data")
	}
	if err := decodeJSONDTO([]byte(`1`), &struct{}{}); err == nil {
		t.Fatal("decodeJSONDTO accepted a JSON value of the wrong DTO shape")
	}

	unsorted := []evidenceWire{{Kind: "z"}, {Kind: "a"}, {Kind: "m"}}
	sorted := canonicalEvidenceWires(unsorted)
	for index := 1; index < len(sorted); index++ {
		left, err := encodeCanonical(sorted[index-1])
		if err != nil {
			t.Fatal(err)
		}
		right, err := encodeCanonical(sorted[index])
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Compare(left, right) > 0 {
			t.Fatal("canonicalEvidenceWires did not sort by canonical bytes")
		}
	}
}
