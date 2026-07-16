//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestOpenTopologyQualifiedTrashDirectoriesRequiresLiteralFDOLayout(t *testing.T) {
	for _, placement := range []mounts.TrashPlacement{
		mounts.TrashPlacementHome,
		mounts.TrashPlacementTopUser,
		mounts.TrashPlacementTopShared,
	} {
		t.Run(string(placement), func(t *testing.T) {
			source := newTopologyTrashTestSource(t, placement)

			directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
			if err != nil {
				t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
			}
			defer directories.Close()

			if !directories.topologyQualified() {
				t.Fatal("topology-qualified directories did not retain the topology requirement")
			}
			if directories.RootID() != source.rootID {
				t.Fatalf("directories RootID() = %q, want %q", directories.RootID(), source.rootID)
			}

			pair, err := directories.duplicateCheckedLocked()
			if err != nil {
				t.Fatalf("topology-qualified directories did not requalify: %v", err)
			}
			if err := pair.Close(); err != nil {
				t.Fatalf("close requalified Trash pair: %v", err)
			}
		})
	}
}

func TestOpenTopologyQualifiedTrashDirectoriesRejectsHostileOrUnrelatedRoles(t *testing.T) {
	tests := []struct {
		name      string
		placement mounts.TrashPlacement
		mutate    func(t *testing.T, source *topologyTrashTestSource)
	}{
		{
			name:      "unrelated files descriptor",
			placement: mounts.TrashPlacementHome,
			mutate: func(t *testing.T, source *topologyTrashTestSource) {
				makeTestDirectory(t, source.rootFD, "unrelated")
				unrelated := openTopologyTestChild(t, source.rootFD, "unrelated")
				source.replaceFiles(t, unrelated)
			},
		},
		{
			name:      "symlinked files child",
			placement: mounts.TrashPlacementTopUser,
			mutate: func(t *testing.T, source *topologyTrashTestSource) {
				makeTestDirectory(t, source.trashRootFD, "elsewhere")
				elsewhere := openTopologyTestChild(t, source.trashRootFD, "elsewhere")
				source.replaceFiles(t, elsewhere)
				if err := unix.Unlinkat(source.trashRootFD, "files", unix.AT_REMOVEDIR); err != nil {
					t.Fatalf("remove literal files child: %v", err)
				}
				if err := unix.Symlinkat("elsewhere", source.trashRootFD, "files"); err != nil {
					t.Fatalf("replace files child with symlink: %v", err)
				}
			},
		},
		{
			name:      "shared parent loses sticky bit",
			placement: mounts.TrashPlacementTopShared,
			mutate: func(t *testing.T, source *topologyTrashTestSource) {
				if err := unix.Fchmod(source.sharedTopFD, 0o700); err != nil {
					t.Fatalf("remove shared Trash sticky bit: %v", err)
				}
			},
		},
		{
			name:      "shared user root is unrelated",
			placement: mounts.TrashPlacementTopShared,
			mutate: func(t *testing.T, source *topologyTrashTestSource) {
				makeTestDirectory(t, source.rootFD, "unrelated")
				unrelated := openTopologyTestChild(t, source.rootFD, "unrelated")
				source.replaceTrashRoot(t, unrelated)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := newTopologyTrashTestSource(t, test.placement)
			test.mutate(t, source)

			directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
			if !errors.Is(err, ErrDrifted) {
				t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v, want ErrDrifted", err)
			}
			if directories != nil {
				defer directories.Close()
				t.Fatal("hostile or unrelated layout produced usable Trash directories")
			}
		})
	}
}

func TestTopologyQualifiedTrashDirectoriesRequalifyLiteralChildrenBeforePublication(t *testing.T) {
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
	}
	defer directories.Close()

	if err := unix.Unlinkat(source.trashRootFD, "files", unix.AT_REMOVEDIR); err != nil {
		t.Fatalf("remove initial literal files child: %v", err)
	}
	makeTestDirectory(t, source.trashRootFD, "files")

	const token = "ldc-0123456789abcdef"
	publication, err := PublishTrashInfoDurable(context.Background(), directories, token, []byte("contents"))
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("PublishTrashInfoDurable() error = %v, want ErrDrifted", err)
	}
	if publication != nil {
		t.Fatal("literal-child drift produced a Trash publication")
	}
	fd, err := unix.Openat(source.infoFD, token+trashInfoFilenameSuffix, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err == nil {
		_ = unix.Close(fd)
		t.Fatal("literal-child drift created a Trash info record")
	}
	if !errors.Is(err, unix.ENOENT) {
		t.Fatalf("open missing Trash info record error = %v, want ENOENT", err)
	}
}

func TestTopologyQualifiedTrashDirectoriesReproveLayoutAfterDurablePublication(t *testing.T) {
	for _, placement := range []mounts.TrashPlacement{
		mounts.TrashPlacementHome,
		mounts.TrashPlacementTopUser,
		mounts.TrashPlacementTopShared,
	} {
		t.Run(string(placement), func(t *testing.T) {
			source := newTopologyTrashTestSource(t, placement)
			directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
			if err != nil {
				t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
			}
			defer directories.Close()

			const token = "ldc-0123456789abcdef"
			contents := []byte("[Trash Info]\nPath=example\n")
			publication, err := PublishTrashInfoDurable(context.Background(), directories, token, contents)
			if err != nil {
				t.Fatalf("PublishTrashInfoDurable() error = %v", err)
			}
			if publication == nil {
				t.Fatal("PublishTrashInfoDurable() returned nil publication")
			}
			if publication.rootID != source.rootID {
				t.Fatalf("publication root ID = %q, want %q", publication.rootID, source.rootID)
			}
			if publication.token != token {
				t.Fatalf("publication token = %q, want %q", publication.token, token)
			}
			if got := readTopologyTestFile(t, source.infoFD, token+trashInfoFilenameSuffix); string(got) != string(contents) {
				t.Fatalf("published topology-qualified Trash info = %q, want %q", got, contents)
			}
			if source.topologyDuplicateCalls < 3 {
				t.Fatalf("topology descriptor handoffs = %d, want initial qualification, pre-write requalification, and post-publication reproof", source.topologyDuplicateCalls)
			}
		})
	}
}

func TestTopologyQualifiedTrashDirectoriesRetainPublicationWhenPostCreateReproofDrifts(t *testing.T) {
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if err != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v", err)
	}
	defer directories.Close()

	const token = "ldc-0123456789abcdef"
	contents := []byte("[Trash Info]\nPath=example\n")
	hooks := defaultTrashInfoPublishHooks()
	syncCalls := 0
	hooks.syncDirectory = func(fd int) error {
		syncCalls++
		if syncCalls == 2 {
			if err := unix.Unlinkat(source.trashRootFD, "files", unix.AT_REMOVEDIR); err != nil {
				t.Fatalf("remove literal files child after metadata create: %v", err)
			}
			makeTestDirectory(t, source.trashRootFD, "files")
		}
		return unix.Fsync(fd)
	}

	publication, err := publishTrashInfoDurableWith(context.Background(), directories, token, contents, hooks)
	if !errors.Is(err, ErrInterrupted) || !errors.Is(err, ErrDrifted) {
		t.Fatalf("publishTrashInfoDurableWith() error = %v, want ErrInterrupted + ErrDrifted", err)
	}
	if publication != nil {
		t.Fatal("post-create topology drift returned a completed publication")
	}
	if got := readTopologyTestFile(t, source.infoFD, token+trashInfoFilenameSuffix); string(got) != string(contents) {
		t.Fatalf("post-create topology drift removed or changed retained Trash info = %q, want %q", got, contents)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want preflight plus post-create sync", syncCalls)
	}
}

func TestOpenTopologyQualifiedTrashDirectoriesRejectsNilLease(t *testing.T) {
	directories, err := OpenTopologyQualifiedTrashDirectories(nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("OpenTopologyQualifiedTrashDirectories(nil) error = %v, want ErrUnsupported", err)
	}
	if directories != nil {
		defer directories.Close()
		t.Fatal("nil lease produced topology-qualified Trash directories")
	}
}

func TestTopologyQualifiedTrashDirectoriesFailClosedForInvalidSources(t *testing.T) {
	if directories, err := openTopologyQualifiedTrashDirectoriesWithSource(nil); !errors.Is(err, ErrUnsupported) || directories != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource(nil) = (%v, %v), want nil + ErrUnsupported", directories, err)
	}

	invalidRoot := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	invalidRoot.rootID = ""
	if directories, err := openTopologyQualifiedTrashDirectoriesWithSource(invalidRoot); !errors.Is(err, ErrUnsupported) || directories != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource(invalid root) = (%v, %v), want nil + ErrUnsupported", directories, err)
	}

	closedSource := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	closedSource.topologyDuplicateErr = mounts.ErrLeaseClosed
	if directories, err := openTopologyQualifiedTrashDirectoriesWithSource(closedSource); !errors.Is(err, ErrDrifted) || directories != nil {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource(closed source) = (%v, %v), want nil + ErrDrifted", directories, err)
	}

	var nilDirectories *TrashDirectories
	if nilDirectories.RootID() != "" {
		t.Fatal("nil TrashDirectories RootID() exposed a trusted root")
	}
}

func TestOpenTopologyQualifiedTrashDirectoriesRejectsIncompleteTopologyAuthority(t *testing.T) {
	source := newTopologyTrashTestSource(t, mounts.TrashPlacementHome)
	source.anchorKind = mounts.TrashAnchorFilesystemTop

	directories, err := openTopologyQualifiedTrashDirectoriesWithSource(source)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openTopologyQualifiedTrashDirectoriesWithSource() error = %v, want ErrUnsupported", err)
	}
	if directories != nil {
		defer directories.Close()
		t.Fatal("mismatched topology authority produced usable Trash directories")
	}
}

func TestInspectTrashTopologyDescriptorSetRejectsMalformedAuthorityBeforeFilesystemAccess(t *testing.T) {
	validNonShared := mounts.TrashTopologyDescriptorSet{AnchorFD: 3, TrashRootFD: 4, FilesFD: 5, InfoFD: 6, SharedTopFD: -1}
	for _, test := range []struct {
		name       string
		set        mounts.TrashTopologyDescriptorSet
		placement  mounts.TrashPlacement
		anchorKind mounts.TrashAnchorKind
		want       error
	}{
		{
			name:       "unknown placement",
			set:        validNonShared,
			placement:  mounts.TrashPlacement("unknown"),
			anchorKind: mounts.TrashAnchorFilesystemTop,
			want:       ErrUnsupported,
		},
		{
			name:       "home with filesystem anchor",
			set:        validNonShared,
			placement:  mounts.TrashPlacementHome,
			anchorKind: mounts.TrashAnchorFilesystemTop,
			want:       ErrUnsupported,
		},
		{
			name:       "top directory with home anchor",
			set:        validNonShared,
			placement:  mounts.TrashPlacementTopUser,
			anchorKind: mounts.TrashAnchorHomeData,
			want:       ErrUnsupported,
		},
		{
			name:       "incomplete descriptor set",
			set:        mounts.TrashTopologyDescriptorSet{AnchorFD: -1, TrashRootFD: 4, FilesFD: 5, InfoFD: 6, SharedTopFD: -1},
			placement:  mounts.TrashPlacementHome,
			anchorKind: mounts.TrashAnchorHomeData,
			want:       ErrDrifted,
		},
		{
			name:       "shared layout without shared parent",
			set:        validNonShared,
			placement:  mounts.TrashPlacementTopShared,
			anchorKind: mounts.TrashAnchorFilesystemTop,
			want:       ErrDrifted,
		},
		{
			name:       "non-shared layout with shared parent",
			set:        mounts.TrashTopologyDescriptorSet{AnchorFD: 3, TrashRootFD: 4, FilesFD: 5, InfoFD: 6, SharedTopFD: 7},
			placement:  mounts.TrashPlacementTopUser,
			anchorKind: mounts.TrashAnchorFilesystemTop,
			want:       ErrDrifted,
		},
		{
			name:       "aliased topology roles",
			set:        mounts.TrashTopologyDescriptorSet{AnchorFD: 3, TrashRootFD: 3, FilesFD: 5, InfoFD: 6, SharedTopFD: -1},
			placement:  mounts.TrashPlacementTopUser,
			anchorKind: mounts.TrashAnchorFilesystemTop,
			want:       ErrDrifted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := inspectTrashTopologyDescriptorSet(test.set, test.placement, test.anchorKind, uint32(unix.Geteuid()))
			if !errors.Is(err, test.want) {
				t.Fatalf("inspectTrashTopologyDescriptorSet() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestTopologyEvidenceHelpersRejectIdentityAndPermissionDrift(t *testing.T) {
	ownerUID := uint32(unix.Geteuid())
	anchor := topologyTestSnapshot(1, ownerUID, 0o700)
	trashRoot := topologyTestSnapshot(2, ownerUID, 0o700)
	files := topologyTestSnapshot(3, ownerUID, 0o700)
	info := topologyTestSnapshot(4, ownerUID, 0o700)
	sharedTop := topologyTestSnapshot(5, ownerUID, 0o1777)
	snapshots := trashTopologySnapshots{
		anchor:    anchor,
		trashRoot: trashRoot,
		files:     files,
		info:      info,
		sharedTop: sharedTop,
	}

	if err := requireSameTrashMount("test", anchor, trashRoot); err != nil {
		t.Fatalf("requireSameTrashMount() error = %v", err)
	}
	differentMount := trashRoot
	differentMount.MountID.Value++
	if err := requireSameTrashMount("test", anchor, differentMount); !errors.Is(err, ErrDrifted) {
		t.Fatalf("requireSameTrashMount(drift) error = %v, want ErrDrifted", err)
	}

	if err := requirePrivateTrashDirectory("test", files, ownerUID); err != nil {
		t.Fatalf("requirePrivateTrashDirectory() error = %v", err)
	}
	wrongOwner := files
	wrongOwner.UID.Value++
	if err := requirePrivateTrashDirectory("test", wrongOwner, ownerUID); !errors.Is(err, ErrDrifted) {
		t.Fatalf("requirePrivateTrashDirectory(wrong owner) error = %v, want ErrDrifted", err)
	}
	wrongMode := files
	wrongMode.Mode.Value = 0o750
	if err := requirePrivateTrashDirectory("test", wrongMode, ownerUID); !errors.Is(err, ErrDrifted) {
		t.Fatalf("requirePrivateTrashDirectory(wrong mode) error = %v, want ErrDrifted", err)
	}

	if err := requireDistinctTrashTopologyRoles(snapshots, mounts.TrashPlacementTopShared); err != nil {
		t.Fatalf("requireDistinctTrashTopologyRoles() error = %v", err)
	}
	aliased := snapshots
	aliased.info = aliased.files
	if err := requireDistinctTrashTopologyRoles(aliased, mounts.TrashPlacementTopShared); !errors.Is(err, ErrDrifted) {
		t.Fatalf("requireDistinctTrashTopologyRoles(alias) error = %v, want ErrDrifted", err)
	}

	directories := &TrashDirectories{
		files: files,
		info:  info,
		topology: &trashTopologyQualification{
			placement: mounts.TrashPlacementTopShared,
			anchor:    anchor,
			trashRoot: trashRoot,
			sharedTop: sharedTop,
		},
	}
	if err := directories.compareTopologySnapshotsLocked(snapshots); err != nil {
		t.Fatalf("compareTopologySnapshotsLocked() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*trashTopologySnapshots)
	}{
		{name: "anchor", mutate: func(observed *trashTopologySnapshots) { observed.anchor.Inode.Value++ }},
		{name: "Trash root", mutate: func(observed *trashTopologySnapshots) { observed.trashRoot.Inode.Value++ }},
		{name: "shared parent", mutate: func(observed *trashTopologySnapshots) { observed.sharedTop.Inode.Value++ }},
		{name: "files", mutate: func(observed *trashTopologySnapshots) { observed.files.Inode.Value++ }},
		{name: "info", mutate: func(observed *trashTopologySnapshots) { observed.info.Inode.Value++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := snapshots
			test.mutate(&observed)
			if err := directories.compareTopologySnapshotsLocked(observed); !errors.Is(err, ErrDrifted) {
				t.Fatalf("compareTopologySnapshotsLocked(%s drift) error = %v, want ErrDrifted", test.name, err)
			}
		})
	}
}

func topologyTestSnapshot(inode uint64, ownerUID uint32, mode uint32) domain.FilesystemSnapshot {
	return domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 1},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 2},
		Inode:       domain.Uint64Fact{Known: true, Value: inode},
		Type:        domain.FileTypeDirectory,
		UID:         domain.Uint32Fact{Known: true, Value: ownerUID},
		GID:         domain.Uint32Fact{Known: true, Value: ownerUID},
		Mode:        domain.Uint32Fact{Known: true, Value: mode},
		MountID:     domain.Uint64Fact{Known: true, Value: 3},
	}
}

type topologyTrashTestSource struct {
	rootID     domain.TrustedRootID
	placement  mounts.TrashPlacement
	anchorKind mounts.TrashAnchorKind
	ownerUID   uint32

	rootFD      int
	anchorFD    int
	trashRootFD int
	filesFD     int
	infoFD      int
	sharedTopFD int

	topologyDuplicateCalls int
	topologyDuplicateErr   error
}

func newTopologyTrashTestSource(t *testing.T, placement mounts.TrashPlacement) *topologyTrashTestSource {
	t.Helper()

	rootID, err := domain.NewTrustedRootID("linuxfs-trash-topology-" + string(placement))
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	source := &topologyTrashTestSource{
		rootID:      rootID,
		placement:   placement,
		ownerUID:    uint32(unix.Geteuid()),
		rootFD:      openTestDirectory(t),
		sharedTopFD: -1,
	}
	t.Cleanup(func() {
		for _, fd := range []int{source.anchorFD, source.trashRootFD, source.filesFD, source.infoFD, source.sharedTopFD, source.rootFD} {
			if fd >= 0 {
				_ = unix.Close(fd)
			}
		}
	})

	makeTestDirectory(t, source.rootFD, "anchor")
	source.anchorFD = openTopologyTestChild(t, source.rootFD, "anchor")

	switch placement {
	case mounts.TrashPlacementHome:
		source.anchorKind = mounts.TrashAnchorHomeData
		makeTestDirectory(t, source.anchorFD, "Trash")
		source.trashRootFD = openTopologyTestChild(t, source.anchorFD, "Trash")
	case mounts.TrashPlacementTopUser:
		source.anchorKind = mounts.TrashAnchorFilesystemTop
		name := ".Trash-" + strconv.FormatUint(uint64(source.ownerUID), 10)
		makeTestDirectory(t, source.anchorFD, name)
		source.trashRootFD = openTopologyTestChild(t, source.anchorFD, name)
	case mounts.TrashPlacementTopShared:
		source.anchorKind = mounts.TrashAnchorFilesystemTop
		makeTestDirectory(t, source.anchorFD, ".Trash")
		source.sharedTopFD = openTopologyTestChild(t, source.anchorFD, ".Trash")
		if err := unix.Fchmod(source.sharedTopFD, 0o1777); err != nil {
			t.Fatalf("make shared Trash sticky: %v", err)
		}
		name := strconv.FormatUint(uint64(source.ownerUID), 10)
		makeTestDirectory(t, source.sharedTopFD, name)
		source.trashRootFD = openTopologyTestChild(t, source.sharedTopFD, name)
	default:
		t.Fatalf("unsupported topology test placement %q", placement)
	}
	makeTestDirectory(t, source.trashRootFD, "files")
	makeTestDirectory(t, source.trashRootFD, "info")
	source.filesFD = openTopologyTestChild(t, source.trashRootFD, "files")
	source.infoFD = openTopologyTestChild(t, source.trashRootFD, "info")
	return source
}

func (source *topologyTrashTestSource) RootID() domain.TrustedRootID {
	if source == nil {
		return ""
	}
	return source.rootID
}

func (source *topologyTrashTestSource) Placement() mounts.TrashPlacement {
	if source == nil {
		return ""
	}
	return source.placement
}

func (source *topologyTrashTestSource) AnchorKind() mounts.TrashAnchorKind {
	if source == nil {
		return ""
	}
	return source.anchorKind
}

func (source *topologyTrashTestSource) OwnerUID() uint32 {
	if source == nil {
		return 0
	}
	return source.ownerUID
}

func (source *topologyTrashTestSource) DuplicateTrashDescriptorPair() (mounts.TrashDescriptorPair, error) {
	if source == nil {
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, mounts.ErrLeaseClosed
	}
	return duplicateTopologyTestPair(source.filesFD, source.infoFD)
}

func (source *topologyTrashTestSource) DuplicateTrashTopologyDescriptorSet() (mounts.TrashTopologyDescriptorSet, error) {
	if source == nil {
		return mounts.EmptyTrashTopologyDescriptorSet(), mounts.ErrLeaseClosed
	}
	if source.topologyDuplicateErr != nil {
		return mounts.EmptyTrashTopologyDescriptorSet(), source.topologyDuplicateErr
	}
	source.topologyDuplicateCalls++
	return duplicateTopologyTestSet(source.anchorFD, source.trashRootFD, source.filesFD, source.infoFD, source.sharedTopFD)
}

func (source *topologyTrashTestSource) replaceFiles(t *testing.T, fd int) {
	t.Helper()
	if source.filesFD >= 0 {
		_ = unix.Close(source.filesFD)
	}
	source.filesFD = fd
}

func (source *topologyTrashTestSource) replaceTrashRoot(t *testing.T, fd int) {
	t.Helper()
	if source.trashRootFD >= 0 {
		_ = unix.Close(source.trashRootFD)
	}
	source.trashRootFD = fd
}

func openTopologyTestChild(t *testing.T, parentFD int, name string) int {
	t.Helper()
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatalf("open topology child %q: %v", name, err)
	}
	return fd
}

func readTopologyTestFile(t *testing.T, directoryFD int, basename string) []byte {
	t.Helper()
	fd, err := unix.Openat(directoryFD, basename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatalf("open topology test file %q: %v", basename, err)
	}
	defer unix.Close(fd)

	var contents []byte
	buffer := make([]byte, 256)
	for {
		count, err := unix.Read(fd, buffer)
		if count > 0 {
			contents = append(contents, buffer[:count]...)
		}
		if err != nil {
			t.Fatalf("read topology test file %q: %v", basename, err)
		}
		if count == 0 {
			return contents
		}
	}
}

func duplicateTopologyTestPair(filesFD, infoFD int) (mounts.TrashDescriptorPair, error) {
	pair := mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}
	var err error
	pair.FilesFD, err = unix.FcntlInt(uintptr(filesFD), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return pair, err
	}
	pair.InfoFD, err = unix.FcntlInt(uintptr(infoFD), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		_ = pair.Close()
		return mounts.TrashDescriptorPair{FilesFD: -1, InfoFD: -1}, err
	}
	return pair, nil
}

func duplicateTopologyTestSet(anchorFD, trashRootFD, filesFD, infoFD, sharedTopFD int) (mounts.TrashTopologyDescriptorSet, error) {
	set := mounts.EmptyTrashTopologyDescriptorSet()
	roles := []struct {
		name string
		fd   int
		out  *int
	}{
		{name: "anchor", fd: anchorFD, out: &set.AnchorFD},
		{name: "Trash root", fd: trashRootFD, out: &set.TrashRootFD},
		{name: "files", fd: filesFD, out: &set.FilesFD},
		{name: "info", fd: infoFD, out: &set.InfoFD},
	}
	if sharedTopFD >= 0 {
		roles = append(roles, struct {
			name string
			fd   int
			out  *int
		}{name: "shared top", fd: sharedTopFD, out: &set.SharedTopFD})
	}
	for _, role := range roles {
		duplicate, err := unix.FcntlInt(uintptr(role.fd), unix.F_DUPFD_CLOEXEC, 3)
		if err != nil {
			_ = set.Close()
			return mounts.EmptyTrashTopologyDescriptorSet(), fmt.Errorf("duplicate %s: %w", role.name, err)
		}
		*role.out = duplicate
	}
	return set, nil
}
