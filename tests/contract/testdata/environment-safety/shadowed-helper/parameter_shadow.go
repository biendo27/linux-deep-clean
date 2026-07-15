package fixture

import "os/exec"

func TestParameterShadow(hermeticGoEnv func([]string) []string) {
	command := exec.Command("go", "list", "std")
	command.Env = hermeticGoEnv(nil)
	_ = command.Run()
}
