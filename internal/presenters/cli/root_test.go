package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/application"
	"github.com/biendo27/linux-deep-clean/internal/presenters/cli"
	"github.com/spf13/cobra"
)

var (
	_ func(application.Bootstrap) *cobra.Command                             = cli.NewRootCommand
	_ func(context.Context, application.Bootstrap, io.Writer, io.Writer) int = cli.Execute
)

const (
	expectedHelp    = "Linux Deep Clean\n\nUsage:\n  ldclean [flags]\n\nFlags:\n  -h, --help      help for ldclean\n      --version   print version and exit\n"
	expectedVersion = "ldclean version 1.2.3\n"
)

func TestExecuteNoCommandWritesExactHelpToSuppliedWriter(t *testing.T) {
	setArgs(t, "ldclean")

	bootstrap := newFakeBootstrap(nil)
	var stdout, stderr bytes.Buffer

	if got := cli.Execute(context.Background(), bootstrap, &stdout, &stderr); got != 0 {
		t.Fatalf("Execute() exit code = %d, want 0", got)
	}
	if got := stdout.String(); got != expectedHelp {
		t.Fatalf("stdout = %q, want %q", got, expectedHelp)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	assertRequireUnprivilegedFirst(t, bootstrap)
}

func TestExecuteVersionWritesExactVersionToSuppliedWriter(t *testing.T) {
	setArgs(t, "ldclean", "--version")

	bootstrap := newFakeBootstrap(nil)
	var stdout, stderr bytes.Buffer

	if got := cli.Execute(context.Background(), bootstrap, &stdout, &stderr); got != 0 {
		t.Fatalf("Execute() exit code = %d, want 0", got)
	}
	if got := stdout.String(); got != expectedVersion {
		t.Fatalf("stdout = %q, want %q", got, expectedVersion)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	assertRequireUnprivilegedFirst(t, bootstrap)
}

func TestExecuteRejectsInvalidBuildMetadataBeforeCommandDispatch(t *testing.T) {
	setArgs(t, "ldclean", "unexpected")

	bootstrap := newFakeBootstrap(nil)
	bootstrap.info = application.BuildInfo{Version: "not-a-semantic-version"}
	var stdout, stderr bytes.Buffer

	if got := cli.Execute(context.Background(), bootstrap, &stdout, &stderr); got != 1 {
		t.Fatalf("Execute() exit code = %d, want 1", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "ldclean: invalid build metadata\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if got, want := bootstrap.calls, []string{"RequireUnprivileged", "BuildInfo"}; !slices.Equal(got, want) {
		t.Fatalf("bootstrap calls = %v, want %v; invalid metadata must stop command dispatch", got, want)
	}
}

func TestExecuteRejectsRootBeforeCommandDispatch(t *testing.T) {
	setArgs(t, "ldclean", "unexpected")

	bootstrap := newFakeBootstrap(errors.New("refusing to run as root"))
	var stdout, stderr bytes.Buffer

	if got := cli.Execute(context.Background(), bootstrap, &stdout, &stderr); got != 1 {
		t.Fatalf("Execute() exit code = %d, want 1", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "ldclean: refusing to run as root\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if got, want := bootstrap.calls, []string{"RequireUnprivileged"}; !slices.Equal(got, want) {
		t.Fatalf("bootstrap calls = %v, want %v; root refusal must stop command dispatch", got, want)
	}
}

func TestExecuteRejectsUnexpectedCommandWithStableSummary(t *testing.T) {
	setArgs(t, "ldclean", "unexpected")

	bootstrap := newFakeBootstrap(nil)
	var stdout, stderr bytes.Buffer

	if got := cli.Execute(context.Background(), bootstrap, &stdout, &stderr); got != 2 {
		t.Fatalf("Execute() exit code = %d, want 2", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "ldclean: unknown command \"unexpected\"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	assertRequireUnprivilegedFirst(t, bootstrap)
}

func setArgs(t *testing.T, args ...string) {
	t.Helper()

	previous := os.Args
	os.Args = append([]string(nil), args...)
	t.Cleanup(func() {
		os.Args = previous
	})
}

func assertRequireUnprivilegedFirst(t *testing.T, bootstrap *fakeBootstrap) {
	t.Helper()

	if len(bootstrap.calls) == 0 {
		t.Fatal("RequireUnprivileged was not called")
	}
	if got := bootstrap.calls[0]; got != "RequireUnprivileged" {
		t.Fatalf("first bootstrap call = %q, want RequireUnprivileged", got)
	}
}

type fakeBootstrap struct {
	info     application.BuildInfo
	guardErr error
	calls    []string
}

func newFakeBootstrap(guardErr error) *fakeBootstrap {
	return &fakeBootstrap{
		info: application.BuildInfo{
			Version:   "1.2.3",
			Commit:    "0123456789abcdef0123456789abcdef01234567",
			BuildTime: "2026-07-15T12:34:56Z",
			GoVersion: "go1.26.5",
		},
		guardErr: guardErr,
	}
}

func (b *fakeBootstrap) BuildInfo() application.BuildInfo {
	b.calls = append(b.calls, "BuildInfo")
	return b.info
}

func (b *fakeBootstrap) RequireUnprivileged() error {
	b.calls = append(b.calls, "RequireUnprivileged")
	return b.guardErr
}

var _ application.Bootstrap = (*fakeBootstrap)(nil)
