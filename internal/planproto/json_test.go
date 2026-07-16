package planproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionedPlanJSONRoundTripsExactRawComponents(t *testing.T) {
	plan := testPlan(t, []byte("cache"), []byte{0xff, 0xfe})
	encoded, err := EncodePlanJSON(plan)
	if err != nil {
		t.Fatalf("EncodePlanJSON() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"base64":"//4="`)) {
		t.Fatalf("exact JSON did not use base64 for invalid UTF-8: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(`"utf8":null`)) || bytes.Contains(encoded, []byte(`"base64":null`)) {
		t.Fatalf("exact JSON emitted an inactive null path authority arm: %s", encoded)
	}
	decoded, err := DecodePlanJSON(encoded, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlanJSON() error = %v", err)
	}
	requirePlanEqual(t, plan, decoded)
}

func TestVersionedResultJSONRoundTripsReconciliation(t *testing.T) {
	plan := testPlan(t)
	result := testIndeterminateResult(t, plan)
	encoded, err := EncodeResultJSON(result)
	if err != nil {
		t.Fatalf("EncodeResultJSON() error = %v", err)
	}
	decoded, err := DecodeResultJSON(encoded, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodeResultJSON() error = %v", err)
	}
	requireResultEqual(t, result, decoded)
}

func TestVersionedJSONRequiresEverySchemaField(t *testing.T) {
	t.Run("plan action dependencies", func(t *testing.T) {
		encoded, err := EncodePlanJSON(testPlan(t))
		if err != nil {
			t.Fatalf("EncodePlanJSON() error = %v", err)
		}
		omitted := bytes.Replace(encoded, []byte(`,"dependencies":[]`), nil, 1)
		if bytes.Equal(omitted, encoded) {
			t.Fatalf("plan fixture did not contain the dependencies field: %s", encoded)
		}
		if _, err := DecodePlanJSON(omitted, DefaultDecodeLimits()); err == nil {
			t.Fatal("DecodePlanJSON() accepted an omitted dependencies field")
		}
	})

	t.Run("result action verified effect", func(t *testing.T) {
		encoded, err := EncodeResultJSON(testResult(t, testPlan(t)))
		if err != nil {
			t.Fatalf("EncodeResultJSON() error = %v", err)
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		actions, ok := document["actions"].([]any)
		if !ok || len(actions) != 1 {
			t.Fatalf("unexpected result actions: %#v", document["actions"])
		}
		action, ok := actions[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected result action: %#v", actions[0])
		}
		delete(action, "verified_effect")
		omitted, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		if _, err := DecodeResultJSON(omitted, DefaultDecodeLimits()); err == nil {
			t.Fatal("DecodeResultJSON() accepted an omitted verified_effect field")
		}
	})
}

func TestVersionedJSONAllowsEquivalentSyntax(t *testing.T) {
	plan := testPlan(t)
	encoded, err := EncodePlanJSON(plan)
	if err != nil {
		t.Fatalf("EncodePlanJSON() error = %v", err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	reordered, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	decoded, err := DecodePlanJSON(reordered, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlanJSON() rejected equivalent whitespace/key order: %v", err)
	}
	requirePlanEqual(t, plan, decoded)

	escaped := bytes.Replace(encoded, []byte(`"display":"application/cache"`), []byte(`"display":"\u0061pplication/cache"`), 1)
	if bytes.Equal(escaped, encoded) {
		t.Fatalf("plan fixture did not contain the display string: %s", encoded)
	}
	decoded, err = DecodePlanJSON(escaped, DefaultDecodeLimits())
	if err != nil {
		t.Fatalf("DecodePlanJSON() rejected equivalent string escaping: %v", err)
	}
	requirePlanEqual(t, plan, decoded)
}

func TestRequireJSONSchemaShapeEnforcesCompleteTypedFieldGraphs(t *testing.T) {
	t.Run("semantic values may differ when the field graph is unchanged", func(t *testing.T) {
		actual := []byte(`{"object":{"label":"received"},"array":[1],"nullable":null,"enabled":false}`)
		canonical := []byte(`{"object":{"label":"canonical"},"array":[2],"nullable":null,"enabled":true}`)
		if err := requireJSONSchemaShape(actual, canonical); err != nil {
			t.Fatalf("requireJSONSchemaShape() error = %v", err)
		}
	})

	cases := []struct {
		name      string
		actual    string
		canonical string
		contains  string
	}{
		{
			name:      "object replaced with array",
			actual:    `[]`,
			canonical: `{}`,
			contains:  "must be an object",
		},
		{
			name:      "object field count differs",
			actual:    `{}`,
			canonical: `{"required":true}`,
			contains:  "requires 1",
		},
		{
			name:      "same count but required field missing",
			actual:    `{"other":true}`,
			canonical: `{"required":true}`,
			contains:  "missing required field",
		},
		{
			name:      "array replaced with object",
			actual:    `{}`,
			canonical: `[]`,
			contains:  "must be an array",
		},
		{
			name:      "array length differs",
			actual:    `[]`,
			canonical: `[true]`,
			contains:  "requires 1",
		},
		{
			name:      "null replaced with boolean",
			actual:    `true`,
			canonical: `null`,
			contains:  "must be null",
		},
		{
			name:      "string replaced with boolean",
			actual:    `false`,
			canonical: `"value"`,
			contains:  "must be a string",
		},
		{
			name:      "boolean replaced with string",
			actual:    `"false"`,
			canonical: `false`,
			contains:  "must be a boolean",
		},
		{
			name:      "number replaced with string",
			actual:    `"1"`,
			canonical: `1`,
			contains:  "must be a number",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := requireJSONSchemaShape([]byte(test.actual), []byte(test.canonical))
			if err == nil {
				t.Fatalf("requireJSONSchemaShape(%s, %s) unexpectedly succeeded", test.actual, test.canonical)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("requireJSONSchemaShape() error = %q, want substring %q", err, test.contains)
			}
		})
	}
}

func TestJSONShapeDecodingRejectsMalformedAndTrailingData(t *testing.T) {
	cases := []struct {
		name     string
		encoded  string
		contains string
	}{
		{
			name:     "malformed first value",
			encoded:  `{`,
			contains: "unexpected EOF",
		},
		{
			name:     "second JSON value",
			encoded:  `{} {}`,
			contains: "trailing JSON value",
		},
		{
			name:     "invalid trailing data",
			encoded:  `{} trailing`,
			contains: "trailing JSON data",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeJSONShape([]byte(test.encoded)); err == nil {
				t.Fatalf("decodeJSONShape(%q) unexpectedly succeeded", test.encoded)
			} else if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("decodeJSONShape(%q) error = %q, want substring %q", test.encoded, err, test.contains)
			}
		})
	}

	if err := decodeJSONDTO([]byte(`{} trailing`), &struct{}{}); err == nil {
		t.Fatal("decodeJSONDTO accepted invalid trailing data")
	} else if !strings.Contains(err.Error(), "trailing JSON data") {
		t.Fatalf("decodeJSONDTO() error = %q, want trailing JSON data", err)
	}
}
