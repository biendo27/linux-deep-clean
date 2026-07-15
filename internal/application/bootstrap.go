// Package application defines use cases shared by LDC presenters.
package application

import "os"

// Bootstrap is the narrow application contract available to the bootstrap
// presenter. It intentionally exposes neither discovery nor mutation.
type Bootstrap interface {
	BuildInfo() BuildInfo
	RequireUnprivileged() error
}

type bootstrap struct {
	buildInfo BuildInfo
}

// NewBootstrap binds build metadata to the production effective-UID guard.
// No caller-controlled root override is available.
func NewBootstrap(buildInfo BuildInfo) Bootstrap {
	return bootstrap{buildInfo: buildInfo}
}

func (b bootstrap) BuildInfo() BuildInfo {
	return b.buildInfo
}

func (b bootstrap) RequireUnprivileged() error {
	return RequireUnprivileged(os.Geteuid())
}
