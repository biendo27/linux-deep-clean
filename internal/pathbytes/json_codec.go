package pathbytes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type exactJSONPath struct {
	Display    string               `json:"display"`
	Components []exactJSONComponent `json:"components"`
}

type exactJSONComponent struct {
	UTF8   *string `json:"utf8,omitempty"`
	Base64 *string `json:"base64,omitempty"`
}

// EncodeJSONExact emits display-only text separately from the authoritative raw
// component bytes. Valid UTF-8 components use utf8; all other bytes use canonical
// padded base64.
func (path BytePath) EncodeJSONExact() ([]byte, error) {
	if err := path.validate(); err != nil {
		return nil, err
	}

	encoded := exactJSONPath{
		Display:    path.Display(),
		Components: make([]exactJSONComponent, len(path.components)),
	}
	for index, component := range path.components {
		if utf8.Valid(component) {
			value := string(component)
			encoded.Components[index].UTF8 = &value
			continue
		}
		value := base64.StdEncoding.EncodeToString(component)
		encoded.Components[index].Base64 = &value
	}
	return json.Marshal(encoded)
}

// DecodeJSONExact decodes exact JSON with the bounded Phase 2 default profile.
// The display field is required for the wire shape but is never read as path
// authority. Call DecodeJSONExactWithLimits when a narrower caller-specific
// budget is required.
func DecodeJSONExact(data []byte) (BytePath, error) {
	return DecodeJSONExactWithLimits(data, DefaultJSONDecodeLimits())
}

// DecodeJSONExactWithLimits decodes only explicitly tagged component values
// while enforcing limits before materializing component values or base64-decoded
// bytes. The display field is required for the wire shape but is never read as
// path authority.
func DecodeJSONExactWithLimits(data []byte, limits DecodeLimits) (BytePath, error) {
	if err := limits.Validate(); err != nil {
		return BytePath{}, fmt.Errorf("exact JSON decode limits: %w", err)
	}
	if len(data) > limits.MaxInputBytes {
		return BytePath{}, fmt.Errorf("exact JSON exceeds input-byte limit of %d", limits.MaxInputBytes)
	}
	if !utf8.Valid(data) {
		return BytePath{}, fmt.Errorf("exact JSON is not valid UTF-8")
	}

	parser := exactJSONParser{data: data, limits: limits}
	parser.skipSpace()
	components, err := parser.parsePath()
	if err != nil {
		return BytePath{}, err
	}
	parser.skipSpace()
	if parser.position != len(parser.data) {
		return BytePath{}, parser.errorf("trailing data")
	}
	return New(components)
}

type exactJSONParser struct {
	data         []byte
	position     int
	limits       DecodeLimits
	decodedBytes int
}

const maxExactJSONFieldNameBytes = 16

func (parser *exactJSONParser) parsePath() ([][]byte, error) {
	if err := parser.expect('{'); err != nil {
		return nil, err
	}

	var (
		displaySeen    bool
		componentsSeen bool
		components     [][]byte
	)
	parser.skipSpace()
	if parser.consume('}') {
		return nil, parser.errorf("missing display and components")
	}
	for {
		key, err := parser.parseString(maxExactJSONFieldNameBytes)
		if err != nil {
			return nil, err
		}
		if err := parser.expect(':'); err != nil {
			return nil, err
		}

		switch key {
		case "display":
			if displaySeen {
				return nil, parser.errorf("duplicate display")
			}
			if _, err := parser.parseString(parser.limits.MaxInputBytes); err != nil {
				return nil, parser.errorf("display must be a string: %v", err)
			}
			displaySeen = true
		case "components":
			if componentsSeen {
				return nil, parser.errorf("duplicate components")
			}
			components, err = parser.parseComponents()
			if err != nil {
				return nil, err
			}
			componentsSeen = true
		default:
			return nil, parser.errorf("unknown path field %q", key)
		}

		parser.skipSpace()
		if parser.consume('}') {
			break
		}
		if err := parser.expect(','); err != nil {
			return nil, err
		}
	}
	if !displaySeen || !componentsSeen {
		return nil, parser.errorf("exact JSON requires display and components")
	}
	return components, nil
}

func (parser *exactJSONParser) parseComponents() ([][]byte, error) {
	if err := parser.expect('['); err != nil {
		return nil, parser.errorf("components must be an array: %v", err)
	}

	components := make([][]byte, 0)
	parser.skipSpace()
	if parser.consume(']') {
		return components, nil
	}
	for {
		if len(components) >= parser.limits.MaxComponents {
			return nil, parser.errorf("components exceed decode limit of %d", parser.limits.MaxComponents)
		}

		remainingDecodedBytes := parser.limits.MaxDecodedBytes - parser.decodedBytes
		component, err := parser.parseComponent(remainingDecodedBytes)
		if err != nil {
			return nil, err
		}
		if len(component) > remainingDecodedBytes {
			return nil, parser.errorf("components exceed decoded-byte limit of %d", parser.limits.MaxDecodedBytes)
		}
		parser.decodedBytes += len(component)
		components = append(components, component)

		parser.skipSpace()
		if parser.consume(']') {
			break
		}
		if err := parser.expect(','); err != nil {
			return nil, err
		}
	}
	return components, nil
}

func (parser *exactJSONParser) parseComponent(maxDecodedBytes int) ([]byte, error) {
	if err := parser.expect('{'); err != nil {
		return nil, parser.errorf("component must be an object: %v", err)
	}

	var (
		formSeen bool
		form     string
		value    string
	)
	parser.skipSpace()
	if parser.consume('}') {
		return nil, parser.errorf("component has no exact value")
	}
	for {
		key, err := parser.parseString(maxExactJSONFieldNameBytes)
		if err != nil {
			return nil, err
		}
		if err := parser.expect(':'); err != nil {
			return nil, err
		}
		if key != "utf8" && key != "base64" {
			return nil, parser.errorf("unknown component field %q", key)
		}
		if formSeen {
			return nil, parser.errorf("component has multiple exact values")
		}

		valueLimit := maxDecodedBytes
		if key == "base64" {
			valueLimit = maxCanonicalBase64Bytes(maxDecodedBytes)
		}
		parsedValue, err := parser.parseString(valueLimit)
		if err != nil {
			return nil, parser.errorf("component value must be a string: %v", err)
		}

		formSeen = true
		form = key
		value = parsedValue

		parser.skipSpace()
		if parser.consume('}') {
			break
		}
		if err := parser.expect(','); err != nil {
			return nil, err
		}
	}
	if !formSeen {
		return nil, parser.errorf("component has no exact value")
	}

	if form == "utf8" {
		return []byte(value), nil
	}
	decodedLength := canonicalBase64DecodedLength(value)
	if decodedLength > maxDecodedBytes {
		return nil, parser.errorf("component exceeds decoded-byte limit of %d", parser.limits.MaxDecodedBytes)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) > maxDecodedBytes || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, parser.errorf("component base64 is not canonical")
	}
	return decoded, nil
}

func (parser *exactJSONParser) parseString(maxBytes int) (string, error) {
	parser.skipSpace()
	if parser.position >= len(parser.data) || parser.data[parser.position] != '"' {
		return "", parser.errorf("expected JSON string")
	}
	parser.position++

	value := make([]byte, 0)
	for parser.position < len(parser.data) {
		current := parser.data[parser.position]
		parser.position++
		switch current {
		case '"':
			return string(value), nil
		case '\\':
			if parser.position >= len(parser.data) {
				return "", parser.errorf("unfinished string escape")
			}
			escape := parser.data[parser.position]
			parser.position++
			switch escape {
			case '"', '\\', '/':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, escape)
			case 'b':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, '\b')
			case 'f':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, '\f')
			case 'n':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, '\n')
			case 'r':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, '\r')
			case 't':
				if len(value) >= maxBytes {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, '\t')
			case 'u':
				runeValue, err := parser.parseEscapedRune()
				if err != nil {
					return "", err
				}
				var encoded [utf8.UTFMax]byte
				size := utf8.EncodeRune(encoded[:], runeValue)
				if size > maxBytes-len(value) {
					return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
				}
				value = append(value, encoded[:size]...)
			default:
				return "", parser.errorf("invalid string escape")
			}
		default:
			if current < 0x20 {
				return "", parser.errorf("unescaped control byte in string")
			}
			if len(value) >= maxBytes {
				return "", parser.errorf("JSON string exceeds decode limit of %d bytes", maxBytes)
			}
			value = append(value, current)
		}
	}
	return "", parser.errorf("unterminated JSON string")
}

func maxCanonicalBase64Bytes(maxDecodedBytes int) int {
	groups := maxDecodedBytes / 3
	if maxDecodedBytes%3 != 0 {
		groups++
	}
	maxInt := int(^uint(0) >> 1)
	if groups > maxInt/4 {
		return maxInt
	}
	return groups * 4
}

func canonicalBase64DecodedLength(value string) int {
	if len(value)%4 != 0 {
		return base64.StdEncoding.DecodedLen(len(value))
	}

	padding := 0
	if len(value) > 0 && value[len(value)-1] == '=' {
		padding++
	}
	if len(value) > 1 && value[len(value)-2] == '=' {
		padding++
	}
	return len(value)/4*3 - padding
}

func (parser *exactJSONParser) parseEscapedRune() (rune, error) {
	first, err := parser.parseHexRune()
	if err != nil {
		return 0, err
	}
	if first >= 0xd800 && first <= 0xdbff {
		if parser.position+2 > len(parser.data) || parser.data[parser.position] != '\\' || parser.data[parser.position+1] != 'u' {
			return 0, parser.errorf("unpaired high surrogate")
		}
		parser.position += 2
		second, err := parser.parseHexRune()
		if err != nil {
			return 0, err
		}
		if second < 0xdc00 || second > 0xdfff {
			return 0, parser.errorf("invalid low surrogate")
		}
		return 0x10000 + ((first - 0xd800) << 10) + (second - 0xdc00), nil
	}
	if first >= 0xdc00 && first <= 0xdfff {
		return 0, parser.errorf("unpaired low surrogate")
	}
	return first, nil
}

func (parser *exactJSONParser) parseHexRune() (rune, error) {
	if parser.position+4 > len(parser.data) {
		return 0, parser.errorf("incomplete unicode escape")
	}
	var value rune
	for offset := 0; offset < 4; offset++ {
		digit, ok := hexDigit(parser.data[parser.position+offset])
		if !ok {
			return 0, parser.errorf("invalid unicode escape")
		}
		value = value<<4 | rune(digit)
	}
	parser.position += 4
	return value, nil
}

func (parser *exactJSONParser) expect(expected byte) error {
	parser.skipSpace()
	if parser.position >= len(parser.data) || parser.data[parser.position] != expected {
		return parser.errorf("expected %q", expected)
	}
	parser.position++
	return nil
}

func (parser *exactJSONParser) consume(expected byte) bool {
	parser.skipSpace()
	if parser.position >= len(parser.data) || parser.data[parser.position] != expected {
		return false
	}
	parser.position++
	return true
}

func (parser *exactJSONParser) skipSpace() {
	for parser.position < len(parser.data) {
		switch parser.data[parser.position] {
		case ' ', '\n', '\r', '\t':
			parser.position++
		default:
			return
		}
	}
}

func (parser *exactJSONParser) errorf(format string, args ...any) error {
	return fmt.Errorf("invalid exact JSON at byte %d: %s", parser.position, fmt.Sprintf(format, args...))
}

func hexDigit(value byte) (byte, bool) {
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
