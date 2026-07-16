package planproto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func TestPlanV1GoldenCompatibility(t *testing.T) {
	planCBOR := readContractFixture(t, "plan-v1.cbor")
	decodedCBORPlan, err := DecodePlan(planCBOR, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlan(plan-v1.cbor) error = %v", err)
	}
	reencodedCBORPlan, err := EncodePlan(decodedCBORPlan)
	if err != nil {
		t.Fatalf("EncodePlan(decoded plan-v1.cbor) error = %v", err)
	}
	if !bytes.Equal(reencodedCBORPlan, planCBOR) {
		t.Fatal("plan-v1.cbor changed after decode/re-encode")
	}

	planJSON := readContractFixture(t, "plan-v1.json")
	decodedJSONPlan, err := DecodePlanJSON(planJSON, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlanJSON(plan-v1.json) error = %v", err)
	}
	reencodedJSONPlan, err := EncodePlanJSON(decodedJSONPlan)
	if err != nil {
		t.Fatalf("EncodePlanJSON(decoded plan-v1.json) error = %v", err)
	}
	if !bytes.Equal(reencodedJSONPlan, planJSON) {
		t.Fatal("plan-v1.json changed after decode/re-encode")
	}
	requirePlanEqual(t, decodedCBORPlan, decodedJSONPlan)

	resultCBOR := readContractFixture(t, "result-indeterminate-v1.cbor")
	decodedResult, err := DecodeResult(resultCBOR, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodeResult(result-indeterminate-v1.cbor) error = %v", err)
	}
	reencodedResult, err := EncodeResult(decodedResult)
	if err != nil {
		t.Fatalf("EncodeResult(decoded result-indeterminate-v1.cbor) error = %v", err)
	}
	if !bytes.Equal(reencodedResult, resultCBOR) {
		t.Fatal("result-indeterminate-v1.cbor changed after decode/re-encode")
	}
	if len(decodedResult.Actions) != 1 || decodedResult.Actions[0].Outcome != domain.OutcomeIndeterminate ||
		decodedResult.Actions[0].Reconciliation != domain.ReconciliationRequired || decodedResult.Actions[0].ReconciliationProbe == nil {
		t.Fatalf("indeterminate result fixture lost its required reconciliation facts: %#v", decodedResult)
	}
}

func readContractFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := contractFixturePath(name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract fixture %s: %v", path, err)
	}
	return contents
}

func contractFixturePath(name string) string {
	return filepath.Join("..", "..", "tests", "fixtures", "contracts", name)
}
