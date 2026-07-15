package main

import (
	"context"
	"os"

	"github.com/biendo27/linux-deep-clean/internal/application"
	"github.com/biendo27/linux-deep-clean/internal/presenters/cli"
)

var (
	version   = "dev"
	commit    = ""
	buildTime = ""
	goVersion = ""
	dirty     = "false"
)

func main() {
	os.Exit(cli.Execute(context.Background(), application.NewBootstrap(buildInfo()), os.Stdout, os.Stderr))
}

func buildInfo() application.BuildInfo {
	return application.BuildInfo{
		Version:     version,
		Commit:      commit,
		BuildTime:   buildTime,
		GoVersion:   goVersion,
		Dirty:       dirty == "true",
		Development: version == "dev",
	}
}
