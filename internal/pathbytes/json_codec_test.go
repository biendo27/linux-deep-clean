package pathbytes

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONExactPreservesInvalidUTF8AndUsesExactComponentAuthority(t *testing.T) {
	t.Parallel()

	want := mustPath(t, [][]byte{[]byte("safe"), {0xff, 0xfe, 'x'}})
	encoded, err := want.EncodeJSONExact()
	if err != nil {
		t.Fatalf("EncodeJSONExact() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"base64":"//54"`)) {
		t.Fatalf("invalid UTF-8 was not represented as base64: %s", encoded)
	}

	got, err := DecodeJSONExact(encoded)
	if err != nil {
		t.Fatalf("DecodeJSONExact() error = %v", err)
	}
	if !want.Equal(got) {
		t.Fatalf("invalid UTF-8 changed after JSON round trip: got %x, want %x", got.Components(), want.Components())
	}

	// The display field is required presentation data, but never path authority.
	got, err = DecodeJSONExact([]byte(`{"display":"../../misleading\\u202e","components":[{"utf8":"safe"},{"base64":"//54"}]}`))
	if err != nil {
		t.Fatalf("DecodeJSONExact() rejected independent display: %v", err)
	}
	if !want.Equal(got) {
		t.Fatalf("DecodeJSONExact trusted display over exact components: got %x, want %x", got.Components(), want.Components())
	}

	if _, err := DecodeJSONExact([]byte(`{"display":"safe/valid","components":[{"base64":"AP8="}]}`)); err == nil {
		t.Fatal("DecodeJSONExact accepted an unsafe exact component because display looked safe")
	}
}

func TestJSONExactRejectsMalformedOrAmbiguousForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{name: "empty", json: ``},
		{name: "non object", json: `[]`},
		{name: "missing display", json: `{"components":[{"utf8":"a"}]}`},
		{name: "missing components", json: `{"display":"a"}`},
		{name: "unknown root field", json: `{"display":"a","components":[{"utf8":"a"}],"extra":true}`},
		{name: "duplicate root field", json: `{"display":"a","display":"b","components":[{"utf8":"a"}]}`},
		{name: "display wrong type", json: `{"display":1,"components":[{"utf8":"a"}]}`},
		{name: "components wrong type", json: `{"display":"a","components":{}}`},
		{name: "component non object", json: `{"display":"a","components":["a"]}`},
		{name: "component unknown field", json: `{"display":"a","components":[{"other":"a"}]}`},
		{name: "component duplicate field", json: `{"display":"a","components":[{"utf8":"a","utf8":"b"}]}`},
		{name: "both exact forms", json: `{"display":"a","components":[{"utf8":"a","base64":"YQ=="}]}`},
		{name: "utf8 wrong type", json: `{"display":"a","components":[{"utf8":1}]}`},
		{name: "base64 wrong type", json: `{"display":"a","components":[{"base64":1}]}`},
		{name: "invalid base64", json: `{"display":"a","components":[{"base64":"%%%"}]}`},
		{name: "non canonical base64", json: `{"display":"a","components":[{"base64":"YQ"}]}`},
		{name: "empty component", json: `{"display":"a","components":[{"utf8":""}]}`},
		{name: "unsafe slash", json: `{"display":"a","components":[{"utf8":"a/b"}]}`},
		{name: "unsafe dot", json: `{"display":"a","components":[{"utf8":"."}]}`},
		{name: "unsafe dotdot", json: `{"display":"a","components":[{"utf8":".."}]}`},
		{name: "unsafe nul", json: `{"display":"a","components":[{"utf8":"a\u0000b"}]}`},
		{name: "empty path", json: `{"display":"","components":[]}`},
		{name: "trailing data", json: `{"display":"a","components":[{"utf8":"a"}]} null`},
		{name: "unpaired high surrogate", json: `{"display":"a","components":[{"utf8":"\uD800"}]}`},
		{name: "unpaired low surrogate", json: `{"display":"a","components":[{"utf8":"\uDC00"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeJSONExact([]byte(test.json)); err == nil {
				t.Fatalf("DecodeJSONExact accepted malformed or ambiguous JSON: %s", test.json)
			}
		})
	}

	invalidUTF8Input := append([]byte(`{"display":"a","components":[{"utf8":"`), 0xff)
	invalidUTF8Input = append(invalidUTF8Input, []byte(`"}]}`)...)
	if _, err := DecodeJSONExact(invalidUTF8Input); err == nil {
		t.Fatal("DecodeJSONExact accepted invalid UTF-8 JSON source bytes")
	}
}

func TestJSONExactCanonicalDisplayAndComponentEncoding(t *testing.T) {
	t.Parallel()

	path := mustPath(t, [][]byte{[]byte("plain"), []byte("caf\xc3\xa9"), {0xff}})
	encoded, err := path.EncodeJSONExact()
	if err != nil {
		t.Fatalf("EncodeJSONExact() error = %v", err)
	}
	if strings.Count(string(encoded), `"display"`) != 1 || strings.Count(string(encoded), `"components"`) != 1 {
		t.Fatalf("EncodeJSONExact() did not include one display and component collection: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(`{"utf8":"plain"}`)) || !bytes.Contains(encoded, []byte(`{"utf8":"café"}`)) || !bytes.Contains(encoded, []byte(`{"base64":"/w=="}`)) {
		t.Fatalf("EncodeJSONExact() used an unexpected exact representation: %s", encoded)
	}
}

func TestJSONExactAcceptsTaggedBase64ForValidUTF8(t *testing.T) {
	t.Parallel()

	got, err := DecodeJSONExact([]byte(`{"display":"not-authority","components":[{"base64":"YQ=="}]}`))
	if err != nil {
		t.Fatalf("DecodeJSONExact() error = %v", err)
	}
	want := mustPath(t, [][]byte{[]byte("a")})
	if !want.Equal(got) {
		t.Fatalf("DecodeJSONExact() = %x, want %x", got.Components(), want.Components())
	}

	encoded, err := got.EncodeJSONExact()
	if err != nil {
		t.Fatalf("EncodeJSONExact() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`{"utf8":"a"}`)) {
		t.Fatalf("EncodeJSONExact() did not use the UTF-8 representation: %s", encoded)
	}
}

func TestJSONExactDecodesStrictEscapesWithoutUsingDisplay(t *testing.T) {
	t.Parallel()

	encoded := []byte(" \n{\n" +
		`"components":[{"utf8":"\"\\\b\f\n\r\t\u0061\uD83D\uDE00"},{"base64":"/w=="}],` +
		`"display":"ignored\/display\u0000"` +
		"\n}\t")
	got, err := DecodeJSONExact(encoded)
	if err != nil {
		t.Fatalf("DecodeJSONExact() error = %v", err)
	}
	want := mustPath(t, [][]byte{{'"', '\\', '\b', '\f', '\n', '\r', '\t', 'a', 0xf0, 0x9f, 0x98, 0x80}, {0xff}})
	if !want.Equal(got) {
		t.Fatalf("DecodeJSONExact() = %x, want %x", got.Components(), want.Components())
	}
}

func TestJSONExactRejectsMalformedStringEscapes(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"display":"\q","components":[{"utf8":"a"}]}`,
		`{"display":"\u00G0","components":[{"utf8":"a"}]}`,
		`{"display":"\u00","components":[{"utf8":"a"}]}`,
		`{"display":"\uD800\u0061","components":[{"utf8":"a"}]}`,
		"{\"display\":\"line\nfeed\",\"components\":[{\"utf8\":\"a\"}]}",
		`{"display":"unterminated,"components":[{"utf8":"a"}]}`,
	}
	for _, encoded := range tests {
		if _, err := DecodeJSONExact([]byte(encoded)); err == nil {
			t.Fatalf("DecodeJSONExact accepted malformed escape: %q", encoded)
		}
	}
}
