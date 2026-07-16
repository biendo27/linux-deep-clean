//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestPublishFileDurablePublishesOneNoReplaceFileInDurabilityOrder(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	var events []string
	err := publishFileDurableWith(context.Background(), lease, "record", []byte("durable record"), durableFileHooks{
		open: func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
			events = append(events, "open")
			return unix.Openat(directoryFD, basename, flags, mode)
		},
		write: func(fd int, contents []byte) (int, error) {
			events = append(events, "write")
			return unix.Write(fd, contents)
		},
		syncFile: func(fd int) error {
			events = append(events, "file-sync")
			return unix.Fsync(fd)
		},
		syncDirectory: func(fd int) error {
			if len(events) == 0 {
				events = append(events, "preflight-sync")
			} else {
				events = append(events, "directory-sync")
			}
			return unix.Fsync(fd)
		},
		close: func(fd int) error {
			events = append(events, "close")
			return unix.Close(fd)
		},
	})
	if err != nil {
		t.Fatalf("publishFileDurableWith() error = %v", err)
	}

	wantEvents := []string{"preflight-sync", "open", "write", "file-sync", "close", "directory-sync"}
	if len(events) != len(wantEvents) {
		t.Fatalf("durability events = %v, want %v", events, wantEvents)
	}
	for index := range wantEvents {
		if events[index] != wantEvents[index] {
			t.Fatalf("durability event %d = %q, want %q; all=%v", index, events[index], wantEvents[index], events)
		}
	}
	if content := readTestFile(t, directory, "record"); string(content) != "durable record" {
		t.Fatalf("published content = %q, want durable record", content)
	}
	if mode := testEntryMode(t, directory, "record"); mode != 0o600 {
		t.Fatalf("published mode = %#o, want 0600", mode)
	}
}

func TestPublishFileDurableFailsBeforeMutationWithoutAQualifiedPrivateLease(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	tests := []struct {
		name      string
		directory *PrivateDirectoryLease
		basename  string
		want      error
	}{
		{name: "nil directory", directory: nil, basename: "record", want: ErrUnsupported},
		{name: "unqualified directory", directory: &PrivateDirectoryLease{}, basename: "record", want: ErrUnsupported},
		{name: "multi component basename", directory: lease, basename: "nested/record", want: ErrUnsupported},
		{name: "dot basename", directory: lease, basename: ".", want: ErrUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := PublishFileDurable(context.Background(), test.directory, test.basename, []byte("contents"))
			if !errors.Is(err, test.want) {
				t.Fatalf("PublishFileDurable() error = %v, want %v", err, test.want)
			}
			if testEntryExists(t, directory, "record") {
				t.Fatal("PublishFileDurable() created an entry before lease/name validation completed")
			}
		})
	}
}

func TestPublishFileDurableNeverOverwritesExistingOrHostileEntry(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	createTestFileWithContents(t, directory, "record", []byte("original"))
	before := testEntryIdentity(t, directory, "record")
	if err := PublishFileDurable(context.Background(), lease, "record", []byte("replacement")); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PublishFileDurable() collision error = %v, want ErrDrifted", err)
	}
	after := testEntryIdentity(t, directory, "record")
	if after != before {
		t.Fatalf("PublishFileDurable() changed existing entry identity = %#v, want %#v", after, before)
	}
	if content := readTestFile(t, directory, "record"); string(content) != "original" {
		t.Fatalf("existing content = %q, want original", content)
	}
}

func TestPublishFileDurableRetainsPostCreateUncertaintyForReconciliation(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	err := publishFileDurableWith(context.Background(), lease, "record", []byte("partial"), durableFileHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      func(int) error { return unix.EIO },
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("publishFileDurableWith() file-sync error = %v, want ErrInterrupted", err)
	}
	if !testEntryExists(t, directory, "record") {
		t.Fatal("PublishFileDurable() removed a post-create record after an uncertain sync")
	}
	if content := readTestFile(t, directory, "record"); string(content) != "partial" {
		t.Fatalf("retained uncertain content = %q, want partial", content)
	}
}

func TestPublishFileDurableRejectsUnsupportedPreflightWithoutCreatingAnEntry(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), durableFileHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: func(int) error { return unix.EOPNOTSUPP },
		close:         unix.Close,
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("publishFileDurableWith() preflight error = %v, want ErrUnsupported", err)
	}
	if testEntryExists(t, directory, "record") {
		t.Fatal("PublishFileDurable() created a record after unsupported preflight durability")
	}
}

func TestPublishFileDurableNeverClaimsSuccessAfterCancellationPostCreate(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := publishFileDurableWith(ctx, lease, "record", []byte("contents"), durableFileHooks{
		open: func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
			fd, err := openDurableFile(directoryFD, basename, flags, mode)
			if err == nil {
				cancel()
			}
			return fd, err
		},
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("publishFileDurableWith() cancellation error = %v, want ErrInterrupted", err)
	}
	if !testEntryExists(t, directory, "record") {
		t.Fatal("PublishFileDurable() removed a record after cancellation became ambiguous")
	}
}

func TestPublishFileDurableDoesNotClaimSuccessAfterSameUIDNameReplacement(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	syncCalls := 0
	err := publishFileDurableWith(context.Background(), lease, "record", []byte("owned"), durableFileHooks{
		open:     openDurableFile,
		write:    unix.Write,
		syncFile: unix.Fsync,
		syncDirectory: func(directoryFD int) error {
			syncCalls++
			if err := unix.Fsync(directoryFD); err != nil {
				return err
			}
			if syncCalls != 2 {
				return nil
			}
			if err := unix.Unlinkat(directoryFD, "record", 0); err != nil {
				return err
			}
			fd, err := unix.Openat(directoryFD, "record", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
			if err != nil {
				return err
			}
			if _, err := unix.Write(fd, []byte("replacement")); err != nil {
				_ = unix.Close(fd)
				return err
			}
			return unix.Close(fd)
		},
		close: unix.Close,
	})
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("publishFileDurableWith() replacement error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if content := readTestFile(t, directory, "record"); string(content) != "replacement" {
		t.Fatalf("replacement content = %q, want replacement retained for reconciliation", content)
	}
}

func TestPublishFileDurableDoesNotClaimSuccessWhenSameUIDChangesNewRecordMode(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	err := publishFileDurableWith(context.Background(), lease, "record", []byte("private"), durableFileHooks{
		open: func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
			fd, err := openDurableFile(directoryFD, basename, flags, mode)
			if err != nil {
				return -1, err
			}
			if err := unix.Fchmod(fd, 0o644); err != nil {
				_ = unix.Close(fd)
				return -1, err
			}
			return fd, nil
		},
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	})
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("publishFileDurableWith() mode-change error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if !testEntryExists(t, directory, "record") {
		t.Fatal("PublishFileDurable() removed an unsafe post-create record instead of retaining it for reconciliation")
	}
	if mode := testEntryMode(t, directory, "record"); mode != 0o644 {
		t.Fatalf("retained unsafe record mode = %#o, want 0644", mode)
	}
}

func TestPrivateDirectoryLeaseLifecyclePreservesIdentityButFailsClosedAfterClose(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	if lease.RootID() != rootID {
		t.Fatalf("PrivateDirectoryLease.RootID() = %q, want %q", lease.RootID(), rootID)
	}
	if lease.Kind() != mounts.LayoutPrivateState {
		t.Fatalf("PrivateDirectoryLease.Kind() = %q, want %q", lease.Kind(), mounts.LayoutPrivateState)
	}

	called := false
	if err := lease.withFD(func(fd int) error {
		called = true
		return unix.Fsync(fd)
	}); err != nil {
		t.Fatalf("PrivateDirectoryLease.withFD() error = %v", err)
	}
	if !called {
		t.Fatal("PrivateDirectoryLease.withFD() did not invoke its scoped operation")
	}
	if err := lease.withFD(nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PrivateDirectoryLease.withFD(nil) error = %v, want ErrUnsupported", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("PrivateDirectoryLease.Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("PrivateDirectoryLease.Close() second error = %v", err)
	}
	if lease.RootID() != rootID || lease.Kind() != mounts.LayoutPrivateState {
		t.Fatalf("closed private lease identity = (%q, %q), want (%q, %q)", lease.RootID(), lease.Kind(), rootID, mounts.LayoutPrivateState)
	}
	if err := lease.withFD(func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed PrivateDirectoryLease.withFD() error = %v, want ErrDrifted", err)
	}

	var nilLease *PrivateDirectoryLease
	if nilLease.RootID() != "" || nilLease.Kind() != "" {
		t.Fatalf("nil private lease identity = (%q, %q), want empty values", nilLease.RootID(), nilLease.Kind())
	}
	if err := nilLease.Close(); err != nil {
		t.Fatalf("nil PrivateDirectoryLease.Close() error = %v", err)
	}
	if err := nilLease.withFD(func(int) error { return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("nil PrivateDirectoryLease.withFD() error = %v, want ErrUnsupported", err)
	}
}

func TestPrivateDirectoryLeaseQualificationRejectsUnsafeDescriptorsAndMetadata(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	if _, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			return -1, nil
		},
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateDirectoryWithSource(negative descriptor) error = %v, want ErrUnsupported", err)
	}

	duplicate := func(t *testing.T) func() (int, error) {
		t.Helper()
		return func() (int, error) {
			fd, err := directory.duplicate()
			if err != nil {
				return -1, err
			}
			return fd, nil
		}
	}

	if _, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{rootID: "", kind: mounts.LayoutPrivateState, duplicate: duplicate(t)}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateDirectoryWithSource(empty root) error = %v, want ErrUnsupported", err)
	}
	if _, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{rootID: rootID, kind: mounts.LayoutTrash, duplicate: duplicate(t)}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateDirectoryWithSource(trash kind) error = %v, want ErrUnsupported", err)
	}

	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, "not-a-directory", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("create regular descriptor test entry: %v", err)
	}
	var regularFD int
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, "not-a-directory", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		regularFD = fd
		return nil
	}); err != nil {
		t.Fatalf("open regular descriptor test entry: %v", err)
	}
	if _, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			fd := regularFD
			regularFD = -1
			return fd, nil
		},
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateDirectoryWithSource(regular descriptor) error = %v, want ErrUnsupported", err)
	}

	unsafeModeFD, err := duplicate(t)()
	if err != nil {
		t.Fatalf("duplicate private directory: %v", err)
	}
	if err := unix.Fchmod(unsafeModeFD, 0o750); err != nil {
		_ = unix.Close(unsafeModeFD)
		t.Fatalf("relax private directory mode: %v", err)
	}
	if _, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			fd := unsafeModeFD
			unsafeModeFD = -1
			return fd, nil
		},
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateDirectoryWithSource(relaxed mode) error = %v, want ErrUnsupported", err)
	}
}

func TestPrivateDirectoryLeaseIdentityComparisonRejectsEveryAttestedFieldChange(t *testing.T) {
	expected := testKnownSnapshot()
	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{name: "device major", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMajor.Value++ }},
		{name: "device minor", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMinor.Value++ }},
		{name: "inode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Inode.Value++ }},
		{name: "type", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Type = domain.FileTypeDirectory }},
		{name: "uid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.UID.Value++ }},
		{name: "gid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.GID.Value++ }},
		{name: "mode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Mode.Value++ }},
		{name: "mount ID", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.MountID.Value++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := expected
			test.mutate(&observed)
			if err := comparePrivateDirectoryIdentity(expected, observed); !errors.Is(err, ErrDrifted) {
				t.Fatalf("comparePrivateDirectoryIdentity() error = %v, want ErrDrifted", err)
			}
		})
	}
}

func TestPublishFileDurableRejectsPreCreateInputsAndIncompleteHooks(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	if err := PublishFileDurable(nil, lease, "record", []byte("contents")); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("PublishFileDurable(nil context) error = %v, want ErrInterrupted", err)
	}
	if err := PublishFileDurable(context.Background(), lease, "..", []byte("contents")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PublishFileDurable(parent basename) error = %v, want ErrUnsupported", err)
	}
	if err := PublishFileDurable(context.Background(), lease, "nul\x00name", []byte("contents")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PublishFileDurable(NUL basename) error = %v, want ErrUnsupported", err)
	}
	if err := PublishFileDurable(context.Background(), lease, "record", make([]byte, maximumDurableFileBytes+1)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PublishFileDurable(oversize contents) error = %v, want ErrUnsupported", err)
	}
	hooks := defaultDurableFileHooks()
	hooks.open = nil
	if err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("publishFileDurableWith(incomplete hooks) error = %v, want ErrUnsupported", err)
	}
	if testEntryExists(t, directory, "record") {
		t.Fatal("pre-create validation or an incomplete hook created a record")
	}
}

func TestPublishFileDurableClassifiesPreflightAndOpenFailuresBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name         string
		preflightErr error
		openErr      error
		want         error
	}{
		{name: "bad directory descriptor", preflightErr: unix.EBADF, want: ErrDrifted},
		{name: "interrupted preflight", preflightErr: unix.EIO, want: ErrInterrupted},
		{name: "hostile open", openErr: unix.ELOOP, want: ErrDrifted},
		{name: "unsupported open", openErr: unix.EOPNOTSUPP, want: ErrUnsupported},
		{name: "interrupted open", openErr: unix.EINTR, want: ErrInterrupted},
		{name: "definite open failure", openErr: unix.EIO, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, _, directory, rootID := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer directory.Close()
			lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
			defer lease.Close()

			openCalls := 0
			hooks := defaultDurableFileHooks()
			hooks.syncDirectory = func(int) error {
				if test.preflightErr != nil {
					return test.preflightErr
				}
				return nil
			}
			hooks.open = func(int, string, int, uint32) (int, error) {
				openCalls++
				return -1, test.openErr
			}

			err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
			if !errors.Is(err, test.want) {
				t.Fatalf("publishFileDurableWith() error = %v, want %v", err, test.want)
			}
			if test.preflightErr != nil && openCalls != 0 {
				t.Fatalf("open calls after failed preflight = %d, want 0", openCalls)
			}
			if test.preflightErr == nil && openCalls != 1 {
				t.Fatalf("open calls after successful preflight = %d, want 1", openCalls)
			}
			if testEntryExists(t, directory, "record") {
				t.Fatal("pre-create failure created a record")
			}
		})
	}
}

func TestPublishFileDurableWritesAllBytesAcrossShortWrites(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	writeCalls := 0
	hooks := defaultDurableFileHooks()
	hooks.write = func(fd int, contents []byte) (int, error) {
		writeCalls++
		if len(contents) > 1 {
			contents = contents[:1]
		}
		return unix.Write(fd, contents)
	}
	if err := publishFileDurableWith(context.Background(), lease, "record", []byte("short writes"), hooks); err != nil {
		t.Fatalf("publishFileDurableWith() short-write error = %v", err)
	}
	if writeCalls < 2 {
		t.Fatalf("short-write publication used %d write calls, want more than one", writeCalls)
	}
	if content := readTestFile(t, directory, "record"); string(content) != "short writes" {
		t.Fatalf("short-write publication content = %q, want %q", content, "short writes")
	}

	if err := writeDurableFileContents(context.Background(), -1, []byte("record"), func(int, []byte) (int, error) {
		return 99, nil
	}); err == nil {
		t.Fatal("writeDurableFileContents() accepted an impossible overlong write result")
	}
}

func TestPublishFileDurableRetainsRecordWhenWriteMakesNoProgress(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	hooks := defaultDurableFileHooks()
	hooks.write = func(int, []byte) (int, error) { return 0, nil }
	err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("publishFileDurableWith() zero-write error = %v, want ErrInterrupted", err)
	}
	if !testEntryExists(t, directory, "record") {
		t.Fatal("zero-write uncertainty removed the post-create record")
	}
}

func TestPublishFileDurableRetainsRecordsAcrossPostCreateDurabilityFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*durableFileHooks)
	}{
		{
			name: "close failure",
			configure: func(hooks *durableFileHooks) {
				hooks.close = func(fd int) error {
					if err := unix.Close(fd); err != nil {
						return err
					}
					return unix.EIO
				}
			},
		},
		{
			name: "final directory sync failure",
			configure: func(hooks *durableFileHooks) {
				syncCalls := 0
				hooks.syncDirectory = func(fd int) error {
					syncCalls++
					if syncCalls == 2 {
						return unix.EIO
					}
					return unix.Fsync(fd)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, _, directory, rootID := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer directory.Close()
			lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
			defer lease.Close()

			hooks := defaultDurableFileHooks()
			test.configure(&hooks)
			err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("publishFileDurableWith() error = %v, want ErrInterrupted", err)
			}
			if !testEntryExists(t, directory, "record") {
				t.Fatal("post-create durability failure removed the record")
			}
		})
	}
}

func TestPublishFileDurableRetainsRecordsWhenFinalVerificationFindsMetadataDrift(t *testing.T) {
	for _, test := range []struct {
		name         string
		mutate       func(int) error
		expectLinked bool
	}{
		{
			name: "mode change",
			mutate: func(directoryFD int) error {
				fd, err := unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				return unix.Fchmod(fd, 0o644)
			},
		},
		{
			name: "additional hard link",
			mutate: func(directoryFD int) error {
				return unix.Linkat(directoryFD, "record", directoryFD, "record-link", 0)
			},
			expectLinked: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, _, directory, rootID := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer directory.Close()
			lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
			defer lease.Close()

			syncCalls := 0
			hooks := defaultDurableFileHooks()
			hooks.syncDirectory = func(directoryFD int) error {
				syncCalls++
				if err := unix.Fsync(directoryFD); err != nil {
					return err
				}
				if syncCalls == 2 {
					return test.mutate(directoryFD)
				}
				return nil
			}
			err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
			if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
				t.Fatalf("publishFileDurableWith() final verification error = %v, want ErrInterrupted + ErrDrifted", err)
			}
			if !testEntryExists(t, directory, "record") {
				t.Fatal("final verification drift removed the record")
			}
			if test.expectLinked && !testEntryExists(t, directory, "record-link") {
				t.Fatal("hard-link drift test did not retain the additional link for reconciliation")
			}
		})
	}
}

func TestPublishFileDurableDoesNotClaimSuccessAfterSameUIDContentTampering(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	syncCalls := 0
	hooks := defaultDurableFileHooks()
	hooks.syncDirectory = func(directoryFD int) error {
		syncCalls++
		if err := unix.Fsync(directoryFD); err != nil {
			return err
		}
		if syncCalls != 2 {
			return nil
		}
		fd, err := unix.Openat(directoryFD, "record", unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		_, err = unix.Write(fd, []byte("replaced")) // Same byte length as "contents".
		return err
	}

	err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("publishFileDurableWith() content-tampering error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if content := readTestFile(t, directory, "record"); string(content) != "replaced" {
		t.Fatalf("tampered record content = %q, want replacement retained for reconciliation", content)
	}
}

func TestPublishFileDurableRejectsNewRecordWithUnexpectedLinkCount(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	hooks := defaultDurableFileHooks()
	hooks.open = func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
		fd, err := openDurableFile(directoryFD, basename, flags, mode)
		if err != nil {
			return -1, err
		}
		if err := unix.Linkat(directoryFD, basename, directoryFD, "record-link", 0); err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		return fd, nil
	}
	err := publishFileDurableWith(context.Background(), lease, "record", []byte("contents"), hooks)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("publishFileDurableWith() linked record error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if !testEntryExists(t, directory, "record") || !testEntryExists(t, directory, "record-link") {
		t.Fatal("unexpected-link record was not retained for reconciliation")
	}
}

func TestVerifyPublishedNameRejectsSameUIDTypeReplacement(t *testing.T) {
	rootFD, _, directory, _ := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	createTestFileWithContents(t, directory, "record", []byte("contents"))
	var expected domain.FilesystemSnapshot
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		expected, err = snapshotPublishedRegularFile(fd)
		return err
	}); err != nil {
		t.Fatalf("capture published record identity: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		if err := unix.Unlinkat(directoryFD, "record", 0); err != nil {
			return err
		}
		return unix.Mkdirat(directoryFD, "record", 0o700)
	}); err != nil {
		t.Fatalf("replace published record with directory: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		return verifyPublishedName(context.Background(), directoryFD, "record", expected, []byte("contents"))
	}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("verifyPublishedName() type replacement error = %v, want ErrDrifted", err)
	}
}

func TestVerifyPublishedContentsConfirmsExactBytesAndRejectsDrift(t *testing.T) {
	for _, test := range []struct {
		name      string
		contents  []byte
		expected  []byte
		cancelled bool
		want      error
	}{
		{name: "exact nonempty contents", contents: []byte("contents"), expected: []byte("contents")},
		{name: "exact empty contents", contents: []byte{}, expected: []byte{}},
		{name: "same length mismatch", contents: []byte("contents"), expected: []byte("contEnts"), want: ErrDrifted},
		{name: "truncated contents", contents: []byte("short"), expected: []byte("shorter"), want: ErrDrifted},
		{name: "trailing contents", contents: []byte("longer"), expected: []byte("long"), want: ErrDrifted},
		{name: "cancelled verification", contents: []byte("contents"), expected: []byte("contents"), cancelled: true, want: ErrInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, _, directory, _ := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer directory.Close()
			createTestFileWithContents(t, directory, "record", test.contents)

			ctx := context.Background()
			if test.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := directory.withFD(func(directoryFD int) error {
				fd, err := unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				return verifyPublishedContents(ctx, fd, test.expected)
			})
			if test.want == nil {
				if err != nil {
					t.Fatalf("verifyPublishedContents() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("verifyPublishedContents() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOpenPublishedRecordBeneathDoesNotBlockOnSameUIDFIFOReplacement(t *testing.T) {
	rootFD, _, directory, _ := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	if err := directory.withFD(func(directoryFD int) error {
		return unix.Mkfifoat(directoryFD, "record", 0o600)
	}); err != nil {
		t.Fatalf("create FIFO replacement: %v", err)
	}
	readerDirectoryFD, err := directory.duplicate()
	if err != nil {
		t.Fatalf("duplicate reader directory: %v", err)
	}
	writerDirectoryFD, err := directory.duplicate()
	if err != nil {
		_ = unix.Close(readerDirectoryFD)
		t.Fatalf("duplicate writer directory: %v", err)
	}
	defer unix.Close(writerDirectoryFD)

	type openResult struct {
		fd  int
		err error
	}
	result := make(chan openResult, 1)
	go func() {
		fd, err := openPublishedRecordBeneath(context.Background(), readerDirectoryFD, "record")
		_ = unix.Close(readerDirectoryFD)
		result <- openResult{fd: fd, err: err}
	}()

	select {
	case opened := <-result:
		if opened.fd >= 0 {
			defer unix.Close(opened.fd)
		}
		if opened.err != nil {
			t.Fatalf("openPublishedRecordBeneath() FIFO error = %v", opened.err)
		}
	case <-time.After(100 * time.Millisecond):
		writerFD, err := unix.Openat(writerDirectoryFD, "record", unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatalf("open FIFO writer to release blocked reader: %v", err)
		}
		if err := unix.Close(writerFD); err != nil {
			t.Fatalf("close FIFO writer: %v", err)
		}
		opened := <-result
		if opened.fd >= 0 {
			_ = unix.Close(opened.fd)
		}
		t.Fatalf("openPublishedRecordBeneath() blocked on FIFO replacement; released result = (%d, %v)", opened.fd, opened.err)
	}
}

func TestVerifyPublishedNameRejectsEveryAttestedIdentityDifference(t *testing.T) {
	rootFD, _, directory, _ := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	createTestFileWithContents(t, directory, "record", []byte("contents"))

	var expected domain.FilesystemSnapshot
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		expected, err = snapshotPublishedRegularFile(fd)
		return err
	}); err != nil {
		t.Fatalf("capture published record identity: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{name: "device major", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMajor.Value++ }},
		{name: "device minor", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMinor.Value++ }},
		{name: "inode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Inode.Value++ }},
		{name: "uid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.UID.Value++ }},
		{name: "gid", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.GID.Value++ }},
		{name: "mode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Mode.Value++ }},
		{name: "link count", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.LinkCount.Value++ }},
		{name: "mount ID", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.MountID.Value++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := expected
			test.mutate(&altered)
			err := directory.withFD(func(directoryFD int) error {
				return verifyPublishedName(context.Background(), directoryFD, "record", altered, []byte("contents"))
			})
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("verifyPublishedName() error = %v, want ErrDrifted", err)
			}
		})
	}
}

func TestSnapshotPublishedRegularFileRejectsUnsafeNewRecordMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, directory *ParentLease) error
		open    func(directoryFD int) (int, error)
	}{
		{
			name: "directory descriptor",
			prepare: func(*testing.T, *ParentLease) error {
				return nil
			},
			open: func(directoryFD int) (int, error) {
				return unix.FcntlInt(uintptr(directoryFD), unix.F_DUPFD_CLOEXEC, 3)
			},
		},
		{
			name: "relaxed mode",
			prepare: func(t *testing.T, directory *ParentLease) error {
				createTestFileWithContents(t, directory, "record", []byte("contents"))
				return directory.withFD(func(directoryFD int) error {
					fd, err := unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
					if err != nil {
						return err
					}
					defer unix.Close(fd)
					return unix.Fchmod(fd, 0o644)
				})
			},
			open: func(directoryFD int) (int, error) {
				return unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			},
		},
		{
			name: "additional hard link",
			prepare: func(t *testing.T, directory *ParentLease) error {
				createTestFileWithContents(t, directory, "record", []byte("contents"))
				return directory.withFD(func(directoryFD int) error {
					return unix.Linkat(directoryFD, "record", directoryFD, "record-link", 0)
				})
			},
			open: func(directoryFD int) (int, error) {
				return unix.Openat(directoryFD, "record", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rootFD, _, directory, _ := stageTestDirectories(t)
			defer unix.Close(rootFD)
			defer directory.Close()
			if err := test.prepare(t, directory); err != nil {
				t.Fatalf("prepare unsafe published record: %v", err)
			}

			err := directory.withFD(func(directoryFD int) error {
				fd, err := test.open(directoryFD)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				_, err = snapshotPublishedRegularFile(fd)
				return err
			})
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("snapshotPublishedRegularFile() error = %v, want ErrDrifted", err)
			}
		})
	}
}

func TestPrivateDirectoryLeaseRequalifiesSourceForEveryScopedOperation(t *testing.T) {
	rootFD, privateDirectory, otherDirectory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer privateDirectory.Close()
	defer otherDirectory.Close()

	duplicateCalls := 0
	source := &testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			duplicateCalls++
			return privateDirectory.duplicate()
		},
	}
	lease, err := openPrivateDirectoryWithSource(source)
	if err != nil {
		t.Fatalf("openPrivateDirectoryWithSource() error = %v", err)
	}
	defer lease.Close()
	if duplicateCalls != 1 {
		t.Fatalf("source duplicates during qualification = %d, want 1", duplicateCalls)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		called := false
		if err := lease.withFD(func(fd int) error {
			called = true
			return unix.Fsync(fd)
		}); err != nil {
			t.Fatalf("PrivateDirectoryLease.withFD() attempt %d error = %v", attempt, err)
		}
		if !called {
			t.Fatalf("PrivateDirectoryLease.withFD() attempt %d did not invoke scoped operation", attempt)
		}
		if duplicateCalls != attempt+1 {
			t.Fatalf("source duplicates after attempt %d = %d, want %d", attempt, duplicateCalls, attempt+1)
		}
	}

	source.duplicate = func() (int, error) {
		duplicateCalls++
		return otherDirectory.duplicate()
	}
	called := false
	if err := lease.withFD(func(int) error {
		called = true
		return nil
	}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease.withFD() changed descriptor error = %v, want ErrDrifted", err)
	}
	if called {
		t.Fatal("PrivateDirectoryLease.withFD() invoked an operation with a changed private directory descriptor")
	}
	if duplicateCalls != 4 {
		t.Fatalf("source duplicates after changed descriptor = %d, want 4", duplicateCalls)
	}

	source.duplicate = func() (int, error) {
		duplicateCalls++
		return privateDirectory.duplicate()
	}
	otherRoot, err := domain.NewTrustedRootID("linuxfs-requalified-other-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source.rootID = otherRoot
	if err := lease.withFD(func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease.withFD() changed source root error = %v, want ErrDrifted", err)
	}
	if duplicateCalls != 4 {
		t.Fatalf("source duplicated after changed root identity: %d calls, want 4", duplicateCalls)
	}

	source.rootID = rootID
	source.kind = mounts.LayoutTrash
	if err := lease.withFD(func(int) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease.withFD() changed source kind error = %v, want ErrDrifted", err)
	}
	if duplicateCalls != 4 {
		t.Fatalf("source duplicated after changed layout kind: %d calls, want 4", duplicateCalls)
	}

	source.kind = mounts.LayoutPrivateState
	source.duplicate = func() (int, error) {
		duplicateCalls++
		return -1, mounts.ErrLeaseClosed
	}
	called = false
	if err := lease.withFD(func(int) error {
		called = true
		return nil
	}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease.withFD() rejected source error = %v, want ErrDrifted", err)
	}
	if called {
		t.Fatal("PrivateDirectoryLease.withFD() invoked a scoped operation after source requalification failed")
	}
	if duplicateCalls != 5 {
		t.Fatalf("source duplicates after rejected source = %d, want 5", duplicateCalls)
	}
}

func TestPrivateDirectoryLeasePreservesUnsupportedSourceQualification(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	unsupported := &testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			return -1, mounts.ErrUnsupported
		},
	}
	if _, err := openPrivateDirectoryWithSource(unsupported); !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrDrifted) {
		t.Fatalf("openPrivateDirectoryWithSource() unsupported source error = %v, want ErrUnsupported without ErrDrifted", err)
	}

	source := &testPrivateDirectorySource{
		rootID: rootID,
		kind:   mounts.LayoutPrivateState,
		duplicate: func() (int, error) {
			return directory.duplicate()
		},
	}
	lease, err := openPrivateDirectoryWithSource(source)
	if err != nil {
		t.Fatalf("openPrivateDirectoryWithSource() qualified source error = %v", err)
	}
	defer lease.Close()
	source.duplicate = func() (int, error) {
		return -1, mounts.ErrUnsupported
	}
	called := false
	if err := lease.withFD(func(int) error {
		called = true
		return nil
	}); !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrDrifted) {
		t.Fatalf("PrivateDirectoryLease.withFD() unsupported source error = %v, want ErrUnsupported without ErrDrifted", err)
	}
	if called {
		t.Fatal("PrivateDirectoryLease.withFD() invoked a scoped operation after unsupported source qualification")
	}
}

func TestPostCreateIdentityErrorPreservesFinalVerificationClassification(t *testing.T) {
	for _, test := range []struct {
		name        string
		cause       error
		want        error
		mustNotHave error
	}{
		{name: "unsupported", cause: ErrUnsupported, want: ErrUnsupported, mustNotHave: ErrDrifted},
		{name: "interrupted", cause: ErrInterrupted, want: ErrInterrupted, mustNotHave: ErrDrifted},
		{name: "drifted", cause: ErrDrifted, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := postCreateIdentityError("final verification", test.cause)
			if !errors.Is(err, ErrInterrupted) || !errors.Is(err, test.want) {
				t.Fatalf("postCreateIdentityError() error = %v, want ErrInterrupted + %v", err, test.want)
			}
			if test.mustNotHave != nil && errors.Is(err, test.mustNotHave) {
				t.Fatalf("postCreateIdentityError() error = %v, must not include %v", err, test.mustNotHave)
			}
		})
	}
}

func testPrivateDirectoryLease(t *testing.T, directory *ParentLease, rootID domain.TrustedRootID, kind mounts.LayoutKind) *PrivateDirectoryLease {
	t.Helper()
	lease, err := openPrivateDirectoryWithSource(&testPrivateDirectorySource{
		rootID: rootID,
		kind:   kind,
		duplicate: func() (int, error) {
			return directory.duplicate()
		},
	})
	if err != nil {
		t.Fatalf("qualify test-owned private directory: %v", err)
	}
	return lease
}

type testPrivateDirectorySource struct {
	rootID    domain.TrustedRootID
	kind      mounts.LayoutKind
	duplicate func() (int, error)
}

func (source *testPrivateDirectorySource) RootID() domain.TrustedRootID {
	if source == nil {
		return ""
	}
	return source.rootID
}

func (source *testPrivateDirectorySource) Kind() mounts.LayoutKind {
	if source == nil {
		return ""
	}
	return source.kind
}

func (source *testPrivateDirectorySource) Duplicate() (int, error) {
	if source == nil || source.duplicate == nil {
		return -1, mounts.ErrLeaseClosed
	}
	return source.duplicate()
}

func readTestFile(t *testing.T, directory *ParentLease, basename string) []byte {
	t.Helper()
	var contents []byte
	err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, basename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		buffer := make([]byte, 256)
		for {
			count, err := unix.Read(fd, buffer)
			if count > 0 {
				contents = append(contents, buffer[:count]...)
			}
			if err != nil {
				return err
			}
			if count == 0 {
				return nil
			}
		}
	})
	if err != nil {
		t.Fatalf("read test file %q: %v", basename, err)
	}
	return contents
}

func createTestFileWithContents(t *testing.T, directory *ParentLease, basename string, contents []byte) {
	t.Helper()
	err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, basename, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		for len(contents) > 0 {
			count, err := unix.Write(fd, contents)
			if err != nil {
				return err
			}
			if count <= 0 {
				return unix.EIO
			}
			contents = contents[count:]
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create test file %q: %v", basename, err)
	}
}

func testEntryMode(t *testing.T, directory *ParentLease, basename string) uint32 {
	t.Helper()
	var stat unix.Stat_t
	if err := directory.withFD(func(directoryFD int) error {
		return unix.Fstatat(directoryFD, basename, &stat, unix.AT_SYMLINK_NOFOLLOW)
	}); err != nil {
		t.Fatalf("stat test entry %q: %v", basename, err)
	}
	return uint32(stat.Mode) & 0o7777
}

type testFileIdentity struct {
	device uint64
	inode  uint64
}

func testEntryIdentity(t *testing.T, directory *ParentLease, basename string) testFileIdentity {
	t.Helper()
	var stat unix.Stat_t
	if err := directory.withFD(func(directoryFD int) error {
		return unix.Fstatat(directoryFD, basename, &stat, unix.AT_SYMLINK_NOFOLLOW)
	}); err != nil {
		t.Fatalf("stat test entry %q: %v", basename, err)
	}
	return testFileIdentity{device: uint64(stat.Dev), inode: stat.Ino}
}
