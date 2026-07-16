package pathbytes

import "fmt"

// PercentEncodeTrashPath encodes raw components in a canonical Trash Path-style
// percent form. Only RFC 3986 unreserved ASCII bytes are literal; slash separates
// components and cannot occur within one.
func PercentEncodeTrashPath(path BytePath) string {
	if path.validate() != nil {
		return ""
	}

	encoded := make([]byte, 0)
	for componentIndex, component := range path.components {
		if componentIndex > 0 {
			encoded = append(encoded, '/')
		}
		for _, value := range component {
			if trashUnreserved(value) {
				encoded = append(encoded, value)
				continue
			}
			encoded = append(encoded, '%', upperHex(value>>4), upperHex(value&0x0f))
		}
	}
	return string(encoded)
}

// PercentDecodeTrashPath accepts only the canonical percent form with the bounded
// Phase 2 default profile. Call PercentDecodeTrashPathWithLimits when a narrower
// caller-specific budget is required.
func PercentDecodeTrashPath(encoded string) (BytePath, error) {
	return PercentDecodeTrashPathWithLimits(encoded, DefaultTrashDecodeLimits())
}

// PercentDecodeTrashPathWithLimits accepts only canonical percent form while
// enforcing limits before materializing raw components. It validates the resulting
// raw components before constructing a BytePath.
func PercentDecodeTrashPathWithLimits(encoded string, limits DecodeLimits) (BytePath, error) {
	if err := limits.Validate(); err != nil {
		return BytePath{}, fmt.Errorf("trash path decode limits: %w", err)
	}
	if len(encoded) == 0 {
		return BytePath{}, fmt.Errorf("trash path is empty")
	}
	if len(encoded) > limits.MaxInputBytes {
		return BytePath{}, fmt.Errorf("trash path exceeds input-byte limit of %d", limits.MaxInputBytes)
	}

	componentCount := 1
	for index := 0; index < len(encoded); index++ {
		if encoded[index] != '/' {
			continue
		}
		if componentCount >= limits.MaxComponents {
			return BytePath{}, fmt.Errorf("trash path exceeds component limit of %d", limits.MaxComponents)
		}
		componentCount++
	}

	components := make([][]byte, 0, componentCount)
	component := make([]byte, 0)
	decodedBytes := 0
	for index := 0; index < len(encoded); {
		value := encoded[index]
		switch {
		case value == '/':
			if len(component) == 0 {
				return BytePath{}, fmt.Errorf("trash path contains an empty component")
			}
			components = append(components, component)
			component = make([]byte, 0)
			index++
		case trashUnreserved(value):
			if decodedBytes >= limits.MaxDecodedBytes {
				return BytePath{}, fmt.Errorf("trash path exceeds decoded-byte limit of %d", limits.MaxDecodedBytes)
			}
			component = append(component, value)
			decodedBytes++
			index++
		case value == '%':
			if index+2 >= len(encoded) {
				return BytePath{}, fmt.Errorf("trash path has incomplete percent escape")
			}
			high, highOK := canonicalHexDigit(encoded[index+1])
			low, lowOK := canonicalHexDigit(encoded[index+2])
			if !highOK || !lowOK {
				return BytePath{}, fmt.Errorf("trash path has non-canonical percent escape")
			}
			decoded := high<<4 | low
			if trashUnreserved(decoded) {
				return BytePath{}, fmt.Errorf("trash path percent-escapes an unreserved byte")
			}
			if decodedBytes >= limits.MaxDecodedBytes {
				return BytePath{}, fmt.Errorf("trash path exceeds decoded-byte limit of %d", limits.MaxDecodedBytes)
			}
			component = append(component, decoded)
			decodedBytes++
			index += 3
		default:
			return BytePath{}, fmt.Errorf("trash path contains a non-canonical raw byte")
		}
	}
	if len(component) == 0 {
		return BytePath{}, fmt.Errorf("trash path contains an empty component")
	}
	components = append(components, component)
	return New(components)
}

func trashUnreserved(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '-' || value == '.' || value == '_' || value == '~'
}

func canonicalHexDigit(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
