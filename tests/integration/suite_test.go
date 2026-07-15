//go:build integration

package integration

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

const helpTimeout = 5 * time.Second

func TestLDCleanHelp(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	buildLDClean(t, binary)

	ctx, cancel := context.WithTimeout(context.Background(), helpTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, "--help").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ldclean --help did not finish within %s", helpTimeout)
	}
	if err != nil {
		t.Fatalf("ldclean --help: %v\n%s", err, output)
	}
	if len(output) == 0 {
		t.Fatal("ldclean --help output is empty")
	}
}

func TestHermeticGoEnvironmentOverridesAmbientGoConfiguration(t *testing.T) {
	environment := hermeticGoEnvironment([]string{
		"GOTOOLCHAIN=auto",
		"GOPROXY=https://proxy.example.invalid",
		"GOWORK=/tmp/untrusted.go.work",
		"GOFLAGS=-tags=unexpected",
		"GOROOT=/tmp/untrusted-go",
		"PATH=/tmp/untrusted-bin",
		"LDCLEAN_VMTEST=1",
		"LDCLEAN_VMTEST_TOKEN=untrusted-token",
	})
	assertGoEnvironmentValue(t, environment, "GOTOOLCHAIN", "local")
	assertGoEnvironmentValue(t, environment, "GOPROXY", "off")
	assertGoEnvironmentValue(t, environment, "GOWORK", "off")
	assertGoEnvironmentValue(t, environment, "GOFLAGS", "")
	assertGoEnvironmentValue(t, environment, "GOROOT", "")
	assertGoEnvironmentValue(t, environment, "PATH", "/usr/bin:/bin")
	assertGoEnvironmentValue(t, environment, "LDCLEAN_VMTEST", "")
	assertGoEnvironmentValue(t, environment, "LDCLEAN_VMTEST_TOKEN", "")
}

func buildLDClean(t *testing.T, outputPath string) {
	t.Helper()

	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-trimpath", "-o", outputPath, "../../cmd/ldclean")
	command.Env = hermeticGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build -trimpath -o %q ./cmd/ldclean: %v\n%s", outputPath, err, output)
	}
}

func hermeticGoEnvironment(ambient []string) []string {
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

func assertGoEnvironmentValue(t *testing.T, environment []string, key, want string) {
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
