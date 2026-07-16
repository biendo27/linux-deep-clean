//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestPublishTrashInfoDurablePublishesInfoOnlyInDurabilityOrder(t *testing.T) {
	directories, source, files, info := openTrashBridgeTestDirectories(t)

	var events []string
	publication, err := publishTrashInfoDurableWith(context.Background(), directories, "ldc-0123456789abcdef", []byte("[Trash Info]\n"), trashInfoPublishHooks{
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
		t.Fatalf("publishTrashInfoDurableWith() error = %v", err)
	}
	if publication == nil {
		t.Fatal("publishTrashInfoDurableWith() returned nil publication")
	}
	if publication.rootID != source.rootID {
		t.Fatalf("publication root = %q, want %q", publication.rootID, source.rootID)
	}
	if publication.token != "ldc-0123456789abcdef" {
		t.Fatalf("publication token = %q, want opaque token", publication.token)
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

	infoBasename := "ldc-0123456789abcdef.trashinfo"
	if content := readTestFile(t, info, infoBasename); string(content) != "[Trash Info]\n" {
		t.Fatalf("published Trash info content = %q, want exact content", content)
	}
	if mode := testEntryMode(t, info, infoBasename); mode != 0o600 {
		t.Fatalf("published Trash info mode = %#o, want 0600", mode)
	}
	if testEntryExists(t, files, infoBasename) {
		t.Fatal("Trash info publication created a record in files")
	}
	for index, pair := range source.pairs {
		for role, fd := range map[string]int{"files": pair.FilesFD, "info": pair.InfoFD} {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatalf("pair %d %s descriptor after publication error = %v, want EBADF", index, role, err)
			}
		}
	}
}

func TestPublishTrashInfoDurableNeverOverwritesExistingRecord(t *testing.T) {
	directories, _, _, info := openTrashBridgeTestDirectories(t)
	const token = "ldc-0123456789abcdef"
	const basename = token + ".trashinfo"
	createTestFileWithContents(t, info, basename, []byte("original"))
	before := testEntryIdentity(t, info, basename)

	publication, err := PublishTrashInfoDurable(context.Background(), directories, token, []byte("replacement"))
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("PublishTrashInfoDurable() collision error = %v, want ErrDrifted", err)
	}
	if publication != nil {
		t.Fatal("PublishTrashInfoDurable() returned a publication after collision")
	}
	if after := testEntryIdentity(t, info, basename); after != before {
		t.Fatalf("collision changed existing Trash info identity = %#v, want %#v", after, before)
	}
	if content := readTestFile(t, info, basename); string(content) != "original" {
		t.Fatalf("existing Trash info content = %q, want original", content)
	}
}

func TestPublishTrashInfoDurableRejectsUnownedTokenBeforeCreatingMetadata(t *testing.T) {
	directories, _, _, info := openTrashBridgeTestDirectories(t)

	for _, token := range []string{
		"foreign-token",
		"ldc-short",
		"ldc-0123456789ABCDEf",
		"ldc-0123456789abcdef/other",
	} {
		t.Run(token, func(t *testing.T) {
			publication, err := PublishTrashInfoDurable(context.Background(), directories, token, []byte("contents"))
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("PublishTrashInfoDurable(%q) error = %v, want ErrUnsupported", token, err)
			}
			if publication != nil {
				t.Fatalf("PublishTrashInfoDurable(%q) returned a publication", token)
			}
			if testEntryExists(t, info, token+".trashinfo") {
				t.Fatalf("PublishTrashInfoDurable(%q) created an unowned metadata record", token)
			}
		})
	}
}

func TestPublishTrashInfoDurableRejectsPreflightFailureWithoutCreatingRecord(t *testing.T) {
	directories, _, _, info := openTrashBridgeTestDirectories(t)
	const token = "ldc-0123456789abcdef"

	publication, err := publishTrashInfoDurableWith(context.Background(), directories, token, []byte("contents"), trashInfoPublishHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: func(int) error { return unix.EOPNOTSUPP },
		close:         unix.Close,
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("publishTrashInfoDurableWith() preflight error = %v, want ErrUnsupported", err)
	}
	if publication != nil {
		t.Fatal("publishTrashInfoDurableWith() returned a publication after preflight failure")
	}
	if testEntryExists(t, info, token+".trashinfo") {
		t.Fatal("publishTrashInfoDurableWith() created a record after failed preflight")
	}
}

func TestPublishTrashInfoDurableRetainsPostCreateInterruption(t *testing.T) {
	directories, _, _, info := openTrashBridgeTestDirectories(t)
	const token = "ldc-0123456789abcdef"

	publication, err := publishTrashInfoDurableWith(context.Background(), directories, token, []byte("partial"), trashInfoPublishHooks{
		open:          openDurableFile,
		write:         unix.Write,
		syncFile:      func(int) error { return unix.EIO },
		syncDirectory: unix.Fsync,
		close:         unix.Close,
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("publishTrashInfoDurableWith() post-create error = %v, want ErrInterrupted", err)
	}
	if publication != nil {
		t.Fatal("publishTrashInfoDurableWith() returned a completed publication after interruption")
	}
	if !testEntryExists(t, info, token+".trashinfo") {
		t.Fatal("publishTrashInfoDurableWith() removed a post-create record")
	}
	if content := readTestFile(t, info, token+".trashinfo"); string(content) != "partial" {
		t.Fatalf("retained interrupted content = %q, want partial", content)
	}
}

func TestPublishTrashInfoDurableClosesAnOwnedReservedDescriptorOnPostCreateFailure(t *testing.T) {
	directories, _, _, _ := openTrashBridgeTestDirectories(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var closed []int
	publication, err := publishTrashInfoDurableWith(ctx, directories, "ldc-0123456789abcdef", []byte("contents"), trashInfoPublishHooks{
		open: func(int, string, int, uint32) (int, error) {
			// A successful opener transfers ownership even when the process had
			// closed a standard descriptor and openat reused it.
			cancel()
			return 0, nil
		},
		write:         unix.Write,
		syncFile:      unix.Fsync,
		syncDirectory: func(int) error { return nil },
		close: func(fd int) error {
			closed = append(closed, fd)
			return nil
		},
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("publishTrashInfoDurableWith() error = %v, want ErrInterrupted", err)
	}
	if publication != nil {
		t.Fatal("publishTrashInfoDurableWith() returned a publication after post-create cancellation")
	}
	if len(closed) != 1 || closed[0] != 0 {
		t.Fatalf("owned reserved descriptor closes = %v, want [0]", closed)
	}
}

func TestPublishTrashInfoDurableVerifiesIdentityAndContentsAfterDirectorySync(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, infoFD int, basename string) error
	}{
		{
			name: "content tampering",
			mutate: func(t *testing.T, infoFD int, basename string) error {
				fd, err := unix.Openat(infoFD, basename, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				_, err = unix.Write(fd, []byte("changed!"))
				return err
			},
		},
		{
			name: "name replacement",
			mutate: func(t *testing.T, infoFD int, basename string) error {
				if err := unix.Unlinkat(infoFD, basename, 0); err != nil {
					return err
				}
				fd, err := unix.Openat(infoFD, basename, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				_, err = unix.Write(fd, []byte("changed!"))
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directories, _, _, info := openTrashBridgeTestDirectories(t)
			const token = "ldc-0123456789abcdef"
			syncCalls := 0
			hooks := defaultTrashInfoPublishHooks()
			hooks.syncDirectory = func(infoFD int) error {
				syncCalls++
				if err := unix.Fsync(infoFD); err != nil {
					return err
				}
				if syncCalls == 2 {
					return test.mutate(t, infoFD, token+".trashinfo")
				}
				return nil
			}

			publication, err := publishTrashInfoDurableWith(context.Background(), directories, token, []byte("contents"), hooks)
			if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
				t.Fatalf("publishTrashInfoDurableWith() verification error = %v, want ErrInterrupted + ErrDrifted", err)
			}
			if publication != nil {
				t.Fatal("publishTrashInfoDurableWith() returned a verified publication after drift")
			}
			if !testEntryExists(t, info, token+".trashinfo") {
				t.Fatal("publishTrashInfoDurableWith() removed the uncertain record")
			}
		})
	}
}

func TestTrashDirectoriesFailClosedForNilClosedAndDriftedSources(t *testing.T) {
	if directories, err := OpenTrashDirectories(nil); !errors.Is(err, ErrUnsupported) || directories != nil {
		t.Fatalf("OpenTrashDirectories(nil) = (%v, %v), want nil + ErrUnsupported", directories, err)
	}

	directories, source, _, info := openTrashBridgeTestDirectories(t)
	if err := directories.Close(); err != nil {
		t.Fatalf("TrashDirectories.Close() error = %v", err)
	}
	if publication, err := PublishTrashInfoDurable(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents")); !errors.Is(err, ErrDrifted) || publication != nil {
		t.Fatalf("PublishTrashInfoDurable(closed directories) = (%v, %v), want nil + ErrDrifted", publication, err)
	}
	if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
		t.Fatal("closed TrashDirectories created a record")
	}

	directories, source, _, info = openTrashBridgeTestDirectories(t)
	source.duplicate = func() (mounts.TrashDescriptorPair, error) {
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, mounts.ErrLeaseClosed
	}
	if publication, err := PublishTrashInfoDurable(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents")); !errors.Is(err, ErrDrifted) || publication != nil {
		t.Fatalf("PublishTrashInfoDurable(closed source) = (%v, %v), want nil + ErrDrifted", publication, err)
	}
	if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
		t.Fatal("closed Trash source created a record")
	}

	directories, source, files, info := openTrashBridgeTestDirectories(t)
	rootFD := openTestDirectory(t)
	t.Cleanup(func() { _ = unix.Close(rootFD) })
	makeTestDirectory(t, rootFD, "other-files")
	makeTestDirectory(t, rootFD, "other-info")
	otherFiles := stageTestParent(t, rootFD, source.rootID, "other-files", "item")
	otherInfo := stageTestParent(t, rootFD, source.rootID, "other-info", "item")
	t.Cleanup(func() {
		_ = otherFiles.Close()
		_ = otherInfo.Close()
	})
	source.duplicate = func() (mounts.TrashDescriptorPair, error) {
		return duplicateTestTrashPair(otherFiles, otherInfo)
	}
	if publication, err := PublishTrashInfoDurable(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents")); !errors.Is(err, ErrDrifted) || publication != nil {
		t.Fatalf("PublishTrashInfoDurable(drifted pair) = (%v, %v), want nil + ErrDrifted", publication, err)
	}
	if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") || testEntryExists(t, files, "ldc-0123456789abcdef.trashinfo") {
		t.Fatal("drifted Trash source created a record in baseline directories")
	}
}

func TestOpenTrashDirectoriesRejectsInvalidDescriptorPairs(t *testing.T) {
	rootID, err := domain.NewTrustedRootID("linuxfs-trash-invalid-pair")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}

	for _, test := range []struct {
		name      string
		duplicate func() (mounts.TrashDescriptorPair, error)
	}{
		{
			name: "reserved descriptors",
			duplicate: func() (mounts.TrashDescriptorPair, error) {
				return mounts.TrashDescriptorPair{FilesFD: 0, InfoFD: 1}, nil
			},
		},
		{
			name: "same descriptor",
			duplicate: func() (mounts.TrashDescriptorPair, error) {
				fd := openTestDirectory(t)
				t.Cleanup(func() { _ = unix.Close(fd) })
				return mounts.TrashDescriptorPair{FilesFD: fd, InfoFD: fd}, nil
			},
		},
		{
			name: "same directory through distinct descriptors",
			duplicate: func() (mounts.TrashDescriptorPair, error) {
				filesFD := openTestDirectory(t)
				infoFD, err := unix.FcntlInt(uintptr(filesFD), unix.F_DUPFD_CLOEXEC, 3)
				if err != nil {
					_ = unix.Close(filesFD)
					return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, err
				}
				t.Cleanup(func() {
					_ = unix.Close(filesFD)
					_ = unix.Close(infoFD)
				})
				return mounts.TrashDescriptorPair{FilesFD: filesFD, InfoFD: infoFD}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directories, err := openTrashDirectoriesWithSource(&testTrashDirectorySource{rootID: rootID, duplicate: test.duplicate})
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("openTrashDirectoriesWithSource() error = %v, want ErrDrifted", err)
			}
			if directories != nil {
				t.Fatal("openTrashDirectoriesWithSource() returned directories for an invalid pair")
			}
		})
	}
}

type testTrashDirectorySource struct {
	rootID    domain.TrustedRootID
	duplicate func() (mounts.TrashDescriptorPair, error)
	pairs     []mounts.TrashDescriptorPair
}

func (source *testTrashDirectorySource) RootID() domain.TrustedRootID {
	if source == nil {
		return ""
	}
	return source.rootID
}

func (source *testTrashDirectorySource) Duplicate() (mounts.TrashDescriptorPair, error) {
	if source == nil || source.duplicate == nil {
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, mounts.ErrLeaseClosed
	}
	pair, err := source.duplicate()
	if err == nil {
		source.pairs = append(source.pairs, pair)
	}
	return pair, err
}

func openTrashBridgeTestDirectories(t *testing.T) (*TrashDirectories, *testTrashDirectorySource, *ParentLease, *ParentLease) {
	t.Helper()

	rootFD := openTestDirectory(t)
	makeTestDirectory(t, rootFD, "files")
	makeTestDirectory(t, rootFD, "info")
	rootID, err := domain.NewTrustedRootID("linuxfs-trash-bridge-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	files := stageTestParent(t, rootFD, rootID, "files", "item")
	info := stageTestParent(t, rootFD, rootID, "info", "item")
	source := &testTrashDirectorySource{rootID: rootID}
	source.duplicate = func() (mounts.TrashDescriptorPair, error) {
		return duplicateTestTrashPair(files, info)
	}
	directories, err := openTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTrashDirectoriesWithSource() error = %v", err)
	}
	t.Cleanup(func() {
		_ = directories.Close()
		_ = files.Close()
		_ = info.Close()
		_ = unix.Close(rootFD)
	})
	return directories, source, files, info
}

func duplicateTestTrashPair(files, info *ParentLease) (mounts.TrashDescriptorPair, error) {
	pair := mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}
	filesFD, err := files.duplicate()
	if err != nil {
		return pair, err
	}
	pair.FilesFD = filesFD
	infoFD, err := info.duplicate()
	if err != nil {
		_ = pair.Close()
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, err
	}
	pair.InfoFD = infoFD
	return pair, nil
}
