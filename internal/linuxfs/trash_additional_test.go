//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestTrashInfoPublishValidationFailsBeforeRequalification(t *testing.T) {
	if err := defaultTrashInfoPublishHooks().validate(); err != nil {
		t.Fatalf("defaultTrashInfoPublishHooks().validate() error = %v", err)
	}

	for _, test := range []struct {
		name  string
		hooks trashInfoPublishHooks
	}{
		{name: "missing opener", hooks: trashInfoPublishHooks{write: unix.Write, syncFile: unix.Fsync, syncDirectory: unix.Fsync, close: unix.Close}},
		{name: "missing writer", hooks: trashInfoPublishHooks{open: openDurableFile, syncFile: unix.Fsync, syncDirectory: unix.Fsync, close: unix.Close}},
		{name: "missing file sync", hooks: trashInfoPublishHooks{open: openDurableFile, write: unix.Write, syncDirectory: unix.Fsync, close: unix.Close}},
		{name: "missing directory sync", hooks: trashInfoPublishHooks{open: openDurableFile, write: unix.Write, syncFile: unix.Fsync, close: unix.Close}},
		{name: "missing closer", hooks: trashInfoPublishHooks{open: openDurableFile, write: unix.Write, syncFile: unix.Fsync, syncDirectory: unix.Fsync}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.hooks.validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("hooks.validate() error = %v, want ErrUnsupported", err)
			}
		})
	}

	directories, source, _, info := openTrashBridgeTestDirectories(t)
	baselinePairs := len(source.pairs)
	if publication, err := publishTrashInfoDurableWith(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents"), trashInfoPublishHooks{}); !errors.Is(err, ErrUnsupported) || publication != nil {
		t.Fatalf("publishTrashInfoDurableWith(incomplete hooks) = (%v, %v), want nil + ErrUnsupported", publication, err)
	}
	if len(source.pairs) != baselinePairs {
		t.Fatalf("incomplete hooks duplicated trusted directories: pairs = %d, want %d", len(source.pairs), baselinePairs)
	}
	if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
		t.Fatal("incomplete hooks created a Trash info record")
	}

	valid := "ldc-" + strings.Repeat("a", maxTrashTokenBytes-len(trashTokenRequiredPrefix))
	if basename, err := trashInfoBasename(valid); err != nil || basename != valid+trashInfoFilenameSuffix {
		t.Fatalf("trashInfoBasename(valid token) = (%q, %v), want %q + nil", basename, err, valid+trashInfoFilenameSuffix)
	}
	for _, token := range []string{
		"",
		strings.Repeat("a", maxTrashTokenBytes+1),
		"ldc-0123456789abcde",
		"ldc-0123456789abcdefA",
		"ldc-0123456789abcde/",
	} {
		if basename, err := trashInfoBasename(token); !errors.Is(err, ErrUnsupported) || basename != "" {
			t.Fatalf("trashInfoBasename(%q) = (%q, %v), want empty + ErrUnsupported", token, basename, err)
		}
	}
}

func TestTrashDirectorySourceFailureClassificationAndCleanup(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "mounts unsupported", err: mounts.ErrUnsupported, want: ErrUnsupported},
		{name: "invalid authority", err: mounts.ErrInvalidAuthority, want: ErrUnsupported},
		{name: "unknown trash", err: mounts.ErrUnknownTrash, want: ErrUnsupported},
		{name: "unknown trusted root", err: mounts.ErrUnknownTrustedRoot, want: ErrUnsupported},
		{name: "lease closed", err: mounts.ErrLeaseClosed, want: ErrDrifted},
		{name: "mount drift", err: mounts.ErrDrifted, want: ErrDrifted},
		{name: "unexpected error", err: unix.EIO, want: ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyTrashDirectorySourceFailure("test duplicate", test.err); !errors.Is(got, test.want) {
				t.Fatalf("classifyTrashDirectorySourceFailure(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}

	t.Run("partial pair from failed source is closed", func(t *testing.T) {
		_, source, files, info := openTrashBridgeTestDirectories(t)
		var duplicated []int
		source.duplicate = func() (mounts.TrashDescriptorPair, error) {
			pair, err := duplicateTestTrashPair(files, info)
			if err != nil {
				return pair, err
			}
			duplicated = []int{pair.FilesFD, pair.InfoFD}
			return pair, mounts.ErrInvalidAuthority
		}

		directories, err := openTrashDirectoriesWithSource(source)
		if !errors.Is(err, ErrUnsupported) || directories != nil {
			t.Fatalf("openTrashDirectoriesWithSource(failed partial pair) = (%v, %v), want nil + ErrUnsupported", directories, err)
		}
		for _, fd := range duplicated {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatalf("failed source descriptor %d remains open: %v", fd, err)
			}
		}
	})

	t.Run("root changes after initial duplicate", func(t *testing.T) {
		_, source, files, info := openTrashBridgeTestDirectories(t)
		otherRoot, err := domain.NewTrustedRootID("linuxfs-trash-other-root")
		if err != nil {
			t.Fatalf("NewTrustedRootID() error = %v", err)
		}
		var duplicated []int
		source.duplicate = func() (mounts.TrashDescriptorPair, error) {
			pair, err := duplicateTestTrashPair(files, info)
			if err != nil {
				return pair, err
			}
			duplicated = []int{pair.FilesFD, pair.InfoFD}
			source.rootID = otherRoot
			return pair, nil
		}

		directories, err := openTrashDirectoriesWithSource(source)
		if !errors.Is(err, ErrDrifted) || directories != nil {
			t.Fatalf("openTrashDirectoriesWithSource(root change) = (%v, %v), want nil + ErrDrifted", directories, err)
		}
		for _, fd := range duplicated {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatalf("root-change descriptor %d remains open: %v", fd, err)
			}
		}
	})

	t.Run("invalid root avoids source duplicate", func(t *testing.T) {
		calls := 0
		directories, err := openTrashDirectoriesWithSource(&testTrashDirectorySource{
			duplicate: func() (mounts.TrashDescriptorPair, error) {
				calls++
				return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, nil
			},
		})
		if !errors.Is(err, ErrUnsupported) || directories != nil {
			t.Fatalf("openTrashDirectoriesWithSource(invalid root) = (%v, %v), want nil + ErrUnsupported", directories, err)
		}
		if calls != 0 {
			t.Fatalf("invalid root called source Duplicate() %d times, want zero", calls)
		}
	})
}

func TestTrashDescriptorPairValidationAndIdentityComparison(t *testing.T) {
	if _, err := snapshotTrashDirectory(2, "test"); !errors.Is(err, ErrDrifted) {
		t.Fatalf("snapshotTrashDirectory(reserved) error = %v, want ErrDrifted", err)
	}

	rootFD := openTestDirectory(t)
	t.Cleanup(func() { _ = unix.Close(rootFD) })
	makeTestDirectory(t, rootFD, "files")
	filesFD, err := unix.Openat(rootFD, "files", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open files directory: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(filesFD) })
	fileFD, err := unix.Openat(rootFD, "not-a-directory", unix.O_CREAT|unix.O_EXCL|unix.O_RDONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("create regular file: %v", err)
	}
	t.Cleanup(func() { _ = unix.Close(fileFD) })
	if _, _, err := inspectTrashDescriptorPair(mounts.TrashDescriptorPair{FilesFD: filesFD, InfoFD: fileFD}); !errors.Is(err, ErrDrifted) {
		t.Fatalf("inspectTrashDescriptorPair(directory, regular file) error = %v, want ErrDrifted", err)
	}

	expected := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 1},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 2},
		Inode:       domain.Uint64Fact{Known: true, Value: 3},
		Type:        domain.FileTypeDirectory,
		UID:         domain.Uint32Fact{Known: true, Value: 4},
		GID:         domain.Uint32Fact{Known: true, Value: 5},
		Mode:        domain.Uint32Fact{Known: true, Value: 0o700},
		MountID:     domain.Uint64Fact{Known: true, Value: 6},
	}
	if err := compareTrashDirectoryIdentity("test", expected, expected); err != nil {
		t.Fatalf("compareTrashDirectoryIdentity(equal) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{name: "device", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.DeviceMajor.Value++ }},
		{name: "inode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Inode.Value++ }},
		{name: "type", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Type = domain.FileTypeRegular }},
		{name: "ownership", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.UID.Value++ }},
		{name: "mode", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.Mode.Value++ }},
		{name: "mount ID", mutate: func(snapshot *domain.FilesystemSnapshot) { snapshot.MountID.Value++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := expected
			test.mutate(&observed)
			if err := compareTrashDirectoryIdentity("test", expected, observed); !errors.Is(err, ErrDrifted) {
				t.Fatalf("compareTrashDirectoryIdentity(%s drift) error = %v, want ErrDrifted", test.name, err)
			}
		})
	}

	directories, source, _, _ := openTrashBridgeTestDirectories(t)
	pair, err := source.Duplicate()
	if err != nil {
		t.Fatalf("duplicate trusted Trash pair: %v", err)
	}
	t.Cleanup(func() { _ = pair.Close() })
	directories.info.Mode.Value++
	if err := directories.verifyPairLocked(pair); !errors.Is(err, ErrDrifted) {
		t.Fatalf("verifyPairLocked(info identity drift) error = %v, want ErrDrifted", err)
	}
}

func TestTrashDirectoriesRequalificationFailsClosed(t *testing.T) {
	if err := (*TrashDirectories)(nil).Close(); err != nil {
		t.Fatalf("nil TrashDirectories.Close() error = %v", err)
	}
	directories := &TrashDirectories{}
	if err := directories.Close(); err != nil {
		t.Fatalf("TrashDirectories.Close() error = %v", err)
	}
	if err := directories.Close(); err != nil {
		t.Fatalf("second TrashDirectories.Close() error = %v", err)
	}
	if _, err := directories.duplicateCheckedLocked(); !errors.Is(err, ErrDrifted) {
		t.Fatalf("duplicateCheckedLocked(closed) error = %v, want ErrDrifted", err)
	}
	if _, err := (&TrashDirectories{}).duplicateCheckedLocked(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("duplicateCheckedLocked(missing source) error = %v, want ErrUnsupported", err)
	}

	rootID, err := domain.NewTrustedRootID("linuxfs-trash-requalification-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source := &testTrashDirectorySource{rootID: rootID}
	if _, err := (&TrashDirectories{source: source}).duplicateCheckedLocked(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("duplicateCheckedLocked(invalid root) error = %v, want ErrUnsupported", err)
	}

	t.Run("root differs before duplicate", func(t *testing.T) {
		directories, source, _, _ := openTrashBridgeTestDirectories(t)
		otherRoot, err := domain.NewTrustedRootID("linuxfs-trash-requalification-other")
		if err != nil {
			t.Fatalf("NewTrustedRootID() error = %v", err)
		}
		source.rootID = otherRoot
		if _, err := directories.duplicateCheckedLocked(); !errors.Is(err, ErrDrifted) {
			t.Fatalf("duplicateCheckedLocked(root mismatch) error = %v, want ErrDrifted", err)
		}
	})

	t.Run("root changes during duplicate closes pair", func(t *testing.T) {
		directories, source, files, info := openTrashBridgeTestDirectories(t)
		otherRoot, err := domain.NewTrustedRootID("linuxfs-trash-requalification-changed")
		if err != nil {
			t.Fatalf("NewTrustedRootID() error = %v", err)
		}
		var duplicated []int
		source.duplicate = func() (mounts.TrashDescriptorPair, error) {
			pair, err := duplicateTestTrashPair(files, info)
			if err != nil {
				return pair, err
			}
			duplicated = []int{pair.FilesFD, pair.InfoFD}
			source.rootID = otherRoot
			return pair, nil
		}
		if _, err := directories.duplicateCheckedLocked(); !errors.Is(err, ErrDrifted) {
			t.Fatalf("duplicateCheckedLocked(root changed) error = %v, want ErrDrifted", err)
		}
		for _, fd := range duplicated {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Fatalf("root-change duplicate %d remains open: %v", fd, err)
			}
		}
	})

	t.Run("requalified pair is validated", func(t *testing.T) {
		directories, source, _, _ := openTrashBridgeTestDirectories(t)
		source.duplicate = func() (mounts.TrashDescriptorPair, error) {
			return mounts.TrashDescriptorPair{FilesFD: 0, InfoFD: 1}, nil
		}
		if _, err := directories.duplicateCheckedLocked(); !errors.Is(err, ErrDrifted) {
			t.Fatalf("duplicateCheckedLocked(reserved pair) error = %v, want ErrDrifted", err)
		}
	})
}

func TestTrashInfoFailureClassificationsDoNotCreateMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "bad descriptor", err: unix.EBADF, want: ErrDrifted},
		{name: "unsupported directory", err: unix.EINVAL, want: ErrUnsupported},
		{name: "interrupted directory sync", err: unix.EIO, want: ErrInterrupted},
	} {
		t.Run("preflight "+test.name, func(t *testing.T) {
			directories, _, _, info := openTrashBridgeTestDirectories(t)
			hooks := defaultTrashInfoPublishHooks()
			hooks.syncDirectory = func(int) error { return test.err }
			publication, err := publishTrashInfoDurableWith(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents"), hooks)
			if !errors.Is(err, test.want) || publication != nil {
				t.Fatalf("publishTrashInfoDurableWith(preflight %v) = (%v, %v), want nil + %v", test.err, publication, err, test.want)
			}
			if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
				t.Fatal("preflight failure created a Trash info record")
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "occupied", err: unix.EEXIST, want: ErrDrifted},
		{name: "unavailable", err: unix.ENOSYS, want: ErrUnsupported},
		{name: "uncertain", err: unix.EINTR, want: ErrInterrupted},
		{name: "definite", err: unix.EIO, want: ErrDrifted},
	} {
		t.Run("open "+test.name, func(t *testing.T) {
			directories, _, _, info := openTrashBridgeTestDirectories(t)
			hooks := defaultTrashInfoPublishHooks()
			hooks.syncDirectory = func(int) error { return nil }
			hooks.open = func(int, string, int, uint32) (int, error) { return -1, test.err }
			publication, err := publishTrashInfoDurableWith(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents"), hooks)
			if !errors.Is(err, test.want) || publication != nil {
				t.Fatalf("publishTrashInfoDurableWith(open %v) = (%v, %v), want nil + %v", test.err, publication, err, test.want)
			}
			if testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
				t.Fatal("open failure created a Trash info record")
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "directory collision", err: unix.ENOTEMPTY, want: ErrDrifted},
		{name: "symlink collision", err: unix.ELOOP, want: ErrDrifted},
		{name: "read-only", err: unix.EROFS, want: ErrUnsupported},
		{name: "retryable", err: unix.EAGAIN, want: ErrInterrupted},
	} {
		t.Run("open classifier "+test.name, func(t *testing.T) {
			if got := classifyTrashInfoOpenFailure(test.err); !errors.Is(got, test.want) {
				t.Fatalf("classifyTrashInfoOpenFailure(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "filesystem unsupported", err: unix.EOPNOTSUPP, want: ErrUnsupported},
		{name: "read-only", err: unix.EROFS, want: ErrUnsupported},
		{name: "interrupted", err: unix.EINTR, want: ErrInterrupted},
	} {
		t.Run("preflight classifier "+test.name, func(t *testing.T) {
			if got := classifyTrashInfoPreflightFailure(test.err); !errors.Is(got, test.want) {
				t.Fatalf("classifyTrashInfoPreflightFailure(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestPublishTrashInfoDurableRetainsPostCreateFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*testing.T, context.CancelFunc, *trashInfoPublishHooks)
		wantDrift bool
	}{
		{
			name: "context changes after open",
			configure: func(t *testing.T, cancel context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				hooks.open = func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
					fd, err := openDurableFile(directoryFD, basename, flags, mode)
					if err == nil {
						cancel()
					}
					return fd, err
				}
			},
		},
		{
			name: "identity mode drifts after open",
			configure: func(t *testing.T, _ context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				hooks.open = func(directoryFD int, basename string, flags int, mode uint32) (int, error) {
					fd, err := openDurableFile(directoryFD, basename, flags, mode)
					if err != nil {
						return fd, err
					}
					if err := unix.Fchmod(fd, 0o644); err != nil {
						_ = unix.Close(fd)
						t.Fatalf("fchmod newly opened Trash info record: %v", err)
					}
					return fd, nil
				}
			},
			wantDrift: true,
		},
		{
			name: "write fails after a partial write",
			configure: func(t *testing.T, _ context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				hooks.write = func(fd int, contents []byte) (int, error) {
					count, err := unix.Write(fd, contents[:1])
					if err != nil {
						return count, err
					}
					return count, unix.EIO
				}
			},
		},
		{
			name: "close reports uncertainty after closing descriptor",
			configure: func(t *testing.T, _ context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				hooks.close = func(fd int) error {
					if err := unix.Close(fd); err != nil {
						return err
					}
					return unix.EIO
				}
			},
		},
		{
			name: "context changes before directory sync",
			configure: func(t *testing.T, cancel context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				hooks.close = func(fd int) error {
					if err := unix.Close(fd); err != nil {
						return err
					}
					cancel()
					return nil
				}
			},
		},
		{
			name: "second directory sync fails",
			configure: func(t *testing.T, _ context.CancelFunc, hooks *trashInfoPublishHooks) {
				t.Helper()
				calls := 0
				hooks.syncDirectory = func(fd int) error {
					calls++
					if calls == 2 {
						return unix.EIO
					}
					return unix.Fsync(fd)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directories, _, _, info := openTrashBridgeTestDirectories(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hooks := defaultTrashInfoPublishHooks()
			test.configure(t, cancel, &hooks)

			publication, err := publishTrashInfoDurableWith(ctx, directories, "ldc-0123456789abcdef", []byte("contents"), hooks)
			if !errors.Is(err, ErrInterrupted) || publication != nil {
				t.Fatalf("publishTrashInfoDurableWith(%s) = (%v, %v), want nil + ErrInterrupted", test.name, publication, err)
			}
			if test.wantDrift && !errors.Is(err, ErrDrifted) {
				t.Fatalf("publishTrashInfoDurableWith(%s) error = %v, want ErrDrifted too", test.name, err)
			}
			if !testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
				t.Fatalf("publishTrashInfoDurableWith(%s) removed or failed to retain the post-create record", test.name)
			}
		})
	}
}

func TestPublishTrashInfoDurableRetainsDirectoryIdentityDrift(t *testing.T) {
	directories, _, _, info := openTrashBridgeTestDirectories(t)
	calls := 0
	hooks := defaultTrashInfoPublishHooks()
	hooks.syncDirectory = func(fd int) error {
		calls++
		if err := unix.Fsync(fd); err != nil {
			return err
		}
		if calls == 2 {
			if err := unix.Fchmod(fd, 0o755); err != nil {
				t.Fatalf("fchmod requalified Trash info directory: %v", err)
			}
		}
		return nil
	}

	publication, err := publishTrashInfoDurableWith(context.Background(), directories, "ldc-0123456789abcdef", []byte("contents"), hooks)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) || publication != nil {
		t.Fatalf("publishTrashInfoDurableWith(directory drift) = (%v, %v), want nil + ErrInterrupted + ErrDrifted", publication, err)
	}
	if !testEntryExists(t, info, "ldc-0123456789abcdef.trashinfo") {
		t.Fatal("directory identity drift removed the post-create Trash info record")
	}
}
