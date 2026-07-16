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
	mainCommandPath       = "cmd/ldclean"
	helperCommandPath     = "cmd/linux-deep-clean-helper"
	mountsPackagePath     = "internal/mounts"
	linuxfsPackagePath    = "internal/linuxfs"
	trashPackagePath      = "internal/trash"
	quarantinePackagePath = "internal/quarantine"
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
	assertPathbytesImports(t, root, standardImports)
	assertDomainImports(t, root, modulePath, standardImports)
	assertPlanprotoImports(t, root, modulePath, standardImports)
	assertMountsImports(t, root, modulePath, standardImports)
	assertLinuxFSImports(t, root, modulePath, standardImports)
	assertTrashImports(t, root, modulePath, standardImports)
	assertQuarantineImports(t, root, modulePath, standardImports)
	assertFilesystemMutationBoundaries(t, root)
	assertRootLeaseDuplicateBoundary(t, root, modulePath)
	assertLayoutLeaseDuplicateBoundary(t, root, modulePath)
	assertTrashLeaseDuplicateBoundary(t, root, modulePath)
	assertProvidersAndPresentersDoNotImportSafetyLayer(t, root, modulePath)
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

func TestFilesystemSafetyEscapeHatchesAreForbidden(t *testing.T) {
	for _, selector := range []string{"RawSyscall", "RawSyscall6", "RawSyscallNoError", "Syscall", "Syscall6", "SyscallNoError"} {
		if _, forbidden := forbiddenUnixEscapeSelectors[selector]; !forbidden {
			t.Errorf("unix.%s is not covered by the filesystem-safety escape-hatch gate", selector)
		}
	}
	for _, selector := range []string{"Open", "OpenFile", "Create", "NewFile"} {
		if _, forbidden := bootstrapForbiddenOSSelectors[selector]; !forbidden {
			t.Errorf("os.%s is not covered by the production descriptor-authority gate", selector)
		}
	}
}

func TestProductionOSFileMutationTrackingIsTypeScoped(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "file_mutation.go", strings.NewReader(`package safety
import "os"
type writer interface { Write([]byte) (int, error) }
func mutate(file *os.File) {
	_, _ = file.Write(nil)
	{
		file := writer(nil)
		_, _ = file.Write(nil)
	}
}
func digest(file writer) { _, _ = file.Write(nil) }
func mutateValue(file os.File) { _, _ = file.Write(nil) }
`), 0)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}

	aliases := map[string]string{"os": "os"}
	mutate := functionDeclarationNamed(t, file, "mutate")
	mutateSelectors := functionSelectorsNamed(mutate.Body, "Write")
	if len(mutateSelectors) != 2 {
		t.Fatalf("found %d Write selectors in mutate, want 2", len(mutateSelectors))
	}
	variables := osFileVariablesForScope(functionParameters(mutate.Type), mutate.Body, aliases)
	if !isTrackedOSFileReceiver(mutateSelectors[0].X, variables, aliases) {
		t.Error("typed *os.File mutation receiver is not detected")
	}
	if isTrackedOSFileReceiver(mutateSelectors[1].X, variables, aliases) {
		t.Error("a shadowed non-file Write receiver is mistaken for *os.File")
	}

	digest := functionDeclarationNamed(t, file, "digest")
	digestSelector := functionSelectorNamed(t, digest.Body, "Write")
	if isTrackedOSFileReceiver(digestSelector.X, osFileVariablesForScope(functionParameters(digest.Type), digest.Body, aliases), aliases) {
		t.Error("unrelated Write receiver is mistaken for *os.File")
	}

	mutateValue := functionDeclarationNamed(t, file, "mutateValue")
	mutateValueSelector := functionSelectorNamed(t, mutateValue.Body, "Write")
	if !isTrackedOSFileReceiver(mutateValueSelector.X, osFileVariablesForScope(functionParameters(mutateValue.Type), mutateValue.Body, aliases), aliases) {
		t.Error("addressable os.File mutation receiver is not detected")
	}
}

func TestRootLeaseDuplicateTrackingRequiresMountsRootLease(t *testing.T) {
	const mountsImportPath = "example.test/project/internal/mounts"
	file, err := parser.ParseFile(token.NewFileSet(), "root_lease.go", strings.NewReader(`package safety
import mounts "example.test/project/internal/mounts"
func duplicate(root *mounts.RootLease) { _, _ = root.Duplicate() }
`), 0)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}

	function := functionDeclarationNamed(t, file, "duplicate")
	selector := functionSelectorNamed(t, function.Body, "Duplicate")
	aliases := map[string]string{"mounts": mountsImportPath}
	variables := mountsRootLeaseVariablesForScope(functionParameters(function.Type), function.Body, aliases, mountsImportPath)
	if !isTrackedMountsRootLeaseReceiver(selector.X, variables, aliases, mountsImportPath) {
		t.Error("mounts.RootLease.Duplicate receiver is not detected")
	}
}

func TestLayoutLeaseDuplicateTrackingRequiresMountsLayoutLease(t *testing.T) {
	const mountsImportPath = "example.test/project/internal/mounts"
	file, err := parser.ParseFile(token.NewFileSet(), "layout_lease.go", strings.NewReader(`package safety
import mounts "example.test/project/internal/mounts"
func duplicate(layout *mounts.LayoutLease) { _, _ = layout.Duplicate() }
`), 0)
	if err != nil {
		t.Fatalf("parse layout lease source: %v", err)
	}

	function := functionDeclarationNamed(t, file, "duplicate")
	selector := functionSelectorNamed(t, function.Body, "Duplicate")
	aliases := map[string]string{"mounts": mountsImportPath}
	variables := mountsLayoutLeaseVariablesForScope(functionParameters(function.Type), function.Body, aliases, mountsImportPath)
	if !isTrackedMountsLayoutLeaseReceiver(selector.X, variables, aliases, mountsImportPath) {
		t.Error("mounts.LayoutLease.Duplicate receiver is not detected")
	}
}

func TestTrashLeaseDuplicateTrackingRequiresMountsTrashLease(t *testing.T) {
	const mountsImportPath = "example.test/project/internal/mounts"
	file, err := parser.ParseFile(token.NewFileSet(), "trash_lease.go", strings.NewReader(`package safety
import mounts "example.test/project/internal/mounts"
func duplicate(trash *mounts.TrashLease) { _, _ = trash.Duplicate() }
`), 0)
	if err != nil {
		t.Fatalf("parse trash lease source: %v", err)
	}

	function := functionDeclarationNamed(t, file, "duplicate")
	selector := functionSelectorNamed(t, function.Body, "Duplicate")
	aliases := map[string]string{"mounts": mountsImportPath}
	variables := mountsTrashLeaseVariablesForScope(functionParameters(function.Type), function.Body, aliases, mountsImportPath)
	if !isTrackedMountsTrashLeaseReceiver(selector.X, variables, aliases, mountsImportPath) {
		t.Error("mounts.TrashLease.Duplicate receiver is not detected")
	}
}

func functionDeclarationNamed(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("function %q not found", name)
	return nil
}

func functionSelectorNamed(t *testing.T, body *ast.BlockStmt, name string) *ast.SelectorExpr {
	t.Helper()

	selectors := functionSelectorsNamed(body, name)
	if len(selectors) == 0 {
		t.Fatalf("selector %q not found", name)
	}
	return selectors[0]
}

func functionSelectorsNamed(body *ast.BlockStmt, name string) []*ast.SelectorExpr {
	var selectors []*ast.SelectorExpr
	ast.Inspect(body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			selectors = append(selectors, selector)
		}
		return true
	})
	return selectors
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

func assertPathbytesImports(t *testing.T, root string, standardImports map[string]struct{}) {
	t.Helper()

	forEachProductionGoFile(t, root, "internal/pathbytes", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}
			if _, standard := standardImports[importPath]; !standard {
				t.Errorf("%s: pathbytes packages may import only the standard library; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertDomainImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	allowedProjectImports := map[string]struct{}{
		modulePath + "/internal/pathbytes": {},
	}
	forEachProductionGoFile(t, root, "internal/domain", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}
			if _, standard := standardImports[importPath]; standard {
				continue
			}
			if _, allowed := allowedProjectImports[importPath]; !allowed {
				t.Errorf("%s: domain packages may import only the standard library or internal/pathbytes; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertPlanprotoImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	allowedImports := map[string]struct{}{
		modulePath + "/internal/domain":    {},
		modulePath + "/internal/pathbytes": {},
		"github.com/fxamacker/cbor/v2":     {},
	}
	forEachProductionGoFile(t, root, "internal/planproto", func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}
			if _, standard := standardImports[importPath]; standard {
				continue
			}
			if _, allowed := allowedImports[importPath]; !allowed {
				t.Errorf("%s: planproto packages may import only the standard library, internal/domain, internal/pathbytes, or fxamacker/cbor/v2; found %q", pathFromRoot(root, path), importPath)
			}
		}
	})
}

func assertMountsImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	assertSafetyPackageImports(t, root, mountsPackagePath, standardImports, map[string]struct{}{
		modulePath + "/internal/domain":    {},
		modulePath + "/internal/pathbytes": {},
		"golang.org/x/sys/unix":            {},
	})
}

func assertLinuxFSImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	assertSafetyPackageImports(t, root, linuxfsPackagePath, standardImports, map[string]struct{}{
		modulePath + "/internal/domain":    {},
		modulePath + "/internal/mounts":    {},
		modulePath + "/internal/pathbytes": {},
		"golang.org/x/sys/unix":            {},
	})
}

func assertTrashImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	assertSafetyPackageImports(t, root, trashPackagePath, standardImports, map[string]struct{}{
		modulePath + "/internal/domain":    {},
		modulePath + "/internal/linuxfs":   {},
		modulePath + "/internal/mounts":    {},
		modulePath + "/internal/pathbytes": {},
	})
}

func assertQuarantineImports(t *testing.T, root, modulePath string, standardImports map[string]struct{}) {
	t.Helper()

	assertSafetyPackageImports(t, root, quarantinePackagePath, standardImports, map[string]struct{}{
		modulePath + "/internal/domain":    {},
		modulePath + "/internal/linuxfs":   {},
		modulePath + "/internal/mounts":    {},
		modulePath + "/internal/pathbytes": {},
	})
}

func assertSafetyPackageImports(t *testing.T, root, packagePath string, standardImports, allowedImports map[string]struct{}) {
	t.Helper()

	forEachProductionGoFile(t, root, packagePath, func(path string, file *ast.File) {
		for _, importSpec := range file.Imports {
			importPath, ok := importPathOf(t, path, importSpec)
			if !ok {
				continue
			}
			if _, standard := standardImports[importPath]; standard {
				continue
			}
			if _, allowed := allowedImports[importPath]; !allowed {
				t.Errorf("%s: %s may import only its explicit safety-layer dependencies; found %q", pathFromRoot(root, path), packagePath, importPath)
				continue
			}
			if importPath == "golang.org/x/sys/unix" && importSpec.Name != nil {
				t.Errorf("%s: %s must import golang.org/x/sys/unix without an alias so raw syscall use remains auditable", pathFromRoot(root, path), packagePath)
			}
		}
	})
}

func assertProvidersAndPresentersDoNotImportSafetyLayer(t *testing.T, root, modulePath string) {
	t.Helper()

	for _, directory := range []string{"internal/providers", "internal/presenters"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			for _, importSpec := range file.Imports {
				importPath, ok := importPathOf(t, path, importSpec)
				if !ok {
					continue
				}
				if isSafetyLayerImport(modulePath, importPath) {
					t.Errorf("%s imports %q; providers and presenters cannot obtain filesystem mutation authority", pathFromRoot(root, path), importPath)
				}
			}
		})
	}
}

func isSafetyLayerImport(modulePath, importPath string) bool {
	for _, packagePath := range []string{mountsPackagePath, linuxfsPackagePath, trashPackagePath, quarantinePackagePath} {
		if importPath == modulePath+"/"+packagePath {
			return true
		}
	}
	return false
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
	"NewFile":      "raw descriptor authority",
	"Open":         "raw descriptor authority",
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
				if reason, forbidden := bootstrapForbiddenRuntimeImports[importPath]; forbidden && !(importPath == "golang.org/x/sys/unix" && isUnixFilesystemSafetySource(root, path)) {
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

			utsnameVariables := unixUtsnameVariables(file, aliases)
			assertNoProductionOSFileMutations(t, root, path, file, aliases)
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if reason, forbidden := bootstrapForbiddenProcessMethods[selector.Sel.Name]; forbidden && !isUnixUtsnameReleaseField(selector, utsnameVariables) {
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

var forbiddenOSFileMutationMethods = map[string]string{
	"Chdir":       "host working-directory mutation",
	"Chmod":       "host permission mutation",
	"Chown":       "host ownership mutation",
	"Truncate":    "host file mutation",
	"Write":       "host file mutation",
	"WriteAt":     "host file mutation",
	"WriteString": "host file mutation",
}

// assertNoProductionOSFileMutations closes the remaining descriptor escape
// hatch after direct os descriptor factories are forbidden. It deliberately
// tracks only syntactically proven os.File bindings so unrelated values with
// methods such as Write are not rejected.
func assertNoProductionOSFileMutations(t *testing.T, root, path string, file *ast.File, aliases map[string]string) {
	t.Helper()

	globalFileVariables := osFileVariablesAtFileScope(file, aliases)
	forEachFunctionScope(file, func(parameters []*ast.Field, body *ast.BlockStmt) {
		fileVariables := osFileVariablesForScope(parameters, body, aliases)
		addVariableObjects(fileVariables, globalFileVariables)
		inspectFunctionScope(body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			reason, forbidden := forbiddenOSFileMutationMethods[selector.Sel.Name]
			if !forbidden || !isTrackedOSFileReceiver(selector.X, fileVariables, aliases) {
				return true
			}
			t.Errorf("%s invokes *os.File.%s for %s; production code may not mutate host files outside the rooted linuxfs API", pathFromRoot(root, path), selector.Sel.Name, reason)
			return true
		})
	})
}

func forEachFunctionScope(file *ast.File, visit func([]*ast.Field, *ast.BlockStmt)) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		visit(functionParameters(function.Type), function.Body)
		forEachFunctionLiteralScope(function.Body, visit)
	}
}

func forEachFunctionLiteralScope(body *ast.BlockStmt, visit func([]*ast.Field, *ast.BlockStmt)) {
	inspectFunctionScope(body, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		visit(functionParameters(literal.Type), literal.Body)
		forEachFunctionLiteralScope(literal.Body, visit)
		return false
	})
}

func functionParameters(functionType *ast.FuncType) []*ast.Field {
	if functionType == nil || functionType.Params == nil {
		return nil
	}
	return functionType.Params.List
}

func inspectFunctionScope(body *ast.BlockStmt, visit func(ast.Node) bool) {
	ast.Inspect(body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		return visit(node)
	})
}

func osFileVariablesAtFileScope(file *ast.File, aliases map[string]string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if ok && isOSFileType(value.Type, aliases) {
				addNamedIdentifiers(variables, value.Names)
			}
		}
	}
	return variables
}

func osFileVariablesForScope(parameters []*ast.Field, body *ast.BlockStmt, aliases map[string]string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, parameter := range parameters {
		if !isOSFileType(parameter.Type, aliases) {
			continue
		}
		addNamedIdentifiers(variables, parameter.Names)
	}

	inspectFunctionScope(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if isOSFileType(node.Type, aliases) {
				addNamedIdentifiers(variables, node.Names)
			}
			for valueIndex, value := range node.Values {
				if !isOSFileFactoryCall(value, aliases) {
					continue
				}
				addFactoryValueSpecTarget(variables, node.Names, len(node.Values), valueIndex)
			}
		case *ast.AssignStmt:
			for valueIndex, value := range node.Rhs {
				if !isOSFileFactoryCall(value, aliases) {
					continue
				}
				addFactoryAssignmentTarget(variables, node.Lhs, len(node.Rhs), valueIndex)
			}
		}
		return true
	})

	return variables
}

func addFactoryAssignmentTarget(variables map[*ast.Object]struct{}, targets []ast.Expr, valueCount, valueIndex int) {
	if len(targets) == 0 {
		return
	}

	targetIndex := valueIndex
	if valueCount == 1 {
		targetIndex = 0
	}
	if targetIndex >= len(targets) {
		return
	}
	identifier, ok := targets[targetIndex].(*ast.Ident)
	if ok && identifier.Name != "_" && identifier.Obj != nil {
		variables[identifier.Obj] = struct{}{}
	}
}

func addFactoryValueSpecTarget(variables map[*ast.Object]struct{}, targets []*ast.Ident, valueCount, valueIndex int) {
	if len(targets) == 0 {
		return
	}

	targetIndex := valueIndex
	if valueCount == 1 {
		targetIndex = 0
	}
	if targetIndex >= len(targets) || targets[targetIndex].Name == "_" || targets[targetIndex].Obj == nil {
		return
	}
	variables[targets[targetIndex].Obj] = struct{}{}
}

func addNamedIdentifiers(variables map[*ast.Object]struct{}, identifiers []*ast.Ident) {
	for _, identifier := range identifiers {
		if identifier.Name != "_" && identifier.Obj != nil {
			variables[identifier.Obj] = struct{}{}
		}
	}
}

func addVariableObjects(destination, source map[*ast.Object]struct{}) {
	for variable := range source {
		destination[variable] = struct{}{}
	}
}

func isOSFileFactoryCall(expression ast.Expr, aliases map[string]string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || aliases[packageName.Name] != "os" {
		return false
	}
	_, factory := bootstrapForbiddenOSSelectors[selector.Sel.Name]
	return factory && (selector.Sel.Name == "Open" || selector.Sel.Name == "OpenFile" || selector.Sel.Name == "Create" || selector.Sel.Name == "NewFile")
}

func isTrackedOSFileReceiver(expression ast.Expr, variables map[*ast.Object]struct{}, aliases map[string]string) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		_, tracked := variables[identifier.Obj]
		return tracked
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		packageName, ok := selector.X.(*ast.Ident)
		if ok && aliases[packageName.Name] == "os" {
			switch selector.Sel.Name {
			case "Stdin", "Stdout", "Stderr":
				return true
			}
		}
	}
	return isOSFileType(expression, aliases)
}

func isOSFileType(expression ast.Expr, aliases map[string]string) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}

	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "File" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == "os"
}

func isUnixFilesystemSafetySource(root, path string) bool {
	packagePath := filepath.ToSlash(filepath.Dir(pathFromRoot(root, path)))
	return packagePath == mountsPackagePath || packagePath == linuxfsPackagePath
}

func unixUtsnameVariables(file *ast.File, aliases map[string]string) map[string]struct{} {
	variables := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) == 0 {
			return true
		}
		typeSelector, ok := value.Type.(*ast.SelectorExpr)
		if !ok || typeSelector.Sel.Name != "Utsname" {
			return true
		}
		packageName, ok := typeSelector.X.(*ast.Ident)
		if !ok || aliases[packageName.Name] != "golang.org/x/sys/unix" {
			return true
		}
		for _, name := range value.Names {
			variables[name.Name] = struct{}{}
		}
		return true
	})
	return variables
}

func isUnixUtsnameReleaseField(selector *ast.SelectorExpr, variables map[string]struct{}) bool {
	if selector.Sel.Name != "Release" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, known := variables[identifier.Name]
	return known
}

var forbiddenUnixEscapeSelectors = map[string]string{
	"Chroot":            "root-directory escape",
	"Mount":             "mount mutation",
	"MoveMount":         "mount mutation",
	"OpenTree":          "mount-tree authority",
	"PivotRoot":         "root-directory escape",
	"RawSyscall":        "raw syscall escape",
	"RawSyscall6":       "raw syscall escape",
	"RawSyscallNoError": "raw syscall escape",
	"Setns":             "mount-namespace mutation",
	"Syscall":           "raw syscall escape",
	"Syscall6":          "raw syscall escape",
	"SyscallNoError":    "raw syscall escape",
	"Unshare":           "mount-namespace mutation",
	"Unmount":           "mount mutation",
	"Unmount2":          "mount mutation",
	"Fsopen":            "filesystem-context authority",
	"Fsconfig":          "filesystem-context authority",
	"Fsmount":           "filesystem-context authority",
	"Fspick":            "filesystem-context authority",
	"MountSetattr":      "mount mutation",
	"NameToHandleAt":    "file-handle authority",
	"OpenByHandleAt":    "file-handle authority",
}

var linuxFSOnlyUnixSelectors = map[string]string{
	"Chmod":        "pathname mutation",
	"Chown":        "pathname mutation",
	"Creat":        "pathname creation",
	"Fchmod":       "descriptor mutation",
	"Fchmodat":     "pathname mutation",
	"Fchown":       "descriptor mutation",
	"Fchownat":     "pathname mutation",
	"Fremovexattr": "descriptor mutation",
	"Fsetxattr":    "descriptor mutation",
	"Ftruncate":    "descriptor mutation",
	"Futimesat":    "pathname mutation",
	"Link":         "pathname mutation",
	"Linkat":       "pathname mutation",
	"Lremovexattr": "pathname mutation",
	"Lsetxattr":    "pathname mutation",
	"Mkdir":        "pathname creation",
	"Mkdirat":      "pathname creation",
	"Mknod":        "pathname creation",
	"Mknodat":      "pathname creation",
	"Open":         "pathname resolution",
	"Openat":       "descriptor-relative resolution",
	"Openat2":      "descriptor-relative resolution",
	"Pwrite":       "descriptor mutation",
	"Pwritev":      "descriptor mutation",
	"Remove":       "pathname mutation",
	"Removexattr":  "pathname mutation",
	"Rename":       "pathname mutation",
	"Renameat":     "descriptor-relative mutation",
	"Renameat2":    "descriptor-relative mutation",
	"Rmdir":        "pathname mutation",
	"Setxattr":     "pathname mutation",
	"Symlink":      "pathname creation",
	"Symlinkat":    "pathname creation",
	"Truncate":     "pathname mutation",
	"Unlink":       "pathname mutation",
	"Unlinkat":     "descriptor-relative mutation",
	"Utimensat":    "pathname mutation",
	"Write":        "descriptor mutation",
	"Writev":       "descriptor mutation",
}

var forbiddenPathMutationSelectors = map[string]string{
	"Remove":       "host pathname mutation",
	"RemoveAll":    "host pathname mutation",
	"Rename":       "host pathname mutation",
	"EvalSymlinks": "string-path resolution",
}

// assertFilesystemMutationBoundaries reserves raw descriptor-relative
// filesystem operations for linuxfs. Mount qualification can inspect held
// descriptors, but it cannot mutate or resolve user targets. Every other
// production package remains unable to bypass the safety layer.
func assertFilesystemMutationBoundaries(t *testing.T, root string) {
	t.Helper()

	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			aliases := productionImportAliases(t, path, file)
			isLinuxFS := filepath.ToSlash(filepath.Dir(pathFromRoot(root, path))) == linuxfsPackagePath

			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				packageName, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}

				switch aliases[packageName.Name] {
				case "golang.org/x/sys/unix":
					if reason, forbidden := forbiddenUnixEscapeSelectors[selector.Sel.Name]; forbidden {
						t.Errorf("%s references unix.%s for %s; no production package may bypass the filesystem safety model", pathFromRoot(root, path), selector.Sel.Name, reason)
						return true
					}
					if reason, restricted := linuxFSOnlyUnixSelectors[selector.Sel.Name]; restricted && !isLinuxFS {
						t.Errorf("%s references unix.%s for %s; only %s may issue raw filesystem operations", pathFromRoot(root, path), selector.Sel.Name, reason, linuxfsPackagePath)
					}
				case "os":
					if reason, forbidden := forbiddenPathMutationSelectors[selector.Sel.Name]; forbidden {
						t.Errorf("%s references os.%s for %s; use the rooted linuxfs API instead", pathFromRoot(root, path), selector.Sel.Name, reason)
					}
				case "path/filepath":
					if reason, forbidden := forbiddenPathMutationSelectors[selector.Sel.Name]; forbidden {
						t.Errorf("%s references filepath.%s for %s; string-path traversal is not a filesystem authority", pathFromRoot(root, path), selector.Sel.Name, reason)
					}
				}
				return true
			})
		})
	}
}

// assertRootLeaseDuplicateBoundary makes the one intentional raw descriptor
// handoff explicit: linuxfs may duplicate a trusted root only to construct its
// rooted traversal lease. Other production packages must use the safe API
// rather than borrow a raw descriptor from mounts.RootLease.
func assertRootLeaseDuplicateBoundary(t *testing.T, root, modulePath string) {
	t.Helper()

	mountsImportPath := modulePath + "/" + mountsPackagePath
	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			if filepath.ToSlash(filepath.Dir(pathFromRoot(root, path))) == linuxfsPackagePath {
				return
			}

			aliases := productionImportAliases(t, path, file)
			globalRootLeases := mountsRootLeaseVariablesAtFileScope(file, aliases, mountsImportPath)
			forEachFunctionScope(file, func(parameters []*ast.Field, body *ast.BlockStmt) {
				rootLeases := mountsRootLeaseVariablesForScope(parameters, body, aliases, mountsImportPath)
				addVariableObjects(rootLeases, globalRootLeases)
				inspectFunctionScope(body, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Duplicate" || !isTrackedMountsRootLeaseReceiver(selector.X, rootLeases, aliases, mountsImportPath) {
						return true
					}
					t.Errorf("%s calls mounts.RootLease.Duplicate; only %s may obtain the intentional raw root-descriptor handoff", pathFromRoot(root, path), linuxfsPackagePath)
					return true
				})
			})
		})
	}
}

// assertLayoutLeaseDuplicateBoundary applies the same raw-descriptor rule to
// engine/helper-owned recovery layouts. Only linuxfs may convert a qualified
// layout lease into its internal descriptor-rooted operation lease.
func assertLayoutLeaseDuplicateBoundary(t *testing.T, root, modulePath string) {
	t.Helper()

	mountsImportPath := modulePath + "/" + mountsPackagePath
	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			if filepath.ToSlash(filepath.Dir(pathFromRoot(root, path))) == linuxfsPackagePath {
				return
			}

			aliases := productionImportAliases(t, path, file)
			globalLayouts := mountsLayoutLeaseVariablesAtFileScope(file, aliases, mountsImportPath)
			forEachFunctionScope(file, func(parameters []*ast.Field, body *ast.BlockStmt) {
				layouts := mountsLayoutLeaseVariablesForScope(parameters, body, aliases, mountsImportPath)
				addVariableObjects(layouts, globalLayouts)
				inspectFunctionScope(body, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Duplicate" || !isTrackedMountsLayoutLeaseReceiver(selector.X, layouts, aliases, mountsImportPath) {
						return true
					}
					t.Errorf("%s calls mounts.LayoutLease.Duplicate; only %s may obtain a raw recovery-layout descriptor", pathFromRoot(root, path), linuxfsPackagePath)
					return true
				})
			})
		})
	}
}

// assertTrashLeaseDuplicateBoundary keeps Freedesktop Trash files/info
// descriptor pairs inside the rooted filesystem layer. Trash policy may select
// a trusted bundle but must not obtain raw descriptors itself.
func assertTrashLeaseDuplicateBoundary(t *testing.T, root, modulePath string) {
	t.Helper()

	mountsImportPath := modulePath + "/" + mountsPackagePath
	for _, directory := range []string{"cmd", "internal"} {
		forEachProductionGoFile(t, root, directory, func(path string, file *ast.File) {
			if filepath.ToSlash(filepath.Dir(pathFromRoot(root, path))) == linuxfsPackagePath {
				return
			}

			aliases := productionImportAliases(t, path, file)
			globalTrashLeases := mountsTrashLeaseVariablesAtFileScope(file, aliases, mountsImportPath)
			forEachFunctionScope(file, func(parameters []*ast.Field, body *ast.BlockStmt) {
				trashLeases := mountsTrashLeaseVariablesForScope(parameters, body, aliases, mountsImportPath)
				addVariableObjects(trashLeases, globalTrashLeases)
				inspectFunctionScope(body, func(node ast.Node) bool {
					selector, ok := node.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Duplicate" || !isTrackedMountsTrashLeaseReceiver(selector.X, trashLeases, aliases, mountsImportPath) {
						return true
					}
					t.Errorf("%s calls mounts.TrashLease.Duplicate; only %s may obtain raw Trash files/info descriptors", pathFromRoot(root, path), linuxfsPackagePath)
					return true
				})
			})
		})
	}
}

func mountsRootLeaseVariablesAtFileScope(file *ast.File, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if ok && isMountsRootLeaseType(value.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, value.Names)
			}
		}
	}
	return variables
}

func mountsRootLeaseVariablesForScope(parameters []*ast.Field, body *ast.BlockStmt, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, parameter := range parameters {
		if isMountsRootLeaseType(parameter.Type, aliases, mountsImportPath) {
			addNamedIdentifiers(variables, parameter.Names)
		}
	}

	inspectFunctionScope(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if isMountsRootLeaseType(node.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, node.Names)
			}
			for valueIndex, value := range node.Values {
				if !isMountsRootLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryValueSpecTarget(variables, node.Names, len(node.Values), valueIndex)
			}
		case *ast.AssignStmt:
			for valueIndex, value := range node.Rhs {
				if !isMountsRootLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryAssignmentTarget(variables, node.Lhs, len(node.Rhs), valueIndex)
			}
		}
		return true
	})

	return variables
}

func isTrackedMountsRootLeaseReceiver(expression ast.Expr, variables map[*ast.Object]struct{}, aliases map[string]string, mountsImportPath string) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		_, tracked := variables[identifier.Obj]
		return tracked
	}
	return isMountsRootLeaseType(expression, aliases, mountsImportPath)
}

func isMountsRootLeaseFactoryCall(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OpenTrustedRoot" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func isMountsRootLeaseType(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "RootLease" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func mountsLayoutLeaseVariablesAtFileScope(file *ast.File, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if ok && isMountsLayoutLeaseType(value.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, value.Names)
			}
		}
	}
	return variables
}

func mountsLayoutLeaseVariablesForScope(parameters []*ast.Field, body *ast.BlockStmt, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, parameter := range parameters {
		if isMountsLayoutLeaseType(parameter.Type, aliases, mountsImportPath) {
			addNamedIdentifiers(variables, parameter.Names)
		}
	}

	inspectFunctionScope(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if isMountsLayoutLeaseType(node.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, node.Names)
			}
			for valueIndex, value := range node.Values {
				if !isMountsLayoutLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryValueSpecTarget(variables, node.Names, len(node.Values), valueIndex)
			}
		case *ast.AssignStmt:
			for valueIndex, value := range node.Rhs {
				if !isMountsLayoutLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryAssignmentTarget(variables, node.Lhs, len(node.Rhs), valueIndex)
			}
		}
		return true
	})

	return variables
}

func isTrackedMountsLayoutLeaseReceiver(expression ast.Expr, variables map[*ast.Object]struct{}, aliases map[string]string, mountsImportPath string) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		_, tracked := variables[identifier.Obj]
		return tracked
	}
	return isMountsLayoutLeaseType(expression, aliases, mountsImportPath)
}

func isMountsLayoutLeaseFactoryCall(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OpenTrustedLayout" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func isMountsLayoutLeaseType(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "LayoutLease" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func mountsTrashLeaseVariablesAtFileScope(file *ast.File, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if ok && isMountsTrashLeaseType(value.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, value.Names)
			}
		}
	}
	return variables
}

func mountsTrashLeaseVariablesForScope(parameters []*ast.Field, body *ast.BlockStmt, aliases map[string]string, mountsImportPath string) map[*ast.Object]struct{} {
	variables := make(map[*ast.Object]struct{})
	for _, parameter := range parameters {
		if isMountsTrashLeaseType(parameter.Type, aliases, mountsImportPath) {
			addNamedIdentifiers(variables, parameter.Names)
		}
	}

	inspectFunctionScope(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			if isMountsTrashLeaseType(node.Type, aliases, mountsImportPath) {
				addNamedIdentifiers(variables, node.Names)
			}
			for valueIndex, value := range node.Values {
				if !isMountsTrashLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryValueSpecTarget(variables, node.Names, len(node.Values), valueIndex)
			}
		case *ast.AssignStmt:
			for valueIndex, value := range node.Rhs {
				if !isMountsTrashLeaseFactoryCall(value, aliases, mountsImportPath) {
					continue
				}
				addFactoryAssignmentTarget(variables, node.Lhs, len(node.Rhs), valueIndex)
			}
		}
		return true
	})

	return variables
}

func isTrackedMountsTrashLeaseReceiver(expression ast.Expr, variables map[*ast.Object]struct{}, aliases map[string]string, mountsImportPath string) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		_, tracked := variables[identifier.Obj]
		return tracked
	}
	return isMountsTrashLeaseType(expression, aliases, mountsImportPath)
}

func isMountsTrashLeaseFactoryCall(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OpenTrustedTrash" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func isMountsTrashLeaseType(expression ast.Expr, aliases map[string]string, mountsImportPath string) bool {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "TrashLease" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && aliases[packageName.Name] == mountsImportPath
}

func productionImportAliases(t *testing.T, path string, file *ast.File) map[string]string {
	t.Helper()

	aliases := make(map[string]string, len(file.Imports))
	for _, importSpec := range file.Imports {
		importPath, ok := importPathOf(t, path, importSpec)
		if !ok {
			continue
		}
		name := filepath.Base(importPath)
		if importSpec.Name != nil {
			name = importSpec.Name.Name
		}
		if name != "_" && name != "." {
			aliases[name] = importPath
		}
	}
	return aliases
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
