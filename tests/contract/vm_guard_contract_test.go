package contract

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	filteredVMTestTimeout = 10 * time.Second
	vmGuardRefusal        = "refusing to run vmtest outside a verified disposable guest: vmtest requires LDCLEAN_VMTEST=1"
	vmTestBodyMarker      = "vmtest guarded test body reached"
)

func TestFilteredVMInvocationFailsBeforeSelectedTestBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), filteredVMTestTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-mod=readonly", "-tags=vmtest", "-run", "^TestVMTestRequiresBothGuards$", "-count=1", "-v", "../../tests/vm")
	command.Env = hermeticVMGoEnv(os.Environ())
	output, err := command.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("filtered vmtest invocation did not finish within %s", filteredVMTestTimeout)
	}
	if err == nil {
		t.Fatalf("filtered vmtest invocation succeeded without its real guards:\n%s", output)
	}

	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("filtered vmtest invocation error = %T %v, want a failing test process:\n%s", err, err, output)
	}
	if got := exitError.ExitCode(); got != 1 {
		t.Fatalf("filtered vmtest invocation exit code = %d, want 1:\n%s", got, output)
	}

	result := string(output)
	if !strings.Contains(result, vmGuardRefusal) {
		t.Fatalf("filtered vmtest output = %q, want guard refusal %q", result, vmGuardRefusal)
	}
	if strings.Contains(result, vmTestBodyMarker) {
		t.Fatalf("filtered vmtest reached the selected test body:\n%s", result)
	}
}

func TestHermeticVMGoEnvOverridesAmbientGoConfiguration(t *testing.T) {
	environment := hermeticVMGoEnv([]string{
		"GOTOOLCHAIN=auto",
		"GOPROXY=https://proxy.example.invalid",
		"GOWORK=/tmp/untrusted.go.work",
		"GOFLAGS=-tags=unexpected",
		"GOROOT=/tmp/untrusted-go",
		"PATH=/tmp/untrusted-bin",
		"LDCLEAN_VMTEST=1",
		"LDCLEAN_VMTEST_TOKEN=untrusted-token",
	})
	assertEnvironmentValue(t, environment, "GOTOOLCHAIN", "local")
	assertEnvironmentValue(t, environment, "GOPROXY", "off")
	assertEnvironmentValue(t, environment, "GOWORK", "off")
	assertEnvironmentValue(t, environment, "GOFLAGS", "")
	assertEnvironmentValue(t, environment, "GOROOT", "")
	assertEnvironmentValue(t, environment, "PATH", "/usr/bin:/bin")
	assertEnvironmentValue(t, environment, "LDCLEAN_VMTEST", "")
	assertEnvironmentValue(t, environment, "LDCLEAN_VMTEST_TOKEN", "")
}

func hermeticVMGoEnv(ambient []string) []string {
	environment := make([]string, 0, len(ambient)+8)
	for _, entry := range ambient {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOFLAGS", "GOPROXY", "GOTOOLCHAIN", "GOWORK", "GOROOT", "PATH", "LDCLEAN_VMTEST", "LDCLEAN_VMTEST_TOKEN":
			continue
		}
		environment = append(environment, entry)
	}

	return append(environment, "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GOROOT=", "PATH=/usr/bin:/bin", "LDCLEAN_VMTEST=", "LDCLEAN_VMTEST_TOKEN=")
}

func assertEnvironmentValue(t *testing.T, environment []string, key, want string) {
	t.Helper()

	var values []string
	for _, entry := range environment {
		entryKey, value, found := strings.Cut(entry, "=")
		if found && entryKey == key {
			values = append(values, value)
		}
	}
	if len(values) != 1 {
		t.Fatalf("environment values for %s = %q, want exactly one value %q", key, values, want)
	}
	if got := values[0]; got != want {
		t.Fatalf("environment value for %s = %q, want %q", key, got, want)
	}
}
