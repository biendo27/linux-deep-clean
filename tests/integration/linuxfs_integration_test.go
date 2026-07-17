//go:build integration && linux

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

const nonMutatingHarnessSchedule = "outside-sentinel-self-check/no-op"

type linuxFSHarnessFixture struct {
	attemptRoot     string
	outsideSentinel string
	seed            string
}

type harnessReplay struct {
	Seed     string
	Schedule string
}

type outsideSentinelSnapshot struct {
	contents []byte
	mode     fs.FileMode
	size     int64
	device   uint64
	inode    uint64
}

func TestLinuxFSHarnessSelfCheckPreservesOutsideSentinel(t *testing.T) {
	fixture := newLinuxFSHarnessFixture(t)
	before := captureOutsideSentinel(t, fixture.outsideSentinel)

	replay, err := runNonMutatingHarnessAttempt(context.Background(), fixture)
	if err != nil {
		t.Fatalf("run non-mutating harness attempt: %v", err)
	}
	if replay.Seed != fixture.seed {
		t.Fatalf("replay seed = %q, want fixture seed %q", replay.Seed, fixture.seed)
	}
	if replay.Schedule != nonMutatingHarnessSchedule {
		t.Fatalf("replay schedule = %q, want %q", replay.Schedule, nonMutatingHarnessSchedule)
	}

	after := captureOutsideSentinel(t, fixture.outsideSentinel)
	if err := assertOutsideSentinelUnchanged(before, after); err != nil {
		t.Fatalf("outside sentinel changed after non-mutating harness attempt: %v", err)
	}

	poisoned := after
	poisoned.contents = append([]byte(nil), after.contents...)
	poisoned.contents[0] ^= 0xff
	if err := assertOutsideSentinelUnchanged(before, poisoned); err == nil {
		t.Fatal("outside-sentinel assertion accepted a poisoned observation")
	}
}

func TestOutsideSentinelComparisonRejectsEveryTrackedDifference(t *testing.T) {
	fixture := newLinuxFSHarnessFixture(t)
	before := captureOutsideSentinel(t, fixture.outsideSentinel)

	for _, test := range []struct {
		name   string
		mutate func(*outsideSentinelSnapshot)
	}{
		{
			name: "device",
			mutate: func(snapshot *outsideSentinelSnapshot) {
				snapshot.device++
			},
		},
		{
			name: "inode",
			mutate: func(snapshot *outsideSentinelSnapshot) {
				snapshot.inode++
			},
		},
		{
			name: "mode",
			mutate: func(snapshot *outsideSentinelSnapshot) {
				snapshot.mode ^= 0o100
			},
		},
		{
			name: "size",
			mutate: func(snapshot *outsideSentinelSnapshot) {
				snapshot.size++
			},
		},
		{
			name: "contents",
			mutate: func(snapshot *outsideSentinelSnapshot) {
				snapshot.contents = append([]byte(nil), snapshot.contents...)
				snapshot.contents[0] ^= 0xff
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			poisoned := before
			test.mutate(&poisoned)
			if err := assertOutsideSentinelUnchanged(before, poisoned); err == nil {
				t.Fatalf("outside-sentinel assertion accepted poisoned %s", test.name)
			}
		})
	}
}

func newLinuxFSHarnessFixture(t *testing.T) linuxFSHarnessFixture {
	t.Helper()

	attemptRoot := t.TempDir()
	outsideRoot := t.TempDir()
	sentinel := filepath.Join(outsideRoot, "outside-sentinel")
	if err := os.WriteFile(sentinel, []byte("linuxfs integration outside sentinel\n"), 0o600); err != nil {
		t.Fatalf("create outside sentinel: %v", err)
	}

	digest := sha256.Sum256([]byte(t.Name()))
	return linuxFSHarnessFixture{
		attemptRoot:     attemptRoot,
		outsideSentinel: sentinel,
		seed:            fmt.Sprintf("%x", digest),
	}
}

// runNonMutatingHarnessAttempt establishes the first replay record and
// outside-sentinel assertion path without performing a filesystem operation.
// VM tests will replace this no-op with production API calls only after their
// root/layout authority and disposable-guest fixtures exist.
func runNonMutatingHarnessAttempt(ctx context.Context, fixture linuxFSHarnessFixture) (harnessReplay, error) {
	if ctx == nil {
		return harnessReplay{}, fmt.Errorf("nil harness context")
	}
	if err := ctx.Err(); err != nil {
		return harnessReplay{}, err
	}
	if fixture.attemptRoot == "" || fixture.outsideSentinel == "" || fixture.seed == "" {
		return harnessReplay{}, fmt.Errorf("incomplete LinuxFS harness fixture")
	}
	return harnessReplay{Seed: fixture.seed, Schedule: nonMutatingHarnessSchedule}, nil
}

func captureOutsideSentinel(t *testing.T, path string) outsideSentinelSnapshot {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat outside sentinel: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("outside sentinel mode = %v, want regular file", info.Mode())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("outside sentinel metadata type = %T, want *syscall.Stat_t", info.Sys())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read outside sentinel: %v", err)
	}
	return outsideSentinelSnapshot{
		contents: append([]byte(nil), contents...),
		mode:     info.Mode(),
		size:     info.Size(),
		device:   uint64(stat.Dev),
		inode:    stat.Ino,
	}
}

func assertOutsideSentinelUnchanged(before, after outsideSentinelSnapshot) error {
	if before.device != after.device || before.inode != after.inode {
		return fmt.Errorf("sentinel identity changed")
	}
	if before.mode != after.mode {
		return fmt.Errorf("sentinel mode changed from %v to %v", before.mode, after.mode)
	}
	if before.size != after.size {
		return fmt.Errorf("sentinel size changed from %d to %d", before.size, after.size)
	}
	if !bytes.Equal(before.contents, after.contents) {
		return fmt.Errorf("sentinel contents changed")
	}
	return nil
}
