package pathbytes

import (
	"bytes"
	"testing"
)

func TestBytePathRejectsUnsafeComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		components [][]byte
	}{
		{name: "nil path", components: nil},
		{name: "empty path", components: [][]byte{}},
		{name: "empty component", components: [][]byte{[]byte("ok"), {}}},
		{name: "nul component", components: [][]byte{[]byte("a\x00b")}},
		{name: "slash component", components: [][]byte{[]byte("a/b")}},
		{name: "current directory", components: [][]byte{[]byte(".")}},
		{name: "parent directory", components: [][]byte{[]byte("..")}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.components); err == nil {
				t.Fatal("New accepted unsafe path components")
			}
		})
	}
}

func TestBytePathAllAllowedRawBytesRoundTrip(t *testing.T) {
	t.Parallel()

	component := make([]byte, 0, 254)
	for value := 1; value <= 255; value++ {
		if byte(value) == '/' {
			continue
		}
		component = append(component, byte(value))
	}

	path, err := New([][]byte{component, []byte("suffix")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	jsonBytes, err := path.EncodeJSONExact()
	if err != nil {
		t.Fatalf("EncodeJSONExact() error = %v", err)
	}
	fromJSON, err := DecodeJSONExact(jsonBytes)
	if err != nil {
		t.Fatalf("DecodeJSONExact() error = %v", err)
	}
	if !path.Equal(fromJSON) {
		t.Fatalf("JSON round trip changed raw bytes: got %x, want %x", fromJSON.Components(), path.Components())
	}

	encodedTrash := PercentEncodeTrashPath(path)
	fromTrash, err := PercentDecodeTrashPath(encodedTrash)
	if err != nil {
		t.Fatalf("PercentDecodeTrashPath() error = %v", err)
	}
	if !path.Equal(fromTrash) {
		t.Fatalf("Trash round trip changed raw bytes: got %x, want %x", fromTrash.Components(), path.Components())
	}
}

func TestBytePathDefensivelyCopiesInputAndOutput(t *testing.T) {
	t.Parallel()

	input := [][]byte{[]byte("alpha"), []byte("beta")}
	path, err := New(input)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	input[0][0] = 'X'
	input[1] = []byte("replaced")
	if got, want := path.Components(), [][]byte{[]byte("alpha"), []byte("beta")}; !componentsEqual(got, want) {
		t.Fatalf("New retained caller storage: got %q, want %q", got, want)
	}

	output := path.Components()
	output[0][0] = 'X'
	output[1] = []byte("replaced")
	if got, want := path.Components(), [][]byte{[]byte("alpha"), []byte("beta")}; !componentsEqual(got, want) {
		t.Fatalf("Components exposed internal storage: got %q, want %q", got, want)
	}
}

func TestBytePathEqualAndDisplayAreRawAndOneWay(t *testing.T) {
	t.Parallel()

	left := mustPath(t, [][]byte{[]byte("plain"), {0xff, 0x1b, 0xe2, 0x80, 0xae, 'x'}})
	right := mustPath(t, [][]byte{[]byte("plain"), {0xff, 0x1b, 0xe2, 0x80, 0xae, 'x'}})
	different := mustPath(t, [][]byte{[]byte("plain"), {0xfe, 0x1b, 0xe2, 0x80, 0xae, 'x'}})

	if !left.Equal(right) {
		t.Fatal("Equal reported equal byte paths as different")
	}
	if left.Equal(different) {
		t.Fatal("Equal reported different raw byte paths as equal")
	}
	if got := left.Display(); got == "plain/\xff\x1bx" || bytes.ContainsRune([]byte(got), '\ufffd') || bytes.Contains([]byte(got), []byte("\u202e")) {
		t.Fatalf("Display did not safely escape raw bytes: %q", got)
	}
}

func mustPath(t *testing.T, components [][]byte) BytePath {
	t.Helper()
	path, err := New(components)
	if err != nil {
		t.Fatalf("New(%x) error = %v", components, err)
	}
	return path
}

func componentsEqual(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index], right[index]) {
			return false
		}
	}
	return true
}
