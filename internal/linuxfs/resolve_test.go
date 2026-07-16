//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

func TestResolveParentUsesAllRequiredOpenat2FlagsAndOneBasename(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	makeTestDirectory(t, rootFD, "parent")
	path := testBytePath(t, "parent", "target")

	openCalls := 0
	parent, basename, err := resolveParentWithRootFD(context.Background(), rootFD, path, func(fd int, name string, how *unix.OpenHow) (int, error) {
		openCalls++
		if name != "parent" {
			t.Fatalf("openat2 component = %q, want parent", name)
		}
		if how.Resolve != requiredOpenat2ResolveFlags {
			t.Fatalf("openat2 resolve flags = %#x, want %#x", how.Resolve, requiredOpenat2ResolveFlags)
		}
		if how.Flags != uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW) {
			t.Fatalf("openat2 flags = %#x, want O_RDONLY|O_DIRECTORY|O_CLOEXEC|O_NOFOLLOW", how.Flags)
		}
		return unix.Openat2(fd, name, how)
	})
	if err != nil {
		t.Fatalf("resolveParentWithRootFD() error = %v", err)
	}
	defer parent.Close()
	if openCalls != 1 {
		t.Fatalf("openat2 calls = %d, want 1 intermediate component", openCalls)
	}
	if basename != "target" {
		t.Fatalf("basename = %q, want target", basename)
	}
}

func TestResolveParentRejectsSymlinkTraversalAndBoundsEAGAIN(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	if err := unix.Symlinkat("elsewhere", rootFD, "link"); err != nil {
		t.Fatalf("create intermediate symlink: %v", err)
	}

	if _, _, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "link", "target"), unix.Openat2); !errors.Is(err, ErrDrifted) {
		t.Fatalf("resolve symlink traversal error = %v, want ErrDrifted", err)
	}

	attempts := 0
	_, _, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "parent", "target"), func(int, string, *unix.OpenHow) (int, error) {
		attempts++
		return -1, unix.EAGAIN
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("resolve EAGAIN exhaustion error = %v, want ErrDrifted", err)
	}
	if attempts != openat2AttemptLimit {
		t.Fatalf("openat2 EAGAIN attempts = %d, want %d", attempts, openat2AttemptLimit)
	}
}

func TestOpenTargetHandleRejectsTrailingSymlinkAndSpecialFiles(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	if err := unix.Symlinkat("elsewhere", rootFD, "link"); err != nil {
		t.Fatalf("create trailing symlink: %v", err)
	}
	if err := unix.Mkfifoat(rootFD, "fifo", 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}

	for _, name := range []string{"link", "fifo"} {
		t.Run(name, func(t *testing.T) {
			parent, basename, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, name), unix.Openat2)
			if err != nil {
				t.Fatalf("resolve parent: %v", err)
			}
			defer parent.Close()
			if _, err := OpenTargetHandle(context.Background(), parent, basename); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("OpenTargetHandle(%q) error = %v, want ErrUnsupported", name, err)
			}
		})
	}
}

func TestOpenTargetHandleSnapshotsHeldRegularFile(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	fileFD, err := unix.Openat(rootFD, "file", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	if err := unix.Close(fileFD); err != nil {
		t.Fatalf("close regular file: %v", err)
	}

	parent, basename, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "file"), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve parent: %v", err)
	}
	defer parent.Close()
	handle, err := OpenTargetHandle(context.Background(), parent, basename)
	if err != nil {
		t.Fatalf("OpenTargetHandle() error = %v", err)
	}
	defer handle.Close()
	required, err := RequiredStatMask(domain.ActionRestoreTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	snapshot, err := handle.Snapshot(required)
	if err != nil {
		t.Fatalf("handle.Snapshot() error = %v", err)
	}
	if snapshot.Type != "regular" {
		t.Fatalf("handle.Snapshot() type = %q, want regular", snapshot.Type)
	}
}

func TestResolveParentStopsBeforeOpeningAfterCancellation(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	context, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := resolveParentWithRootFD(context, rootFD, testBytePath(t, "parent", "target"), func(int, string, *unix.OpenHow) (int, error) {
		t.Fatal("openat2 must not run after context cancellation")
		return -1, nil
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("resolve canceled context error = %v, want ErrInterrupted", err)
	}
}

func TestResolveParentDoesNotRetryAmbiguousClose(t *testing.T) {
	var closed []int
	_, _, err := resolveParentFromOwnedRootFDForRootWithClose(
		context.Background(),
		101,
		domain.TrustedRootID("root-test"),
		testBytePath(t, "parent", "target"),
		func(fd int, name string, how *unix.OpenHow) (int, error) {
			if fd != 101 || name != "parent" {
				t.Fatalf("openat2 input = (%d, %q), want (101, parent)", fd, name)
			}
			return 102, nil
		},
		func(fd int) error {
			closed = append(closed, fd)
			if fd == 101 {
				return unix.EIO
			}
			return nil
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("resolve error = %v, want ErrDrifted", err)
	}
	if len(closed) != 2 || closed[0] != 101 || closed[1] != 102 {
		t.Fatalf("closed descriptors = %v, want each descriptor closed once", closed)
	}
}

func TestParentLeaseSyncFailsClosed(t *testing.T) {
	rootFD := openTestDirectory(t)
	defer unix.Close(rootFD)
	makeTestDirectory(t, rootFD, "parent")
	parent, _, err := resolveParentWithRootFD(context.Background(), rootFD, testBytePath(t, "parent", "target"), unix.Openat2)
	if err != nil {
		t.Fatalf("resolve parent: %v", err)
	}
	defer parent.Close()

	if err := syncParentLeaseWith(parent, func(int) error { return unix.EIO }); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("syncParentLeaseWith(EIO) error = %v, want ErrInterrupted", err)
	}
	if err := syncParentLeaseWith(parent, func(int) error { return unix.EINVAL }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("syncParentLeaseWith(EINVAL) error = %v, want ErrUnsupported", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatalf("close parent: %v", err)
	}
	if err := syncParentLeaseWith(parent, func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("sync closed parent error = %v, want ErrDrifted", err)
	}
}

func openTestDirectory(t *testing.T) int {
	t.Helper()
	fd, err := unix.Open(t.TempDir(), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open temporary directory: %v", err)
	}
	return fd
}

func makeTestDirectory(t *testing.T, parentFD int, name string) {
	t.Helper()
	if err := unix.Mkdirat(parentFD, name, 0o700); err != nil {
		t.Fatalf("mkdir %q: %v", name, err)
	}
}

func testBytePath(t *testing.T, components ...string) pathbytes.BytePath {
	t.Helper()
	bytes := make([][]byte, len(components))
	for index, component := range components {
		bytes[index] = []byte(component)
	}
	path, err := pathbytes.New(bytes)
	if err != nil {
		t.Fatalf("pathbytes.New(%q) error = %v", components, err)
	}
	return path
}
