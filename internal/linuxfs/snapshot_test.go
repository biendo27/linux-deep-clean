//go:build linux

package linuxfs

import (
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

func TestRequiredStatMaskFailsClosed(t *testing.T) {
	baseline := domain.FilesystemFieldDevice |
		domain.FilesystemFieldInode |
		domain.FilesystemFieldType |
		domain.FilesystemFieldUID |
		domain.FilesystemFieldGID |
		domain.FilesystemFieldMode |
		domain.FilesystemFieldMountID
	destructive := baseline |
		domain.FilesystemFieldLinkCount |
		domain.FilesystemFieldSize |
		domain.FilesystemFieldModifiedAt |
		domain.FilesystemFieldChangedAt

	for _, test := range []struct {
		kind domain.ActionKind
		want domain.FilesystemFieldMask
	}{
		{kind: domain.ActionTrashPath, want: destructive},
		{kind: domain.ActionDeleteRecreatablePath, want: destructive},
		{kind: domain.ActionQuarantinePath, want: destructive},
		{kind: domain.ActionRestoreTrashPath, want: baseline},
		{kind: domain.ActionRestoreQuarantinePath, want: baseline},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			got, err := RequiredStatMask(test.kind)
			if err != nil {
				t.Fatalf("RequiredStatMask(%q) error = %v", test.kind, err)
			}
			if got != test.want {
				t.Fatalf("RequiredStatMask(%q) = %b, want %b", test.kind, got, test.want)
			}
		})
	}

	if _, err := RequiredStatMask(domain.ActionRepairState); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RequiredStatMask(non-filesystem action) error = %v, want ErrUnsupported", err)
	}
}

func TestSnapshotFromStatxFailsClosedWhenRequiredBitsAreMissing(t *testing.T) {
	var statx unix.Statx_t
	statx.Mask = unix.STATX_TYPE
	statx.Mode = unix.S_IFREG | 0o600

	_, err := snapshotFromStatx(statx, domain.FilesystemFieldInode|domain.FilesystemFieldMountID)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("snapshotFromStatx() error = %v, want ErrUnsupported", err)
	}
}

func TestSnapshotFDReadsRequiredFactsFromHeldDescriptor(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open temporary root: %v", err)
	}
	defer unix.Close(fd)

	required, err := RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	snapshot, err := SnapshotFD(fd, required)
	if err != nil {
		t.Fatalf("SnapshotFD() error = %v", err)
	}
	if err := snapshot.ValidateFor(required); err != nil {
		t.Fatalf("SnapshotFD() returned invalid snapshot: %v", err)
	}
	if snapshot.Type != domain.FileTypeDirectory {
		t.Fatalf("SnapshotFD() type = %q, want directory", snapshot.Type)
	}
}

func TestComparePreconditionRequiresEveryNamedIdentityFact(t *testing.T) {
	required, err := RequiredStatMask(domain.ActionRestoreTrashPath)
	if err != nil {
		t.Fatalf("RequiredStatMask() error = %v", err)
	}
	expected := testFilesystemPrecondition(t, required, testKnownSnapshot())

	observed := testKnownSnapshot()
	if err := ComparePrecondition(expected, observed); err != nil {
		t.Fatalf("ComparePrecondition() error = %v", err)
	}

	observed.Inode.Value++
	if err := ComparePrecondition(expected, observed); !errors.Is(err, ErrDrifted) {
		t.Fatalf("ComparePrecondition() with changed inode error = %v, want ErrDrifted", err)
	}

	observed = testKnownSnapshot()
	observed.MountID.Value = expected.Snapshot.MountID.Value
	observed.UID.Value++
	if err := ComparePrecondition(expected, observed); !errors.Is(err, ErrDrifted) {
		t.Fatalf("ComparePrecondition() with matching mount ID but changed UID error = %v, want ErrDrifted", err)
	}
}

func testFilesystemPrecondition(t *testing.T, required domain.FilesystemFieldMask, snapshot domain.FilesystemSnapshot) domain.FilesystemPrecondition {
	t.Helper()

	root, err := domain.NewTrustedRootID("linuxfs-unit-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	path, err := pathbytes.New([][]byte{[]byte("cache")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	return domain.FilesystemPrecondition{Target: target, Required: required, Snapshot: snapshot}
}

func testKnownSnapshot() domain.FilesystemSnapshot {
	return domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 8},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 1},
		Inode:       domain.Uint64Fact{Known: true, Value: 41},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true, Value: 1000},
		GID:         domain.Uint32Fact{Known: true, Value: 1000},
		Mode:        domain.Uint32Fact{Known: true, Value: 0o100600},
		LinkCount:   domain.Uint64Fact{Known: true, Value: 1},
		Size:        domain.Uint64Fact{Known: true, Value: 128},
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 1_700_000_000_000_000_000},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 1_700_000_000_000_000_000},
		MountID:     domain.Uint64Fact{Known: true, Value: 77},
	}
}
