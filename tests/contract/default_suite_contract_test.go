package contract

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	integrationSuitePath      = "tests/integration"
	vmSuitePath               = "tests/vm"
	integrationBuildTag       = "integration"
	vmBuildTag                = "vmtest"
	vmGuardUnitBuildTag       = "vmguardunit"
	vmGuardImplementationFile = "guard.go"
	vmGuardUnitTestFile       = "guard_unit_test.go"
	vmGuardNormalTestFile     = "guard_test.go"
	vmGuardOpenAdapterName    = "openDisposableGuestSentinel"
)

var vmExpectedBuildConstraints = map[string]string{
	"guard.go":           "vmtest",
	"guard_test.go":      "vmtest && !vmguardunit",
	"guard_unit_test.go": "vmtest && vmguardunit",
}

var vmNormalAllowedImports = map[string]map[string]struct{}{
	"guard.go": {
		"errors":  {},
		"fmt":     {},
		"io":      {},
		"io/fs":   {},
		"os":      {},
		"syscall": {},
	},
	"guard_test.go": {
		"fmt":     {},
		"os":      {},
		"testing": {},
	},
}

var defaultLaneForbiddenImports = map[string]string{
	"C":                        "native escape hatch",
	"net":                      "network access",
	"net/http":                 "network access",
	"net/http/httptest":        "network listener setup",
	"net/rpc":                  "network access",
	"net/smtp":                 "network access",
	"net/textproto":            "network protocol handling",
	"plugin":                   "runtime code loading",
	"reflect":                  "runtime mutation indirection",
	"io/ioutil":                "legacy host file mutation API",
	"syscall":                  "host privilege or mutation",
	"unsafe":                   "memory safety bypass",
	"golang.org/x/sys/unix":    "host privilege or mutation",
	"golang.org/x/net":         "network access",
	"golang.org/x/net/context": "network-related dependency",
}

var defaultLaneForbiddenCommands = map[string]string{
	"ash":       "shell execution",
	"apt":       "package-manager mutation",
	"apt-get":   "package-manager mutation",
	"bash":      "shell execution",
	"csh":       "shell execution",
	"chown":     "privilege-sensitive host mutation",
	"chmod":     "host mutation",
	"curl":      "network access",
	"dash":      "shell execution",
	"dnf":       "package-manager mutation",
	"dnf5":      "package-manager mutation",
	"doas":      "privilege escalation",
	"dpkg":      "package-manager mutation",
	"env":       "interpreter indirection",
	"fish":      "shell execution",
	"flatpak":   "package-manager mutation",
	"ftp":       "network access",
	"git":       "possible network access",
	"ksh":       "shell execution",
	"kill":      "host process mutation",
	"mksh":      "shell execution",
	"mount":     "host mount mutation",
	"mv":        "host mutation",
	"nc":        "network access",
	"ncat":      "network access",
	"pacman":    "package-manager mutation",
	"ping":      "network access",
	"pkexec":    "privilege escalation",
	"reboot":    "host mutation",
	"rm":        "host mutation",
	"rpm":       "package-manager mutation",
	"runuser":   "privilege escalation",
	"scp":       "network access",
	"service":   "host service mutation",
	"setpriv":   "privilege escalation",
	"sftp":      "network access",
	"shutdown":  "host mutation",
	"snap":      "package-manager mutation",
	"ssh":       "network access",
	"sh":        "shell execution",
	"su":        "privilege escalation",
	"sudo":      "privilege escalation",
	"systemctl": "host service mutation",
	"telnet":    "network access",
	"umount":    "host mount mutation",
	"wget":      "network access",
	"yash":      "shell execution",
	"yum":       "package-manager mutation",
	"zsh":       "shell execution",
	"zypper":    "package-manager mutation",
}

var defaultLaneForbiddenOSSelectors = map[string]string{
	"Chmod":        "host permission mutation",
	"Chown":        "host ownership mutation",
	"Chtimes":      "host timestamp mutation",
	"Create":       "host file creation",
	"CreateTemp":   "host file creation",
	"FindProcess":  "host process control",
	"Kill":         "host process signal",
	"Lchown":       "host ownership mutation",
	"Link":         "host filesystem mutation",
	"Mkdir":        "host directory creation",
	"MkdirAll":     "host directory creation",
	"MkdirTemp":    "host directory creation",
	"OpenFile":     "host file mutation",
	"Process":      "host process control",
	"Remove":       "host file removal",
	"RemoveAll":    "host file removal",
	"Rename":       "host filesystem mutation",
	"Setenv":       "process-environment mutation",
	"StartProcess": "runtime process execution",
	"Symlink":      "host filesystem mutation",
	"Truncate":     "host file mutation",
	"Unsetenv":     "process-environment mutation",
	"WriteFile":    "host file mutation",
	"Clearenv":     "process-environment mutation",
}

var defaultLaneForbiddenProcessMethods = map[string]string{
	"Kill":    "host process mutation",
	"Release": "host process control",
	"Setenv":  "process-environment mutation",
	"Signal":  "host process mutation",
}

var defaultLaneGoBuildPackages = map[string]struct{}{
	"../../cmd/ldclean":                 {},
	"../../cmd/linux-deep-clean-helper": {},
}

var defaultLaneGoListPackages = map[string]struct{}{
	"../../...":                         {},
	"../../cmd/ldclean":                 {},
	"../../cmd/linux-deep-clean-helper": {},
}

// TestDefaultSuiteContract keeps the ordinary Go test lane a read-only,
// unprivileged, offline contract. It intentionally inspects source files only:
// it must not launch a process, contact a service, or alter the host to prove
// that integration and VM qualifications remain opt-in.
func TestDefaultSuiteContract(t *testing.T) {
	root := repositoryRoot(t)

	assertTaggedSuite(t, root, integrationSuitePath, integrationBuildTag)
	assertTaggedSuite(t, root, vmSuitePath, vmBuildTag)
	assertVMTestPackagesStayInGuardedRoot(t, root)
	assertVMSourceSetIsFixed(t, root)
	assertVMGuardUnitSuiteIsInjectionOnly(t, root)
	assertNormalVMTestSourcesHaveNoPreGuardInitialization(t, root)
	assertNormalVMTestMainIsGuarded(t, root)
	assertDefaultPackageSelection(t)

	defaultSources, err := defaultLaneSourceFiles(root)
	if err != nil {
		t.Fatalf("discover default-lane source files: %v", err)
	}
	for _, path := range defaultSources {
		assertDefaultLaneSourceIsHostSafe(t, root, path)
	}
}

func TestDefaultLaneSourceFilesIncludeNonTestSources(t *testing.T) {
	root := repositoryRoot(t)
	paths, err := defaultLaneSourceFiles(root)
	if err != nil {
		t.Fatalf("discover default-lane source files: %v", err)
	}

	for _, want := range []string{
		filepath.Join(root, "cmd", "ldclean", "main.go"),
		filepath.Join(root, "tests", "contract", "default_suite_contract_test.go"),
	} {
		index := sort.SearchStrings(paths, want)
		if index == len(paths) || paths[index] != want {
			t.Errorf("default-lane source files do not include %s", relativePath(t, root, want))
		}
	}

	for _, unwanted := range []string{
		filepath.Join(root, "tests", "integration", "suite_test.go"),
		filepath.Join(root, "tests", "vm", "guard.go"),
	} {
		index := sort.SearchStrings(paths, unwanted)
		if index < len(paths) && paths[index] == unwanted {
			t.Errorf("default-lane source files include opt-in source %s", relativePath(t, root, unwanted))
		}
	}
}

func TestVMGuardUnitLaneAllowsOnlyItsFixedImplementationSource(t *testing.T) {
	if !isVMGuardUnitImplementationFile(vmGuardImplementationFile) {
		t.Errorf("%s is not accepted as the fixed vmguardunit implementation source", vmGuardImplementationFile)
	}
	if isVMGuardUnitImplementationFile("unsafe.go") {
		t.Error("an arbitrary non-test source is accepted in the unguarded vmguardunit lane")
	}
}

func TestVMBuildConstraintIsFixed(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		constraint string
		want       bool
	}{
		{name: "guard implementation", file: "guard.go", constraint: "vmtest", want: true},
		{name: "normal guard test", file: "guard_test.go", constraint: "vmtest && !vmguardunit", want: true},
		{name: "injected guard unit test", file: "guard_unit_test.go", constraint: "vmtest && vmguardunit", want: true},
		{name: "alternate normal lane", file: "guard_test.go", constraint: "vmtest && !vmguardunit && !bypass", want: false},
		{name: "unexpected VM source", file: "bypass_test.go", constraint: "vmtest && bypass", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expression, err := constraint.Parse("//go:build " + tt.constraint)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.constraint, err)
			}
			if got := vmBuildConstraintIsFixed(tt.file, expression, true); got != tt.want {
				t.Errorf("vmBuildConstraintIsFixed(%q, %q) = %t, want %t", tt.file, tt.constraint, got, tt.want)
			}
		})
	}
}

func TestVMSourcePackageIsFixed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
		want   bool
	}{
		{name: "guarded package", source: "package vmtest", want: true},
		{name: "external test package", source: "package vmtest_test", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmSourcePackageIsFixed(file); got != tt.want {
				t.Errorf("vmSourcePackageIsFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardUnitTestingMethodsAreRestricted(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "fixed test-only methods",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) {
	t.Helper()
	t.Run("nested", func(t *testing.T) { t.Fatal("expected") })
}`,
			want: true,
		},
		{
			name: "temporary directory mutation",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) { _ = t.TempDir() }`,
			want: false,
		},
		{
			name: "working directory mutation",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) { t.Chdir("/") }`,
			want: false,
		},
		{
			name: "environment mutation",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) { t.Setenv("LDCLEAN_VMTEST", "1") }`,
			want: false,
		},
		{
			name: "artifact directory mutation",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) { _ = t.ArtifactDir() }`,
			want: false,
		},
		{
			name: "aliased temporary directory mutation",
			source: `package vmtest
import "testing"
func TestGuard(t *testing.T) {
	handle := t
	_ = handle.TempDir()
}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardUnitTestingSelectorsAreAllowed(file, importAliases(t, file)); got != tt.want {
				t.Errorf("vmGuardUnitTestingSelectorsAreAllowed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardUnitUsesOnlyInjectedProductionDependencies(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "injected guard only",
			source: `package vmtest
func TestGuard() { _ = requireDisposableGuestWith }`,
			want: true,
		},
		{
			name: "production wrapper",
			source: `package vmtest
func TestGuard() { _ = requireDisposableGuest }`,
			want: false,
		},
		{
			name: "production open adapter",
			source: `package vmtest
func TestGuard() { _ = openDisposableGuestSentinel }`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardUnitUsesOnlyInjectedProductionDependencies(file); got != tt.want {
				t.Errorf("vmGuardUnitUsesOnlyInjectedProductionDependencies() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMNormalImportsAreFixed(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		source string
		want   bool
	}{
		{
			name: "fixed normal test imports",
			file: "guard_test.go",
			source: `package vmtest
import (
	"fmt"
	"os"
	"testing"
)`,
			want: true,
		},
		{
			name: "cgo constructor import",
			file: "guard_test.go",
			source: `package vmtest
import "C"`,
			want: false,
		},
		{
			name: "unsafe import",
			file: "guard_test.go",
			source: `package vmtest
import "unsafe"`,
			want: false,
		},
		{
			name: "hidden side effect import",
			file: "guard_test.go",
			source: `package vmtest
import _ "net/http"`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmNormalImportsAreFixed(tt.file, file); got != tt.want {
				t.Errorf("vmNormalImportsAreFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardNormalTestOSSelectorsAreFixed(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "fixed stderr and exit harness",
			source: `package vmtest
import (
	"fmt"
	"os"
)
func TestMain() { _, _ = fmt.Fprintf(os.Stderr, "refusal"); os.Exit(1); os.Exit(1) }`,
			want: true,
		},
		{
			name: "filesystem mutation",
			source: `package vmtest
import "os"
func TestMain() { _ = os.Remove("/tmp/not-allowed") }`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardNormalTestOSSelectorsAreFixed(file, importAliases(t, file)); got != tt.want {
				t.Errorf("vmGuardNormalTestOSSelectorsAreFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMSourcePackageInitializationIsDetected(t *testing.T) {
	sourcePath := filepath.Join("testdata", "vm-safety", "unguarded_init.go")
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sourcePath, err)
	}
	if !vmSourceHasPackageInitialization(file) {
		t.Error("package initialization in an unguarded VM source is not detected")
	}
}

func TestVMGuardImplementationAllowsOnlyReadOnlyOSSelectors(t *testing.T) {
	for _, selector := range []string{"Getenv", "Lstat", "Open"} {
		if !vmGuardImplementationOSSelectorIsAllowed(selector) {
			t.Errorf("os.%s is rejected despite being required for fixed-sentinel guard validation", selector)
		}
	}
	for _, selector := range []string{"Chmod", "Remove", "StartProcess"} {
		if vmGuardImplementationOSSelectorIsAllowed(selector) {
			t.Errorf("os.%s is accepted in the unguarded vmguardunit implementation", selector)
		}
	}
}

func TestVMGuardOpenAdapterIsFixed(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "fixed read-only adapter",
			source: `package vmtest
import "os"
type sentinelFile interface{}
func openDisposableGuestSentinel(path string) (sentinelFile, error) {
	return os.Open(path)
}`,
			want: true,
		},
		{
			name: "mutates an opened file",
			source: `package vmtest
import "os"
type sentinelFile interface{}
func openDisposableGuestSentinel(path string) (sentinelFile, error) {
	file, err := os.Open(path)
	if err != nil { return nil, err }
	_ = file.Chmod(0o600)
	return file, nil
}`,
			want: false,
		},
		{
			name: "opens outside the fixed adapter",
			source: `package vmtest
import "os"
type sentinelFile interface{}
func openDisposableGuestSentinel(path string) (sentinelFile, error) {
	return os.Open(path)
}
func mutate(path string) {
	file, _ := os.Open(path)
	_ = file.Chmod(0o600)
}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardOpenAdapterIsFixed(file, map[string]string{"os": "os"}); got != tt.want {
				t.Errorf("vmGuardOpenAdapterIsFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardProductionWrapperIsFixed(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "delegates to the fixed dependency set",
			source: `package vmtest
import "os"
type sentinelFile interface{}
type guardDependencies struct{}
func openDisposableGuestSentinel(string) (sentinelFile, error) { return nil, nil }
func requireDisposableGuestWith(guardDependencies) error { return nil }
func requireDisposableGuest() error {
	return requireDisposableGuestWith(guardDependencies{
		getenv: os.Getenv,
		lstat: os.Lstat,
		open: openDisposableGuestSentinel,
	})
}`,
			want: true,
		},
		{
			name: "returns without verification",
			source: `package vmtest
import "os"
func requireDisposableGuest() error {
	return nil
}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardProductionWrapperIsFixed(file, map[string]string{"os": "os"}); got != tt.want {
				t.Errorf("vmGuardProductionWrapperIsFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardOSUsageIsFixed(t *testing.T) {
	source := `package vmtest
import "os"
type sentinelFile interface{}
type guardDependencies struct{}
func openDisposableGuestSentinel(path string) (sentinelFile, error) { return os.Open(path) }
func requireDisposableGuestWith(guardDependencies) error { return nil }
func requireDisposableGuest() error {
	return requireDisposableGuestWith(guardDependencies{
		getenv: os.Getenv,
		lstat: os.Lstat,
		open: openDisposableGuestSentinel,
	})
}`
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "fixed production bindings and adapter",
			source: source,
			want:   true,
		},
		{
			name: "adds a production-only environment shortcut",
			source: strings.Replace(source, "func requireDisposableGuestWith(guardDependencies) error { return nil }", `func requireDisposableGuestWith(guardDependencies) error {
	if os.Getenv("LDCLEAN_VMTEST") == "1" { return nil }
	return nil
}`, 1),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardOSUsageIsFixed(file, map[string]string{"os": "os"}); got != tt.want {
				t.Errorf("vmGuardOSUsageIsFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMTestMainIsFixed(t *testing.T) {
	source := `package vmtest
import (
	"os"
	"testing"
)
var vmTestGuardVerified bool
func requireDisposableGuest() error { return nil }
func TestMain(m *testing.M) {
	if err := requireDisposableGuest(); err != nil {
		os.Exit(1)
	}
	vmTestGuardVerified = true
	os.Exit(m.Run())
}`
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "fixed guarded test main",
			source: source,
			want:   true,
		},
		{
			name: "skips the guard for an opt-in environment",
			source: strings.Replace(source, "func TestMain(m *testing.M) {", `func TestMain(m *testing.M) {
	if os.Getenv("LDCLEAN_VMTEST") == "1" { os.Exit(m.Run()) }`, 1),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmTestMainIsFixed(file, map[string]string{"os": "os", "testing": "testing"}); got != tt.want {
				t.Errorf("vmTestMainIsFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestVMGuardVerificationFlowIsFixed(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, vmSuitePath, vmGuardImplementationFile)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", relativePath(t, root, path), err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath(t, root, path), err)
	}
	if !vmGuardVerificationFlowIsFixed(file, importAliases(t, file)) {
		t.Fatalf("vmGuardVerificationFlowIsFixed() rejects the fixed guard implementation")
	}

	for _, bypass := range []struct {
		name    string
		prepend string
	}{
		{
			name: "additional enablement shortcut",
			prepend: `if deps.getenv("LDCLEAN_VMTEST_BYPASS") == "1" {
	return nil
}

`,
		},
		{
			name: "magic token shortcut",
			prepend: `if deps.getenv("LDCLEAN_VMTEST_TOKEN") == "vmguardunit-bypass" {
	return nil
}

`,
		},
	} {
		t.Run(bypass.name, func(t *testing.T) {
			mutated := strings.Replace(string(source), "func requireDisposableGuestWith(deps guardDependencies) error {\n", "func requireDisposableGuestWith(deps guardDependencies) error {\n"+bypass.prepend, 1)
			mutatedFile, err := parser.ParseFile(token.NewFileSet(), bypass.name+".go", strings.NewReader(mutated), 0)
			if err != nil {
				t.Fatalf("parse mutated guard: %v", err)
			}
			if vmGuardVerificationFlowIsFixed(mutatedFile, importAliases(t, mutatedFile)) {
				t.Error("vmGuardVerificationFlowIsFixed() accepts an early-success VM guard bypass")
			}
		})
	}

	t.Run("arbitrary host path sentinel", func(t *testing.T) {
		mutated := strings.Replace(string(source), `const disposableGuestSentinel = "/run/linux-deep-clean/disposable-guest"`, `const disposableGuestSentinel = "/etc/hostname"`, 1)
		mutatedFile, err := parser.ParseFile(token.NewFileSet(), "arbitrary-sentinel.go", strings.NewReader(mutated), 0)
		if err != nil {
			t.Fatalf("parse mutated guard: %v", err)
		}
		if vmGuardVerificationFlowIsFixed(mutatedFile, importAliases(t, mutatedFile)) {
			t.Error("vmGuardVerificationFlowIsFixed() accepts an arbitrary sentinel path")
		}
	})
}

func TestVMGuardMetadataIdentityIsFixed(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, vmSuitePath, vmGuardImplementationFile)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", relativePath(t, root, path), err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath(t, root, path), err)
	}
	if !vmGuardMetadataIdentityIsFixed(file, importAliases(t, file)) {
		t.Fatalf("vmGuardMetadataIdentityIsFixed() rejects the fixed metadata validation")
	}

	mutated := strings.Replace(string(source), "func sentinelIdentityFromMetadata(info fs.FileInfo) (sentinelIdentity, error) {\n", `func sentinelIdentityFromMetadata(info fs.FileInfo) (sentinelIdentity, error) {
	if info.Size() > 0 {
		return sentinelIdentity{}, nil
	}
`, 1)
	mutatedFile, err := parser.ParseFile(token.NewFileSet(), "metadata-bypass.go", strings.NewReader(mutated), 0)
	if err != nil {
		t.Fatalf("parse mutated metadata validation: %v", err)
	}
	if vmGuardMetadataIdentityIsFixed(mutatedFile, importAliases(t, mutatedFile)) {
		t.Error("vmGuardMetadataIdentityIsFixed() accepts a size-based metadata bypass")
	}
}

func TestVMGuardImplementationDeclarationsAreFixed(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, vmSuitePath, vmGuardImplementationFile)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", relativePath(t, root, path), err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath(t, root, path), err)
	}
	if !vmGuardImplementationDeclarationsAreFixed(file) {
		t.Fatal("vmGuardImplementationDeclarationsAreFixed() rejects the fixed guard declaration surface")
	}

	mutated := string(source) + `
func injectedSentinelFile(path string) (sentinelFile, error) {
	return openDisposableGuestSentinel(path)
}
`
	mutatedFile, err := parser.ParseFile(token.NewFileSet(), "adapter-wrapper.go", strings.NewReader(mutated), 0)
	if err != nil {
		t.Fatalf("parse mutated guard: %v", err)
	}
	if vmGuardImplementationDeclarationsAreFixed(mutatedFile) {
		t.Error("vmGuardImplementationDeclarationsAreFixed() accepts an unreviewed descriptor wrapper")
	}
}

func TestVMGuardImplementationRejectsMutatingFileMethodSelectors(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "mutating_file.go", strings.NewReader(`package vmtest
import "os"
func mutate(file *os.File) error {
	return file.Chmod(0o600)
}`), 0)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}

	selector, found := vmGuardImplementationForbiddenMethodSelector(file)
	if !found || selector != "Chmod" {
		t.Errorf("vmGuardImplementationForbiddenMethodSelector() = (%q, %t), want (\"Chmod\", true)", selector, found)
	}
}

func TestVMGuardOpenedSentinelUsesAreFixed(t *testing.T) {
	source := `package vmtest
import "io"
const disposableGuestSentinel = "/run/linux-deep-clean/disposable-guest"
type sentinelFile interface {
	io.Reader
	Stat() (any, error)
	Close() error
}
type guardDependencies struct {
	open func(string) (sentinelFile, error)
}
func requireDisposableGuestWith(deps guardDependencies) error {
	token := "verified-token"
	file, err := deps.open(disposableGuestSentinel)
	if err != nil { return err }
	if file == nil { return nil }
	defer file.Close()
	if _, err := file.Stat(); err != nil { return err }
	_, err = io.ReadAll(io.LimitReader(file, int64(len(token)+1)))
	return err
}`
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "fixed descriptor flow",
			source: source,
			want:   true,
		},
		{
			name: "seeks before the token comparison",
			source: strings.Replace(source, "\tdefer file.Close()", `	if seeker, ok := file.(interface { Seek(int64, int) (int64, error) }); ok {
		_, _ = seeker.Seek(1, 0)
	}
	defer file.Close()`, 1),
			want: false,
		},
		{
			name:   "consumes the descriptor outside the bounded read",
			source: strings.Replace(source, "\tdefer file.Close()", "\t_, _ = io.Copy(io.Discard, file)\n\tdefer file.Close()", 1),
			want:   false,
		},
		{
			name: "shadows the io import with a forged reader",
			source: strings.Replace(strings.Replace(source, "func requireDisposableGuestWith", `type forgedIO struct{}
func (forgedIO) LimitReader(reader sentinelFile, _ int64) sentinelFile { return reader }
func (forgedIO) ReadAll(sentinelFile) ([]byte, error) { return []byte("verified-token"), nil }
func requireDisposableGuestWith`, 1), "\ttoken := \"verified-token\"", "\tio := forgedIO{}\n\ttoken := \"verified-token\"", 1),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.name+".go", strings.NewReader(tt.source), 0)
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			if got := vmGuardOpenedSentinelUsesAreFixed(file, map[string]string{"io": "io"}); got != tt.want {
				t.Errorf("vmGuardOpenedSentinelUsesAreFixed() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestGoCommandSafetyRejectsUnsafeCommandLifetime(t *testing.T) {
	sourcePath := filepath.Join("testdata", "default-safety", "command_reassignment.go")
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse regression fixture: %v", err)
	}

	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("parse regression fixture import: %v", err)
		}
		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = importPath
	}

	var goCommands []*ast.CallExpr
	var tempDirCommands []*ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isExecConstructorCall(call, aliases) {
			return true
		}
		selector := call.Fun.(*ast.SelectorExpr)
		argument := commandNameArgument(call, selector.Sel.Name)
		if argument == nil {
			return true
		}
		if command, ok := stringLiteral(argument); ok && command == "go" {
			goCommands = append(goCommands, call)
			return true
		}
		if identifier, ok := argument.(*ast.Ident); ok && identifier.Name == "binary" {
			tempDirCommands = append(tempDirCommands, call)
		}
		return true
	})
	if len(goCommands) != 14 {
		t.Fatalf("regression fixture Go command count = %d, want 14", len(goCommands))
	}

	for index, command := range goCommands {
		if goCommandUsesApprovedHermeticEnvironment(sourcePath, file, command, aliases) {
			t.Errorf("Go command %d is accepted despite an unsafe command lifetime", index)
		}
	}
	if len(tempDirCommands) != 8 {
		t.Fatalf("regression fixture TempDir command count = %d, want 8", len(tempDirCommands))
	}
	for index, command := range tempDirCommands {
		selector := command.Fun.(*ast.SelectorExpr)
		argument := commandNameArgument(command, selector.Sel.Name)
		function, ok := enclosingFunction(file, command.Pos())
		if !ok {
			t.Fatalf("find regression fixture function for TempDir command %d", index)
		}
		if function.Name.Name == "TestTempDirCommandPathMutation" {
			if !isTempDirLocalBinary(file, command.Pos(), argument, aliases) {
				t.Errorf("TempDir command %d no longer recognizes its safe locally built binary", index)
			}
			if tempDirCommandLifetimeIsSafe(file, command) {
				t.Errorf("TempDir command %d accepts a post-construction Path mutation", index)
			}
			continue
		}
		if isTempDirLocalBinary(file, command.Pos(), argument, aliases) {
			t.Errorf("TempDir command %d is accepted despite an unsafe binary-variable lifetime", index)
		}
	}

	directConstructors := directExecConstructorSelectorPositions(file, aliases)
	var storedConstructorCount int
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !isExecConstructorSelector(selector, aliases) {
			return true
		}
		if _, direct := directConstructors[selector.Pos()]; !direct {
			storedConstructorCount++
		}
		return true
	})
	if storedConstructorCount != 1 {
		t.Errorf("stored os/exec constructor count = %d, want 1", storedConstructorCount)
	}
}

func TestHermeticEnvironmentHelperRequiresFinalControls(t *testing.T) {
	sourcePath := filepath.Join("testdata", "environment-safety", "nonterminal_controls.go")
	if hermeticEnvironmentFunctionIsSafe(sourcePath, "hermeticGoEnv") {
		t.Error("hermetic helper with controls outside its returned environment is accepted")
	}
}

func TestHermeticEnvironmentHelperRequiresBuiltinAppend(t *testing.T) {
	tests := []struct {
		name       string
		sourcePath string
		function   string
	}{
		{
			name:       "package declaration",
			sourcePath: filepath.Join("testdata", "environment-safety", "shadowed-append", "shadowed_append.go"),
			function:   "hermeticGoEnv",
		},
		{
			name:       "dot import",
			sourcePath: filepath.Join("testdata", "environment-safety", "dot-import-append", "dot_import_append.go"),
			function:   "hermeticGoEnv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if hermeticEnvironmentFunctionIsSafe(tt.sourcePath, tt.function) {
				t.Errorf("%s accepts a shadowed append call", tt.sourcePath)
			}
		})
	}
}

func TestHermeticEnvironmentHelperRequiresDirectFunctionBinding(t *testing.T) {
	for _, filename := range []string{"parameter_shadow.go", "import_shadow.go"} {
		sourcePath := filepath.Join("testdata", "environment-safety", "shadowed-helper", filename)
		file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", sourcePath, err)
		}
		aliases := importAliases(t, file)
		var command *ast.CallExpr
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isExecConstructorCall(call, aliases) {
				return true
			}
			command = call
			return false
		})
		if command == nil {
			t.Fatalf("%s contains no exec constructor", sourcePath)
		}
		if goCommandUsesApprovedHermeticEnvironment(sourcePath, file, command, aliases) {
			t.Errorf("%s accepts a shadowed hermetic helper binding", sourcePath)
		}
	}
}

func TestDefaultLaneGoExecutableUsesRuntimeToolchain(t *testing.T) {
	aliases := map[string]string{
		"filepath": "path/filepath",
		"runtime":  "runtime",
	}
	trusted, err := parser.ParseExpr(`filepath.Join(runtime.GOROOT(), "bin", "go")`)
	if err != nil {
		t.Fatalf("parse trusted Go executable expression: %v", err)
	}
	if !isDefaultLaneGoExecutable(trusted, aliases) {
		t.Error("the runtime Go toolchain executable is rejected")
	}

	pathLookup, err := parser.ParseExpr(`"go"`)
	if err != nil {
		t.Fatalf("parse PATH Go executable expression: %v", err)
	}
	if isDefaultLaneGoExecutable(pathLookup, aliases) {
		t.Error("a PATH-resolved Go executable is accepted")
	}

	untrusted, err := parser.ParseExpr(`filepath.Join(t.TempDir(), "go")`)
	if err != nil {
		t.Fatalf("parse untrusted Go executable expression: %v", err)
	}
	if isDefaultLaneGoExecutable(untrusted, aliases) {
		t.Error("a TempDir lookalike is accepted as the Go toolchain")
	}
}

func TestTrustedGoToolchainRejectsShadowedImportAliases(t *testing.T) {
	sourcePath := filepath.Join("testdata", "default-safety", "trusted_go_alias.go")
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse trusted Go alias regression fixture: %v", err)
	}

	aliases := importAliases(t, file)
	var command *ast.CallExpr
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && isExecConstructorCall(call, aliases) {
			command = call
			return false
		}
		return true
	})
	if command == nil {
		t.Fatal("trusted Go alias regression fixture has no exec constructor")
	}
	selector := command.Fun.(*ast.SelectorExpr)
	argument := commandNameArgument(command, selector.Sel.Name)
	if defaultLaneGoCommandUsesTrustedToolchain(file, command.Pos(), argument, aliases) {
		t.Error("shadowed filepath/runtime aliases are accepted as the trusted Go toolchain")
	}
}

func importAliases(t *testing.T, file *ast.File) map[string]string {
	t.Helper()

	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("parse import path: %v", err)
		}
		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = importPath
	}
	return aliases
}

func isExecConstructorCall(call *ast.CallExpr, aliases map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == "os/exec" && (selector.Sel.Name == "Command" || selector.Sel.Name == "CommandContext")
}

// assertVMTestPackagesStayInGuardedRoot prevents a future VM test subpackage
// from silently gaining a separate test binary without the root package's
// TestMain guard. A shared guard harness must be established first.
func assertVMTestPackagesStayInGuardedRoot(t *testing.T, root string) {
	t.Helper()

	vmRoot := filepath.Join(root, vmSuitePath)
	goFiles, err := findGoFiles(vmRoot)
	if err != nil {
		t.Fatalf("discover VM test files: %v", err)
	}
	for _, path := range goFiles {
		relative, err := filepath.Rel(vmRoot, path)
		if err != nil {
			t.Fatalf("find VM test package relative path: %v", err)
		}
		if filepath.Dir(relative) != "." {
			t.Errorf("%s introduces a VM test subpackage without a process-wide guard; establish a shared VM guard harness before adding it", relativePath(t, root, path))
		}
	}
}

// assertVMSourceSetIsFixed closes alternate build-tag lanes. The phase-one VM
// harness intentionally has only one normal TestMain binary and one injected
// unit-test binary; a new tag combination needs a reviewed harness design.
func assertVMSourceSetIsFixed(t *testing.T, root string) {
	t.Helper()

	vmRoot := filepath.Join(root, vmSuitePath)
	goFiles, err := findGoFiles(vmRoot)
	if err != nil {
		t.Fatalf("discover fixed VM source set: %v", err)
	}

	remaining := make(map[string]struct{}, len(vmExpectedBuildConstraints))
	for filename := range vmExpectedBuildConstraints {
		remaining[filename] = struct{}{}
	}
	for _, path := range goFiles {
		filename := filepath.Base(path)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath(t, root, path), err)
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			t.Errorf("%s has an invalid //go:build constraint: %v", relativePath(t, root, path), err)
			continue
		}
		if !vmBuildConstraintIsFixed(filename, expression, found) {
			t.Errorf("%s is not one of the fixed Phase 1 VM harness sources/build constraints; an alternate vmtest lane could bypass TestMain", relativePath(t, root, path))
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relativePath(t, root, path), err)
			continue
		}
		if !vmSourcePackageIsFixed(file) {
			t.Errorf("%s declares package %q; every Phase 1 VM source must stay in package vmtest so its TestMain resolves the production guard", relativePath(t, root, path), file.Name.Name)
		}
		delete(remaining, filename)
	}
	for filename := range remaining {
		t.Errorf("tests/vm is missing fixed Phase 1 harness source %s", filename)
	}
}

func vmBuildConstraintIsFixed(filename string, expression constraint.Expr, found bool) bool {
	want, known := vmExpectedBuildConstraints[filename]
	if !known || !found || expression == nil {
		return false
	}
	expected, err := constraint.Parse("//go:build " + want)
	return err == nil && expression.String() == expected.String()
}

func vmSourcePackageIsFixed(file *ast.File) bool {
	return file != nil && file.Name != nil && file.Name.Name == "vmtest"
}

var vmGuardUnitAllowedImports = map[string]struct{}{
	"bytes":   {},
	"io/fs":   {},
	"strings": {},
	"syscall": {},
	"testing": {},
	"time":    {},
}

var vmForbiddenTestingHostMutationSelectors = map[string]struct{}{
	"ArtifactDir": {},
	"Chdir":       {},
	"Setenv":      {},
	"TempDir":     {},
}

var vmGuardUnitForbiddenProductionIdentifiers = map[string]struct{}{
	"openDisposableGuestSentinel": {},
	"requireDisposableGuest":      {},
}

var vmGuardImplementationAllowedImports = map[string]struct{}{
	"errors":  {},
	"fmt":     {},
	"io":      {},
	"io/fs":   {},
	"os":      {},
	"syscall": {},
}

var vmGuardImplementationAllowedOSSelectors = map[string]struct{}{
	"Getenv": {},
	"Lstat":  {},
	"Open":   {},
}

var vmGuardImplementationForbiddenMethodSelectors = map[string]string{
	"Chdir":            "host working-directory mutation",
	"Chmod":            "host permission mutation",
	"Chown":            "host ownership mutation",
	"CombinedOutput":   "process execution",
	"Fd":               "raw descriptor escape",
	"Kill":             "host process mutation",
	"Name":             "opened-file authority escape",
	"Output":           "process execution",
	"Read":             "sentinel verification bypass",
	"ReadAt":           "opened-file read outside the fixed verification flow",
	"ReadDir":          "opened-file offset mutation",
	"ReadFrom":         "host file mutation",
	"Release":          "host process control",
	"Run":              "process execution",
	"Seek":             "sentinel verification bypass",
	"SetDeadline":      "opened-file state mutation",
	"Setenv":           "process-environment mutation",
	"SetReadDeadline":  "opened-file state mutation",
	"SetWriteDeadline": "opened-file state mutation",
	"Signal":           "host process mutation",
	"Start":            "process execution",
	"Sync":             "opened-file synchronization",
	"SyscallConn":      "raw descriptor escape",
	"Truncate":         "host file mutation",
	"Write":            "host file mutation",
	"WriteAt":          "host file mutation",
	"WriteString":      "host file mutation",
}

// assertVMGuardUnitSuiteIsInjectionOnly keeps the unguarded vmguardunit lane
// limited to its fake-file tests. Normal VM test bodies must explicitly
// exclude that tag and therefore always pass through TestMain.
func assertVMGuardUnitSuiteIsInjectionOnly(t *testing.T, root string) {
	t.Helper()

	vmRoot := filepath.Join(root, vmSuitePath)
	goFiles, err := findGoFiles(vmRoot)
	if err != nil {
		t.Fatalf("discover VM guard-unit files: %v", err)
	}

	for _, path := range goFiles {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath(t, root, path), err)
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			t.Errorf("%s has an invalid //go:build constraint: %v", relativePath(t, root, path), err)
			continue
		}

		if !strings.HasSuffix(path, "_test.go") {
			if !isVMGuardUnitImplementationFile(path) {
				t.Errorf("%s adds non-test source to the unguarded %s lane; only %s may be compiled without TestMain", relativePath(t, root, path), vmGuardUnitBuildTag, vmGuardImplementationFile)
				continue
			}
			assertVMSourceHasNoPackageInitialization(t, root, path, "the unguarded vmguardunit lane")
			assertVMGuardImplementationImportsAreSafe(t, root, path)
			continue
		}

		if filepath.Base(path) == vmGuardUnitTestFile {
			if !found || !requiresBuildTag(expression, vmGuardUnitBuildTag) {
				t.Errorf("%s must require -tags=%s so unguarded fake-file tests cannot join normal VM test binaries", relativePath(t, root, path), vmGuardUnitBuildTag)
			}
			assertVMSourceHasNoPackageInitialization(t, root, path, "the unguarded vmguardunit lane")
			assertVMGuardUnitImportsAreInjectionOnly(t, root, path)
			assertVMGuardUnitUsesInjectedDependencies(t, root, path)
			continue
		}

		if found && expressionSatisfiable(expression, map[string]bool{vmGuardUnitBuildTag: true}) {
			t.Errorf("%s can run with -tags=%s without the VM TestMain guard; normal VM test bodies must exclude that tag", relativePath(t, root, path), vmGuardUnitBuildTag)
		}
	}
}

func isVMGuardUnitImplementationFile(path string) bool {
	return filepath.Base(path) == vmGuardImplementationFile
}

func assertVMGuardImplementationImportsAreSafe(t *testing.T, root, path string) {
	t.Helper()

	assertVMGuardImportsAreAllowed(t, root, path, vmGuardImplementationAllowedImports, "the vmtest guard implementation")
	assertVMGuardImplementationOSSelectorsAreReadOnly(t, root, path)
}

func assertVMGuardImplementationOSSelectorsAreReadOnly(t *testing.T, root, path string) {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}
	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Errorf("parse import path in %s: %v", relativePath(t, root, path), err)
			continue
		}
		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." || name == "_" {
			t.Errorf("%s imports %q as %q; the unguarded vmguardunit implementation requires explicit, auditable standard-library bindings", relativePath(t, root, path), importPath, name)
			continue
		}
		aliases[name] = importPath
	}

	for alias, importPath := range aliases {
		if fileHasShadowedImportAlias(file, alias) {
			t.Errorf("%s shadows the %q import alias %q; the unguarded vmguardunit implementation requires auditable standard-library bindings", relativePath(t, root, path), importPath, alias)
		}
	}
	for importPath := range vmGuardImplementationAllowedImports {
		if !vmGuardImportedBindingIsTrusted(file, aliases, importPath) {
			t.Errorf("%s does not retain one explicit, unshadowed binding for %q; the unguarded vmguardunit implementation must not redirect guard dependencies", relativePath(t, root, path), importPath)
		}
	}
	if !vmGuardImplementationDeclarationsAreFixed(file) {
		t.Errorf("%s adds, removes, or redirects a reviewed guard declaration; the unguarded vmguardunit lane must not gain a path to the real opened descriptor", relativePath(t, root, path))
	}
	if !vmGuardOpenAdapterIsFixed(file, aliases) {
		t.Errorf("%s must use os.Open only through the fixed %s adapter; the unguarded vmguardunit implementation must not expose an opened file for mutation", relativePath(t, root, path), vmGuardOpenAdapterName)
	}
	if !vmGuardProductionWrapperIsFixed(file, aliases) {
		t.Errorf("%s must make requireDisposableGuest delegate only to requireDisposableGuestWith with os.Getenv, os.Lstat, and %s; the production VM lane may not bypass verification", relativePath(t, root, path), vmGuardOpenAdapterName)
	}
	if !vmGuardOSUsageIsFixed(file, aliases) {
		t.Errorf("%s may use os only for the fixed production Getenv/Lstat bindings and %s; any other direct os call could bypass VM verification", relativePath(t, root, path), vmGuardOpenAdapterName)
	}
	if !vmGuardVerificationFlowIsFixed(file, aliases) {
		t.Errorf("%s must retain the fixed fail-closed dependency-injected verification flow; normal VM execution may not add an environment, metadata, or token shortcut", relativePath(t, root, path))
	}
	if !vmGuardMetadataIdentityIsFixed(file, aliases) {
		t.Errorf("%s must retain the fixed root-owned, regular, non-symlink, non-writable sentinel metadata validation; normal VM execution may not add a metadata shortcut", relativePath(t, root, path))
	}
	if !vmGuardOpenedSentinelUsesAreFixed(file, aliases) {
		t.Errorf("%s must use the opened sentinel only for nil checking, Stat, Close, and the fixed bounded token read; descriptor control could bypass exact-token verification", relativePath(t, root, path))
	}
	if selector, found := vmGuardImplementationForbiddenMethodSelector(file); found {
		t.Errorf("%s invokes .%s for %s; the unguarded vmguardunit implementation may not mutate or control the host through an opened file", relativePath(t, root, path), selector, vmGuardImplementationForbiddenMethodSelectors[selector])
	}

	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || aliases[packageName.Name] != "os" {
			return true
		}
		if !vmGuardImplementationOSSelectorIsAllowed(selector.Sel.Name) {
			t.Errorf("%s references os.%s; the unguarded vmguardunit implementation may use only os.Getenv, os.Lstat, and os.Open", relativePath(t, root, path), selector.Sel.Name)
		}
		return true
	})
}

func vmGuardOpenAdapterIsFixed(file *ast.File, aliases map[string]string) bool {
	var adapter *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != vmGuardOpenAdapterName {
			continue
		}
		if adapter != nil {
			return false
		}
		adapter = function
	}
	if !vmGuardOpenAdapterHasFixedSignature(adapter) || len(adapter.Body.List) != 1 {
		return false
	}

	statement, ok := adapter.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return false
	}
	openCall, ok := statement.Results[0].(*ast.CallExpr)
	if !ok || !vmGuardOSCallHasSelector(openCall, aliases, "Open") || len(openCall.Args) != 1 || !astExpressionIsIdentifier(openCall.Args[0], "path") {
		return false
	}

	openCallCount := 0
	fixedOpenCall := true
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !vmGuardOSCallHasSelector(call, aliases, "Open") {
			return true
		}
		openCallCount++
		if call != openCall {
			fixedOpenCall = false
		}
		return true
	})

	return fixedOpenCall && openCallCount == 1
}

// vmGuardImplementationDeclarationsAreFixed prevents the unguarded unit lane
// from reaching the real os.Open adapter through an added wrapper. The guard
// implementation is intentionally a closed, reviewable declaration surface.
func vmGuardImplementationDeclarationsAreFixed(file *ast.File) bool {
	functions := map[string]int{
		"openDisposableGuestSentinel":  1,
		"requireDisposableGuest":       1,
		"requireDisposableGuestWith":   1,
		"sentinelIdentityFromMetadata": 1,
	}
	types := map[string]int{
		"guardDependencies": 1,
		"sentinelFile":      1,
		"sentinelIdentity":  1,
	}
	constants := map[string]int{
		"disposableGuestSentinel": 1,
	}

	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Recv != nil || functions[declaration.Name.Name] == 0 {
				return false
			}
			functions[declaration.Name.Name]--
		case *ast.GenDecl:
			switch declaration.Tok {
			case token.IMPORT:
				continue
			case token.TYPE:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok || types[typeSpec.Name.Name] == 0 {
						return false
					}
					types[typeSpec.Name.Name]--
				}
			case token.CONST:
				for _, specification := range declaration.Specs {
					value, ok := specification.(*ast.ValueSpec)
					if !ok {
						return false
					}
					for _, name := range value.Names {
						if constants[name.Name] == 0 {
							return false
						}
						constants[name.Name]--
					}
				}
			default:
				return false
			}
		default:
			return false
		}
	}

	return vmGuardDeclarationCountsAreExhausted(functions) && vmGuardDeclarationCountsAreExhausted(types) && vmGuardDeclarationCountsAreExhausted(constants)
}

func vmGuardDeclarationCountsAreExhausted(declarations map[string]int) bool {
	for _, count := range declarations {
		if count != 0 {
			return false
		}
	}
	return true
}

func vmGuardOpenAdapterHasFixedSignature(adapter *ast.FuncDecl) bool {
	if adapter == nil || adapter.Recv != nil || adapter.Type.Params == nil || adapter.Type.Results == nil {
		return false
	}
	parameters := adapter.Type.Params.List
	if len(parameters) != 1 || len(parameters[0].Names) != 1 || parameters[0].Names[0].Name != "path" || !astExpressionIsIdentifier(parameters[0].Type, "string") {
		return false
	}
	results := adapter.Type.Results.List
	return len(results) == 2 && len(results[0].Names) == 0 && len(results[1].Names) == 0 && astExpressionIsIdentifier(results[0].Type, "sentinelFile") && astExpressionIsIdentifier(results[1].Type, "error")
}

func vmGuardProductionWrapperIsFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardImportedBindingIsTrusted(file, aliases, "os") {
		return false
	}
	wrapper := vmGuardFunctionDeclaration(file, "requireDisposableGuest")
	if !vmGuardProductionWrapperHasFixedSignature(wrapper) || len(wrapper.Body.List) != 1 {
		return false
	}

	statement, ok := wrapper.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return false
	}
	call, ok := statement.Results[0].(*ast.CallExpr)
	if !ok || !astExpressionIsIdentifier(call.Fun, "requireDisposableGuestWith") || len(call.Args) != 1 {
		return false
	}
	dependencies, ok := call.Args[0].(*ast.CompositeLit)
	if !ok || !astExpressionIsIdentifier(dependencies.Type, "guardDependencies") || len(dependencies.Elts) != 3 {
		return false
	}

	values := make(map[string]ast.Expr, len(dependencies.Elts))
	for _, element := range dependencies.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return false
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || values[key.Name] != nil {
			return false
		}
		values[key.Name] = field.Value
	}
	return vmGuardPackageSelectorHasName(values["getenv"], aliases, "os", "Getenv") &&
		vmGuardPackageSelectorHasName(values["lstat"], aliases, "os", "Lstat") &&
		astExpressionIsIdentifier(values["open"], vmGuardOpenAdapterName)
}

func vmGuardProductionWrapperHasFixedSignature(wrapper *ast.FuncDecl) bool {
	if wrapper == nil || wrapper.Recv != nil || wrapper.Type.Results == nil {
		return false
	}
	if wrapper.Type.Params != nil && len(wrapper.Type.Params.List) != 0 {
		return false
	}
	results := wrapper.Type.Results.List
	return len(results) == 1 && len(results[0].Names) == 0 && astExpressionIsIdentifier(results[0].Type, "error")
}

func vmGuardPackageSelectorHasName(expression ast.Expr, aliases map[string]string, importPath, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == importPath
}

func vmGuardOSUsageIsFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardImportedBindingIsTrusted(file, aliases, "os") || !vmGuardOpenAdapterIsFixed(file, aliases) || !vmGuardProductionWrapperIsFixed(file, aliases) {
		return false
	}
	open, ok := vmGuardOpenAdapterOSSelector(file)
	if !ok {
		return false
	}
	getenv, lstat, ok := vmGuardProductionWrapperOSSelectors(file)
	if !ok {
		return false
	}
	expected := map[*ast.SelectorExpr]struct{}{
		open:   {},
		getenv: {},
		lstat:  {},
	}

	count := 0
	fixed := true
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || aliases[packageName.Name] != "os" {
			return true
		}
		count++
		if _, known := expected[selector]; !known {
			fixed = false
		}
		return true
	})
	return fixed && count == len(expected)
}

func vmGuardOpenAdapterOSSelector(file *ast.File) (*ast.SelectorExpr, bool) {
	adapter := vmGuardFunctionDeclaration(file, vmGuardOpenAdapterName)
	if adapter == nil || adapter.Body == nil || len(adapter.Body.List) != 1 {
		return nil, false
	}
	statement, ok := adapter.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return nil, false
	}
	call, ok := statement.Results[0].(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return selector, ok
}

func vmGuardProductionWrapperOSSelectors(file *ast.File) (*ast.SelectorExpr, *ast.SelectorExpr, bool) {
	wrapper := vmGuardFunctionDeclaration(file, "requireDisposableGuest")
	if wrapper == nil || wrapper.Body == nil || len(wrapper.Body.List) != 1 {
		return nil, nil, false
	}
	statement, ok := wrapper.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return nil, nil, false
	}
	call, ok := statement.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, nil, false
	}
	dependencies, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		return nil, nil, false
	}
	values := make(map[string]ast.Expr, len(dependencies.Elts))
	for _, element := range dependencies.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return nil, nil, false
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || values[key.Name] != nil {
			return nil, nil, false
		}
		values[key.Name] = field.Value
	}
	getenv, getenvOK := values["getenv"].(*ast.SelectorExpr)
	lstat, lstatOK := values["lstat"].(*ast.SelectorExpr)
	return getenv, lstat, getenvOK && lstatOK
}

func vmTestMainIsFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardImportedBindingIsTrusted(file, aliases, "os") || !vmGuardImportedBindingIsTrusted(file, aliases, "testing") {
		return false
	}
	main := vmGuardFunctionDeclaration(file, "TestMain")
	if !vmTestMainHasFixedSignature(main, aliases) || len(main.Body.List) != 3 {
		return false
	}

	guard, ok := main.Body.List[0].(*ast.IfStmt)
	if !ok || guard.Else != nil || !vmTestMainChecksGuard(guard) || !vmTestMainErrorBranchExits(guard.Body, aliases) {
		return false
	}
	if !vmTestMainMarksGuardVerified(main.Body.List[1]) {
		return false
	}
	return vmTestMainRunsTests(main.Body.List[2], aliases)
}

func vmTestMainHasFixedSignature(main *ast.FuncDecl, aliases map[string]string) bool {
	if main == nil || main.Recv != nil || main.Type.Params == nil || len(main.Type.Params.List) != 1 {
		return false
	}
	parameter := main.Type.Params.List[0]
	if len(parameter.Names) != 1 || parameter.Names[0].Name != "m" || !isTestingHandleType(parameter.Type, aliases) {
		return false
	}
	return main.Type.Results == nil || len(main.Type.Results.List) == 0
}

func vmTestMainChecksGuard(statement *ast.IfStmt) bool {
	assignment, ok := statement.Init.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	errName, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || errName.Name != "err" {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 0 || !astExpressionIsIdentifier(call.Fun, "requireDisposableGuest") {
		return false
	}
	comparison, ok := statement.Cond.(*ast.BinaryExpr)
	return ok && comparison.Op == token.NEQ && astExpressionIsIdentifier(comparison.X, "err") && astExpressionIsNil(comparison.Y)
}

func vmTestMainErrorBranchExits(body *ast.BlockStmt, aliases map[string]string) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	last, ok := body.List[len(body.List)-1].(*ast.ExprStmt)
	if !ok || !vmTestMainOSExitHasCode(last.X, aliases, "1") {
		return false
	}
	runsTests := false
	ast.Inspect(body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Run" {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if ok && receiver.Name == "m" {
			runsTests = true
		}
		return true
	})
	return !runsTests
}

func vmTestMainMarksGuardVerified(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	return astExpressionIsIdentifier(assignment.Lhs[0], "vmTestGuardVerified") && astExpressionIsIdentifier(assignment.Rhs[0], "true")
}

func vmTestMainRunsTests(statement ast.Stmt, aliases map[string]string) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	exit, ok := expression.X.(*ast.CallExpr)
	if !ok || !vmGuardPackageCallHasSelector(exit, aliases, "os", "Exit") || len(exit.Args) != 1 {
		return false
	}
	run, ok := exit.Args[0].(*ast.CallExpr)
	if !ok || len(run.Args) != 0 {
		return false
	}
	selector, ok := run.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Run" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "m"
}

func vmTestMainOSExitHasCode(expression ast.Expr, aliases map[string]string, code string) bool {
	exit, ok := expression.(*ast.CallExpr)
	return ok && vmGuardPackageCallHasSelector(exit, aliases, "os", "Exit") && len(exit.Args) == 1 && astIntegerLiteralHasValue(exit.Args[0], code)
}

func astIntegerLiteralHasValue(expression ast.Expr, value string) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == value
}

// vmGuardVerificationFlowIsFixed intentionally fixes the control flow of the
// dependency-injected guard. The vmguardunit lane runs without TestMain, so a
// seemingly harmless environment-specific early success would otherwise make
// the normal VM lane opt-in rather than fail-closed.
func vmGuardVerificationFlowIsFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardDisposableGuestSentinelIsFixed(file) {
		return false
	}
	for _, importPath := range []string{"errors", "fmt", "io"} {
		if !vmGuardImportedBindingIsTrusted(file, aliases, importPath) {
			return false
		}
	}

	guard := vmGuardFunctionDeclaration(file, "requireDisposableGuestWith")
	if !vmGuardVerificationFunctionHasFixedSignature(guard) || len(guard.Body.List) != 22 {
		return false
	}

	statements := guard.Body.List
	return vmGuardDependenciesAvailableCheck(statements[0], aliases) &&
		vmGuardEnablementCheck(statements[1], aliases) &&
		vmGuardTokenBindingIsFixed(statements[2]) &&
		vmGuardTokenPresentCheck(statements[3], aliases) &&
		vmGuardDependencyBindingIsFixed(statements[4], "info", "lstat") &&
		vmGuardErrorCheckIsFixed(statements[5], aliases) &&
		vmGuardNilCheckIsFixed(statements[6], "info", aliases) &&
		vmGuardIdentityBindingIsFixed(statements[7], "expectedIdentity", "info") &&
		vmGuardErrorCheckIsFixed(statements[8], aliases) &&
		vmGuardDependencyBindingIsFixed(statements[9], "file", "open") &&
		vmGuardErrorCheckIsFixed(statements[10], aliases) &&
		vmGuardNilCheckIsFixed(statements[11], "file", aliases) &&
		vmGuardFileCloseIsDeferred(statements[12]) &&
		vmGuardFileStatBindingIsFixed(statements[13]) &&
		vmGuardErrorCheckIsFixed(statements[14], aliases) &&
		vmGuardIdentityBindingIsFixed(statements[15], "openedIdentity", "openedInfo") &&
		vmGuardErrorCheckIsFixed(statements[16], aliases) &&
		vmGuardIdentityComparisonIsFixed(statements[17], aliases) &&
		vmGuardContentsBindingIsFixed(statements[18], aliases) &&
		vmGuardErrorCheckIsFixed(statements[19], aliases) &&
		vmGuardTokenComparisonIsFixed(statements[20], aliases) &&
		vmGuardFinalSuccessIsFixed(statements[21])
}

func vmGuardDisposableGuestSentinelIsFixed(file *ast.File) bool {
	constCount := 0
	fixed := true
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, specification := range group.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range value.Names {
				if name.Name != "disposableGuestSentinel" {
					continue
				}
				constCount++
				if len(value.Names) != 1 || len(value.Values) != 1 || index != 0 || !vmGuardStringLiteralIs(value.Values[0], "/run/linux-deep-clean/disposable-guest") {
					fixed = false
				}
			}
		}
	}
	return fixed && constCount == 1
}

func vmGuardMetadataIdentityIsFixed(file *ast.File, aliases map[string]string) bool {
	for _, importPath := range []string{"errors", "io/fs", "syscall"} {
		if !vmGuardImportedBindingIsTrusted(file, aliases, importPath) {
			return false
		}
	}

	helper := vmGuardFunctionDeclaration(file, "sentinelIdentityFromMetadata")
	if !vmGuardMetadataIdentityHasFixedSignature(helper, aliases) || len(helper.Body.List) != 7 {
		return false
	}

	statements := helper.Body.List
	return vmGuardMetadataInfoNilCheck(statements[0], aliases) &&
		vmGuardMetadataSymlinkCheck(statements[1], aliases) &&
		vmGuardMetadataRegularCheck(statements[2], aliases) &&
		vmGuardMetadataPermissionsCheck(statements[3], aliases) &&
		vmGuardMetadataStatBindingIsFixed(statements[4], aliases) &&
		vmGuardMetadataOwnerCheck(statements[5], aliases) &&
		vmGuardMetadataSuccessIsFixed(statements[6])
}

func vmGuardMetadataIdentityHasFixedSignature(helper *ast.FuncDecl, aliases map[string]string) bool {
	if helper == nil || helper.Recv != nil || helper.Type.TypeParams != nil || helper.Type.Params == nil || helper.Type.Results == nil {
		return false
	}
	parameters := helper.Type.Params.List
	results := helper.Type.Results.List
	return len(parameters) == 1 && len(parameters[0].Names) == 1 && parameters[0].Names[0].Name == "info" &&
		vmGuardPackageSelectorHasName(parameters[0].Type, aliases, "io/fs", "FileInfo") &&
		len(results) == 2 && len(results[0].Names) == 0 && len(results[1].Names) == 0 &&
		astExpressionIsIdentifier(results[0].Type, "sentinelIdentity") && astExpressionIsIdentifier(results[1].Type, "error")
}

func vmGuardMetadataInfoNilCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsIdentityError(statement, func(condition ast.Expr) bool {
		return vmGuardIdentifierNilComparison(condition, "info", token.EQL)
	}, aliases)
}

func vmGuardMetadataSymlinkCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsIdentityError(statement, func(condition ast.Expr) bool {
		comparison, ok := condition.(*ast.BinaryExpr)
		return ok && comparison.Op == token.NEQ && vmGuardInfoModeAndFSConstant(comparison.X, aliases, "ModeSymlink") && astIntegerLiteralHasValue(comparison.Y, "0")
	}, aliases)
}

func vmGuardMetadataRegularCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsIdentityError(statement, func(condition ast.Expr) bool {
		negation, ok := condition.(*ast.UnaryExpr)
		return ok && negation.Op == token.NOT && vmGuardInfoModeMethodCallIsFixed(negation.X, "IsRegular")
	}, aliases)
}

func vmGuardMetadataPermissionsCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsIdentityError(statement, func(condition ast.Expr) bool {
		comparison, ok := condition.(*ast.BinaryExpr)
		if !ok || comparison.Op != token.NEQ || !astIntegerLiteralHasValue(comparison.Y, "0") {
			return false
		}
		and, ok := comparison.X.(*ast.BinaryExpr)
		return ok && and.Op == token.AND && vmGuardInfoModeMethodCallIsFixed(and.X, "Perm") && astIntegerLiteralHasValue(and.Y, "0o022")
	}, aliases)
}

func vmGuardMetadataStatBindingIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 ||
		!astExpressionIsIdentifier(assignment.Lhs[0], "stat") || !astExpressionIsIdentifier(assignment.Lhs[1], "ok") {
		return false
	}
	assertion, ok := assignment.Rhs[0].(*ast.TypeAssertExpr)
	if !ok || !vmGuardInfoMethodCallIsFixed(assertion.X, "Sys") {
		return false
	}
	pointer, ok := assertion.Type.(*ast.StarExpr)
	return ok && vmGuardPackageSelectorHasName(pointer.X, aliases, "syscall", "Stat_t")
}

func vmGuardMetadataOwnerCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsIdentityError(statement, func(condition ast.Expr) bool {
		terms := vmGuardOrTerms(condition)
		if len(terms) != 2 {
			return false
		}
		first, firstOK := terms[0].(*ast.UnaryExpr)
		if !firstOK || first.Op != token.NOT || !astExpressionIsIdentifier(first.X, "ok") {
			return false
		}
		second, secondOK := terms[1].(*ast.BinaryExpr)
		return secondOK && second.Op == token.NEQ && vmGuardSelectorHasIdentifier(second.X, "stat", "Uid") && astIntegerLiteralHasValue(second.Y, "0")
	}, aliases)
}

func vmGuardMetadataSuccessIsFixed(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 2 || !astExpressionIsNil(result.Results[1]) {
		return false
	}
	identity, ok := result.Results[0].(*ast.CompositeLit)
	if !ok || !astExpressionIsIdentifier(identity.Type, "sentinelIdentity") || len(identity.Elts) != 2 {
		return false
	}
	device, deviceOK := identity.Elts[0].(*ast.KeyValueExpr)
	inode, inodeOK := identity.Elts[1].(*ast.KeyValueExpr)
	if !deviceOK || !inodeOK || !astExpressionIsIdentifier(device.Key, "device") || !astExpressionIsIdentifier(inode.Key, "inode") || !vmGuardUint64StatDeviceIsFixed(device.Value) {
		return false
	}
	return vmGuardSelectorHasIdentifier(inode.Value, "stat", "Ino")
}

func vmGuardIfReturnsIdentityError(statement ast.Stmt, conditionMatches func(ast.Expr) bool, aliases map[string]string) bool {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !conditionMatches(guard.Cond) || guard.Body == nil || len(guard.Body.List) != 1 {
		return false
	}
	result, ok := guard.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 2 || !vmGuardEmptyIdentityLiteral(result.Results[0]) {
		return false
	}
	call, ok := result.Results[1].(*ast.CallExpr)
	if !ok || !vmGuardPackageCallHasSelector(call, aliases, "errors", "New") || len(call.Args) != 1 {
		return false
	}
	_, messageIsString := stringLiteral(call.Args[0])
	return messageIsString
}

func vmGuardEmptyIdentityLiteral(expression ast.Expr) bool {
	identity, ok := expression.(*ast.CompositeLit)
	return ok && astExpressionIsIdentifier(identity.Type, "sentinelIdentity") && len(identity.Elts) == 0
}

func vmGuardInfoModeAndFSConstant(expression ast.Expr, aliases map[string]string, constant string) bool {
	and, ok := expression.(*ast.BinaryExpr)
	return ok && and.Op == token.AND && vmGuardInfoModeMethodCallIsFixed(and.X, "") && vmGuardPackageSelectorHasName(and.Y, aliases, "io/fs", constant)
}

func vmGuardInfoModeMethodCallIsFixed(expression ast.Expr, method string) bool {
	if method == "" {
		return vmGuardInfoMethodCallIsFixed(expression, "Mode")
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == method && vmGuardInfoMethodCallIsFixed(selector.X, "Mode")
}

func vmGuardInfoMethodCallIsFixed(expression ast.Expr, method string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == method && astExpressionIsIdentifier(selector.X, "info")
}

func vmGuardSelectorHasIdentifier(expression ast.Expr, identifier, selectorName string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == selectorName && astExpressionIsIdentifier(selector.X, identifier)
}

func vmGuardUint64StatDeviceIsFixed(expression ast.Expr) bool {
	conversion, ok := expression.(*ast.CallExpr)
	return ok && astExpressionIsIdentifier(conversion.Fun, "uint64") && len(conversion.Args) == 1 && vmGuardSelectorHasIdentifier(conversion.Args[0], "stat", "Dev")
}

func vmGuardVerificationFunctionHasFixedSignature(guard *ast.FuncDecl) bool {
	if guard == nil || guard.Recv != nil || guard.Type.TypeParams != nil || guard.Type.Params == nil || guard.Type.Results == nil {
		return false
	}
	parameters := guard.Type.Params.List
	results := guard.Type.Results.List
	return len(parameters) == 1 && len(parameters[0].Names) == 1 && parameters[0].Names[0].Name == "deps" &&
		astExpressionIsIdentifier(parameters[0].Type, "guardDependencies") &&
		len(results) == 1 && len(results[0].Names) == 0 && astExpressionIsIdentifier(results[0].Type, "error")
}

func vmGuardDependenciesAvailableCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		terms := vmGuardOrTerms(condition)
		if len(terms) != 3 {
			return false
		}
		expected := map[string]struct{}{"getenv": {}, "lstat": {}, "open": {}}
		for _, term := range terms {
			field, ok := vmGuardDependencyNilTerm(term)
			if !ok {
				return false
			}
			if _, found := expected[field]; !found {
				return false
			}
			delete(expected, field)
		}
		return len(expected) == 0
	}, aliases)
}

func vmGuardEnablementCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		return vmGuardDependencyStringComparison(condition, "getenv", "LDCLEAN_VMTEST", token.NEQ, "1")
	}, aliases)
}

func vmGuardTokenBindingIsFixed(statement ast.Stmt) bool {
	return vmGuardBindingFromDependencyCall(statement, "token", "getenv", "LDCLEAN_VMTEST_TOKEN")
}

func vmGuardTokenPresentCheck(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		return vmGuardIdentifierStringComparison(condition, "token", token.EQL, "")
	}, aliases)
}

func vmGuardDependencyBindingIsFixed(statement ast.Stmt, binding, field string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 ||
		!astExpressionIsIdentifier(assignment.Lhs[0], binding) || !astExpressionIsIdentifier(assignment.Lhs[1], "err") {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return ok && vmGuardDependencyCallHasStringArgument(call, field, "disposableGuestSentinel", false)
}

func vmGuardErrorCheckIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		return vmGuardIdentifierNilComparison(condition, "err", token.NEQ)
	}, aliases)
}

func vmGuardNilCheckIsFixed(statement ast.Stmt, identifier string, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		return vmGuardIdentifierNilComparison(condition, identifier, token.EQL)
	}, aliases)
}

func vmGuardIdentityBindingIsFixed(statement ast.Stmt, binding, argument string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 ||
		!astExpressionIsIdentifier(assignment.Lhs[0], binding) || !astExpressionIsIdentifier(assignment.Lhs[1], "err") {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return ok && astExpressionIsIdentifier(call.Fun, "sentinelIdentityFromMetadata") && len(call.Args) == 1 && astExpressionIsIdentifier(call.Args[0], argument)
}

func vmGuardFileCloseIsDeferred(statement ast.Stmt) bool {
	deferred, ok := statement.(*ast.DeferStmt)
	if !ok || len(deferred.Call.Args) != 0 {
		return false
	}
	selector, ok := deferred.Call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Close" && astExpressionIsIdentifier(selector.X, "file")
}

func vmGuardFileStatBindingIsFixed(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 ||
		!astExpressionIsIdentifier(assignment.Lhs[0], "openedInfo") || !astExpressionIsIdentifier(assignment.Lhs[1], "err") {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Stat" && astExpressionIsIdentifier(selector.X, "file")
}

func vmGuardIdentityComparisonIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		comparison, ok := condition.(*ast.BinaryExpr)
		return ok && comparison.Op == token.NEQ && astExpressionIsIdentifier(comparison.X, "openedIdentity") && astExpressionIsIdentifier(comparison.Y, "expectedIdentity")
	}, aliases)
}

func vmGuardContentsBindingIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 ||
		!astExpressionIsIdentifier(assignment.Lhs[0], "contents") || !astExpressionIsIdentifier(assignment.Lhs[1], "err") {
		return false
	}
	readAll, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || !vmGuardPackageCallHasSelector(readAll, aliases, "io", "ReadAll") || len(readAll.Args) != 1 {
		return false
	}
	limitReader, ok := readAll.Args[0].(*ast.CallExpr)
	return ok && vmGuardPackageCallHasSelector(limitReader, aliases, "io", "LimitReader") && len(limitReader.Args) == 2 &&
		astExpressionIsIdentifier(limitReader.Args[0], "file") && vmGuardLimitReaderBoundIsExact(limitReader.Args[1])
}

func vmGuardTokenComparisonIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	return vmGuardIfReturnsError(statement, func(condition ast.Expr) bool {
		comparison, ok := condition.(*ast.BinaryExpr)
		if !ok || comparison.Op != token.NEQ || !astExpressionIsIdentifier(comparison.Y, "token") {
			return false
		}
		conversion, ok := comparison.X.(*ast.CallExpr)
		return ok && astExpressionIsIdentifier(conversion.Fun, "string") && len(conversion.Args) == 1 && astExpressionIsIdentifier(conversion.Args[0], "contents")
	}, aliases)
}

func vmGuardFinalSuccessIsFixed(statement ast.Stmt) bool {
	result, ok := statement.(*ast.ReturnStmt)
	return ok && len(result.Results) == 1 && astExpressionIsNil(result.Results[0])
}

func vmGuardIfReturnsError(statement ast.Stmt, conditionMatches func(ast.Expr) bool, aliases map[string]string) bool {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !conditionMatches(guard.Cond) || guard.Body == nil || len(guard.Body.List) != 1 {
		return false
	}
	return vmGuardRefusalReturnIsFixed(guard.Body.List[0], aliases)
}

func vmGuardRefusalReturnIsFixed(statement ast.Stmt, aliases map[string]string) bool {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return false
	}
	if astExpressionIsIdentifier(result.Results[0], "err") {
		return true
	}
	call, ok := result.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	_, messageIsString := stringLiteral(call.Args[0])
	return messageIsString && (vmGuardPackageCallHasSelector(call, aliases, "errors", "New") || vmGuardPackageCallHasSelector(call, aliases, "fmt", "Errorf"))
}

func vmGuardOrTerms(expression ast.Expr) []ast.Expr {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.LOR {
		return []ast.Expr{expression}
	}
	return append(vmGuardOrTerms(comparison.X), vmGuardOrTerms(comparison.Y)...)
}

func vmGuardDependencyNilTerm(expression ast.Expr) (string, bool) {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL || !astExpressionIsNil(comparison.Y) {
		return "", false
	}
	selector, ok := comparison.X.(*ast.SelectorExpr)
	if !ok || !astExpressionIsIdentifier(selector.X, "deps") {
		return "", false
	}
	return selector.Sel.Name, true
}

func vmGuardDependencyStringComparison(expression ast.Expr, field, argument string, operation token.Token, value string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	if !ok || comparison.Op != operation || !vmGuardStringLiteralIs(comparison.Y, value) {
		return false
	}
	call, ok := comparison.X.(*ast.CallExpr)
	return ok && vmGuardDependencyCallHasStringArgument(call, field, argument, true)
}

func vmGuardIdentifierStringComparison(expression ast.Expr, identifier string, operation token.Token, value string) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	return ok && comparison.Op == operation && astExpressionIsIdentifier(comparison.X, identifier) && vmGuardStringLiteralIs(comparison.Y, value)
}

func vmGuardIdentifierNilComparison(expression ast.Expr, identifier string, operation token.Token) bool {
	comparison, ok := expression.(*ast.BinaryExpr)
	return ok && comparison.Op == operation && astExpressionIsIdentifier(comparison.X, identifier) && astExpressionIsNil(comparison.Y)
}

func vmGuardBindingFromDependencyCall(statement ast.Stmt, binding, field, argument string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 || !astExpressionIsIdentifier(assignment.Lhs[0], binding) {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return ok && vmGuardDependencyCallHasStringArgument(call, field, argument, true)
}

func vmGuardDependencyCallHasStringArgument(call *ast.CallExpr, field, argument string, quoted bool) bool {
	if !vmGuardDependencyCallHasSelector(call, field) || len(call.Args) != 1 {
		return false
	}
	if quoted {
		return vmGuardStringLiteralIs(call.Args[0], argument)
	}
	return astExpressionIsIdentifier(call.Args[0], argument)
}

func vmGuardDependencyCallHasSelector(call *ast.CallExpr, field string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == field && astExpressionIsIdentifier(selector.X, "deps")
}

func vmGuardStringLiteralIs(expression ast.Expr, value string) bool {
	actual, ok := stringLiteral(expression)
	return ok && actual == value
}

func vmGuardOpenedSentinelUsesAreFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardImportedBindingIsTrusted(file, aliases, "io") {
		return false
	}
	guard := vmGuardFunctionDeclaration(file, "requireDisposableGuestWith")
	if guard == nil || guard.Body == nil {
		return false
	}

	parents := astParentNodes(guard.Body)
	var openCalls []*ast.CallExpr
	ast.Inspect(guard.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && vmGuardDependencyOpenCall(call) {
			openCalls = append(openCalls, call)
		}
		return true
	})
	if len(openCalls) != 1 {
		return false
	}

	binding, ok := vmGuardOpenedSentinelBinding(openCalls[0], parents)
	if !ok || binding.Obj == nil {
		return false
	}

	useCount := 0
	fixedUses := true
	ast.Inspect(guard.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || identifier == binding || identifier.Obj != binding.Obj {
			return true
		}
		useCount++
		if !vmGuardOpenedSentinelUseIsAllowed(identifier, parents, aliases) {
			fixedUses = false
		}
		return true
	})

	return fixedUses && useCount == 4
}

func vmGuardImportedBindingIsTrusted(file *ast.File, aliases map[string]string, importPath string) bool {
	binding := ""
	for alias, importedPath := range aliases {
		if importedPath != importPath {
			continue
		}
		if binding != "" {
			return false
		}
		binding = alias
	}
	return binding != "" && !fileHasShadowedImportAlias(file, binding)
}

func vmGuardFunctionDeclaration(file *ast.File, name string) *ast.FuncDecl {
	var result *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name {
			continue
		}
		if result != nil {
			return nil
		}
		result = function
	}
	return result
}

func astParentNodes(root ast.Node) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	var stack []ast.Node
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return false
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func vmGuardDependencyOpenCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "open" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "deps"
}

func vmGuardOpenedSentinelBinding(call *ast.CallExpr, parents map[ast.Node]ast.Node) (*ast.Ident, bool) {
	assignment, ok := parents[call].(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 || assignment.Rhs[0] != call || len(call.Args) != 1 || !astExpressionIsIdentifier(call.Args[0], "disposableGuestSentinel") {
		return nil, false
	}
	binding, ok := assignment.Lhs[0].(*ast.Ident)
	return binding, ok && binding.Name == "file"
}

func vmGuardOpenedSentinelUseIsAllowed(identifier *ast.Ident, parents map[ast.Node]ast.Node, aliases map[string]string) bool {
	parent := parents[identifier]
	if comparison, ok := parent.(*ast.BinaryExpr); ok && comparison.Op == token.EQL {
		return (comparison.X == identifier && astExpressionIsNil(comparison.Y)) || (comparison.Y == identifier && astExpressionIsNil(comparison.X))
	}
	if selector, ok := parent.(*ast.SelectorExpr); ok && selector.X == identifier && (selector.Sel.Name == "Close" || selector.Sel.Name == "Stat") {
		call, ok := parents[selector].(*ast.CallExpr)
		return ok && call.Fun == selector && len(call.Args) == 0
	}
	call, ok := parent.(*ast.CallExpr)
	return ok && len(call.Args) == 2 && call.Args[0] == identifier && vmGuardPackageCallHasSelector(call, aliases, "io", "LimitReader") && vmGuardLimitReaderBoundIsExact(call.Args[1])
}

func astExpressionIsNil(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func vmGuardPackageCallHasSelector(call *ast.CallExpr, aliases map[string]string, importPath, selector string) bool {
	function, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || function.Sel.Name != selector {
		return false
	}
	packageName, ok := function.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == importPath
}

func vmGuardLimitReaderBoundIsExact(expression ast.Expr) bool {
	conversion, ok := expression.(*ast.CallExpr)
	if !ok || len(conversion.Args) != 1 || !astExpressionIsIdentifier(conversion.Fun, "int64") {
		return false
	}
	addition, ok := conversion.Args[0].(*ast.BinaryExpr)
	if !ok || addition.Op != token.ADD || !astIntegerLiteralIsOne(addition.Y) {
		return false
	}
	length, ok := addition.X.(*ast.CallExpr)
	return ok && len(length.Args) == 1 && astExpressionIsIdentifier(length.Fun, "len") && astExpressionIsIdentifier(length.Args[0], "token")
}

func astIntegerLiteralIsOne(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "1"
}

func vmGuardOSCallHasSelector(call *ast.CallExpr, aliases map[string]string, selector string) bool {
	function, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || function.Sel.Name != selector {
		return false
	}
	packageName, ok := function.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == "os"
}

func astExpressionIsIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func vmGuardImplementationForbiddenMethodSelector(file *ast.File) (string, bool) {
	var forbidden string
	ast.Inspect(file, func(node ast.Node) bool {
		if forbidden != "" {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, found := vmGuardImplementationForbiddenMethodSelectors[selector.Sel.Name]; found {
			forbidden = selector.Sel.Name
			return false
		}
		return true
	})
	return forbidden, forbidden != ""
}

func vmGuardImplementationOSSelectorIsAllowed(name string) bool {
	_, allowed := vmGuardImplementationAllowedOSSelectors[name]
	return allowed
}

func assertVMGuardUnitImportsAreInjectionOnly(t *testing.T, root, path string) {
	t.Helper()

	assertVMGuardImportsAreAllowed(t, root, path, vmGuardUnitAllowedImports, "the unguarded vmguardunit lane")
}

func assertVMGuardUnitUsesInjectedDependencies(t *testing.T, root, path string) {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}
	aliases := importAliases(t, file)
	if !vmGuardUnitTestingSelectorsAreAllowed(file, aliases) {
		t.Errorf("%s invokes a host-mutating testing API; vmguardunit may not create files, change directories, mutate the environment, or allocate artifacts", relativePath(t, root, path))
	}
	if !vmGuardUnitUsesOnlyInjectedProductionDependencies(file) {
		t.Errorf("%s references the production VM wrapper or open adapter; the unguarded vmguardunit lane must use only injected fake dependencies", relativePath(t, root, path))
	}
}

func vmGuardUnitUsesOnlyInjectedProductionDependencies(file *ast.File) bool {
	fixed := true
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if _, forbidden := vmGuardUnitForbiddenProductionIdentifiers[identifier.Name]; forbidden {
			fixed = false
		}
		return true
	})
	return fixed
}

func vmGuardUnitTestingSelectorsAreAllowed(file *ast.File, aliases map[string]string) bool {
	return vmGuardImportedBindingIsTrusted(file, aliases, "testing") && vmTestingSelectorsAvoidHostMutation(file)
}

func vmTestingSelectorsAvoidHostMutation(file *ast.File) bool {
	allowed := true
	ast.Inspect(file, func(node ast.Node) bool {
		if !allowed {
			return false
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, forbidden := vmForbiddenTestingHostMutationSelectors[selector.Sel.Name]; forbidden {
			allowed = false
		}
		return true
	})
	return allowed
}

func assertVMGuardImportsAreAllowed(t *testing.T, root, path string, allowedImports map[string]struct{}, lane string) {
	t.Helper()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}

	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Errorf("parse import path in %s: %v", relativePath(t, root, path), err)
			continue
		}
		if _, allowed := allowedImports[importPath]; !allowed {
			t.Errorf("%s imports %q; %s is limited to its reviewed standard-library surface", relativePath(t, root, path), importPath, lane)
		}

		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if importPath == "syscall" && (name == "." || name == "_") {
			t.Errorf("%s imports syscall as %q; %s permits only explicit syscall.Stat_t metadata", relativePath(t, root, path), name, lane)
			continue
		}
		if name != "_" && name != "." {
			aliases[name] = importPath
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && aliases[packageName.Name] == "syscall" && selector.Sel.Name != "Stat_t" {
			t.Errorf("%s references syscall.%s; %s may construct Stat_t metadata but must not invoke syscalls", relativePath(t, root, path), selector.Sel.Name, lane)
		}
		return true
	})
}

// assertNormalVMTestSourcesHaveNoPreGuardInitialization keeps TestMain as the
// first executable behavior in the normal VM lane. Package initializers run
// before TestMain, so any future VM fixture that needs one must first move to
// a separately designed, process-wide guarded harness.
func assertNormalVMTestSourcesHaveNoPreGuardInitialization(t *testing.T, root string) {
	t.Helper()

	vmRoot := filepath.Join(root, vmSuitePath)
	goFiles, err := findGoFiles(vmRoot)
	if err != nil {
		t.Fatalf("discover normal VM test sources: %v", err)
	}

	for _, path := range goFiles {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath(t, root, path), err)
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			t.Errorf("%s has an invalid //go:build constraint: %v", relativePath(t, root, path), err)
			continue
		}
		if !found || !expressionSatisfiable(expression, map[string]bool{
			vmBuildTag:          true,
			vmGuardUnitBuildTag: false,
		}) {
			continue
		}

		assertVMSourceHasNoPackageInitialization(t, root, path, "normal vmtest execution")
		assertVMNormalImportsAreSafe(t, root, path)
	}
}

func assertVMNormalImportsAreSafe(t *testing.T, root, path string) {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}
	if !vmNormalImportsAreFixed(filepath.Base(path), file) {
		t.Errorf("%s imports outside the fixed normal-vm allowlist; pre-TestMain sources may not use cgo, unsafe, or hidden side-effect imports", relativePath(t, root, path))
	}
	if filepath.Base(path) == vmGuardNormalTestFile {
		aliases := importAliases(t, file)
		if !vmGuardNormalTestOSSelectorsAreFixed(file, aliases) {
			t.Errorf("%s references os outside its fixed stderr/exit harness surface; Phase 1 normal VM tests may not add host filesystem or process behavior", relativePath(t, root, path))
		}
		if !vmTestingSelectorsAvoidHostMutation(file) {
			t.Errorf("%s invokes a host-mutating testing API; Phase 1 normal VM tests contain no destructive case", relativePath(t, root, path))
		}
	}
}

func vmNormalImportsAreFixed(filename string, file *ast.File) bool {
	allowed, known := vmNormalAllowedImports[filename]
	if !known {
		return false
	}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return false
		}
		if _, permitted := allowed[importPath]; !permitted || spec.Name != nil {
			return false
		}
	}
	return true
}

func vmGuardNormalTestOSSelectorsAreFixed(file *ast.File, aliases map[string]string) bool {
	if !vmGuardImportedBindingIsTrusted(file, aliases, "fmt") || !vmGuardImportedBindingIsTrusted(file, aliases, "os") {
		return false
	}
	parents := astParentNodes(file)
	exitCount := 0
	stderrCount := 0
	fixed := true
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || aliases[packageName.Name] != "os" {
			return true
		}
		switch selector.Sel.Name {
		case "Exit":
			call, ok := parents[selector].(*ast.CallExpr)
			if !ok || call.Fun != selector || len(call.Args) != 1 {
				fixed = false
				return true
			}
			exitCount++
		case "Stderr":
			call, ok := parents[selector].(*ast.CallExpr)
			if !ok || !vmGuardPackageCallHasSelector(call, aliases, "fmt", "Fprintf") || len(call.Args) < 2 || call.Args[0] != selector {
				fixed = false
				return true
			}
			stderrCount++
		default:
			fixed = false
		}
		return true
	})
	return fixed && exitCount == 2 && stderrCount == 1
}

// assertNormalVMTestMainIsGuarded pins the one process-wide gate used by the
// normal VM lane. It deliberately rejects alternate TestMain declarations:
// a second test binary entry point could skip disposable-guest verification.
func assertNormalVMTestMainIsGuarded(t *testing.T, root string) {
	t.Helper()

	vmRoot := filepath.Join(root, vmSuitePath)
	goFiles, err := findGoFiles(vmRoot)
	if err != nil {
		t.Fatalf("discover normal VM TestMain sources: %v", err)
	}

	testMainCount := 0
	for _, path := range goFiles {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath(t, root, path), err)
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			t.Errorf("%s has an invalid //go:build constraint: %v", relativePath(t, root, path), err)
			continue
		}
		if !found || !expressionSatisfiable(expression, map[string]bool{
			vmBuildTag:          true,
			vmGuardUnitBuildTag: false,
		}) {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", relativePath(t, root, path), err)
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != "TestMain" {
				continue
			}
			testMainCount++
			if filepath.Base(path) != vmGuardNormalTestFile {
				t.Errorf("%s defines TestMain outside %s; normal vmtest execution must retain one auditable guard entry point", relativePath(t, root, path), vmGuardNormalTestFile)
				continue
			}
			if !vmTestMainIsFixed(file, importAliases(t, file)) {
				t.Errorf("%s must call requireDisposableGuest before marking verification or running tests; normal vmtest execution must fail closed", relativePath(t, root, path))
			}
		}
	}

	if testMainCount != 1 {
		t.Errorf("normal vmtest execution defines %d TestMain functions; want exactly one guarded entry point in %s", testMainCount, vmGuardNormalTestFile)
	}
}

func assertVMSourceHasNoPackageInitialization(t *testing.T, root, path, lane string) {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}
	if vmSourceHasPackageInitialization(file) {
		t.Errorf("%s defines an init function or initialized package variable; %s must perform no package initialization", relativePath(t, root, path), lane)
	}
}

func vmSourceHasPackageInitialization(file *ast.File) bool {
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Recv == nil && declaration.Name.Name == "init" {
				return true
			}
		case *ast.GenDecl:
			if declaration.Tok != token.VAR {
				continue
			}
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if ok && len(value.Values) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func assertDefaultPackageSelection(t *testing.T) {
	t.Helper()

	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-mod=readonly", "../../...")
	command.Env = hermeticGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -mod=readonly ./... without opt-in tags: %v\n%s", err, output)
	}

	for _, importPath := range strings.Fields(string(output)) {
		if strings.HasSuffix(importPath, "/tests/integration") || strings.HasSuffix(importPath, "/tests/vm") {
			t.Errorf("default go list selected opt-in package %q", importPath)
		}
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

func assertTaggedSuite(t *testing.T, root, suitePath, requiredTag string) {
	t.Helper()

	directory := filepath.Join(root, suitePath)
	goFiles, err := findGoFiles(directory)
	if err != nil {
		t.Fatalf("%s: %v", suitePath, err)
	}

	var testFileCount int
	for _, path := range goFiles {
		if strings.HasSuffix(path, "_test.go") {
			testFileCount++
		}

		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath(t, root, path), err)
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			t.Errorf("%s has an invalid //go:build constraint: %v", relativePath(t, root, path), err)
			continue
		}
		if !found {
			t.Errorf("%s is in the %s suite but has no //go:build %s constraint", relativePath(t, root, path), suitePath, requiredTag)
			continue
		}
		if !requiresBuildTag(expression, requiredTag) {
			t.Errorf("%s can be included without -tags=%s; %s must be opt-in", relativePath(t, root, path), requiredTag, suitePath)
		}
		if !canUseBuildTag(expression, requiredTag) {
			t.Errorf("%s cannot be selected with -tags=%s", relativePath(t, root, path), requiredTag)
		}
	}

	if testFileCount == 0 {
		t.Errorf("%s has no test files; establish the opt-in %s lane instead of leaving its safety boundary implicit", suitePath, requiredTag)
	}
}

func defaultLaneSourceFiles(root string) ([]string, error) {
	goFiles, err := findGoFiles(root)
	if err != nil {
		return nil, err
	}

	var defaultSources []string
	for _, path := range goFiles {
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		expression, found, err := buildExpression(source)
		if err != nil {
			return nil, err
		}
		if found && !canRunWithoutOptInTags(expression) {
			continue
		}
		defaultSources = append(defaultSources, path)
	}

	sort.Strings(defaultSources)
	return defaultSources, nil
}

func findGoFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: root, Err: os.ErrInvalid}
	}

	var paths []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}

func buildExpression(source []byte) (constraint.Expr, bool, error) {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if strings.HasPrefix(line, "//go:build ") {
			expression, err := constraint.Parse(line)
			return expression, true, err
		}
	}
	return nil, false, nil
}

func requiresBuildTag(expression constraint.Expr, tag string) bool {
	return !expressionSatisfiable(expression, map[string]bool{tag: false})
}

func canUseBuildTag(expression constraint.Expr, tag string) bool {
	return expressionSatisfiable(expression, map[string]bool{tag: true})
}

func canRunWithoutOptInTags(expression constraint.Expr) bool {
	return expressionSatisfiable(expression, map[string]bool{
		integrationBuildTag: false,
		vmBuildTag:          false,
	})
}

func expressionSatisfiable(expression constraint.Expr, fixed map[string]bool) bool {
	tagSet := make(map[string]struct{})
	collectBuildTags(expression, tagSet)

	var variables []string
	for tag := range tagSet {
		if _, isFixed := fixed[tag]; !isFixed {
			variables = append(variables, tag)
		}
	}
	sort.Strings(variables)
	if len(variables) >= strconv.IntSize {
		return false
	}

	possibilities := 1 << len(variables)
	for mask := 0; mask < possibilities; mask++ {
		values := make(map[string]bool, len(fixed)+len(variables))
		for tag, value := range fixed {
			values[tag] = value
		}
		for index, tag := range variables {
			values[tag] = mask&(1<<index) != 0
		}
		if expression.Eval(func(tag string) bool { return values[tag] }) {
			return true
		}
	}
	return false
}

func collectBuildTags(expression constraint.Expr, tags map[string]struct{}) {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		tags[expression.Tag] = struct{}{}
	case *constraint.NotExpr:
		collectBuildTags(expression.X, tags)
	case *constraint.AndExpr:
		collectBuildTags(expression.X, tags)
		collectBuildTags(expression.Y, tags)
	case *constraint.OrExpr:
		collectBuildTags(expression.X, tags)
		collectBuildTags(expression.Y, tags)
	}
}

func assertDefaultLaneSourceIsHostSafe(t *testing.T, root, path string) {
	t.Helper()

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", relativePath(t, root, path), err)
		return
	}

	aliases := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Errorf("parse import path in %s: %v", relativePath(t, root, path), err)
			continue
		}
		if reason, forbidden := defaultLaneForbiddenImports[importPath]; forbidden {
			t.Errorf("%s imports %q for %s; default tests must use recorded inputs and remain offline, unprivileged, and host-safe", relativePath(t, root, path), importPath, reason)
		}

		name := filepath.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." && (importPath == "os" || importPath == "os/exec") {
			t.Errorf("%s dot-imports %q; explicit qualification is required for the default-lane safety gate", relativePath(t, root, path), importPath)
			continue
		}
		if name != "_" {
			aliases[name] = importPath
		}
	}
	directExecConstructors := directExecConstructorSelectorPositions(file, aliases)

	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			assertDefaultLaneOSSelectorIsSafe(t, root, path, aliases, node)
			assertDefaultLaneProcessSelectorIsSafe(t, root, path, node)
			assertNoDefaultLaneExecCmdType(t, root, path, aliases, node)
			assertNoStoredExecConstructor(t, root, path, aliases, node, directExecConstructors)
		case *ast.CompositeLit:
			if isExecCmdCompositeLiteral(node, aliases) {
				t.Errorf("%s constructs os/exec.Cmd directly; default tests may launch only the Go tool or a binary built in t.TempDir/b.TempDir", relativePath(t, root, path))
			}
		case *ast.AssignStmt:
			assertNoExecConstructorValue(t, root, path, aliases, node.Rhs)
		case *ast.ValueSpec:
			assertNoExecConstructorValue(t, root, path, aliases, node.Values)
		case *ast.ReturnStmt:
			assertNoExecConstructorValue(t, root, path, aliases, node.Results)
		case *ast.CallExpr:
			assertNoExecConstructorValue(t, root, path, aliases, node.Args)
			assertDefaultLaneProcessCallIsSafe(t, root, path, file, aliases, node)
		}
		return true
	})
}

func directExecConstructorSelectorPositions(file *ast.File, aliases map[string]string) map[token.Pos]struct{} {
	positions := make(map[token.Pos]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && isExecConstructorSelector(selector, aliases) {
			positions[selector.Pos()] = struct{}{}
		}
		return true
	})
	return positions
}

func assertNoStoredExecConstructor(t *testing.T, root, path string, aliases map[string]string, selector *ast.SelectorExpr, directConstructors map[token.Pos]struct{}) {
	t.Helper()

	if !isExecConstructorSelector(selector, aliases) {
		return
	}
	if _, direct := directConstructors[selector.Pos()]; !direct {
		t.Errorf("%s stores, wraps, or passes an os/exec constructor; default tests must make an auditable direct invocation", relativePath(t, root, path))
	}
}

func assertNoDefaultLaneExecCmdType(t *testing.T, root, path string, aliases map[string]string, selector *ast.SelectorExpr) {
	t.Helper()

	packageName, ok := selector.X.(*ast.Ident)
	if ok && aliases[packageName.Name] == "os/exec" && selector.Sel.Name == "Cmd" {
		t.Errorf("%s references os/exec.Cmd directly; default tests may launch only the Go tool or a binary built in t.TempDir/b.TempDir", relativePath(t, root, path))
	}
}

func assertDefaultLaneOSSelectorIsSafe(t *testing.T, root, path string, aliases map[string]string, selector *ast.SelectorExpr) {
	t.Helper()

	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "os" {
		return
	}
	if reason, forbidden := defaultLaneForbiddenOSSelectors[selector.Sel.Name]; forbidden {
		t.Errorf("%s references os.%s for %s; default tests must remain offline, unprivileged, and host-safe", relativePath(t, root, path), selector.Sel.Name, reason)
	}
}

func assertNoExecConstructorValue(t *testing.T, root, path string, aliases map[string]string, expressions []ast.Expr) {
	t.Helper()

	for _, expression := range expressions {
		if isExecConstructorSelector(expression, aliases) {
			t.Errorf("%s stores or returns an os/exec constructor; default tests must make an auditable direct invocation", relativePath(t, root, path))
		}
	}
}

func assertDefaultLaneProcessCallIsSafe(t *testing.T, root, path string, file *ast.File, aliases map[string]string, call *ast.CallExpr) {
	t.Helper()

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "os/exec" {
		return
	}

	commandArgument := commandNameArgument(call, selector.Sel.Name)
	if commandArgument == nil {
		return
	}
	if defaultLaneGoCommandUsesTrustedToolchain(file, call.Pos(), commandArgument, aliases) {
		assertDefaultLaneGoCommandIsSafe(t, root, path, file, aliases, call, selector.Sel.Name)
		return
	}
	if command, ok := stringLiteral(commandArgument); ok {
		base := filepath.Base(command)
		if reason, forbidden := defaultLaneForbiddenCommands[base]; forbidden {
			t.Errorf("%s invokes %q via os/exec for %s; move that coverage to an explicitly tagged suite", relativePath(t, root, path), command, reason)
			return
		}
		t.Errorf("%s invokes %q via os/exec; default tests may launch only the Go tool or a binary built in t.TempDir/b.TempDir", relativePath(t, root, path), command)
		return
	}
	if !isTempDirLocalBinary(file, call.Pos(), commandArgument, aliases) {
		t.Errorf("%s invokes a dynamic executable via os/exec; default tests may launch only the Go tool or a binary built directly in t.TempDir/b.TempDir", relativePath(t, root, path))
		return
	}
	if !tempDirCommandLifetimeIsSafe(file, call) {
		t.Errorf("%s mutates or escapes a TempDir-local command after construction; default tests may execute only the exact locally built binary", relativePath(t, root, path))
	}
}

func assertDefaultLaneProcessSelectorIsSafe(t *testing.T, root, path string, selector *ast.SelectorExpr) {
	t.Helper()

	if reason, forbidden := defaultLaneForbiddenProcessMethods[selector.Sel.Name]; forbidden {
		t.Errorf("%s references .%s for %s; default tests must remain offline, unprivileged, and host-safe", relativePath(t, root, path), selector.Sel.Name, reason)
	}
}

func assertDefaultLaneGoCommandIsSafe(t *testing.T, root, path string, file *ast.File, aliases map[string]string, call *ast.CallExpr, method string) {
	t.Helper()

	arguments := commandArguments(call, method)
	if len(arguments) == 0 {
		t.Errorf("%s invokes go without a literal safe subcommand; default tests may use only hermetic go list, go build, or the guarded VM go test", relativePath(t, root, path))
		return
	}
	subcommand, ok := stringLiteral(arguments[0])
	if !ok {
		t.Errorf("%s invokes go with a dynamic subcommand; default tests may use only hermetic go list, go build, or the guarded VM go test", relativePath(t, root, path))
		return
	}
	if !goCommandUsesApprovedHermeticEnvironment(path, file, call, aliases) {
		t.Errorf("%s invokes go without an approved hermetic command environment; default tests must set local toolchain, offline proxy, workspace-off, and empty GOFLAGS", relativePath(t, root, path))
	}

	switch subcommand {
	case "build":
		assertDefaultLaneGoBuildIsSafe(t, root, path, file, aliases, call, arguments[1:])
	case "list":
		assertDefaultLaneGoListIsSafe(t, root, path, arguments[1:])
	case "test":
		assertDefaultLaneGoTestIsSafe(t, root, path, arguments[1:])
	default:
		t.Errorf("%s invokes unsupported go subcommand %q; default tests may use only hermetic go list, go build, or the guarded VM go test", relativePath(t, root, path), subcommand)
	}
}

func assertDefaultLaneGoBuildIsSafe(t *testing.T, root, path string, file *ast.File, aliases map[string]string, call *ast.CallExpr, arguments []ast.Expr) {
	t.Helper()

	var hasReadonly, hasTrimpath, hasOutput, hasCommandPackage bool
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		value, literal := stringLiteral(argument)
		if !literal {
			t.Errorf("%s passes a dynamic argument to go build; only the temporary output path may be dynamic", relativePath(t, root, path))
			continue
		}
		switch value {
		case "-mod=readonly":
			hasReadonly = true
		case "-trimpath":
			hasTrimpath = true
		case "-o":
			hasOutput = true
			index++
			if index >= len(arguments) || !isTempDirLocalBinary(file, call.Pos(), arguments[index], aliases) {
				t.Errorf("%s sends go build output outside a directly verified t.TempDir/b.TempDir path", relativePath(t, root, path))
			}
		default:
			if _, allowed := defaultLaneGoBuildPackages[value]; allowed {
				hasCommandPackage = true
				continue
			}
			t.Errorf("%s passes unsupported go build argument %q; default tests may build only a local command package into TempDir", relativePath(t, root, path), value)
		}
	}
	if !hasReadonly || !hasTrimpath || !hasOutput || !hasCommandPackage {
		t.Errorf("%s must use go build -mod=readonly -trimpath -o <TempDir binary> ../../cmd/... in the default lane", relativePath(t, root, path))
	}
}

func assertDefaultLaneGoListIsSafe(t *testing.T, root, path string, arguments []ast.Expr) {
	t.Helper()

	if len(arguments) == 1 {
		if value, ok := stringLiteral(arguments[0]); ok && value == "std" {
			return
		}
	}

	var hasReadonly, hasPackage bool
	for _, argument := range arguments {
		value, literal := stringLiteral(argument)
		if !literal {
			t.Errorf("%s passes a dynamic go list flag; default tests must use a fixed read-only query", relativePath(t, root, path))
			continue
		}
		switch value {
		case "-deps", "-json":
		case "-mod=readonly":
			hasReadonly = true
		default:
			if _, allowed := defaultLaneGoListPackages[value]; allowed {
				hasPackage = true
				continue
			}
			t.Errorf("%s passes unsupported go list argument %q; default tests must use a fixed read-only query", relativePath(t, root, path), value)
		}
	}
	if !hasReadonly || !hasPackage {
		t.Errorf("%s must use go list -mod=readonly against ../../... or ../../cmd/... in the default lane", relativePath(t, root, path))
	}
}

func assertDefaultLaneGoTestIsSafe(t *testing.T, root, path string, arguments []ast.Expr) {
	t.Helper()

	var hasReadonly, hasVMTag, hasRun, hasCount, hasVerbose, hasVMPackage bool
	for index := 0; index < len(arguments); index++ {
		value, literal := stringLiteral(arguments[index])
		if !literal {
			t.Errorf("%s passes a dynamic argument to go test; only the fixed guard-refusal probe is safe in the default lane", relativePath(t, root, path))
			continue
		}
		switch value {
		case "-mod=readonly":
			hasReadonly = true
		case "-tags=vmtest":
			hasVMTag = true
		case "-run":
			hasRun = true
			index++
			if index >= len(arguments) {
				t.Errorf("%s passes -run to go test without the fixed VM guard probe", relativePath(t, root, path))
				continue
			}
			pattern, ok := stringLiteral(arguments[index])
			if !ok || pattern != "^TestVMTestRequiresBothGuards$" {
				t.Errorf("%s uses go test -run outside the fixed VM guard-refusal probe", relativePath(t, root, path))
			}
		case "-count=1":
			hasCount = true
		case "-v":
			hasVerbose = true
		case "../../tests/vm":
			hasVMPackage = true
		default:
			t.Errorf("%s passes unsupported go test argument %q; only the fixed VM guard-refusal probe is safe in the default lane", relativePath(t, root, path), value)
		}
	}
	if !hasReadonly || !hasVMTag || !hasRun || !hasCount || !hasVerbose || !hasVMPackage {
		t.Errorf("%s must use the fixed read-only VM guard-refusal probe in the default lane", relativePath(t, root, path))
	}
}

func commandArguments(call *ast.CallExpr, method string) []ast.Expr {
	switch method {
	case "Command":
		if len(call.Args) > 1 {
			return call.Args[1:]
		}
	case "CommandContext":
		if len(call.Args) > 2 {
			return call.Args[2:]
		}
	}
	return nil
}

var approvedHermeticEnvironmentFunctions = map[string]struct{}{
	"hermeticGoEnv":         {},
	"hermeticGoEnvironment": {},
	"hermeticVMGoEnv":       {},
	"localGoEnvironment":    {},
}

func goCommandUsesApprovedHermeticEnvironment(sourcePath string, file *ast.File, call *ast.CallExpr, aliases map[string]string) bool {
	function, ok := enclosingFunction(file, call.Pos())
	if !ok || isInsideFunctionLiteral(file, call.Pos()) || functionContainsFunctionLiteral(function) || functionContainsGotoOrLabel(function) {
		return false
	}
	commandName, ok := commandAssignmentName(function, call)
	if !ok {
		return false
	}

	foundEnvironment := false
	configurationSafe := true
	allowedEnvironmentSelectors := make(map[token.Pos]struct{})
	inspectFunctionBody(function.Body, func(node ast.Node) {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return
		}
		for index, left := range assignment.Lhs {
			selector, ok := left.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if !isCommandReceiver(selector.X, commandName) {
				continue
			}
			if isInsideConditionalControlFlow(function, assignment.Pos()) {
				configurationSafe = false
				continue
			}
			switch selector.Sel.Name {
			case "Env":
				if selector.Pos() <= call.End() || foundEnvironment {
					configurationSafe = false
					continue
				}
				foundEnvironment = true
				if index >= len(assignment.Rhs) || !isApprovedHermeticEnvironment(sourcePath, file, assignment.Rhs[index]) {
					configurationSafe = false
					continue
				}
				allowedEnvironmentSelectors[selector.Pos()] = struct{}{}
			case "Args", "Dir", "Path", "Process", "SysProcAttr":
				configurationSafe = false
			}
		}
	})

	inspectFunctionBody(function.Body, func(node ast.Node) {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return
		}
		if !isCommandReceiver(selector.X, commandName) {
			return
		}
		switch selector.Sel.Name {
		case "Env":
			if _, allowed := allowedEnvironmentSelectors[selector.Pos()]; !allowed {
				configurationSafe = false
			}
		case "Args", "Dir", "Path", "Process", "SysProcAttr":
			configurationSafe = false
		}
	})

	if commandEscapes(function, commandName) {
		configurationSafe = false
	}
	if commandIsReassigned(function, commandName, call) {
		configurationSafe = false
	}
	if commandReferencesForbiddenExecutionConfiguration(function, commandName) {
		configurationSafe = false
	}
	if commandUsesExecutionMethodUnsafely(function, commandName, allowedEnvironmentSelectors) {
		configurationSafe = false
	}
	if functionMutatesProcessEnvironment(function) {
		configurationSafe = false
	}

	return foundEnvironment && configurationSafe
}

func functionMutatesProcessEnvironment(function *ast.FuncDecl) bool {
	mutatesEnvironment := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return
		}
		switch selector.Sel.Name {
		case "Clearenv", "Setenv", "Unsetenv":
			mutatesEnvironment = true
		}
	})
	return mutatesEnvironment
}

func isCommandReceiver(expression ast.Expr, commandName string) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name == commandName
	case *ast.ParenExpr:
		return isCommandReceiver(expression.X, commandName)
	case *ast.StarExpr:
		return isCommandReceiver(expression.X, commandName)
	case *ast.UnaryExpr:
		return expression.Op == token.AND && isCommandReceiver(expression.X, commandName)
	default:
		return false
	}
}

func isInsideConditionalControlFlow(function *ast.FuncDecl, position token.Pos) bool {
	inside := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if node == nil || position < node.Pos() || node.End() < position {
			return false
		}
		switch node.(type) {
		case *ast.CaseClause, *ast.CommClause, *ast.DeferStmt, *ast.ForStmt, *ast.GoStmt, *ast.IfStmt, *ast.RangeStmt, *ast.SelectStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
			inside = true
			return false
		}
		return true
	})
	return inside
}

func commandIsReassigned(function *ast.FuncDecl, commandName string, constructor *ast.CallExpr) bool {
	reassigned := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			for index, left := range node.Lhs {
				if !isCommandReceiver(left, commandName) {
					continue
				}
				if index >= len(node.Rhs) || !sameExpression(node.Rhs[index], constructor) {
					reassigned = true
				}
			}
		case *ast.ValueSpec:
			for index, identifier := range node.Names {
				if identifier.Name != commandName {
					continue
				}
				if index >= len(node.Values) || !sameExpression(node.Values[index], constructor) {
					reassigned = true
				}
			}
		case *ast.RangeStmt:
			if node.Tok != token.DEFINE {
				return
			}
			for _, expression := range []ast.Expr{node.Key, node.Value} {
				identifier, ok := expression.(*ast.Ident)
				if ok && identifier.Name == commandName {
					reassigned = true
				}
			}
		case *ast.TypeSwitchStmt:
			assignment, ok := node.Assign.(*ast.AssignStmt)
			if !ok || assignment.Tok != token.DEFINE {
				return
			}
			for _, expression := range assignment.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if ok && identifier.Name == commandName {
					reassigned = true
				}
			}
		}
	})
	return reassigned
}

func commandReferencesForbiddenExecutionConfiguration(function *ast.FuncDecl, commandName string) bool {
	forbiddenConfiguration := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !isCommandReceiver(selector.X, commandName) {
			return
		}
		switch selector.Sel.Name {
		case "Args", "Dir", "Path", "Process", "SysProcAttr":
			forbiddenConfiguration = true
		}
	})
	return forbiddenConfiguration
}

func sameExpression(left ast.Expr, right ast.Expr) bool {
	return left.Pos() == right.Pos() && left.End() == right.End()
}

func commandUsesExecutionMethodUnsafely(function *ast.FuncDecl, commandName string, allowedEnvironmentSelectors map[token.Pos]struct{}) bool {
	if commandUsesExecutionMethodValue(function, commandName) {
		return true
	}
	var environmentPosition token.Pos
	for position := range allowedEnvironmentSelectors {
		if environmentPosition == token.NoPos || position < environmentPosition {
			environmentPosition = position
		}
	}
	if environmentPosition == token.NoPos {
		return true
	}

	unsafeExecutionMethodUse := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		if !isCommandReceiver(selector.X, commandName) {
			return
		}
		switch selector.Sel.Name {
		case "CombinedOutput", "Output", "Run", "Start", "Wait":
			if call.Pos() < environmentPosition || isInsideConditionalControlFlow(function, call.Pos()) {
				unsafeExecutionMethodUse = true
			}
		}
	})
	return unsafeExecutionMethodUse
}

func commandUsesExecutionMethodValue(function *ast.FuncDecl, commandName string) bool {
	directExecutionSelectors := make(map[token.Pos]struct{})
	inspectFunctionBody(function.Body, func(node ast.Node) {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isCommandReceiver(selector.X, commandName) {
			return
		}
		switch selector.Sel.Name {
		case "CombinedOutput", "Output", "Run", "Start", "Wait":
			directExecutionSelectors[selector.Pos()] = struct{}{}
		}
	})

	methodValueUsed := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return
		}
		if !isCommandReceiver(selector.X, commandName) {
			return
		}
		switch selector.Sel.Name {
		case "CombinedOutput", "Output", "Run", "Start", "Wait":
			if _, direct := directExecutionSelectors[selector.Pos()]; !direct {
				methodValueUsed = true
			}
		}
	})
	return methodValueUsed
}

func commandAssignmentName(function *ast.FuncDecl, call *ast.CallExpr) (string, bool) {
	var name string
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return
			}
			for index, value := range node.Rhs {
				if value.Pos() != call.Pos() || value.End() != call.End() || index >= len(node.Lhs) {
					continue
				}
				identifier, ok := node.Lhs[index].(*ast.Ident)
				if ok && identifierDirectlyDeclaresAt(identifier, node) {
					name = identifier.Name
				}
			}
		case *ast.ValueSpec:
			for index, value := range node.Values {
				if value.Pos() != call.Pos() || value.End() != call.End() || index >= len(node.Names) {
					continue
				}
				if identifierDirectlyDeclaresAt(node.Names[index], node) {
					name = node.Names[index].Name
				}
			}
		}
	})
	return name, name != ""
}

func identifierDirectlyDeclaresAt(identifier *ast.Ident, declaration ast.Node) bool {
	return identifier.Obj != nil && identifier.Obj.Decl == declaration
}

func commandEscapes(function *ast.FuncDecl, commandName string) bool {
	escaped := false
	inspectExpressions := func(expressions []ast.Expr) {
		for _, expression := range expressions {
			if containsEscapingCommandIdentifier(expression, commandName) {
				escaped = true
			}
		}
	}
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			inspectExpressions(node.Rhs)
		case *ast.ValueSpec:
			inspectExpressions(node.Values)
		case *ast.ReturnStmt:
			inspectExpressions(node.Results)
		case *ast.CallExpr:
			inspectExpressions(node.Args)
		}
	})
	return escaped
}

func containsEscapingCommandIdentifier(expression ast.Expr, name string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok {
			if isCommandReceiver(selector.X, name) {
				return false
			}
			return true
		}
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func functionContainsFunctionLiteral(function *ast.FuncDecl) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func functionContainsGotoOrLabel(function *ast.FuncDecl) bool {
	found := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.BranchStmt:
			if node.Tok == token.GOTO {
				found = true
			}
		case *ast.LabeledStmt:
			found = true
		}
	})
	return found
}

func isApprovedHermeticEnvironment(sourcePath string, file *ast.File, expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	function, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	if _, approved := approvedHermeticEnvironmentFunctions[function.Name]; !approved {
		return false
	}
	declaration, ok := sameFileFunctionDeclaration(file, function)
	if !ok {
		return false
	}
	return hasSingleHermeticEnvironmentReturn(sourcePath, file, declaration)
}

var requiredHermeticEnvironmentValues = map[string]string{
	"GOFLAGS":              "GOFLAGS=",
	"GOPROXY":              "GOPROXY=off",
	"GOTOOLCHAIN":          "GOTOOLCHAIN=local",
	"GOROOT":               "GOROOT=",
	"GOWORK":               "GOWORK=off",
	"LDCLEAN_VMTEST":       "LDCLEAN_VMTEST=",
	"LDCLEAN_VMTEST_TOKEN": "LDCLEAN_VMTEST_TOKEN=",
	"PATH":                 "PATH=/usr/bin:/bin",
}

func hermeticEnvironmentFunctionIsSafe(sourcePath, name string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		return false
	}
	for _, candidate := range file.Decls {
		declaration, ok := candidate.(*ast.FuncDecl)
		if !ok || declaration.Name.Name != name {
			continue
		}
		return hasSingleHermeticEnvironmentReturn(sourcePath, file, declaration)
	}
	return false
}

func sameFileFunctionDeclaration(file *ast.File, function *ast.Ident) (*ast.FuncDecl, bool) {
	if function.Obj == nil || function.Obj.Kind != ast.Fun {
		return nil, false
	}
	declaration, ok := function.Obj.Decl.(*ast.FuncDecl)
	if !ok || declaration.Recv != nil || declaration.Name.Obj != function.Obj {
		return nil, false
	}
	for _, candidate := range file.Decls {
		if candidate == declaration {
			return declaration, true
		}
	}
	return nil, false
}

func hasSingleHermeticEnvironmentReturn(sourcePath string, file *ast.File, declaration *ast.FuncDecl) bool {
	if declaration == nil || declaration.Body == nil || fileHasDotImport(file) || packageDeclaresName(sourcePath, file.Name.Name, "append") {
		return false
	}
	var returns []*ast.ReturnStmt
	hasFunctionLiteral := false
	ast.Inspect(declaration.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ReturnStmt:
			returns = append(returns, node)
		case *ast.FuncLit:
			hasFunctionLiteral = true
		}
		return true
	})
	if hasFunctionLiteral || len(returns) != 1 || len(returns[0].Results) != 1 {
		return false
	}

	appendCall, ok := returns[0].Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	appendFunction, ok := appendCall.Fun.(*ast.Ident)
	if !ok || appendFunction.Name != "append" || appendFunction.Obj != nil || fileImportsName(file, "append") || len(appendCall.Args) != len(requiredHermeticEnvironmentValues)+1 {
		return false
	}

	foundValues := make(map[string]bool)
	for _, argument := range appendCall.Args[1:] {
		value, ok := stringLiteral(argument)
		if !ok {
			return false
		}
		for key, required := range requiredHermeticEnvironmentValues {
			if value == required {
				foundValues[key] = true
			} else if strings.HasPrefix(value, key+"=") {
				return false
			}
		}
	}
	return len(foundValues) == len(requiredHermeticEnvironmentValues)
}

func fileHasDotImport(file *ast.File) bool {
	return fileImportsName(file, ".")
}

func fileImportsName(file *ast.File, name string) bool {
	for _, spec := range file.Imports {
		importName := ""
		if spec.Name != nil {
			importName = spec.Name.Name
		} else if importPath, err := strconv.Unquote(spec.Path.Value); err == nil {
			importName = filepath.Base(importPath)
		}
		if importName == name {
			return true
		}
	}
	return false
}

func packageDeclaresName(sourcePath, packageName, name string) bool {
	entries, err := os.ReadDir(filepath.Dir(sourcePath))
	if err != nil {
		return true
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(sourcePath), entry.Name()), nil, 0)
		if err != nil {
			return true
		}
		if file.Name.Name != packageName {
			continue
		}
		for _, candidate := range file.Decls {
			switch candidate := candidate.(type) {
			case *ast.FuncDecl:
				if candidate.Name.Name == name {
					return true
				}
			case *ast.GenDecl:
				for _, specification := range candidate.Specs {
					switch specification := specification.(type) {
					case *ast.TypeSpec:
						if specification.Name.Name == name {
							return true
						}
					case *ast.ValueSpec:
						if identifiersDeclareName(specification.Names, name) {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func isExecCmdCompositeLiteral(literal *ast.CompositeLit, aliases map[string]string) bool {
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == "os/exec" && selector.Sel.Name == "Cmd"
}

func isExecConstructorSelector(expression ast.Expr, aliases map[string]string) bool {
	expression = unparenthesizedExpression(expression)
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "os/exec" {
		return false
	}
	return selector.Sel.Name == "Command" || selector.Sel.Name == "CommandContext"
}

func unparenthesizedExpression(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func tempDirCommandLifetimeIsSafe(file *ast.File, call *ast.CallExpr) bool {
	function, ok := enclosingFunction(file, call.Pos())
	if !ok || isInsideFunctionLiteral(file, call.Pos()) || functionContainsFunctionLiteral(function) {
		return false
	}
	commandName, ok := commandAssignmentName(function, call)
	if !ok {
		return false
	}
	return !commandEscapes(function, commandName) &&
		!commandIsReassigned(function, commandName, call) &&
		!commandReferencesForbiddenExecutionConfiguration(function, commandName) &&
		!commandUsesExecutionMethodValue(function, commandName)
}

func isTempDirLocalBinary(file *ast.File, position token.Pos, expression ast.Expr, aliases map[string]string) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok || identifier.Obj == nil {
		return false
	}
	function, ok := enclosingFunction(file, position)
	if !ok || isInsideFunctionLiteral(file, position) {
		return false
	}
	if functionContainsFunctionLiteral(function) || tempDirVariableAddressTaken(function, identifier.Name) || tempDirVariableIsReassigned(function, identifier.Obj) {
		return false
	}
	handles := testingHandleParameters(function, aliases)
	if len(handles) == 0 || hasShadowedTestingHandle(function, handles) {
		return false
	}

	definition, ok := tempDirLocalBinaryDefinition(function, identifier)
	return ok && isTempDirJoin(function, definition, aliases, handles)
}

func tempDirLocalBinaryDefinition(function *ast.FuncDecl, identifier *ast.Ident) (ast.Expr, bool) {
	switch declaration := identifier.Obj.Decl.(type) {
	case *ast.AssignStmt:
		if declaration.Tok != token.DEFINE || !declarationIsInFunctionBody(function, declaration) {
			return nil, false
		}
		for index, name := range declaration.Lhs {
			declared, ok := name.(*ast.Ident)
			if !ok || declared.Obj != identifier.Obj || index >= len(declaration.Rhs) {
				continue
			}
			return declaration.Rhs[index], true
		}
	case *ast.ValueSpec:
		if !declarationIsInFunctionBody(function, declaration) {
			return nil, false
		}
		for index, name := range declaration.Names {
			if name.Obj != identifier.Obj || index >= len(declaration.Values) {
				continue
			}
			return declaration.Values[index], true
		}
	}
	return nil, false
}

func declarationIsInFunctionBody(function *ast.FuncDecl, declaration ast.Node) bool {
	return function.Body != nil && function.Body.Pos() <= declaration.Pos() && declaration.End() <= function.Body.End()
}

func tempDirVariableAddressTaken(function *ast.FuncDecl, name string) bool {
	addressTaken := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		unary, ok := node.(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return
		}
		if isCommandReceiver(unary.X, name) {
			addressTaken = true
		}
	})
	return addressTaken
}

func tempDirVariableIsReassigned(function *ast.FuncDecl, object *ast.Object) bool {
	rebound := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			for _, expression := range node.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if !ok || identifier.Obj != object {
					continue
				}
				if node.Tok == token.DEFINE && identifierDirectlyDeclaresAt(identifier, node) {
					continue
				}
				if identifier.Obj == object {
					rebound = true
				}
			}
		case *ast.ValueSpec:
			for _, identifier := range node.Names {
				if identifier.Obj == object && identifier.Obj.Decl != node {
					rebound = true
				}
			}
		case *ast.RangeStmt:
			for _, expression := range []ast.Expr{node.Key, node.Value} {
				identifier, ok := expression.(*ast.Ident)
				if ok && identifier.Obj == object {
					rebound = true
				}
			}
		case *ast.TypeSwitchStmt:
			assignment, ok := node.Assign.(*ast.AssignStmt)
			if !ok {
				return
			}
			for _, expression := range assignment.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if ok && identifier.Obj == object {
					rebound = true
				}
			}
		}
	})
	return rebound
}

func isTempDirJoin(function *ast.FuncDecl, expression ast.Expr, aliases map[string]string, handles map[string]struct{}) bool {
	join, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := join.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Join" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "path/filepath" || hasShadowedImportAlias(function, packageName.Name) || len(join.Args) < 2 {
		return false
	}

	temporaryDirectory, ok := join.Args[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	temporaryDirectorySelector, ok := temporaryDirectory.Fun.(*ast.SelectorExpr)
	if !ok || temporaryDirectorySelector.Sel.Name != "TempDir" {
		return false
	}
	receiver, ok := temporaryDirectorySelector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = handles[receiver.Name]
	if !ok {
		return false
	}
	for _, argument := range join.Args[1:] {
		name, ok := stringLiteral(argument)
		if !ok || !isSafeTempDirBasename(name) {
			return false
		}
	}
	return true
}

func hasShadowedImportAlias(function *ast.FuncDecl, name string) bool {
	if name == "" || name == "_" || name == "." {
		return false
	}
	if fieldListDeclaresName(function.Recv, name) ||
		fieldListDeclaresName(function.Type.TypeParams, name) ||
		fieldListDeclaresName(function.Type.Params, name) ||
		fieldListDeclaresName(function.Type.Results, name) {
		return true
	}

	shadowed := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE && expressionsDeclareName(node.Lhs, name) {
				shadowed = true
			}
		case *ast.ValueSpec:
			if identifiersDeclareName(node.Names, name) {
				shadowed = true
			}
		case *ast.TypeSpec:
			if node.Name.Name == name {
				shadowed = true
			}
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE && expressionsDeclareName([]ast.Expr{node.Key, node.Value}, name) {
				shadowed = true
			}
		case *ast.TypeSwitchStmt:
			assignment, ok := node.Assign.(*ast.AssignStmt)
			if ok && assignment.Tok == token.DEFINE && expressionsDeclareName(assignment.Lhs, name) {
				shadowed = true
			}
		}
	})
	return shadowed
}

func fileHasShadowedImportAlias(file *ast.File, name string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && hasShadowedImportAlias(function, name) {
			return true
		}
	}
	return false
}

func fieldListDeclaresName(fields *ast.FieldList, name string) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if identifiersDeclareName(field.Names, name) {
			return true
		}
	}
	return false
}

func identifiersDeclareName(identifiers []*ast.Ident, name string) bool {
	for _, identifier := range identifiers {
		if identifier.Name == name {
			return true
		}
	}
	return false
}

func expressionsDeclareName(expressions []ast.Expr, name string) bool {
	for _, expression := range expressions {
		identifier, ok := expression.(*ast.Ident)
		if ok && identifier.Name == name {
			return true
		}
	}
	return false
}

func isSafeTempDirBasename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

func enclosingFunction(file *ast.File, position token.Pos) (*ast.FuncDecl, bool) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		if function.Body.Pos() <= position && position <= function.Body.End() {
			return function, true
		}
	}
	return nil, false
}

func isInsideFunctionLiteral(file *ast.File, position token.Pos) bool {
	inside := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok || literal.Body == nil {
			return true
		}
		if literal.Body.Pos() <= position && position <= literal.Body.End() {
			inside = true
			return false
		}
		return true
	})
	return inside
}

func testingHandleParameters(function *ast.FuncDecl, aliases map[string]string) map[string]struct{} {
	handles := make(map[string]struct{})
	if function.Type.Params == nil {
		return handles
	}
	for _, field := range function.Type.Params.List {
		if !isTestingHandleType(field.Type, aliases) {
			continue
		}
		for _, name := range field.Names {
			handles[name.Name] = struct{}{}
		}
	}
	return handles
}

func isTestingHandleType(expression ast.Expr, aliases map[string]string) bool {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "T" && selector.Sel.Name != "B" && selector.Sel.Name != "M") {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == "testing"
}

func hasShadowedTestingHandle(function *ast.FuncDecl, handles map[string]struct{}) bool {
	shadowed := false
	inspectFunctionBody(function.Body, func(node ast.Node) {
		switch node := node.(type) {
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return
			}
			for _, expression := range node.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if !ok {
					continue
				}
				if _, isHandle := handles[identifier.Name]; isHandle {
					shadowed = true
				}
			}
		case *ast.ValueSpec:
			for _, identifier := range node.Names {
				if _, isHandle := handles[identifier.Name]; isHandle {
					shadowed = true
				}
			}
		case *ast.RangeStmt:
			if node.Tok != token.DEFINE {
				return
			}
			for _, expression := range []ast.Expr{node.Key, node.Value} {
				identifier, ok := expression.(*ast.Ident)
				if !ok {
					continue
				}
				if _, isHandle := handles[identifier.Name]; isHandle {
					shadowed = true
				}
			}
		case *ast.TypeSwitchStmt:
			assignment, ok := node.Assign.(*ast.AssignStmt)
			if !ok || assignment.Tok != token.DEFINE {
				return
			}
			for _, expression := range assignment.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if !ok {
					continue
				}
				if _, isHandle := handles[identifier.Name]; isHandle {
					shadowed = true
				}
			}
		}
	})
	return shadowed
}

func inspectFunctionBody(body *ast.BlockStmt, visit func(ast.Node)) {
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		visit(node)
		return true
	})
}

func commandNameArgument(call *ast.CallExpr, method string) ast.Expr {
	switch method {
	case "Command":
		if len(call.Args) > 0 {
			return call.Args[0]
		}
	case "CommandContext":
		if len(call.Args) > 1 {
			return call.Args[1]
		}
	}
	return nil
}

func defaultLaneGoCommandUsesTrustedToolchain(file *ast.File, position token.Pos, expression ast.Expr, aliases map[string]string) bool {
	function, ok := enclosingFunction(file, position)
	filepathAlias, runtimeAlias, trusted := defaultLaneGoExecutableImportAliases(expression, aliases)
	return ok && trusted &&
		!hasShadowedImportAlias(function, filepathAlias) &&
		!hasShadowedImportAlias(function, runtimeAlias)
}

func isDefaultLaneGoExecutable(expression ast.Expr, aliases map[string]string) bool {
	_, _, trusted := defaultLaneGoExecutableImportAliases(expression, aliases)
	return trusted
}

func defaultLaneGoExecutableImportAliases(expression ast.Expr, aliases map[string]string) (string, string, bool) {
	join, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok {
		return "", "", false
	}
	selector, ok := join.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Join" || len(join.Args) != 3 {
		return "", "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "path/filepath" {
		return "", "", false
	}
	runtimeAlias, ok := runtimeGOROOTImportAlias(join.Args[0], aliases)
	if !ok {
		return "", "", false
	}
	second, secondIsString := stringLiteral(join.Args[1])
	third, thirdIsString := stringLiteral(join.Args[2])
	return packageName.Name, runtimeAlias, secondIsString && thirdIsString && second == "bin" && third == "go"
}

func runtimeGOROOTImportAlias(expression ast.Expr, aliases map[string]string) (string, bool) {
	call, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "GOROOT" {
		return "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "runtime" {
		return "", false
	}
	return packageName.Name, true
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}

	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func relativePath(t *testing.T, root, path string) string {
	t.Helper()

	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relative path from %q to %q: %v", root, path, err)
	}
	return filepath.ToSlash(relative)
}
