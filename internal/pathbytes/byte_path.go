// Package pathbytes models relative filesystem paths as validated raw-byte components.
//
// A BytePath deliberately has no parser for Display. Display is presentation-only;
// callers must use an exact codec or independently trusted raw components to create
// a BytePath.
package pathbytes

import (
	"bytes"
	"fmt"
	"strings"
)

// BytePath is an immutable-by-construction ordered collection of raw filesystem
// component bytes. Construct it with New so the component safety invariants hold.
type BytePath struct {
	components [][]byte
}

// DecodeLimits bounds one untrusted exact-path decoding operation. MaxInputBytes
// applies to the encoded input, MaxComponents to the resulting raw path, and
// MaxDecodedBytes to the total raw component bytes. All budgets must be positive.
//
// These are decoder budgets rather than filesystem limits: component-name limits
// remain a point-of-use filesystem concern.
type DecodeLimits struct {
	MaxInputBytes   int
	MaxComponents   int
	MaxDecodedBytes int
}

const (
	phaseTwoMaxJSONScalarBytes = 1 << 20
	phaseTwoMaxPathComponents  = 1024
	phaseTwoMaxPathBytes       = 256 << 10
)

// DefaultJSONDecodeLimits returns the Phase 2 bounded profile for an exact JSON
// path envelope: one 1 MiB JSON scalar envelope, at most 1,024 components, and
// at most 256 KiB of authoritative decoded path bytes.
func DefaultJSONDecodeLimits() DecodeLimits {
	return DecodeLimits{
		MaxInputBytes:   phaseTwoMaxJSONScalarBytes,
		MaxComponents:   phaseTwoMaxPathComponents,
		MaxDecodedBytes: phaseTwoMaxPathBytes,
	}
}

// DefaultTrashDecodeLimits returns the Phase 2 bounded profile for a Trash
// percent-encoded path: at most 256 KiB of encoded input, 1,024 components, and
// 256 KiB of authoritative decoded path bytes.
func DefaultTrashDecodeLimits() DecodeLimits {
	return DecodeLimits{
		MaxInputBytes:   phaseTwoMaxPathBytes,
		MaxComponents:   phaseTwoMaxPathComponents,
		MaxDecodedBytes: phaseTwoMaxPathBytes,
	}
}

// Validate rejects an incomplete or nonsensical decoder budget before a decoder
// begins materializing untrusted components.
func (limits DecodeLimits) Validate() error {
	if limits.MaxInputBytes <= 0 {
		return fmt.Errorf("decode input-byte limit must be positive")
	}
	if limits.MaxComponents <= 0 {
		return fmt.Errorf("decode component limit must be positive")
	}
	if limits.MaxDecodedBytes <= 0 {
		return fmt.Errorf("decode decoded-byte limit must be positive")
	}
	return nil
}

// New validates components and copies every slice before constructing a BytePath.
func New(components [][]byte) (BytePath, error) {
	if err := validateComponents(components); err != nil {
		return BytePath{}, err
	}
	return BytePath{components: cloneComponents(components)}, nil
}

// Components returns a deep copy of the path's raw components.
func (path BytePath) Components() [][]byte {
	return cloneComponents(path.components)
}

// Equal reports whether two paths have the same number of components and exact raw
// bytes in each position.
func (path BytePath) Equal(other BytePath) bool {
	if len(path.components) != len(other.components) {
		return false
	}
	for index := range path.components {
		if !bytes.Equal(path.components[index], other.components[index]) {
			return false
		}
	}
	return true
}

// Display returns a deterministic escaped presentation form. It is intentionally
// one-way: this package never converts Display output back into path authority.
func (path BytePath) Display() string {
	if len(path.components) == 0 {
		return ""
	}

	var display strings.Builder
	for componentIndex, component := range path.components {
		if componentIndex > 0 {
			display.WriteByte('/')
		}
		for _, value := range component {
			if displayPlainByte(value) {
				display.WriteByte(value)
				continue
			}
			display.WriteByte('\\')
			display.WriteByte('x')
			display.WriteByte(upperHex(value >> 4))
			display.WriteByte(upperHex(value & 0x0f))
		}
	}
	return display.String()
}

func (path BytePath) validate() error {
	return validateComponents(path.components)
}

func validateComponents(components [][]byte) error {
	if len(components) == 0 {
		return fmt.Errorf("byte path must contain at least one component")
	}
	for componentIndex, component := range components {
		if len(component) == 0 {
			return fmt.Errorf("byte path component %d is empty", componentIndex)
		}
		if bytes.Equal(component, []byte(".")) || bytes.Equal(component, []byte("..")) {
			return fmt.Errorf("byte path component %d is not a file name", componentIndex)
		}
		for _, value := range component {
			switch value {
			case 0:
				return fmt.Errorf("byte path component %d contains NUL", componentIndex)
			case '/':
				return fmt.Errorf("byte path component %d contains slash", componentIndex)
			}
		}
	}
	return nil
}

func cloneComponents(components [][]byte) [][]byte {
	if components == nil {
		return nil
	}
	clone := make([][]byte, len(components))
	for index, component := range components {
		clone[index] = append([]byte(nil), component...)
	}
	return clone
}

func displayPlainByte(value byte) bool {
	return value >= 0x21 && value <= 0x7e && value != '\\'
}

func upperHex(value byte) byte {
	if value < 10 {
		return '0' + value
	}
	return 'A' + (value - 10)
}
