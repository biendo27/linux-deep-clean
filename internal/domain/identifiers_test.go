package domain

import "testing"

func TestIdentifiersRejectUnsafeOrUnstableValues(t *testing.T) {
	validProvider := testProvider(t)
	if got := validProvider.String(); got != "xdg-cache" {
		t.Fatalf("ProviderID.String() = %q, want %q", got, "xdg-cache")
	}

	tests := []struct {
		name string
		new  func(string) error
	}{
		{"provider absolute path", func(value string) error { _, err := NewProviderID(value); return err }},
		{"root whitespace", func(value string) error { _, err := NewTrustedRootID(value); return err }},
		{"candidate control", func(value string) error { _, err := NewCandidateID(value); return err }},
		{"action option-like", func(value string) error { _, err := NewActionID(value); return err }},
		{"run traversal", func(value string) error { _, err := NewRunID(value); return err }},
		{"capability empty", func(value string) error { _, err := NewCapabilityID(value); return err }},
	}

	values := []string{"/etc/passwd", "contains space", "bad\x00id", "-option", "../escape", ""}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.new(values[index]); err == nil {
				t.Fatalf("constructor accepted %q", values[index])
			}
		})
	}
}

func TestPlanDigestUsesExactFixedLengthValue(t *testing.T) {
	if _, err := NewPlanDigest(make([]byte, planDigestLength-1)); err == nil {
		t.Fatal("NewPlanDigest(short) error = nil, want error")
	}
	if _, err := NewPlanDigest(make([]byte, planDigestLength)); err == nil {
		t.Fatal("NewPlanDigest(zero) error = nil, want error")
	}

	digest := testDigest(t, 9)
	if len(digest.Bytes()) != planDigestLength {
		t.Fatalf("PlanDigest.Bytes() length = %d, want %d", len(digest.Bytes()), planDigestLength)
	}
}
