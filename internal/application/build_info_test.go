package application

import "testing"

func TestBuildInfoValidateAcceptsExactDevelopmentBuild(t *testing.T) {
	info := BuildInfo{
		Version:     "dev",
		Development: true,
	}

	if err := info.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for the exact development form", err)
	}
}

func TestBuildInfoValidateRejectsDevelopmentMetadataVariations(t *testing.T) {
	validCommit := "0123456789abcdef0123456789abcdef01234567"

	tests := []struct {
		name string
		info BuildInfo
	}{
		{
			name: "non-dev version",
			info: BuildInfo{
				Version:     "1.2.3",
				Development: true,
			},
		},
		{
			name: "commit",
			info: BuildInfo{
				Version:     "dev",
				Commit:      validCommit,
				Development: true,
			},
		},
		{
			name: "build time",
			info: BuildInfo{
				Version:     "dev",
				BuildTime:   "2026-07-15T12:34:56Z",
				Development: true,
			},
		},
		{
			name: "Go version",
			info: BuildInfo{
				Version:     "dev",
				GoVersion:   "go1.26.5",
				Development: true,
			},
		},
		{
			name: "dirty tree",
			info: BuildInfo{
				Version:     "dev",
				Dirty:       true,
				Development: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.info.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestBuildInfoValidateAcceptsReleaseSemVer(t *testing.T) {
	tests := []BuildInfo{
		{
			Version:   "1.2.3",
			Commit:    "0123456789abcdef0123456789abcdef01234567",
			BuildTime: "2026-07-15T12:34:56Z",
			GoVersion: "go1.26.5",
		},
		{
			Version:   "1.2.3-rc.1+build.42",
			Commit:    "abcdef0123456789abcdef0123456789abcdef01",
			BuildTime: "2026-07-15T19:34:56+07:00",
			GoVersion: "go1.26.5",
		},
	}

	for _, info := range tests {
		if err := info.Validate(); err != nil {
			t.Fatalf("Validate() error = %v, want nil for valid release %+v", err, info)
		}
	}
}

func TestBuildInfoValidateRejectsInvalidReleaseMetadata(t *testing.T) {
	valid := BuildInfo{
		Version:   "1.2.3",
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		BuildTime: "2026-07-15T12:34:56Z",
		GoVersion: "go1.26.5",
	}

	tests := []struct {
		name string
		info BuildInfo
	}{
		{
			name: "development version without development flag",
			info: BuildInfo{
				Version: "dev",
			},
		},
		{
			name: "malformed semantic version",
			info: BuildInfo{
				Version:   "1.2",
				Commit:    valid.Commit,
				BuildTime: valid.BuildTime,
				GoVersion: valid.GoVersion,
			},
		},
		{
			name: "missing commit",
			info: BuildInfo{
				Version:   valid.Version,
				BuildTime: valid.BuildTime,
				GoVersion: valid.GoVersion,
			},
		},
		{
			name: "short commit",
			info: BuildInfo{
				Version:   valid.Version,
				Commit:    valid.Commit[:39],
				BuildTime: valid.BuildTime,
				GoVersion: valid.GoVersion,
			},
		},
		{
			name: "non-hex commit",
			info: BuildInfo{
				Version:   valid.Version,
				Commit:    "g123456789abcdef0123456789abcdef01234567",
				BuildTime: valid.BuildTime,
				GoVersion: valid.GoVersion,
			},
		},
		{
			name: "invalid RFC3339 build time",
			info: BuildInfo{
				Version:   valid.Version,
				Commit:    valid.Commit,
				BuildTime: "2026-07-15 12:34:56",
				GoVersion: valid.GoVersion,
			},
		},
		{
			name: "non-Go-1 version",
			info: BuildInfo{
				Version:   valid.Version,
				Commit:    valid.Commit,
				BuildTime: valid.BuildTime,
				GoVersion: "go2.0.0",
			},
		},
		{
			name: "dirty release",
			info: BuildInfo{
				Version:   valid.Version,
				Commit:    valid.Commit,
				BuildTime: valid.BuildTime,
				GoVersion: valid.GoVersion,
				Dirty:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.info.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}
