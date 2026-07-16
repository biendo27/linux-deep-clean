package pathbytes

import (
	"strings"
	"testing"
)

func TestTrashPercentCodecRoundTripsExactlyAndIsCanonical(t *testing.T) {
	t.Parallel()

	want := mustPath(t, [][]byte{[]byte("a b"), {0xff, '%', '~'}, []byte("caf\xc3\xa9")})
	encoded := PercentEncodeTrashPath(want)
	if got, expected := encoded, "a%20b/%FF%25~/caf%C3%A9"; got != expected {
		t.Fatalf("PercentEncodeTrashPath() = %q, want %q", got, expected)
	}
	got, err := PercentDecodeTrashPath(encoded)
	if err != nil {
		t.Fatalf("PercentDecodeTrashPath() error = %v", err)
	}
	if !want.Equal(got) {
		t.Fatalf("trash percent round trip changed bytes: got %x, want %x", got.Components(), want.Components())
	}
}

func TestTrashPercentCodecRejectsMalformedNonCanonicalAndUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"/a",
		"a/",
		"a//b",
		"%",
		"%0",
		"%GG",
		"%2f",
		"a b",
		"%41",
		"a%2Fb",
		"%00",
		".",
		"..",
		"%2E",
		"%2E%2E",
	}

	for _, encoded := range tests {
		t.Run(strings.ReplaceAll(encoded, "/", "slash"), func(t *testing.T) {
			t.Parallel()
			if _, err := PercentDecodeTrashPath(encoded); err == nil {
				t.Fatalf("PercentDecodeTrashPath accepted %q", encoded)
			}
		})
	}

	if _, err := PercentDecodeTrashPath(string([]byte{'a', 0xff})); err == nil {
		t.Fatal("PercentDecodeTrashPath accepted raw non-ASCII byte")
	}
}
