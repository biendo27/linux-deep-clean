package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	mainCommandPath   = "cmd/ldclean"
	helperCommandPath = "cmd/linux-deep-clean-helper"
)

type listedPackage struct {
	ImportPath string
	Imports    []string
	Standard   bool
	Error      *struct {
		Err string
	}
}

// TestArchitectureImportAllowlists keeps the Phase 1 executable boundaries
// explicit. It deliberately fails until both command directories exist.
func TestArchitectureImportAllowlists(t *testing.T) {
	root := repositoryRoot(t)
	modulePath := modulePath(t, root)
	standardImports := standardLibraryImports(t)

	assertNoPkgTree(t, root)
	assertProjectDependencyGraph(t)
	assertNoExecutableShellScripts(t, root)
	assertNoRuntimeShellScripts(t, root)
	assertBootstrapRuntimeSafety(t, root)
	assertApplicationImports(t, root, modulePath, standardImports)
	assertPresenterImports(t, root, modulePath, standardImports)
	assertDomainImports(t, root, standardImports)
	assertCobraPresenterOnly(t, root)

	mainPackages, mainListed := listMainCommandDependencies(t, root)
	if mainListed {
		assertMainCommandImports(t, modulePath, mainPackages)
	}

	helperPackages, helperListed := listHelperCommandDependencies(t, root)
	if helperListed {
		assertHelperDependencies(t, modulePath, helperPackages)
	}
}

func standardLibraryImports(t *testing.T) map[string]struct{} {
	t.Helper()

	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "std")
	command.Env = localGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list std: %v\n%s", err, output)
	}

	imports := make(map[string]struct{})
	for _, importPath := range strings.Fields(string(output)) {
		imports[importPath] = struct{}{}
	}
	if len(imports) == 0 {
		t.Fatal("go list std returned no standard-library packages")
	}
	return imports
}

func assertProjectDependencyGraph(t *testing.T) {
	t.Helper()

	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-deps", "-mod=readonly", "../../...")
	command.Env = localGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps -mod=readonly ./... (project dependency graph): %v\n%s", err, output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		goMod := filepath.Join(directory, "go.mod")
		if info, err := os.Stat(goMod); err == nil && !info.IsDir() {
			return directory
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}

	t.Fatal("could not find repository go.mod")
	return ""
}

func modulePath(t *testing.T, root string) string {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}

		path := fields[1]
		if strings.HasPrefix(path, "\"") {
			unquoted, err := strconv.Unquote(path)
			if err != nil {
				t.Fatalf("parse module path %q: %v", path, err)
			}
			path = unquoted
		}
		return path
	}

	t.Fatal("go.mod has no module declaration")
	return ""
}

func listMainCommandDependencies(t *testing.T, root string) (map[string]listedPackage, bool) {
	t.Helper()

	if !commandDirectoryExists(t, root, mainCommandPath) {
		return nil, false
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.Command(
		filepath.Join(runtime.GOROOT(), "bin", "go"),
		"list",
		"-deps",
		"-json",
		"-mod=readonly",
		"../../cmd/ldclean",
	)
	command.Env = localGoEnvironment(os.Environ())
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		t.Errorf("go list -deps -json ../../cmd/ldclean: %v\nstderr:\n%s", runErr, strings.TrimSpace(stderr.String()))
		return nil, false
	}

	return decodeCommandDependencies(t, mainCommandPath, &stdout)
}

func listHelperCommandDependencies(t *testing.T, root string) (map[string]listedPackage, bool) {
	t.Helper()

	if !commandDirectoryExists(t, root, helperCommandPath) {
		return nil, false
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.Command(
		filepath.Join(runtime.GOROOT(), "bin", "go"),
		"list",
		"-deps",
		"-json",
		"-mod=readonly",
		"../../cmd/linux-deep-clean-helper",
	)
	command.Env = localGoEnvironment(os.Environ())
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if runErr != nil {
		t.Errorf("go list -deps -json ../../cmd/linux-deep-clean-helper: %v\nstderr:\n%s", runErr, strings.TrimSpace(stderr.String()))
		return nil, false
	}

	return decodeCommandDependencies(t, helperCommandPath, &stdout)
}

func commandDirectoryExists(t *testing.T, root, commandPath string) bool {
	t.Helper()

	commandDirectory := filepath.Join(root, filepath.FromSlash(commandPath))
	info, err := os.Stat(commandDirectory)
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("required command directory %q is missing", commandPath)
		return false
	}
	if err != nil {
		t.Errorf("stat command directory %q: %v", commandPath, err)
		return false
	}
	if !info.IsDir() {
		t.Errorf("required command path %q is not a directory", commandPath)
		return false
	}
	return true
}

func decodeCommandDependencies(t *testing.T, commandPath string, stdout io.Reader) (map[string]listedPackage, bool) {
	t.Helper()

	packages := make(map[string]listedPackage)
	decoder := json.NewDecoder(stdout)
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Errorf("decode go list output for ../../%s: %v", filepath.ToSlash(commandPath), err)
			return nil, false
		}
		if pkg.ImportPath == "" {
			t.Errorf("go list -deps -json ../../%s returned a package without an import path", filepath.ToSlash(commandPath))
			return nil, false
		}
		if pkg.Error != nil {
			t.Errorf("go list -deps -json ../../%s reports %s: %s", filepath.ToSlash(commandPath), pkg.ImportPath, pkg.Error.Err)
			return nil, false
		}
		packages[pkg.ImportPath] = pkg
	}

	if len(packages) == 0 {
		t.Errorf("go list -deps -json ../../%s returned no packages", filepath.ToSlash(commandPath))
		return nil, false
	}

	return packages, true
}

func localGoEnvironment(environment []string) []string {
	local := make([]string, 0, len(environment)+8)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GOFLAGS" || key == "GOPROXY" || key == "GOTOOLCHAIN" || key == "GOWORK" || key == "GOROOT" || key == "PATH" || key == "LDCLEAN_VMTEST" || key == "LDCLEAN_VMTEST_TOKEN" {
			continue
		}
		local = append(local, entry)
	}

	return append(local, "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "GOROOT=", "PATH=/usr/bin:/bin", "LDCLEAN_VMTEST=", "LDCLEAN_VMTEST_TOKEN=")
}

func assertMainCommandImports(t *testing.T, modulePath string, packages map[string]listedPackage) {
	t.Helper()

	mainImportPath := modulePath + "/" + mainCommandPath
	mainPackage, ok := packages[mainImportPath]
	if !ok {
		t.Errorf("go list did not return main command package %q", mainImportPath)
		return
	}

	allowedProjectImports := map[string]struct{}{
		modulePath + "/internal/application":    {},
		modulePath + "/internal/presenters/cli": {},
	}
	for _, imported := range mainPackage.Imports {
		if _, ok := allowedProjectImports[imported]; ok {
			continue
		}
		if dependency, ok := packages[imported]; ok && dependency.Standard {
			continue
		}

		t.Errorf(
			"%s may import only the application and CLI presenter project boundaries (or the standard library); found %q",
			mainCommandPath,
			imported,
		)
	}
}

func assertHelperDependencies(t *testing.T, modulePath string, packages map[string]listedPackage) {
	t.Helper()

	helperImportPath := modulePath + "/" + helperCommandPath
	if _, ok := packages[helperImportPath]; !ok {
		t.Errorf("go list did not return helper command package %q", helperImportPath)
		return
	}

	for importPath, pkg := range packages {
		if importPath == helperImportPath || pkg.Standard {
			continue
		}
		t.Errorf("%s must depend only on the standard library; found %q", helperCommandPath, importPath)
	}
}

func assertNoPkgTree(t *testing.T, root string) {
	t.Helper()

	if _, err := os.Lstat(filepath.Join(root, "pkg")); err == nil {
		t.Error("pkg/ is forbidden; keep project packages under internal/")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("inspect pkg/: %v", err)
	}
}

func assertPresenterImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	allowedProjectImports := map[string]struct{}{
		modulePath + "/internal/application": {},
		modulePath + "/internal/domain":      {},
	}
	forEachProductionGoFile(t, root, "internal/presenters", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}

			if _, standard := standardImports[importPath]; standard {
				continue
			}
			if importPath == "github.com/spf13/cobra" && strings.HasPrefix(pathFromRoot(root, path), "internal/presenters/cli/") {
				continue
			}
			if _, allowed := allowedProjectImports[importPath]; !allowed {
				t.Errorf("%s: presenters may import only standard-library packages, Cobra in the CLI presenter, or application/domain project packages; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertApplicationImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	allowedProjectImports := map[string]struct{}{
		modulePath + "/internal/domain": {},
	}
	forEachProductionGoFile(t, root, "internal/application", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}

			if _, standard := standardImports[importPath]; standard {
				continue
			}
			if _, allowed := allowedProjectImports[importPath]; !allowed {
				t.Errorf("%s: application may import only standard-library or domain project packages; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertCobraPresenterOnly(t *testing.T, root string) {
	t.Helper()

	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			for _, importSpec := range file.Imports {
				importPath, ok := importPathOf(t, path, importSpec)
				if !ok || importPath != "github.com/spf13/cobra" {
					continue
				}
				if strings.HasPrefix(pathFromRoot(root, path), "internal/presenters/") {
					continue
				}
				t.Errorf("%s: Cobra is presenter-only in Phase 1", pathFromRoot(root, path))
			}
		})
	}
}

func assertDomainImports(t *testing.T, root string, standardImports map[string]struct{}) {
	t.Helper()

	forEachProductionGoFile(t, root, "internal/domain", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}
			if _, standard := standardImports[importPath]; !standard {
				t.Errorf("%s: domain packages may import only the standard library; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertNoExecutableShellScripts(t *testing.T, root string) {
	t.Helper()

	for _, relativeDirectory := range []string{"cmd", "internal"} {
		directory := filepath.Join(root, relativeDirectory)
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return nil
			}

			if isExecutableShellScript(path, entry.Name()) {
				t.Errorf("%s: executable shell scripts are forbidden in production code", pathFromRoot(root, path))
			}
			return nil
		})
		if err != nil {
			t.Errorf("inspect executable files in %q: %v", relativeDirectory, err)
		}
	}
}

func isExecutableShellScript(path, name string) bool {
	if strings.HasSuffix(name, ".sh") {
		return true
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	firstLine, _, _ := strings.Cut(string(contents), "\n")
	if !strings.HasPrefix(firstLine, "#!") {
		return false
	}

	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, "#!")))
	if len(fields) == 0 {
		return false
	}
	if isShellExecutable(fields[0]) {
		return true
	}
	if filepath.Base(fields[0]) != "env" {
		return false
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.Contains(field, "=") {
			continue
		}
		return isShellExecutable(field)
	}
	return false
}

func assertNoRuntimeShellScripts(t *testing.T, root string) {
	t.Helper()

	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			aliases := newShellAPIAliases(file)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}

				argument, ok := shellExecutableArgument(call, aliases)
				if !ok {
					return true
				}

				program, shell, known := shellExecutable(call.Args[argument:])
				if !known {
					t.Errorf(
						"%s: runtime process execution must use a literal executable so shell execution cannot be hidden",
						pathFromRoot(root, path),
					)
					return true
				}
				if !shell {
					return true
				}
				t.Errorf(
					"%s: runtime shell execution via %q is forbidden in production code",
					pathFromRoot(root, path),
					program,
				)
				return true
			})
		})
	}
}

var bootstrapForbiddenRuntimeImports = map[string]string{
	"C":                        "native escape hatch",
	"io/ioutil":                "legacy host file mutation API",
	"net":                      "network access",
	"net/http":                 "network access",
	"net/http/httptest":        "network listener setup",
	"net/rpc":                  "network access",
	"net/smtp":                 "network access",
	"net/textproto":            "network protocol handling",
	"os/exec":                  "runtime process execution",
	"plugin":                   "runtime code loading",
	"reflect":                  "runtime mutation indirection",
	"syscall":                  "runtime process execution or host mutation",
	"unsafe":                   "memory safety bypass",
	"golang.org/x/net":         "network access",
	"golang.org/x/net/context": "network-related dependency",
	"golang.org/x/sys/unix":    "runtime process execution or host mutation",
}

var bootstrapForbiddenOSSelectors = map[string]string{
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
	"StartProcess": "runtime process execution",
	"Symlink":      "host filesystem mutation",
	"Truncate":     "host file mutation",
	"WriteFile":    "host file mutation",
}

var bootstrapForbiddenProcessMethods = map[string]string{
	"Kill":    "host process mutation",
	"Release": "host process control",
	"Signal":  "host process mutation",
}

// assertBootstrapRuntimeSafety makes the no-network, no-process, and
// no-mutation Phase 1 boundary explicit even when a call is hidden behind a
// function value or a struct value. The restriction is intentionally applied
// to all production code, not just the bootstrap command entry point.
func assertBootstrapRuntimeSafety(t *testing.T, root string) {
	t.Helper()

	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			aliases := make(map[string]string)
			for _, importSpec := range file.Imports {
				importPath, ok := importPathOf(t, path, importSpec)
				if !ok {
					continue
				}
				if reason, forbidden := bootstrapForbiddenRuntimeImports[importPath]; forbidden {
					t.Errorf("%s imports %q for %s; Phase 1 production code must remain offline, process-free, and non-mutating", pathFromRoot(root, path), importPath, reason)
				}

				name := filepath.Base(importPath)
				if importSpec.Name != nil {
					name = importSpec.Name.Name
				}
				if name == "." && importPath == "os" {
					t.Errorf("%s dot-imports os; explicit qualification is required for the Phase 1 runtime-safety gate", pathFromRoot(root, path))
					continue
				}
				if name != "_" {
					aliases[name] = importPath
				}
			}

			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if reason, forbidden := bootstrapForbiddenProcessMethods[selector.Sel.Name]; forbidden {
					t.Errorf("%s references .%s for %s; Phase 1 production code must remain offline, process-free, and non-mutating", pathFromRoot(root, path), selector.Sel.Name, reason)
				}
				packageName, ok := selector.X.(*ast.Ident)
				if !ok || aliases[packageName.Name] != "os" {
					return true
				}
				if reason, forbidden := bootstrapForbiddenOSSelectors[selector.Sel.Name]; forbidden {
					t.Errorf("%s references os.%s for %s; Phase 1 production code must remain offline, process-free, and non-mutating", pathFromRoot(root, path), selector.Sel.Name, reason)
				}
				return true
			})
		})
	}
}

func forEachProductionGoFile(t *testing.T, root, relativeDirectory string, visit func(string, *ast.File)) {
	t.Helper()

	directory := filepath.Join(root, filepath.FromSlash(relativeDirectory))
	if _, err := os.Stat(directory); errors.Is(err, fs.ErrNotExist) {
		return
	} else if err != nil {
		t.Errorf("stat production directory %q: %v", relativeDirectory, err)
		return
	}

	fileSet := token.NewFileSet()
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return err
		}
		visit(path, file)
		return nil
	})
	if err != nil {
		t.Errorf("inspect production directory %q: %v", relativeDirectory, err)
	}
}

func importPathOf(t *testing.T, filename string, importSpec *ast.ImportSpec) (string, bool) {
	t.Helper()

	importPath, err := strconv.Unquote(importSpec.Path.Value)
	if err != nil {
		t.Errorf("%s: parse import path %q: %v", filename, importSpec.Path.Value, err)
		return "", false
	}
	return importPath, true
}

func pathFromRoot(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}

type shellAPIAliases struct {
	exec    map[string]struct{}
	syscall map[string]struct{}
	os      map[string]struct{}
	dotExec bool
	dotSys  bool
	dotOS   bool
}

func newShellAPIAliases(file *ast.File) shellAPIAliases {
	aliases := shellAPIAliases{
		exec:    make(map[string]struct{}),
		syscall: make(map[string]struct{}),
		os:      make(map[string]struct{}),
	}

	for _, importSpec := range file.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			continue
		}

		name := filepath.Base(importPath)
		if importSpec.Name != nil {
			name = importSpec.Name.Name
		}

		switch importPath {
		case "os/exec":
			if name == "." {
				aliases.dotExec = true
			} else if name != "_" {
				aliases.exec[name] = struct{}{}
			}
		case "syscall":
			if name == "." {
				aliases.dotSys = true
			} else if name != "_" {
				aliases.syscall[name] = struct{}{}
			}
		case "os":
			if name == "." {
				aliases.dotOS = true
			} else if name != "_" {
				aliases.os[name] = struct{}{}
			}
		}
	}

	return aliases
}

func shellExecutableArgument(call *ast.CallExpr, aliases shellAPIAliases) (int, bool) {
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		packageName, ok := function.X.(*ast.Ident)
		if !ok {
			return 0, false
		}

		if _, ok := aliases.exec[packageName.Name]; ok {
			switch function.Sel.Name {
			case "Command":
				return 0, true
			case "CommandContext":
				return 1, true
			}
		}
		if _, ok := aliases.syscall[packageName.Name]; ok && (function.Sel.Name == "Exec" || function.Sel.Name == "ForkExec") {
			return 0, true
		}
		if _, ok := aliases.os[packageName.Name]; ok && function.Sel.Name == "StartProcess" {
			return 0, true
		}
	case *ast.Ident:
		switch function.Name {
		case "Command":
			if aliases.dotExec {
				return 0, true
			}
		case "CommandContext":
			if aliases.dotExec {
				return 1, true
			}
		case "Exec", "ForkExec":
			if aliases.dotSys {
				return 0, true
			}
		case "StartProcess":
			if aliases.dotOS {
				return 0, true
			}
		}
	}

	return 0, false
}

func shellExecutable(arguments []ast.Expr) (program string, shell, known bool) {
	if len(arguments) == 0 {
		return "", false, false
	}

	program, ok := stringLiteral(arguments[0])
	if !ok {
		return "", false, false
	}
	if isShellExecutable(program) {
		return program, true, true
	}
	if strings.HasSuffix(program, ".sh") {
		return program, true, true
	}
	if filepath.Base(program) != "env" {
		return program, false, true
	}

	for _, argument := range arguments[1:] {
		candidate, ok := stringLiteral(argument)
		if !ok {
			return "", false, false
		}
		if strings.HasPrefix(candidate, "-") || strings.Contains(candidate, "=") {
			continue
		}
		if isShellExecutable(candidate) {
			return candidate, true, true
		}
		if strings.HasSuffix(candidate, ".sh") {
			return candidate, true, true
		}
		return candidate, false, true
	}

	return program, false, true
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

func isShellExecutable(program string) bool {
	_, ok := shellExecutableNames[filepath.Base(program)]
	return ok
}

var shellExecutableNames = map[string]struct{}{
	"ash":  {},
	"bash": {},
	"csh":  {},
	"dash": {},
	"fish": {},
	"ksh":  {},
	"mksh": {},
	"sh":   {},
	"tcsh": {},
	"yash": {},
	"zsh":  {},
}
