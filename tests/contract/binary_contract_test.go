package contract

import (
	"bytes"
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
	helperTimeout   = 2 * time.Second
	helperRejection = "linux-deep-clean-helper: requests are not accepted in this build\n"
)

func TestBuiltExecutableIdentity(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	outputDir := filepath.Dir(binary)
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-trimpath", "-o", binary, "../../cmd/ldclean")
	command.Env = hermeticGoEnv(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go build -mod=readonly -trimpath -o %q ./cmd/ldclean: %v\n%s", binary, err, output)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", outputDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("built artifact entries = %d, want exactly one named ldclean: %v", len(entries), entries)
	}
	if got := entries[0].Name(); got != "ldclean" {
		t.Fatalf("built artifact name = %q, want ldclean", got)
	}

	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("artifact Info(): %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("ldclean mode = %v, want regular executable file", info.Mode())
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("ldclean mode = %v, want an executable file", info.Mode())
	}

	if _, err := os.Lstat(filepath.Join(outputDir, "ldc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ldc artifact check error = %v, want no ldc executable or alias", err)
	}
}

func TestHelperRejectsEveryInput(t *testing.T) {
	helper := filepath.Join(t.TempDir(), "linux-deep-clean-helper")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-trimpath", "-o", helper, "../../cmd/linux-deep-clean-helper")
	build.Env = hermeticGoEnv(os.Environ())
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("go build -mod=readonly -trimpath -o %q ./cmd/linux-deep-clean-helper: %v\n%s", helper, err, output)
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name:    "empty",
			payload: nil,
		},
		{
			name:    "valid looking",
			payload: []byte(`{"version":1,"operation":"clean","targets":["/tmp"]}`),
		},
		{
			name:    "truncated",
			payload: []byte{0xa3, 0x67, 'r', 'e', 'q'},
		},
		{
			name:    "four MiB",
			payload: bytes.Repeat([]byte("x"), 4*1024*1024),
		},
	}

	for _, tt := range tests {
		ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)

		command := exec.CommandContext(ctx, helper)
		command.Stdin = bytes.NewReader(tt.payload)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr

		err := command.Run()
		cancel()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("%s: helper did not reject input within %s", tt.name, helperTimeout)
		}
		if err == nil {
			t.Fatalf("%s: helper exit error = nil, want exit status 1", tt.name)
		}

		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("%s: helper error = %T %v, want exit status 1", tt.name, err, err)
		}
		if got := exitError.ExitCode(); got != 1 {
			t.Fatalf("%s: helper exit code = %d, want 1", tt.name, got)
		}
		if got := stdout.String(); got != "" {
			t.Fatalf("%s: helper stdout = %q, want empty", tt.name, got)
		}
		if got := stderr.String(); got != helperRejection {
			t.Fatalf("%s: helper stderr = %q, want %q", tt.name, got, helperRejection)
		}
	}
}

func hermeticGoEnv(ambient []string) []string {
	environment := make([]string, 0, len(ambient)+8)
	for _, variable := range ambient {
		key, _, _ := strings.Cut(variable, "=")
		switch key {
		case "GOFLAGS", "GOPROXY", "GOTOOLCHAIN", "GOWORK", "GOROOT", "PATH", "LDCLEAN_VMTEST", "LDCLEAN_VMTEST_TOKEN":
			continue
		}
		environment = append(environment, variable)
	}
	return append(environment, "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GOROOT=", "PATH=/usr/bin:/bin", "LDCLEAN_VMTEST=", "LDCLEAN_VMTEST_TOKEN=")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	root := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q does not contain go.mod: %v", root, err)
	}
	return root
}
