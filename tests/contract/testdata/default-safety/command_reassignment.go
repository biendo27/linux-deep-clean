package fixture

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func hermeticGoEnv(ambient []string) []string {
	environment := make([]string, 0, len(ambient)+8)
	return append(environment, "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GOROOT=", "PATH=/usr/bin:/bin", "LDCLEAN_VMTEST=", "LDCLEAN_VMTEST_TOKEN=")
}

func TestCommandReassignment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	command = exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	_ = command.Run()
}

func TestCommandRunsBeforeItsEnvironment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	_ = command.Run()
	command.Env = hermeticGoEnv(nil)
}

func TestCommandMethodValueRunsBeforeItsEnvironment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	run := command.Run
	_ = run()
	command.Env = hermeticGoEnv(nil)
}

func TestCommandPathMutation() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	command.Path = "/bin/sh"
	_ = command.Run()
}

func TestCommandEnvironmentMutation() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	command.Env[0] = "GOPROXY=https://proxy.example.invalid"
	_ = command.Run()
}

func TestCommandWorkingDirectoryMutation() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	command.Dir = "/tmp/untrusted-module"
	_ = command.Run()
}

var escapedGoCommand *exec.Cmd

func prepareEscapedGoCommand() {
	escapedGoCommand = exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	escapedGoCommand.Env = hermeticGoEnv(nil)
}

func TestEscapedGoCommand() {
	prepareEscapedGoCommand()
	escapedGoCommand.Path = "/bin/sh"
	_ = escapedGoCommand.Run()
}

func TestCommandConditionalEnvironment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	if false {
		command.Env = hermeticGoEnv(nil)
	}
	_ = command.Run()
}

func TestCommandGotoSkipsEnvironment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	goto run
	command.Env = hermeticGoEnv(nil)
run:
	_ = command.Run()
}

func TestCommandPathEnvironmentMutation(t *testing.T) {
	t.Setenv("PATH", "/tmp/untrusted-bin")
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	_ = command.Run()
}

func TestCommandPointerPathMutation() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	(*command).Path = "/bin/sh"
	_ = command.Run()
}

func TestCommandPointerExecutionBeforeEnvironment() {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	_ = (*command).Run()
	command.Env = hermeticGoEnv(nil)
}

func TestCommandParameterShadowsHermeticEnvironment(hermeticGoEnv func([]string) []string) {
	command := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-o", "safe", "./cmd/ldclean")
	command.Env = hermeticGoEnv(nil)
	_ = command.Run()
}

func TestTempDirBinaryPointerMutation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	*(&binary) = "/bin/sh"
	_ = exec.Command(binary).Run()
}

func TestTempDirBinaryParenthesizedPointerMutation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	pointer := &(binary)
	*pointer = "/bin/sh"
	_ = exec.Command(binary).Run()
}

func TestTempDirBinaryRangeShadow(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	for _, binary := range []string{"/bin/sh"} {
		_ = exec.Command(binary).Run()
	}
}

func TestTempDirBinaryRangeReassignment(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	for _, binary = range []string{"/bin/sh"} {
		_ = exec.Command(binary).Run()
	}
}

func TestTempDirCommandPathMutation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	command := exec.Command(binary)
	command.Path = "/bin/sh"
	_ = command.Run()
}

type fakePath struct{}

func (fakePath) Join(...string) string {
	return "/bin/sh"
}

func TestTempDirBinaryShadowedFilepath(t *testing.T) {
	filepath := fakePath{}
	binary := filepath.Join(t.TempDir(), "ldclean")
	_ = exec.Command(binary).Run()
}

var binary = "/bin/sh"

func TestTempDirBinaryBindingShadow(t *testing.T) {
	if true {
		binary := filepath.Join(t.TempDir(), "ldclean")
		_ = binary
	}
	_ = exec.Command(binary).Run()
}

func TestTempDirBinaryShortDeclarationReassignment(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ldclean")
	binary, marker := "/bin/sh", 0
	_ = marker
	_ = exec.Command(binary).Run()
}

func TestStoredExecConstructor() {
	factory := (exec.Command)
	command := factory("/bin/sh", "-c", "exit 0")
	_ = command.Run()
}
