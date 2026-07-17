//go:build linux

package trash

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestWriteTrashInfoDurableSerializesTrustedMetadataThenPublishes(t *testing.T) {
	rootID, err := domain.NewTrustedRootID("trash-write-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}

	for _, test := range []struct {
		name      string
		basis     mounts.TrashMetadataBasis
		mapped    pathbytes.BytePath
		want      string
		deletedAt time.Time
	}{
		{
			name:   "home absolute raw bytes",
			basis:  mounts.TrashMetadataBasisHomeAbsolute,
			mapped: mustTrashPath(t, [][]byte{[]byte("home"), {0xff, '%', 'u', 's', 'e', 'r'}, []byte("cache")}),
			want:   "[Trash Info]\nPath=/home/%FF%25user/cache\nDeletionDate=2026-07-16T12:34:56\n",
			deletedAt: time.Date(2026, time.July, 16, 12, 34, 56, 0,
				time.FixedZone("filesystem", 7*60*60)),
		},
		{
			name:   "top relative raw bytes",
			basis:  mounts.TrashMetadataBasisTopRelative,
			mapped: mustTrashPath(t, [][]byte{[]byte("project"), []byte("space name"), {0xfe, '%'}}),
			want:   "[Trash Info]\nPath=project/space%20name/%FE%25\nDeletionDate=2026-07-16T01:02:03\n",
			deletedAt: time.Date(2026, time.July, 16, 1, 2, 3, 0,
				time.FixedZone("filesystem", -4*60*60)),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := mustTrashPath(t, [][]byte{[]byte("source"), {0xff, 'x'}})
			mappedCalls := 0
			authority := &writeTestAuthority{
				rootID: rootID,
				basis:  test.basis,
				mapPath: func(got pathbytes.BytePath) (pathbytes.BytePath, error) {
					mappedCalls++
					if !got.Equal(source) {
						t.Fatalf("MetadataPathFor() path = %x, want %x", got.Components(), source.Components())
					}
					return test.mapped, nil
				},
			}
			directories := &writeTestDirectories{publication: &linuxfs.TrashInfoPublication{}}
			selectorCalls := 0

			publication, err := writeTrashInfoDurableWith(
				context.Background(),
				authority,
				"ldc-0123456789abcdef",
				source,
				test.deletedAt,
				func() (trashInfoPublicationDirectory, error) {
					selectorCalls++
					return directories, nil
				},
			)
			if err != nil {
				t.Fatalf("writeTrashInfoDurableWith() error = %v", err)
			}
			if publication == nil {
				t.Fatal("writeTrashInfoDurableWith() returned no metadata receipt")
			}
			if publication.RootID() != rootID {
				t.Fatalf("publication.RootID() = %q, want %q", publication.RootID(), rootID)
			}
			if publication.Token() != "ldc-0123456789abcdef" {
				t.Fatalf("publication.Token() = %q, want LDC token", publication.Token())
			}
			if mappedCalls != 1 || selectorCalls != 1 || directories.publishCalls != 1 || directories.closeCalls != 1 {
				t.Fatalf("calls = map:%d select:%d publish:%d close:%d, want 1 each", mappedCalls, selectorCalls, directories.publishCalls, directories.closeCalls)
			}
			if directories.token != "ldc-0123456789abcdef" {
				t.Fatalf("published token = %q, want LDC token", directories.token)
			}
			if got := string(directories.contents); got != test.want {
				t.Fatalf("published metadata = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWriteTrashInfoDurableFailsBeforeSelectingForInvalidInputs(t *testing.T) {
	rootID, err := domain.NewTrustedRootID("trash-write-preflight-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	validPath := mustTrashPath(t, [][]byte{[]byte("source")})
	validTime := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)

	for _, test := range []struct {
		name      string
		ctx       context.Context
		authority trashMetadataAuthority
		token     string
		path      pathbytes.BytePath
		deletedAt time.Time
		want      error
	}{
		{
			name: "nil authority", ctx: context.Background(), token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime, want: linuxfs.ErrUnsupported,
		},
		{
			name: "invalid root", ctx: context.Background(), authority: &writeTestAuthority{basis: mounts.TrashMetadataBasisTopRelative, mapPath: identityWritePath}, token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime, want: linuxfs.ErrUnsupported,
		},
		{
			name: "unknown basis", ctx: context.Background(), authority: &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasis("future"), mapPath: identityWritePath}, token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime, want: linuxfs.ErrUnsupported,
		},
		{
			name: "mapping rejected", ctx: context.Background(), authority: &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasisTopRelative, mapPath: func(pathbytes.BytePath) (pathbytes.BytePath, error) {
				return pathbytes.BytePath{}, mounts.ErrLeaseClosed
			}}, token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime, want: linuxfs.ErrDrifted,
		},
		{
			name: "invalid token", ctx: context.Background(), authority: &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasisTopRelative, mapPath: identityWritePath}, token: "foreign", path: validPath, deletedAt: validTime, want: linuxfs.ErrUnsupported,
		},
		{
			name: "fractional deletion time", ctx: context.Background(), authority: &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasisTopRelative, mapPath: identityWritePath}, token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime.Add(time.Nanosecond), want: linuxfs.ErrUnsupported,
		},
		{
			name: "nil context", ctx: nil, authority: &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasisTopRelative, mapPath: identityWritePath}, token: "ldc-0123456789abcdef", path: validPath, deletedAt: validTime, want: linuxfs.ErrInterrupted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selectorCalls := 0
			publication, err := writeTrashInfoDurableWith(test.ctx, test.authority, test.token, test.path, test.deletedAt, func() (trashInfoPublicationDirectory, error) {
				selectorCalls++
				return &writeTestDirectories{publication: &linuxfs.TrashInfoPublication{}}, nil
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("writeTrashInfoDurableWith() error = %v, want %v", err, test.want)
			}
			if publication != nil {
				t.Fatal("invalid request returned a metadata receipt")
			}
			if selectorCalls != 0 {
				t.Fatalf("selector calls = %d, want 0 before invalid request rejection", selectorCalls)
			}
		})
	}
}

func TestWriteTrashInfoDurableRejectsCanceledContextBeforeMappingOrSelection(t *testing.T) {
	rootID, err := domain.NewTrustedRootID("trash-write-canceled-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mappedCalls := 0
	selectorCalls := 0

	publication, err := writeTrashInfoDurableWith(
		ctx,
		&writeTestAuthority{
			rootID: rootID,
			basis:  mounts.TrashMetadataBasisTopRelative,
			mapPath: func(pathbytes.BytePath) (pathbytes.BytePath, error) {
				mappedCalls++
				return pathbytes.BytePath{}, nil
			},
		},
		"ldc-0123456789abcdef",
		mustTrashPath(t, [][]byte{[]byte("source")}),
		time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC),
		func() (trashInfoPublicationDirectory, error) {
			selectorCalls++
			return nil, nil
		},
	)
	if !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("writeTrashInfoDurableWith(canceled context) error = %v, want linuxfs.ErrInterrupted", err)
	}
	if publication != nil {
		t.Fatal("canceled context returned a metadata receipt")
	}
	if mappedCalls != 0 || selectorCalls != 0 {
		t.Fatalf("calls after canceled context = map:%d select:%d, want 0 each", mappedCalls, selectorCalls)
	}
}

func TestWriteTrashInfoDurablePropagatesSelectionAndPublicationFailureWithoutCleanup(t *testing.T) {
	rootID, err := domain.NewTrustedRootID("trash-write-errors-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	authority := &writeTestAuthority{
		rootID:  rootID,
		basis:   mounts.TrashMetadataBasisTopRelative,
		mapPath: identityWritePath,
	}
	path := mustTrashPath(t, [][]byte{[]byte("source")})
	deletedAt := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)

	selectionFailure := errors.New("layout drift")
	if publication, err := writeTrashInfoDurableWith(context.Background(), authority, "ldc-0123456789abcdef", path, deletedAt, func() (trashInfoPublicationDirectory, error) {
		return nil, selectionFailure
	}); !errors.Is(err, selectionFailure) || publication != nil {
		t.Fatalf("selection failure = (%v, %v), want nil + propagated error", publication, err)
	}

	publishFailure := errors.New("metadata created but sync interrupted")
	directories := &writeTestDirectories{publishErr: publishFailure}
	if publication, err := writeTrashInfoDurableWith(context.Background(), authority, "ldc-0123456789abcdef", path, deletedAt, func() (trashInfoPublicationDirectory, error) {
		return directories, nil
	}); !errors.Is(err, publishFailure) || publication != nil {
		t.Fatalf("publication failure = (%v, %v), want nil + propagated error", publication, err)
	}
	if directories.closeCalls != 1 {
		t.Fatalf("directories close calls = %d, want 1 after failed publication", directories.closeCalls)
	}

	missingDirectories, err := writeTrashInfoDurableWith(context.Background(), authority, "ldc-0123456789abcdef", path, deletedAt, func() (trashInfoPublicationDirectory, error) {
		return nil, nil
	})
	if !errors.Is(err, linuxfs.ErrUnsupported) || missingDirectories != nil {
		t.Fatalf("missing directories = (%v, %v), want nil + linuxfs.ErrUnsupported", missingDirectories, err)
	}

	missingReceipt := &writeTestDirectories{}
	publication, err := writeTrashInfoDurableWith(context.Background(), authority, "ldc-0123456789abcdef", path, deletedAt, func() (trashInfoPublicationDirectory, error) {
		return missingReceipt, nil
	})
	if !errors.Is(err, linuxfs.ErrInterrupted) || publication != nil {
		t.Fatalf("missing publication receipt = (%v, %v), want nil + linuxfs.ErrInterrupted", publication, err)
	}
	if missingReceipt.closeCalls != 1 {
		t.Fatalf("missing receipt directory close calls = %d, want 1", missingReceipt.closeCalls)
	}
}

func TestWriteTrashInfoDurableRejectsNilTrustedLease(t *testing.T) {
	publication, err := WriteTrashInfoDurable(
		context.Background(),
		nil,
		"ldc-0123456789abcdef",
		mustTrashPath(t, [][]byte{[]byte("source")}),
		time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC),
	)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("WriteTrashInfoDurable(nil) error = %v, want linuxfs.ErrUnsupported", err)
	}
	if publication != nil {
		t.Fatal("WriteTrashInfoDurable(nil) returned a metadata receipt")
	}
}

func TestWriteTrashInfoDurablePrivateSelectorAndReceiptHelpersFailClosed(t *testing.T) {
	path := mustTrashPath(t, [][]byte{[]byte("source")})
	deletedAt := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)
	rootID, err := domain.NewTrustedRootID("trash-write-helper-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	authority := &writeTestAuthority{rootID: rootID, basis: mounts.TrashMetadataBasisTopRelative, mapPath: identityWritePath}

	publication, err := writeTrashInfoDurableWith(context.Background(), authority, "ldc-0123456789abcdef", path, deletedAt, nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) || publication != nil {
		t.Fatalf("writeTrashInfoDurableWith(nil selector) = (%v, %v), want nil + linuxfs.ErrUnsupported", publication, err)
	}

	for _, selected := range []*selectedTrashInfoDirectories{nil, {}} {
		infoReceipt, err := selected.publish(context.Background(), "ldc-0123456789abcdef", []byte("contents"))
		if !errors.Is(err, linuxfs.ErrUnsupported) || infoReceipt != nil {
			t.Fatalf("selected.publish(%#v) = (%v, %v), want nil + linuxfs.ErrUnsupported", selected, infoReceipt, err)
		}
		if err := selected.close(); err != nil {
			t.Fatalf("selected.close(%#v) error = %v", selected, err)
		}
	}

	selected := &selectedTrashInfoDirectories{directories: &linuxfs.TrashDirectories{}}
	infoReceipt, err := selected.publish(context.Background(), "ldc-0123456789abcdef", []byte("contents"))
	if !errors.Is(err, linuxfs.ErrUnsupported) || infoReceipt != nil {
		t.Fatalf("selected.publish(empty directories) = (%v, %v), want nil + linuxfs.ErrUnsupported", infoReceipt, err)
	}
	if err := selected.close(); err != nil {
		t.Fatalf("selected.close(empty directories) error = %v", err)
	}
}

func TestMetadataPublicationNilAccessorsAndMetadataAuthorityFailureClassification(t *testing.T) {
	var publication *MetadataPublication
	if publication.RootID() != "" {
		t.Fatalf("nil MetadataPublication.RootID() = %q, want empty", publication.RootID())
	}
	if publication.Token() != "" {
		t.Fatalf("nil MetadataPublication.Token() = %q, want empty", publication.Token())
	}

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "lease closed", err: mounts.ErrLeaseClosed, want: linuxfs.ErrDrifted},
		{name: "mount drift", err: mounts.ErrDrifted, want: linuxfs.ErrDrifted},
		{name: "unsupported", err: mounts.ErrUnsupported, want: linuxfs.ErrUnsupported},
		{name: "invalid authority", err: mounts.ErrInvalidAuthority, want: linuxfs.ErrUnsupported},
		{name: "unknown trash", err: mounts.ErrUnknownTrash, want: linuxfs.ErrUnsupported},
		{name: "unclassified mapping failure", err: errors.New("mapping I/O interrupted"), want: linuxfs.ErrDrifted},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := classifyTrashMetadataAuthorityFailure(test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyTrashMetadataAuthorityFailure(%v) = %v, want %v", test.err, err, test.want)
			}
		})
	}
}

type writeTestAuthority struct {
	rootID  domain.TrustedRootID
	basis   mounts.TrashMetadataBasis
	mapPath func(pathbytes.BytePath) (pathbytes.BytePath, error)
}

func (authority *writeTestAuthority) RootID() domain.TrustedRootID {
	if authority == nil {
		return ""
	}
	return authority.rootID
}

func (authority *writeTestAuthority) MetadataBasis() mounts.TrashMetadataBasis {
	if authority == nil {
		return ""
	}
	return authority.basis
}

func (authority *writeTestAuthority) MetadataPathFor(path pathbytes.BytePath) (pathbytes.BytePath, error) {
	if authority == nil || authority.mapPath == nil {
		return pathbytes.BytePath{}, mounts.ErrLeaseClosed
	}
	return authority.mapPath(path)
}

type writeTestDirectories struct {
	publishCalls int
	closeCalls   int
	token        string
	contents     []byte
	publication  *linuxfs.TrashInfoPublication
	publishErr   error
}

func (directories *writeTestDirectories) publish(ctx context.Context, token string, contents []byte) (*linuxfs.TrashInfoPublication, error) {
	directories.publishCalls++
	directories.token = token
	directories.contents = append([]byte(nil), contents...)
	return directories.publication, directories.publishErr
}

func (directories *writeTestDirectories) close() error {
	directories.closeCalls++
	return nil
}

func identityWritePath(path pathbytes.BytePath) (pathbytes.BytePath, error) {
	return path, nil
}
