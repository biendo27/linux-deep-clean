package planproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

// EncodePlanJSON returns the explicit schema-v1 JSON contract for a validated
// plan. The JSON representation is for interoperable contracts, not human
// presenter output; path Display fields remain non-authoritative.
func EncodePlanJSON(plan domain.Plan) ([]byte, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("plan validation: %w", err)
	}
	if err := VerifyDigest(plan); err != nil {
		return nil, err
	}
	return json.Marshal(toPlanWire(plan))
}

// DecodePlanJSON accepts one bounded, strict schema-v1 JSON plan. It rejects
// duplicate and unknown fields, trailing data, oversized containers, and any
// malformed exact path form before converting into the closed domain value.
func DecodePlanJSON(encoded []byte, limits DecodeLimits) (domain.Plan, error) {
	if err := scanJSON(encoded, limits); err != nil {
		return domain.Plan{}, fmt.Errorf("plan JSON pre-scan: %w", err)
	}
	var wire planWire
	if err := decodeJSONDTO(encoded, &wire); err != nil {
		return domain.Plan{}, fmt.Errorf("plan JSON decode: %w", err)
	}
	plan, err := fromPlanWire(wire, limits)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("plan domain conversion: %w", err)
	}
	if err := VerifyDigest(plan); err != nil {
		return domain.Plan{}, err
	}
	canonical, err := EncodePlanJSON(plan)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("plan canonical JSON: %w", err)
	}
	if err := requireJSONSchemaShape(encoded, canonical); err != nil {
		return domain.Plan{}, fmt.Errorf("plan JSON schema shape: %w", err)
	}
	return plan, nil
}

// EncodeResultJSON returns the explicit schema-v1 JSON contract for a
// validated result, including its derived summary and typed reconciliation
// state. It is not a presentation formatter.
func EncodeResultJSON(result domain.Result) ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("result validation: %w", err)
	}
	normalized, err := domain.NewResult(result)
	if err != nil {
		return nil, fmt.Errorf("result normalization: %w", err)
	}
	return json.Marshal(toResultWire(normalized))
}

// DecodeResultJSON accepts one bounded, strict schema-v1 JSON result and
// preserves indeterminate/reconciliation facts as typed data.
func DecodeResultJSON(encoded []byte, limits DecodeLimits) (domain.Result, error) {
	if err := scanJSON(encoded, limits); err != nil {
		return domain.Result{}, fmt.Errorf("result JSON pre-scan: %w", err)
	}
	var wire resultWire
	if err := decodeJSONDTO(encoded, &wire); err != nil {
		return domain.Result{}, fmt.Errorf("result JSON decode: %w", err)
	}
	result, err := fromResultWire(wire, limits)
	if err != nil {
		return domain.Result{}, fmt.Errorf("result domain conversion: %w", err)
	}
	canonical, err := EncodeResultJSON(result)
	if err != nil {
		return domain.Result{}, fmt.Errorf("result canonical JSON: %w", err)
	}
	if err := requireJSONSchemaShape(encoded, canonical); err != nil {
		return domain.Result{}, fmt.Errorf("result JSON schema shape: %w", err)
	}
	return result, nil
}

func decodeJSONDTO(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

// requireJSONSchemaShape requires the complete schema-v1 field graph without
// making JSON whitespace, object order, or string escape spelling semantic.
// The DTO decoder and domain conversion validate values; this check closes the
// zero-value omission gap that Go struct decoding otherwise leaves open.
func requireJSONSchemaShape(encoded, canonical []byte) error {
	actual, err := decodeJSONShape(encoded)
	if err != nil {
		return fmt.Errorf("decode received JSON shape: %w", err)
	}
	expected, err := decodeJSONShape(canonical)
	if err != nil {
		return fmt.Errorf("decode canonical JSON shape: %w", err)
	}
	return compareJSONSchemaShape(actual, expected, "$")
}

func decodeJSONShape(encoded []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing JSON data: %w", err)
	}
	return value, nil
}

func compareJSONSchemaShape(actual, expected any, path string) error {
	switch expected := expected.(type) {
	case map[string]any:
		actualObject, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		if len(actualObject) != len(expected) {
			return fmt.Errorf("%s has %d fields; schema v1 requires %d", path, len(actualObject), len(expected))
		}
		for field, expectedValue := range expected {
			actualValue, ok := actualObject[field]
			if !ok {
				return fmt.Errorf("%s is missing required field %q", path, field)
			}
			if err := compareJSONSchemaShape(actualValue, expectedValue, path+"."+field); err != nil {
				return err
			}
		}
		return nil
	case []any:
		actualArray, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if len(actualArray) != len(expected) {
			return fmt.Errorf("%s has %d entries; schema v1 requires %d", path, len(actualArray), len(expected))
		}
		for index, expectedValue := range expected {
			if err := compareJSONSchemaShape(actualArray[index], expectedValue, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	case nil:
		if actual != nil {
			return fmt.Errorf("%s must be null", path)
		}
		return nil
	case string:
		if _, ok := actual.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
		return nil
	case bool:
		if _, ok := actual.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
		return nil
	case json.Number:
		if _, ok := actual.(json.Number); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported canonical JSON type %T", path, expected)
	}
}

// scanJSON supplies the same pre-allocation budget discipline as CBOR. It is
// intentionally a token stream, not a generic decoded JSON map: objects stay
// closed by DisallowUnknownFields while this scanner rejects duplicates and
// constrains arrays before the DTO decoder allocates them.
func scanJSON(encoded []byte, limits DecodeLimits) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if len(encoded) == 0 {
		return fmt.Errorf("JSON frame is empty")
	}
	if len(encoded) > limits.MaxFrameBytes {
		return fmt.Errorf("JSON frame exceeds %d-byte limit", limits.MaxFrameBytes)
	}
	if !utf8.Valid(encoded) {
		return fmt.Errorf("JSON frame is not valid UTF-8")
	}
	if err := validateJSONSurrogateEscapes(encoded); err != nil {
		return fmt.Errorf("JSON Unicode escape: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0, "", limits); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON frame has trailing value")
		}
		return err
	}
	return nil
}

// validateJSONSurrogateEscapes checks every raw JSON string before the
// standard decoder can replace malformed UTF-16 surrogate escapes with
// U+FFFD. It deliberately leaves JSON grammar validation to json.Decoder.
func validateJSONSurrogateEscapes(encoded []byte) error {
	for position := 0; position < len(encoded); {
		if encoded[position] != '"' {
			position++
			continue
		}
		end := endJSONString(encoded, position)
		if err := validateJSONStringSurrogateEscapes(encoded[position:end]); err != nil {
			return err
		}
		position = end
	}
	return nil
}

func endJSONString(encoded []byte, start int) int {
	for position := start + 1; position < len(encoded); {
		switch encoded[position] {
		case '"':
			return position + 1
		case '\\':
			position += 2
		default:
			position++
		}
	}
	return len(encoded)
}

// validateJSONStringSurrogateEscapes inspects an original JSON string token
// before the standard decoder can replace malformed Unicode escapes with
// U+FFFD. A raw U+FFFD remains valid; only invalid \u surrogate syntax fails.
func validateJSONStringSurrogateEscapes(encoded []byte) error {
	encoded = bytes.TrimSpace(encoded)
	if len(encoded) < 2 || encoded[0] != '"' {
		return nil
	}

	for position := 1; position < len(encoded)-1; {
		if encoded[position] != '\\' {
			position++
			continue
		}
		position++
		if position >= len(encoded)-1 {
			return nil
		}
		if encoded[position] != 'u' {
			position++
			continue
		}

		first, err := parseJSONUnicodeEscape(encoded, position+1)
		if err != nil {
			return err
		}
		position += 5
		if first >= 0xd800 && first <= 0xdbff {
			if position+6 > len(encoded)-1 || encoded[position] != '\\' || encoded[position+1] != 'u' {
				return fmt.Errorf("unpaired high surrogate")
			}
			second, err := parseJSONUnicodeEscape(encoded, position+2)
			if err != nil {
				return err
			}
			if second < 0xdc00 || second > 0xdfff {
				return fmt.Errorf("invalid low surrogate")
			}
			position += 6
			continue
		}
		if first >= 0xdc00 && first <= 0xdfff {
			return fmt.Errorf("unpaired low surrogate")
		}
	}
	return nil
}

func parseJSONUnicodeEscape(encoded []byte, position int) (rune, error) {
	if position+4 > len(encoded)-1 {
		return 0, fmt.Errorf("incomplete unicode escape")
	}
	var value rune
	for offset := 0; offset < 4; offset++ {
		digit, ok := jsonHexDigit(encoded[position+offset])
		if !ok {
			return 0, fmt.Errorf("invalid unicode escape")
		}
		value = value<<4 | rune(digit)
	}
	return value, nil
}

func jsonHexDigit(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func scanJSONValue(decoder *json.Decoder, depth int, field string, limits DecodeLimits) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		if depth+1 > limits.MaxDepth {
			return fmt.Errorf("JSON nesting exceeds %d-level limit", limits.MaxDepth)
		}
		switch value {
		case '{':
			return scanJSONObject(decoder, depth+1, limits)
		case '[':
			return scanJSONArray(decoder, depth+1, field, limits)
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", value)
		}
	case string:
		if len(value) > limits.MaxScalarBytes {
			return fmt.Errorf("JSON string exceeds %d-byte limit", limits.MaxScalarBytes)
		}
	case json.Number:
		if len(value) > limits.MaxScalarBytes {
			return fmt.Errorf("JSON number exceeds %d-byte limit", limits.MaxScalarBytes)
		}
		if strings.ContainsAny(value.String(), ".eE") {
			return fmt.Errorf("JSON floats and exponent numbers are forbidden")
		}
	case bool, nil:
		return nil
	default:
		return fmt.Errorf("unsupported JSON token %T", token)
	}
	return nil
}

func scanJSONObject(decoder *json.Decoder, depth int, limits DecodeLimits) error {
	seen := make(map[string]struct{})
	// Token has consumed the opening delimiter. Include it in the raw path
	// object span so MaxEncodedPathBytes applies to every received byte from
	// '{' through the closing '}', including JSON escape spellings.
	start := decoder.InputOffset() - 1
	hasComponents := false
	for decoder.More() {
		if len(seen) >= limits.MaxMapPairs {
			return fmt.Errorf("JSON object exceeds %d-field limit", limits.MaxMapPairs)
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		field, ok := token.(string)
		if !ok {
			return fmt.Errorf("JSON object key is not a string")
		}
		if len(field) > limits.MaxScalarBytes {
			return fmt.Errorf("JSON object key exceeds %d-byte limit", limits.MaxScalarBytes)
		}
		if _, exists := seen[field]; exists {
			return fmt.Errorf("JSON object has duplicate field %q", field)
		}
		seen[field] = struct{}{}
		if field == "components" {
			hasComponents = true
		}
		if err := scanJSONValue(decoder, depth, field, limits); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("JSON object has wrong closing delimiter")
	}
	if hasComponents && decoder.InputOffset()-start > int64(limits.MaxEncodedPathBytes) {
		return fmt.Errorf("encoded JSON path exceeds %d-byte limit", limits.MaxEncodedPathBytes)
	}
	return nil
}

func scanJSONArray(decoder *json.Decoder, depth int, field string, limits DecodeLimits) error {
	maximum := limits.MaxArrayItems
	switch field {
	case "actions":
		maximum = limits.MaxActions
	case "components":
		maximum = limits.MaxPathComponents
	}
	count := 0
	for decoder.More() {
		if count >= maximum {
			return fmt.Errorf("JSON %s array exceeds %d-item limit", field, maximum)
		}
		if err := scanJSONValue(decoder, depth, "", limits); err != nil {
			return err
		}
		count++
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim(']') {
		return fmt.Errorf("JSON array has wrong closing delimiter")
	}
	return nil
}
