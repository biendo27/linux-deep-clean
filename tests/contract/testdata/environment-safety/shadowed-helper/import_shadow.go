package fixture

import (
	hermeticGoEnv "example.invalid/unsafe-helper"
	"os/exec"
)

func TestImportShadow() {
	command := exec.Command("go", "list", "std")
	command.Env = hermeticGoEnv(nil)
	_ = command.Run()
}
