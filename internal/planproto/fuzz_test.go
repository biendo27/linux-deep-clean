package planproto

import (
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func FuzzCanonicalCBOR(f *testing.F) {
	f.Add([]byte{0xa0})
	f.Add([]byte{0xbf, 0xff})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		limits := DefaultDecodeLimits()
		if len(encoded) > limits.MaxFrameBytes {
			return
		}
		_, _ = DecodePlan(encoded, limits)
		_, _ = DecodeResult(encoded, limits)
	})
}

func FuzzPlanDigest(f *testing.F) {
	f.Add("clean")
	f.Add("outside-contract")
	f.Fuzz(func(t *testing.T, command string) {
		if len(command) > 128 {
			return
		}
		body := testPlan(t).CanonicalBody()
		body.Command = domain.Command(command)
		_, _ = ComputeDigest(body)
	})
}
