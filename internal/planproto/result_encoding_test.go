package planproto

import (
	"bytes"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestResultEncodersNormalizeOrdinaryZeroStates(t *testing.T) {
	plan := testPlan(t)
	result := testResult(t, plan).Clone()
	result.Actions[0].Reconciliation = ""
	result.Actions[0].Recovery = ""
	if err := result.Validate(); err != nil {
		t.Fatalf("ordinary zero-state Result.Validate() error = %v", err)
	}
	expected, err := domain.NewResult(result)
	if err != nil {
		t.Fatalf("domain.NewResult() error = %v", err)
	}

	t.Run("CBOR", func(t *testing.T) {
		encoded, err := EncodeResult(result)
		if err != nil {
			t.Fatalf("EncodeResult() error = %v", err)
		}
		decoded, err := DecodeResult(encoded, DefaultDecodeLimits())
		if err != nil {
			t.Fatalf("DecodeResult() error = %v", err)
		}
		reencoded, err := EncodeResult(decoded)
		if err != nil {
			t.Fatalf("EncodeResult(decoded) error = %v", err)
		}
		if !bytes.Equal(encoded, reencoded) {
			t.Fatal("CBOR result changed after decode/re-encode")
		}
		requireCanonicalOrdinaryResult(t, expected, decoded)
	})

	t.Run("JSON", func(t *testing.T) {
		encoded, err := EncodeResultJSON(result)
		if err != nil {
			t.Fatalf("EncodeResultJSON() error = %v", err)
		}
		decoded, err := DecodeResultJSON(encoded, DefaultDecodeLimits())
		if err != nil {
			t.Fatalf("DecodeResultJSON() error = %v", err)
		}
		reencoded, err := EncodeResultJSON(decoded)
		if err != nil {
			t.Fatalf("EncodeResultJSON(decoded) error = %v", err)
		}
		if !bytes.Equal(encoded, reencoded) {
			t.Fatal("JSON result changed after decode/re-encode")
		}
		requireCanonicalOrdinaryResult(t, expected, decoded)
	})
}

func requireCanonicalOrdinaryResult(t *testing.T, want, got domain.Result) {
	t.Helper()
	if len(got.Actions) != 1 || got.Actions[0].Reconciliation != domain.ReconciliationNotRequired || got.Actions[0].Recovery != domain.RecoveryNotApplicable {
		t.Fatalf("ordinary result did not retain explicit canonical states: %#v", got.Actions)
	}
	requireResultEqual(t, want, got)
}
