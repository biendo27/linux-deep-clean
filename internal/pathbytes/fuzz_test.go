package pathbytes

import (
	"strings"
	"testing"
)

func FuzzBytePathCodecs(f *testing.F) {
	f.Add([]byte("plain"))
	f.Add([]byte{0xff, 0xfe, '%', ' '})
	f.Add([]byte{'.'})
	f.Add([]byte{0x00, '/'})
	jsonLimits := DefaultJSONDecodeLimits()
	trashLimits := DefaultTrashDecodeLimits()
	f.Add([]byte(`{"display":"","components":[` + strings.Repeat(`{"utf8":"a"},`, jsonLimits.MaxComponents) + `{"utf8":"a"}]}`))
	f.Add([]byte(`{"display":"","components":[{"utf8":"` + strings.Repeat("a", jsonLimits.MaxDecodedBytes+1) + `"}]}`))
	f.Add([]byte(strings.Repeat("a/", trashLimits.MaxComponents) + "a"))

	f.Fuzz(func(t *testing.T, input []byte) {
		component := make([]byte, len(input))
		copy(component, input)
		if len(component) == 0 {
			component = []byte("x")
		}
		for index, value := range component {
			if value == 0 || value == '/' {
				component[index] = 'x'
			}
		}
		if string(component) == "." || string(component) == ".." {
			component = append(component, 'x')
		}

		path, err := New([][]byte{component})
		if err != nil {
			t.Fatalf("New() rejected sanitized fuzz component %x: %v", component, err)
		}

		jsonBytes, err := path.EncodeJSONExact()
		if err != nil {
			t.Fatalf("EncodeJSONExact() error = %v", err)
		}
		fromJSON, err := DecodeJSONExact(jsonBytes)
		jsonFitsDefaultLimits := len(jsonBytes) <= jsonLimits.MaxInputBytes && len(component) <= jsonLimits.MaxDecodedBytes
		if jsonFitsDefaultLimits && (err != nil || !path.Equal(fromJSON)) {
			t.Fatalf("JSON round trip failed: err=%v got=%x want=%x", err, fromJSON.Components(), path.Components())
		}
		if !jsonFitsDefaultLimits && err == nil {
			t.Fatalf("DecodeJSONExact accepted encoded path outside its default limits: input=%d decoded=%d", len(jsonBytes), len(component))
		}

		percent := PercentEncodeTrashPath(path)
		fromPercent, err := PercentDecodeTrashPath(percent)
		trashFitsDefaultLimits := len(percent) <= trashLimits.MaxInputBytes && len(component) <= trashLimits.MaxDecodedBytes
		if trashFitsDefaultLimits && (err != nil || !path.Equal(fromPercent)) {
			t.Fatalf("percent round trip failed: err=%v got=%x want=%x", err, fromPercent.Components(), path.Components())
		}
		if !trashFitsDefaultLimits && err == nil {
			t.Fatalf("PercentDecodeTrashPath accepted encoded path outside its default limits: input=%d decoded=%d", len(percent), len(component))
		}

		if decoded, err := DecodeJSONExact(input); err == nil {
			reencoded, encodeErr := decoded.EncodeJSONExact()
			if encodeErr != nil {
				t.Fatalf("accepted JSON could not re-encode: %v", encodeErr)
			}
			_, decodeErr := DecodeJSONExact(reencoded)
			if len(reencoded) <= jsonLimits.MaxInputBytes && decodeErr != nil {
				t.Fatalf("re-encoded JSON could not decode: %v", decodeErr)
			}
			if len(reencoded) > jsonLimits.MaxInputBytes && decodeErr == nil {
				t.Fatal("re-encoded JSON exceeded the default input budget but decoded")
			}
		}
		if decoded, err := PercentDecodeTrashPath(string(input)); err == nil {
			reencoded := PercentEncodeTrashPath(decoded)
			_, decodeErr := PercentDecodeTrashPath(reencoded)
			if len(reencoded) <= trashLimits.MaxInputBytes && decodeErr != nil {
				t.Fatalf("re-encoded percent path could not decode: %v", decodeErr)
			}
			if len(reencoded) > trashLimits.MaxInputBytes && decodeErr == nil {
				t.Fatal("re-encoded percent path exceeded the default input budget but decoded")
			}
		}
	})
}
