package application

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	fuzzBuildInfoReleaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	fuzzBuildInfoCommit         = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	fuzzBuildInfoGoVersion      = regexp.MustCompile(`^go1\.[0-9]+(?:\.[0-9]+)?$`)
)

func FuzzBuildInfoValidate(f *testing.F) {
	const (
		commit          = "0123456789abcdef0123456789abcdef01234567"
		mixedCaseCommit = "0123456789aBcDeF0123456789AbCdEf01234567"
	)

	f.Add("dev", "", "", "", false, true)
	f.Add("1.2.3", commit, "2026-07-15T12:34:56Z", "go1.26.5", false, false)
	f.Add("1.2.3", mixedCaseCommit, "2026-07-15T12:34:56Z", "go1.26.5", false, false)
	f.Add("1.2.3-rc.1+build.42", commit, "2026-07-15T19:34:56+07:00", "go1.26.5", false, false)
	f.Add("1.2.3-01", commit, "2026-07-15T12:34:56Z", "go1.26.5", false, false)
	f.Add("dev", commit, "", "", false, true)
	f.Add("1.2", commit, "2026-07-15T12:34:56Z", "go1.26.5", false, false)
	f.Add("1.2.3", "", "2026-07-15T12:34:56Z", "go1.26.5", false, false)
	f.Add("1.2.3", commit, "not-a-time", "go2.0.0", true, false)

	f.Fuzz(func(t *testing.T, version, commit, buildTime, goVersion string, dirty, development bool) {
		info := BuildInfo{
			Version:     version,
			Commit:      commit,
			BuildTime:   buildTime,
			GoVersion:   goVersion,
			Dirty:       dirty,
			Development: development,
		}

		err, panicValue := fuzzCallBuildInfoValidate(info)
		if panicValue != nil {
			t.Fatalf("Validate() panicked for %+v: %v", info, panicValue)
		}

		wantValid := fuzzBuildInfoHasValidShape(info)
		if gotValid := err == nil; gotValid != wantValid {
			t.Fatalf("Validate() valid = %t, want %t for %+v (err = %v)", gotValid, wantValid, info, err)
		}
	})
}

func fuzzCallBuildInfoValidate(info BuildInfo) (err error, panicValue any) {
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			panicValue = recovered
		} else if !completed {
			panicValue = "panic with nil value"
		}
	}()

	err = info.Validate()
	completed = true
	return err, nil
}

func fuzzBuildInfoHasValidShape(info BuildInfo) bool {
	if info.Development {
		return info.Version == "dev" && info.Commit == "" && info.BuildTime == "" && info.GoVersion == "" && !info.Dirty
	}

	if info.Dirty || !fuzzBuildInfoReleaseVersionHasValidPrerelease(info.Version) || !fuzzBuildInfoCommit.MatchString(info.Commit) || !fuzzBuildInfoGoVersion.MatchString(info.GoVersion) {
		return false
	}

	_, err := time.Parse(time.RFC3339, info.BuildTime)
	return err == nil
}

func fuzzBuildInfoReleaseVersionHasValidPrerelease(version string) bool {
	if !fuzzBuildInfoReleaseVersion.MatchString(version) {
		return false
	}

	withoutBuild, _, _ := strings.Cut(version, "+")
	_, prerelease, hasPrerelease := strings.Cut(withoutBuild, "-")
	if !hasPrerelease {
		return true
	}

	for _, identifier := range strings.Split(prerelease, ".") {
		if len(identifier) > 1 && identifier[0] == '0' && fuzzBuildInfoDecimalIdentifier(identifier) {
			return false
		}
	}

	return true
}

func fuzzBuildInfoDecimalIdentifier(identifier string) bool {
	for _, character := range identifier {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
