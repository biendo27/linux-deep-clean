//go:build vmtest && vmguardunit

package vmtest

import (
	"bytes"
	"io/fs"
	"strings"
	"syscall"
	"testing"
	"time"
)

// vmguardunit selects dependency-injection tests only. Normal VM test bodies
// use vmtest && !vmguardunit and therefore cannot run in this unguarded mode.
func TestRequireDisposableGuestWithAcceptsOnlyTheFixedVerifiedSentinel(t *testing.T) {
	var lstatPath, openPath string
	deps := validGuardDependencies()
	deps.lstat = func(path string) (fs.FileInfo, error) {
		lstatPath = path
		return verifiedTestFileInfo(0o600, 0, 1, 1), nil
	}
	deps.open = func(path string) (sentinelFile, error) {
		openPath = path
		return newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 1), []byte("verified-token")), nil
	}

	if err := requireDisposableGuestWith(deps); err != nil {
		t.Fatalf("requireDisposableGuestWith() error = %v", err)
	}
	if lstatPath != disposableGuestSentinel || openPath != disposableGuestSentinel {
		t.Fatalf("guard paths = lstat %q, open %q; want fixed sentinel %q", lstatPath, openPath, disposableGuestSentinel)
	}
}

func TestRequireDisposableGuestWithRejectsUnsafeState(t *testing.T) {
	tests := []struct {
		name      string
		deps      guardDependencies
		wantError string
	}{
		{
			name:      "missing guard dependencies",
			deps:      guardDependencies{},
			wantError: "guard dependencies are unavailable",
		},
		{
			name: "missing enablement environment",
			deps: guardDependencies{
				getenv: func(string) string { return "" },
				lstat:  mustNotLstat(t),
				open:   mustNotOpen(t),
			},
			wantError: "LDCLEAN_VMTEST=1",
		},
		{
			name: "missing token",
			deps: guardDependencies{
				getenv: func(name string) string {
					if name == "LDCLEAN_VMTEST" {
						return "1"
					}
					return ""
				},
				lstat: mustNotLstat(t),
				open:  mustNotOpen(t),
			},
			wantError: "nonempty LDCLEAN_VMTEST_TOKEN",
		},
		{
			name: "missing sentinel",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist },
				open:   mustNotOpen(t),
			},
			wantError: "vmtest disposable-guest sentinel",
		},
		{
			name: "missing sentinel metadata",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return nil, nil },
				open:   mustNotOpen(t),
			},
			wantError: "sentinel metadata is unavailable",
		},
		{
			name: "symlink sentinel",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return verifiedTestFileInfo(fs.ModeSymlink|0o777, 0, 1, 1), nil },
				open:   mustNotOpen(t),
			},
			wantError: "must not be a symlink",
		},
		{
			name: "nonregular sentinel",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return verifiedTestFileInfo(fs.ModeDir|0o700, 0, 1, 1), nil },
				open:   mustNotOpen(t),
			},
			wantError: "must be a regular file",
		},
		{
			name: "group writable sentinel",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return verifiedTestFileInfo(0o620, 0, 1, 1), nil },
				open:   mustNotOpen(t),
			},
			wantError: "must not be group or world writable",
		},
		{
			name: "world writable sentinel",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return verifiedTestFileInfo(0o602, 0, 1, 1), nil },
				open:   mustNotOpen(t),
			},
			wantError: "must not be group or world writable",
		},
		{
			name: "wrong owner",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return verifiedTestFileInfo(0o600, 1000, 1, 1), nil },
				open:   mustNotOpen(t),
			},
			wantError: "must be root-owned",
		},
		{
			name: "missing stat metadata",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return testFileInfo{mode: 0o600}, nil },
				open:   mustNotOpen(t),
			},
			wantError: "must be root-owned",
		},
		{
			name: "non Stat_t metadata",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  func(string) (fs.FileInfo, error) { return testFileInfo{mode: 0o600, system: struct{}{}}, nil },
				open:   mustNotOpen(t),
			},
			wantError: "must be root-owned",
		},
		{
			name: "open sentinel error",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open:   func(string) (sentinelFile, error) { return nil, fs.ErrPermission },
			},
			wantError: "open vmtest disposable-guest sentinel",
		},
		{
			name: "opened sentinel is unavailable",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open:   func(string) (sentinelFile, error) { return nil, nil },
			},
			wantError: "sentinel file is unavailable",
		},
		{
			name: "opened sentinel metadata is unavailable",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					return newTestSentinelFile(nil, nil), nil
				},
			},
			wantError: "sentinel metadata is unavailable",
		},
		{
			name: "opened sentinel stat error",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					file := newTestSentinelFile(nil, nil)
					file.statErr = fs.ErrPermission
					return file, nil
				},
			},
			wantError: "stat opened vmtest disposable-guest sentinel",
		},
		{
			name: "opened sentinel has unsafe metadata",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					return newTestSentinelFile(verifiedTestFileInfo(0o622, 0, 1, 1), nil), nil
				},
			},
			wantError: "must not be group or world writable",
		},
		{
			name: "read sentinel error",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					file := newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 1), nil)
					file.readErr = fs.ErrPermission
					return file, nil
				},
			},
			wantError: "read vmtest disposable-guest sentinel",
		},
		{
			name: "wrong token",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					return newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 1), []byte("different-token")), nil
				},
			},
			wantError: "token does not match LDCLEAN_VMTEST_TOKEN exactly",
		},
		{
			name: "sentinel token has an appended suffix",
			deps: guardDependencies{
				getenv: validGetenv,
				lstat:  validLstat,
				open: func(string) (sentinelFile, error) {
					return newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 1), []byte("verified-token-extra")), nil
				},
			},
			wantError: "token does not match LDCLEAN_VMTEST_TOKEN exactly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireDisposableGuestWith(tt.deps)
			if err == nil {
				t.Fatal("requireDisposableGuestWith() error = nil, want refusal")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("requireDisposableGuestWith() error = %q, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestRequireDisposableGuestWithRejectsSentinelSwappedAfterMetadataCheck(t *testing.T) {
	deps := validGuardDependencies()
	deps.open = func(path string) (sentinelFile, error) {
		if path != disposableGuestSentinel {
			t.Fatalf("open path = %q, want fixed sentinel %q", path, disposableGuestSentinel)
		}
		return newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 2), []byte("verified-token")), nil
	}

	err := requireDisposableGuestWith(deps)
	if err == nil {
		t.Fatal("requireDisposableGuestWith() error = nil, want refusal after sentinel replacement")
	}
	if !strings.Contains(err.Error(), "changed during verification") {
		t.Fatalf("requireDisposableGuestWith() error = %q, want replacement refusal", err)
	}
}

func validGuardDependencies() guardDependencies {
	return guardDependencies{
		getenv: validGetenv,
		lstat:  validLstat,
		open: func(string) (sentinelFile, error) {
			return newTestSentinelFile(verifiedTestFileInfo(0o600, 0, 1, 1), []byte("verified-token")), nil
		},
	}
}

func validLstat(string) (fs.FileInfo, error) {
	return verifiedTestFileInfo(0o600, 0, 1, 1), nil
}

func validGetenv(name string) string {
	switch name {
	case "LDCLEAN_VMTEST":
		return "1"
	case "LDCLEAN_VMTEST_TOKEN":
		return "verified-token"
	default:
		return ""
	}
}

func mustNotLstat(t *testing.T) func(string) (fs.FileInfo, error) {
	t.Helper()

	return func(string) (fs.FileInfo, error) {
		t.Fatal("lstat must not run")
		return nil, nil
	}
}

func mustNotOpen(t *testing.T) func(string) (sentinelFile, error) {
	t.Helper()

	return func(string) (sentinelFile, error) {
		t.Fatal("open must not run")
		return nil, nil
	}
}

type testSentinelFile struct {
	reader  *bytes.Reader
	info    fs.FileInfo
	statErr error
	readErr error
}

func newTestSentinelFile(info fs.FileInfo, contents []byte) *testSentinelFile {
	return &testSentinelFile{reader: bytes.NewReader(contents), info: info}
}

func (file *testSentinelFile) Read(buffer []byte) (int, error) {
	if file.readErr != nil {
		return 0, file.readErr
	}
	return file.reader.Read(buffer)
}

func (file *testSentinelFile) Stat() (fs.FileInfo, error) {
	return file.info, file.statErr
}

func (file *testSentinelFile) Close() error { return nil }

type testFileInfo struct {
	mode   fs.FileMode
	system any
}

func verifiedTestFileInfo(mode fs.FileMode, uid uint32, device, inode uint64) testFileInfo {
	return testFileInfo{mode: mode, system: &syscall.Stat_t{Uid: uid, Dev: device, Ino: inode}}
}

func (info testFileInfo) Name() string       { return "disposable-guest" }
func (info testFileInfo) Size() int64        { return 0 }
func (info testFileInfo) Mode() fs.FileMode  { return info.mode }
func (info testFileInfo) ModTime() time.Time { return time.Time{} }
func (info testFileInfo) IsDir() bool        { return false }
func (info testFileInfo) Sys() any           { return info.system }
