package fixture

func hermeticGoEnv() []string {
	_ = []string{"GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS="}
	return []string{"GOPROXY=https://proxy.example.invalid"}
}
