//go:build linux

package linuxfs

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestProbeOwnedTrashInfoReadsExactOwnedRecordWithoutMutation(t *testing.T) {
	source, directories := newTrashProbeDirectories(t)
	const token = "ldc-0123456789abcdef"
	want := []byte("[Trash Info]\nPath=source/item\nDeletionDate=2026-07-18T12:00:00\n")

	if _, err := PublishTrashInfoDurable(context.Background(), directories, token, want); err != nil {
		t.Fatalf("PublishTrashInfoDurable() error = %v", err)
	}
	beforeQualification := source.topologyDuplicateCalls

	got, present, err := ProbeOwnedTrashInfo(context.Background(), directories, token)
	if err != nil {
		t.Fatalf("ProbeOwnedTrashInfo() error = %v", err)
	}
	if !present {
		t.Fatal("ProbeOwnedTrashInfo() reported a published record absent")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ProbeOwnedTrashInfo() contents = %q, want %q", got, want)
	}
	if source.topologyDuplicateCalls <= beforeQualification {
		t.Fatal("ProbeOwnedTrashInfo() did not requalify the descriptor-rooted topology")
	}

	got[0] = 'x'
	if retained := readTopologyTestFile(t, source.infoFD, token+trashInfoFilenameSuffix); !bytes.Equal(retained, want) {
		t.Fatalf("probe changed retained metadata: got %q, want %q", retained, want)
	}
	if trashMoveEntryExists(t, source.filesFD, token) {
		t.Fatal("probe created or moved a Trash files entry")
	}
}

func TestProbeOwnedTrashInfoReportsStableExactAbsence(t *testing.T) {
	source, directories := newTrashProbeDirectories(t)
	const token = "ldc-fedcba9876543210"
	beforeQualification := source.topologyDuplicateCalls

	contents, present, err := ProbeOwnedTrashInfo(context.Background(), directories, token)
	if err != nil {
		t.Fatalf("ProbeOwnedTrashInfo() error = %v", err)
	}
	if present || contents != nil {
		t.Fatalf("ProbeOwnedTrashInfo() = (%q, %t), want nil, false", contents, present)
	}
	if source.topologyDuplicateCalls <= beforeQualification {
		t.Fatal("absence probe did not requalify the descriptor-rooted topology")
	}
	if trashMoveEntryExists(t, source.infoFD, token+trashInfoFilenameSuffix) {
		t.Fatal("absence probe created metadata")
	}
	if trashMoveEntryExists(t, source.filesFD, token) {
		t.Fatal("absence probe created or moved a Trash files entry")
	}
}

func TestProbeOwnedTrashInfoRejectsUnsafeRetainedEntriesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, source *topologyTrashTestSource, basename string)
	}{
		{
			name: "symlink",
			prepare: func(t *testing.T, source *topologyTrashTestSource, basename string) {
				t.Helper()
				if err := unix.Symlinkat("untrusted-target", source.infoFD, basename); err != nil {
					t.Fatalf("Symlinkat() error = %v", err)
				}
			},
		},
		{
			name: "directory",
			prepare: func(t *testing.T, source *topologyTrashTestSource, basename string) {
				t.Helper()
				if err := unix.Mkdirat(source.infoFD, basename, 0o700); err != nil {
					t.Fatalf("Mkdirat() error = %v", err)
				}
			},
		},
		{
			name: "wrong mode",
			prepare: func(t *testing.T, source *topologyTrashTestSource, basename string) {
				t.Helper()
				createTrashProbeRegularFile(t, source.infoFD, basename, 0o644, 0)
			},
		},
		{
			name: "hard linked file",
			prepare: func(t *testing.T, source *topologyTrashTestSource, basename string) {
				t.Helper()
				createTrashProbeRegularFile(t, source.infoFD, basename, durableFileMode, 0)
				if err := unix.Linkat(source.infoFD, basename, source.infoFD, "linked-trash-probe-record", 0); err != nil {
					t.Fatalf("Linkat() error = %v", err)
				}
			},
		},
		{
			name: "oversized file",
			prepare: func(t *testing.T, source *topologyTrashTestSource, basename string) {
				t.Helper()
				createTrashProbeRegularFile(t, source.infoFD, basename, durableFileMode, maximumDurableFileBytes+1)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, directories := newTrashProbeDirectories(t)
			const token = "ldc-0123456789abcdef"
			basename, err := trashInfoBasename(token)
			if err != nil {
				t.Fatalf("trashInfoBasename() error = %v", err)
			}
			test.prepare(t, source, basename)

			contents, present, err := ProbeOwnedTrashInfo(context.Background(), directories, token)
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("ProbeOwnedTrashInfo() error = %v, want ErrDrifted", err)
			}
			if present || contents != nil {
				t.Fatalf("ProbeOwnedTrashInfo() = (%q, %t), want nil, false on unsafe record", contents, present)
			}
			if !trashMoveEntryExists(t, source.infoFD, basename) {
				t.Fatal("probe removed the unsafe retained metadata entry")
			}
			if trashMoveEntryExists(t, source.filesFD, token) {
				t.Fatal("probe created or moved a Trash files entry")
			}
		})
	}
}

func TestProbeOwnedTrashInfoRejectsLegacyDirectoriesAndCanceledContext(t *testing.T) {
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	legacy, err := openTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTrashDirectoriesWithSource() error = %v", err)
	}
	defer legacy.Close()

	if _, _, err := ProbeOwnedTrashInfo(context.Background(), legacy, "ldc-0123456789abcdef"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ProbeOwnedTrashInfo(legacy) error = %v, want ErrUnsupported", err)
	}

	_, directories := newTrashProbeDirectories(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ProbeOwnedTrashInfo(ctx, directories, "ldc-0123456789abcdef"); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("ProbeOwnedTrashInfo(canceled) error = %v, want ErrInterrupted", err)
	}
}

func TestReadOwnedTrashInfoContentsClassifiesInterruptedReads(t *testing.T) {
	for _, test := range []struct {
		name string
		read func(call int, contents []byte) (int, error)
	}{
		{
			name: "content read",
			read: func(_ int, _ []byte) (int, error) {
				return 0, unix.EINTR
			},
		},
		{
			name: "terminator read",
			read: func(call int, contents []byte) (int, error) {
				if call == 1 {
					contents[0] = 'x'
					return 1, nil
				}
				return 0, unix.EINTR
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, err := readOwnedTrashInfoContentsWith(context.Background(), 42, 1, func(_ int, contents []byte) (int, error) {
				calls++
				return test.read(calls, contents)
			})
			if !errors.Is(err, ErrInterrupted) {
				t.Fatalf("readOwnedTrashInfoContentsWith() error = %v, want ErrInterrupted", err)
			}
		})
	}
}

func TestProbeOwnedTrashInfoRejectsSameInodeRewriteBeforeFinalVerification(t *testing.T) {
	source, directories := newTrashProbeDirectories(t)
	const token = "ldc-0123456789abcdef"
	want := []byte("[Trash Info]\nPath=source/item\nDeletionDate=2026-07-18T12:00:00\n")
	replacement := bytes.Repeat([]byte("x"), len(want))

	if _, err := PublishTrashInfoDurable(context.Background(), directories, token, want); err != nil {
		t.Fatalf("PublishTrashInfoDurable() error = %v", err)
	}
	basename, err := trashInfoBasename(token)
	if err != nil {
		t.Fatalf("trashInfoBasename() error = %v", err)
	}
	var before unix.Stat_t
	if err := unix.Fstatat(source.infoFD, basename, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("Fstatat(before rewrite) error = %v", err)
	}

	contents, present, err := probeOwnedTrashInfoWith(
		context.Background(),
		directories,
		token,
		func(ctx context.Context, directoryFD int, name string, expected domain.FilesystemSnapshot, observed []byte) error {
			fd, err := unix.Openat(source.infoFD, basename, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return err
			}
			defer unix.Close(fd)
			count, err := unix.Pwrite(fd, replacement, 0)
			if err != nil {
				return err
			}
			if count != len(replacement) {
				return errors.New("short same-inode test rewrite")
			}
			return verifyOwnedTrashInfoName(ctx, directoryFD, name, expected, observed)
		},
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("probeOwnedTrashInfoWith() error = %v, want ErrDrifted", err)
	}
	if present || contents != nil {
		t.Fatalf("probeOwnedTrashInfoWith() = (%q, %t), want nil, false after same-inode rewrite", contents, present)
	}
	var after unix.Stat_t
	if err := unix.Fstatat(source.infoFD, basename, &after, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatalf("Fstatat(after rewrite) error = %v", err)
	}
	if before.Dev != after.Dev || before.Ino != after.Ino {
		t.Fatalf("same-inode rewrite replaced the record: before=(%d,%d), after=(%d,%d)", before.Dev, before.Ino, after.Dev, after.Ino)
	}
	if retained := readTopologyTestFile(t, source.infoFD, basename); !bytes.Equal(retained, replacement) {
		t.Fatalf("final verifier changed retained rewritten metadata: got %q, want %q", retained, replacement)
	}
	if trashMoveEntryExists(t, source.filesFD, token) {
		t.Fatal("probe created or moved a Trash files entry after a same-inode rewrite")
	}
}

func TestSameOwnedTrashInfoIdentityIncludesContentChangeFacts(t *testing.T) {
	baseline := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 8},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 1},
		Inode:       domain.Uint64Fact{Known: true, Value: 42},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true, Value: 1000},
		GID:         domain.Uint32Fact{Known: true, Value: 1000},
		Mode:        domain.Uint32Fact{Known: true, Value: durableFileMode},
		LinkCount:   domain.Uint64Fact{Known: true, Value: 1},
		Size:        domain.Uint64Fact{Known: true, Value: 64},
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 100},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 101},
		MountID:     domain.Uint64Fact{Known: true, Value: 17},
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.FilesystemSnapshot)
	}{
		{
			name: "size",
			mutate: func(snapshot *domain.FilesystemSnapshot) {
				snapshot.Size.Value++
			},
		},
		{
			name: "modified time",
			mutate: func(snapshot *domain.FilesystemSnapshot) {
				snapshot.ModifiedAt.Value++
			},
		},
		{
			name: "changed time",
			mutate: func(snapshot *domain.FilesystemSnapshot) {
				snapshot.ChangedAt.Value++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := baseline
			test.mutate(&observed)
			if sameOwnedTrashInfoIdentity(baseline, observed) {
				t.Fatal("sameOwnedTrashInfoIdentity() accepted an in-place content-change fact")
			}
		})
	}
}

func newTrashProbeDirectories(t *testing.T) (*topologyTrashTestSource, *TrashDirectories) {
	t.Helper()
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
	}
	t.Cleanup(func() { _ = directories.Close() })
	return source, directories
}

func createTrashProbeRegularFile(t *testing.T, directoryFD int, basename string, mode uint32, size int) {
	t.Helper()
	fd, err := unix.Openat(directoryFD, basename, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, durableFileMode)
	if err != nil {
		t.Fatalf("Openat(%q) error = %v", basename, err)
	}
	defer unix.Close(fd)
	if err := unix.Fchmod(fd, mode); err != nil {
		t.Fatalf("Fchmod(%q) error = %v", basename, err)
	}
	if size > 0 {
		if err := unix.Ftruncate(fd, int64(size)); err != nil {
			t.Fatalf("Ftruncate(%q) error = %v", basename, err)
		}
	}
}
