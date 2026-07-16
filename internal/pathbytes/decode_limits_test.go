package pathbytes

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecodeLimitsRequirePositiveBudgets(t *testing.T) {
	t.Parallel()

	for _, limits := range []DecodeLimits{
		{},
		{MaxInputBytes: 1, MaxComponents: 1},
		{MaxInputBytes: 1, MaxDecodedBytes: 1},
		{MaxComponents: 1, MaxDecodedBytes: 1},
	} {
		if err := limits.Validate(); err == nil {
			t.Fatalf("DecodeLimits.Validate() accepted %+v", limits)
		}
	}
}

func TestDecodeJSONExactWithLimitsRejectsOversizedInputsBeforeAuthorityConstruction(t *testing.T) {
	t.Parallel()

	limits := DecodeLimits{
		MaxInputBytes:   96,
		MaxComponents:   2,
		MaxDecodedBytes: 3,
	}
	encodedBase64 := base64.StdEncoding.EncodeToString([]byte("abcd"))
	tests := []struct {
		name string
		data []byte
	}{
		{name: "wire input", data: []byte(strings.Repeat(" ", limits.MaxInputBytes+1))},
		{name: "component count", data: []byte(`{"display":"","components":[{"utf8":"a"},{"utf8":"b"},{"utf8":"c"}]}`)},
		{name: "utf8 decoded bytes", data: []byte(`{"display":"","components":[{"utf8":"abcd"}]}`)},
		{name: "base64 decoded bytes", data: []byte(`{"display":"","components":[{"base64":"` + encodedBase64 + `"}]}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeJSONExactWithLimits(test.data, limits); err == nil {
				t.Fatalf("DecodeJSONExactWithLimits accepted %s over budget", test.name)
			}
		})
	}

	got, err := DecodeJSONExactWithLimits([]byte(`{"display":"ignored","components":[{"utf8":"a"},{"utf8":"bc"}]}`), limits)
	if err != nil {
		t.Fatalf("DecodeJSONExactWithLimits() error = %v", err)
	}
	if want := mustPath(t, [][]byte{[]byte("a"), []byte("bc")}); !want.Equal(got) {
		t.Fatalf("DecodeJSONExactWithLimits() = %x, want %x", got.Components(), want.Components())
	}
}

func TestDecodeJSONExactWithLimitsCountsEscapesAndBase64ByDecodedBytes(t *testing.T) {
	t.Parallel()

	limits := DecodeLimits{
		MaxInputBytes:   96,
		MaxComponents:   1,
		MaxDecodedBytes: 1,
	}
	got, err := DecodeJSONExactWithLimits([]byte(`{"display":"","components":[{"base64":"YQ=="}]}`), limits)
	if err != nil {
		t.Fatalf("DecodeJSONExactWithLimits() rejected one decoded base64 byte: %v", err)
	}
	if want := mustPath(t, [][]byte{[]byte("a")}); !want.Equal(got) {
		t.Fatalf("DecodeJSONExactWithLimits() = %x, want %x", got.Components(), want.Components())
	}

	if _, err := DecodeJSONExactWithLimits([]byte(`{"display":"","components":[{"utf8":"\u0061\u0062"}]}`), limits); err == nil {
		t.Fatal("DecodeJSONExactWithLimits accepted escaped UTF-8 bytes over the decoded-byte budget")
	}
}

func TestPercentDecodeTrashPathWithLimitsRejectsOversizedInputsBeforeAuthorityConstruction(t *testing.T) {
	t.Parallel()

	limits := DecodeLimits{
		MaxInputBytes:   7,
		MaxComponents:   2,
		MaxDecodedBytes: 3,
	}
	tests := []struct {
		name    string
		encoded string
	}{
		{name: "wire input", encoded: "abcdefgh"},
		{name: "component count", encoded: "a/b/c"},
		{name: "decoded bytes", encoded: "a%20bc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := PercentDecodeTrashPathWithLimits(test.encoded, limits); err == nil {
				t.Fatalf("PercentDecodeTrashPathWithLimits accepted %s over budget", test.name)
			}
		})
	}

	got, err := PercentDecodeTrashPathWithLimits("a/bc", limits)
	if err != nil {
		t.Fatalf("PercentDecodeTrashPathWithLimits() error = %v", err)
	}
	if want := mustPath(t, [][]byte{[]byte("a"), []byte("bc")}); !want.Equal(got) {
		t.Fatalf("PercentDecodeTrashPathWithLimits() = %x, want %x", got.Components(), want.Components())
	}
}

func TestDefaultDecoderLimitsBoundPhaseTwoPathBudget(t *testing.T) {
	t.Parallel()

	jsonLimits := DefaultJSONDecodeLimits()
	trashLimits := DefaultTrashDecodeLimits()
	if jsonLimits.MaxInputBytes != 1<<20 || jsonLimits.MaxComponents != 1024 || jsonLimits.MaxDecodedBytes != 256<<10 {
		t.Fatalf("DefaultJSONDecodeLimits() = %+v, want Phase 2 JSON/path budgets", jsonLimits)
	}
	if trashLimits.MaxInputBytes != 256<<10 || trashLimits.MaxComponents != 1024 || trashLimits.MaxDecodedBytes != 256<<10 {
		t.Fatalf("DefaultTrashDecodeLimits() = %+v, want Phase 2 path budgets", trashLimits)
	}
	if _, err := DecodeJSONExact([]byte(strings.Repeat(" ", jsonLimits.MaxInputBytes+1))); err == nil {
		t.Fatal("DecodeJSONExact accepted more than the default input-byte budget")
	}
	if _, err := PercentDecodeTrashPath(strings.Repeat("a", trashLimits.MaxInputBytes+1)); err == nil {
		t.Fatal("PercentDecodeTrashPath accepted more than the default input-byte budget")
	}

	tooManyJSONComponents := `{"display":"","components":[` + strings.Repeat(`{"utf8":"a"},`, jsonLimits.MaxComponents) + `{"utf8":"a"}]}`
	if _, err := DecodeJSONExact([]byte(tooManyJSONComponents)); err == nil {
		t.Fatal("DecodeJSONExact accepted more than the default component budget")
	}

	tooManyTrashComponents := strings.Repeat("a/", trashLimits.MaxComponents) + "a"
	if _, err := PercentDecodeTrashPath(tooManyTrashComponents); err == nil {
		t.Fatal("PercentDecodeTrashPath accepted more than the default component budget")
	}

	tooManyDecodedBytes := `{"display":"","components":[{"utf8":"` + strings.Repeat("a", jsonLimits.MaxDecodedBytes+1) + `"}]}`
	if _, err := DecodeJSONExact([]byte(tooManyDecodedBytes)); err == nil {
		t.Fatal("DecodeJSONExact accepted more than the default decoded-byte budget")
	}
}
