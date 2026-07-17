//go:build linux

package trash

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

// MetadataPublication is an opaque receipt for one durably verified LDC-owned
// Trash metadata record. It carries no pathname, descriptor, recovery handle,
// or content-move authority. A future move primitive must bind to this receipt
// plus a durable source-bound intent rather than accepting a token alone.
type MetadataPublication struct {
	rootID      domain.TrustedRootID
	token       string
	publication *linuxfs.TrashInfoPublication
}

// RootID reports the trusted root that selected the published metadata. It is
// data only and cannot reopen a directory or restore content.
func (publication *MetadataPublication) RootID() domain.TrustedRootID {
	if publication == nil {
		return ""
	}
	return publication.rootID
}

// Token reports the already-validated LDC-owned metadata token. It is not a
// reservation for a corresponding files entry and cannot select any path.
func (publication *MetadataPublication) Token() string {
	if publication == nil {
		return ""
	}
	return publication.token
}

// WriteTrashInfoDurable serializes a lease-mapped lexical metadata path and
// creates its LDC-owned .trashinfo record beneath a literal,
// topology-qualified Trash layout. It does not resolve, open, or prove the
// sourceRelative path; a future move needs a source-bound durable intent for
// that. This operation also does not reserve a token, move source content,
// emit a recovery handle, remove metadata, or reconcile an orphan. Any error
// after record creation is reported by linuxfs and intentionally retains the
// record rather than attempting cleanup.
func WriteTrashInfoDurable(
	ctx context.Context,
	lease *mounts.TrashLease,
	token string,
	sourceRelative pathbytes.BytePath,
	deletedAt time.Time,
) (*MetadataPublication, error) {
	if lease == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash lease is required", linuxfs.ErrUnsupported)
	}
	return writeTrashInfoDurableWith(ctx, lease, token, sourceRelative, deletedAt, func() (trashInfoPublicationDirectory, error) {
		directories, err := SelectTrashRoot(lease)
		if err != nil {
			return nil, err
		}
		return &selectedTrashInfoDirectories{directories: directories}, nil
	})
}

// trashMetadataAuthority exposes only the trusted metadata facts required by
// this façade. The production entry point requires *mounts.TrashLease; this
// private interface exists solely to keep pre-publication behavior testable
// without creating a second public authority path.
type trashMetadataAuthority interface {
	RootID() domain.TrustedRootID
	MetadataBasis() mounts.TrashMetadataBasis
	MetadataPathFor(pathbytes.BytePath) (pathbytes.BytePath, error)
}

// trashInfoPublicationDirectory is the narrow descriptor-rooted operation
// needed after a literal topology has been selected. It intentionally exposes
// no files-directory operation, move, restore, deletion, or raw descriptor.
type trashInfoPublicationDirectory interface {
	publish(context.Context, string, []byte) (*linuxfs.TrashInfoPublication, error)
	close() error
}

type selectedTrashInfoDirectories struct {
	directories *linuxfs.TrashDirectories
}

func (selected *selectedTrashInfoDirectories) publish(ctx context.Context, token string, contents []byte) (*linuxfs.TrashInfoPublication, error) {
	if selected == nil || selected.directories == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash directories are required", linuxfs.ErrUnsupported)
	}
	return linuxfs.PublishTrashInfoDurable(ctx, selected.directories, token, contents)
}

func (selected *selectedTrashInfoDirectories) close() error {
	if selected == nil || selected.directories == nil {
		return nil
	}
	return selected.directories.Close()
}

func writeTrashInfoDurableWith(
	ctx context.Context,
	authority trashMetadataAuthority,
	token string,
	sourceRelative pathbytes.BytePath,
	deletedAt time.Time,
	openDirectories func() (trashInfoPublicationDirectory, error),
) (*MetadataPublication, error) {
	if err := writeTrashInfoContextError(ctx); err != nil {
		return nil, err
	}
	if authority == nil {
		return nil, fmt.Errorf("%w: trusted Trash metadata authority is required", linuxfs.ErrUnsupported)
	}
	if openDirectories == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash selector is required", linuxfs.ErrUnsupported)
	}
	rootID := authority.RootID()
	if err := rootID.Validate(); err != nil {
		return nil, fmt.Errorf("%w: trusted Trash root identity: %v", linuxfs.ErrUnsupported, err)
	}

	metadataPath, err := authority.MetadataPathFor(sourceRelative)
	if err != nil {
		return nil, classifyTrashMetadataAuthorityFailure(err)
	}
	basis, err := trashPathBasisFromMounts(authority.MetadataBasis())
	if err != nil {
		return nil, err
	}
	info, err := newTrashInfo(token, basis, metadataPath, deletedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: serialize trusted Trash metadata: %v", linuxfs.ErrUnsupported, err)
	}
	contents, err := info.marshal()
	if err != nil {
		return nil, fmt.Errorf("%w: serialize trusted Trash metadata: %v", linuxfs.ErrUnsupported, err)
	}

	directories, err := openDirectories()
	if err != nil {
		return nil, err
	}
	if directories == nil {
		return nil, fmt.Errorf("%w: topology-qualified Trash selector returned no directories", linuxfs.ErrUnsupported)
	}
	defer directories.close()

	receipt, err := directories.publish(ctx, token, contents)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, fmt.Errorf("%w: metadata publication returned no durable receipt", linuxfs.ErrInterrupted)
	}
	return &MetadataPublication{rootID: rootID, token: token, publication: receipt}, nil
}

func trashPathBasisFromMounts(basis mounts.TrashMetadataBasis) (trashPathBasis, error) {
	switch basis {
	case mounts.TrashMetadataBasisHomeAbsolute:
		return trashPathBasisHomeAbsolute, nil
	case mounts.TrashMetadataBasisTopRelative:
		return trashPathBasisTopDirectoryRelative, nil
	default:
		return 0, fmt.Errorf("%w: unknown trusted Trash metadata basis %q", linuxfs.ErrUnsupported, basis)
	}
}

func classifyTrashMetadataAuthorityFailure(err error) error {
	switch {
	case errors.Is(err, mounts.ErrLeaseClosed), errors.Is(err, mounts.ErrDrifted):
		return fmt.Errorf("%w: trusted Trash metadata authority changed: %v", linuxfs.ErrDrifted, err)
	case errors.Is(err, mounts.ErrUnsupported), errors.Is(err, mounts.ErrInvalidAuthority), errors.Is(err, mounts.ErrUnknownTrash):
		return fmt.Errorf("%w: trusted Trash metadata authority is unavailable: %v", linuxfs.ErrUnsupported, err)
	default:
		return fmt.Errorf("%w: trusted Trash metadata authority could not map source metadata: %v", linuxfs.ErrDrifted, err)
	}
}

func writeTrashInfoContextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", linuxfs.ErrInterrupted)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", linuxfs.ErrInterrupted, err)
	}
	return nil
}
