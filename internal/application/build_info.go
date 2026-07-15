package application

import (
	"errors"
	"regexp"
	"time"
)

var (
	semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:(?:0|[1-9][0-9]*)|(?:[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))(?:\.(?:(?:0|[1-9][0-9]*)|(?:[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	commitPattern          = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	goVersionPattern       = regexp.MustCompile(`^go1\.[0-9]+(?:\.[0-9]+)?$`)
)

// BuildInfo is the validated metadata embedded in a bootstrap binary. A
// development build is deliberately explicit and carries no release claims.
type BuildInfo struct {
	Version     string
	Commit      string
	BuildTime   string
	GoVersion   string
	Dirty       bool
	Development bool
}

// Validate accepts either the one explicit development form or complete,
// reproducible release metadata. A dirty build is never a valid release.
func (b BuildInfo) Validate() error {
	if b.Development {
		if b.Version == "dev" && b.Commit == "" && b.BuildTime == "" && b.GoVersion == "" && !b.Dirty {
			return nil
		}
		return errors.New("invalid development build metadata")
	}

	if b.Dirty {
		return errors.New("release build metadata must be clean")
	}
	if !semanticVersionPattern.MatchString(b.Version) {
		return errors.New("release version is not semantic versioning")
	}
	if !commitPattern.MatchString(b.Commit) {
		return errors.New("release commit must be a full hexadecimal revision")
	}
	if _, err := time.Parse(time.RFC3339, b.BuildTime); err != nil {
		return errors.New("release build time must be RFC3339")
	}
	if !goVersionPattern.MatchString(b.GoVersion) {
		return errors.New("release Go version must identify Go 1.x")
	}

	return nil
}
