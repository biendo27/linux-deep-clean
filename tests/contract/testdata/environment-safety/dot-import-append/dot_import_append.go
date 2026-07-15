package fixture

import . "example.invalid/append"

func hermeticGoEnv(ambient []string) []string {
	return append(ambient, "GOTOOLCHAIN=local", "GOPROXY=off", "GOWORK=off", "GOFLAGS=", "GOROOT=", "PATH=/usr/bin:/bin", "LDCLEAN_VMTEST=", "LDCLEAN_VMTEST_TOKEN=")
}
