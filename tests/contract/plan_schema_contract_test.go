package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/planproto"
)

func TestPlanSchemaV1ContractFixtures(t *testing.T) {
	planCBOR := readPlanSchemaFixture(t, "plan-v1.cbor")
	planFromCBOR, err := planproto.DecodePlan(planCBOR, planproto.DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlan(plan-v1.cbor) error = %v", err)
	}
	reencodedPlanCBOR, err := planproto.EncodePlan(planFromCBOR)
	if err != nil {
		t.Fatalf("EncodePlan(decoded plan-v1.cbor) error = %v", err)
	}
	if !bytes.Equal(reencodedPlanCBOR, planCBOR) {
		t.Fatal("plan-v1.cbor changed after strict decode/re-encode")
	}

	planJSON := readPlanSchemaFixture(t, "plan-v1.json")
	planFromJSON, err := planproto.DecodePlanJSON(planJSON, planproto.DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlanJSON(plan-v1.json) error = %v", err)
	}
	reencodedPlanJSON, err := planproto.EncodePlanJSON(planFromJSON)
	if err != nil {
		t.Fatalf("EncodePlanJSON(decoded plan-v1.json) error = %v", err)
	}
	if !bytes.Equal(reencodedPlanJSON, planJSON) {
		t.Fatal("plan-v1.json changed after strict decode/re-encode")
	}
	planFromJSONCBOR, err := planproto.EncodePlan(planFromJSON)
	if err != nil {
		t.Fatalf("EncodePlan(decoded plan-v1.json) error = %v", err)
	}
	if !bytes.Equal(planFromJSONCBOR, planCBOR) {
		t.Fatal("plan CBOR and JSON fixtures do not represent the same canonical plan")
	}

	resultCBOR := readPlanSchemaFixture(t, "result-indeterminate-v1.cbor")
	result, err := planproto.DecodeResult(resultCBOR, planproto.DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodeResult(result-indeterminate-v1.cbor) error = %v", err)
	}
	reencodedResultCBOR, err := planproto.EncodeResult(result)
	if err != nil {
		t.Fatalf("EncodeResult(decoded result-indeterminate-v1.cbor) error = %v", err)
	}
	if !bytes.Equal(reencodedResultCBOR, resultCBOR) {
		t.Fatal("result-indeterminate-v1.cbor changed after strict decode/re-encode")
	}
	if len(result.Actions) != 1 || result.Actions[0].Outcome != domain.OutcomeIndeterminate ||
		result.Actions[0].Reconciliation != domain.ReconciliationRequired || result.Actions[0].ReconciliationProbe == nil {
		t.Fatalf("result fixture lost required indeterminate reconciliation facts: %#v", result)
	}
}

func readPlanSchemaFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("..", "fixtures", "contracts", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plan schema fixture %s: %v", name, err)
	}
	return contents
}
