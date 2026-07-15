package application

import (
	"errors"
	"os"
	"testing"
)

func TestRequireUnprivilegedRejectsRoot(t *testing.T) {
	if err := RequireUnprivileged(0); err == nil {
		t.Fatal("RequireUnprivileged(0) error = nil, want error")
	}
}

func TestRequireUnprivilegedAcceptsNonRoot(t *testing.T) {
	for _, euid := range []int{1, 1000, 65534} {
		t.Run("euid", func(t *testing.T) {
			if err := RequireUnprivileged(euid); err != nil {
				t.Fatalf("RequireUnprivileged(%d) error = %v, want nil", euid, err)
			}
		})
	}
}

func TestNewBootstrapRetainsBuildInfoAndUsesProcessEffectiveUID(t *testing.T) {
	wantInfo := BuildInfo{
		Version:   "1.2.3",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		BuildTime: "2026-07-15T12:34:56Z",
		GoVersion: "go1.26.5",
	}
	bootstrap := NewBootstrap(wantInfo)

	if gotInfo := bootstrap.BuildInfo(); gotInfo != wantInfo {
		t.Fatalf("NewBootstrap(...).BuildInfo() = %+v, want exact %+v", gotInfo, wantInfo)
	}

	wantErr := RequireUnprivileged(os.Geteuid())
	gotErr := bootstrap.RequireUnprivileged()
	if wantErr == nil {
		if gotErr != nil {
			t.Fatalf("NewBootstrap(...).RequireUnprivileged() error = %v, want nil for effective UID %d", gotErr, os.Geteuid())
		}
		return
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("NewBootstrap(...).RequireUnprivileged() error = %v, want the same category as RequireUnprivileged(os.Geteuid()) = %v", gotErr, wantErr)
	}
}

type bootstrapContract struct {
	info BuildInfo
	err  error
}

func (b bootstrapContract) BuildInfo() BuildInfo {
	return b.info
}

func (b bootstrapContract) RequireUnprivileged() error {
	return b.err
}

var _ Bootstrap = bootstrapContract{}
