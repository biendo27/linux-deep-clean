package main

import (
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/application"
)

func TestBuildInfoComposesLinkerMetadata(t *testing.T) {
	originalVersion := version
	originalCommit := commit
	originalBuildTime := buildTime
	originalGoVersion := goVersion
	originalDirty := dirty
	t.Cleanup(func() {
		version = originalVersion
		commit = originalCommit
		buildTime = originalBuildTime
		goVersion = originalGoVersion
		dirty = originalDirty
	})

	tests := []struct {
		name      string
		version   string
		commit    string
		buildTime string
		goVersion string
		dirty     string
		want      application.BuildInfo
	}{
		{
			name:    "explicit development metadata",
			version: "dev",
			dirty:   "false",
			want: application.BuildInfo{
				Version:     "dev",
				Development: true,
			},
		},
		{
			name:      "complete clean release metadata",
			version:   "1.2.3-rc.1+build.42",
			commit:    "0123456789abcdef0123456789abcdef01234567",
			buildTime: "2026-07-15T12:34:56Z",
			goVersion: "go1.26.5",
			dirty:     "false",
			want: application.BuildInfo{
				Version:   "1.2.3-rc.1+build.42",
				Commit:    "0123456789abcdef0123456789abcdef01234567",
				BuildTime: "2026-07-15T12:34:56Z",
				GoVersion: "go1.26.5",
			},
		},
		{
			name:      "complete dirty release metadata",
			version:   "1.2.3",
			commit:    "abcdef0123456789abcdef0123456789abcdef01",
			buildTime: "2026-07-15T19:34:56+07:00",
			goVersion: "go1.26.5",
			dirty:     "true",
			want: application.BuildInfo{
				Version:   "1.2.3",
				Commit:    "abcdef0123456789abcdef0123456789abcdef01",
				BuildTime: "2026-07-15T19:34:56+07:00",
				GoVersion: "go1.26.5",
				Dirty:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = tt.version
			commit = tt.commit
			buildTime = tt.buildTime
			goVersion = tt.goVersion
			dirty = tt.dirty

			if got := buildInfo(); got != tt.want {
				t.Fatalf("buildInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
