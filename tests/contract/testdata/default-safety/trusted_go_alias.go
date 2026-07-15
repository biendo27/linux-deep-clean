package fixture

import (
	"os/exec"
	fp "path/filepath"
	rt "runtime"
)

type fakePath struct{}

func (fakePath) Join(...string) string {
	return "/bin/sh"
}

type fakeRuntime struct{}

func (fakeRuntime) GOROOT() string {
	return "/tmp/untrusted"
}

func TestShadowedTrustedGoToolchain() {
	fp := fakePath{}
	rt := fakeRuntime{}
	command := exec.Command(fp.Join(rt.GOROOT(), "bin", "go"), "list", "std")
	command.Env = hermeticGoEnv(nil)
	_ = command.Run()
}
