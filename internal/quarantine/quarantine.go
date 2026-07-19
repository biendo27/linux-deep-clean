//go:build linux

// Package quarantine owns the narrow recovery-policy boundary for a
// per-mount private quarantine layout. It does not discover a location or
// expose a descriptor; the engine/helper must provide a qualified layout
// lease for the trusted root.
package quarantine

import (
	"fmt"
	"sync"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
)

// Store is a per-mount quarantine authority. Its only content operation is
// Retain, a bounded source-bound move whose result remains intentionally
// unavailable for later restore or reconciliation because the durable ledger
// has no quarantine-layout binding. It never exposes a path, descriptor, or
// generic mutator.
type Store struct {
	mu sync.Mutex

	// directory preserves the private lifecycle seam used while opening and
	// closing a qualified authority. privateLease is the concrete, opaque
	// LinuxFS bridge used only inside withRetain; it is populated for every
	// production Store and is never returned to callers.
	directory    privateDirectory
	privateLease *linuxfs.PrivateDirectoryLease
	rootID       domain.TrustedRootID
	retain       quarantineRetainer
}

// privateDirectory is the minimum qualified directory capability this package
// needs to hold. It deliberately has no mutation or descriptor operation.
// linuxfs.PrivateDirectoryLease is the production implementation; keeping the
// seam private lets this package exercise fail-closed ownership behavior.
type privateDirectory interface {
	RootID() domain.TrustedRootID
	Kind() mounts.LayoutKind
	Close() error
}

// OpenPerMountQuarantine accepts only an engine/helper-attested private
// quarantine layout. linuxfs requalifies the held layout descriptor before it
// becomes the opaque private-directory lease; callers cannot supply a path or
// borrow a raw descriptor through this package.
func OpenPerMountQuarantine(layout *mounts.LayoutLease) (*Store, error) {
	if layout == nil {
		return nil, fmt.Errorf("%w: trusted private quarantine layout is required", linuxfs.ErrUnsupported)
	}
	return openPerMountQuarantineWith(layout.Kind(), func() (privateDirectory, error) {
		return linuxfs.OpenPrivateDirectory(layout)
	})
}

func openPerMountQuarantineWith(kind mounts.LayoutKind, openDirectory func() (privateDirectory, error)) (*Store, error) {
	if kind != mounts.LayoutPrivateQuarantine {
		return nil, fmt.Errorf("%w: trusted private quarantine layout is required", linuxfs.ErrUnsupported)
	}
	if openDirectory == nil {
		return nil, fmt.Errorf("%w: trusted private quarantine opener is required", linuxfs.ErrUnsupported)
	}
	directory, err := openDirectory()
	if err != nil {
		if directory != nil {
			_ = directory.Close()
		}
		return nil, err
	}
	if directory == nil {
		return nil, fmt.Errorf("%w: private quarantine layout qualification returned no directory", linuxfs.ErrUnsupported)
	}
	if directory.Kind() != mounts.LayoutPrivateQuarantine {
		_ = directory.Close()
		return nil, fmt.Errorf("%w: private quarantine layout qualification changed", linuxfs.ErrDrifted)
	}
	rootID := directory.RootID()
	if err := rootID.Validate(); err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("%w: private quarantine root identity: %v", linuxfs.ErrUnsupported, err)
	}

	privateLease, _ := directory.(*linuxfs.PrivateDirectoryLease)
	return &Store{
		directory:    directory,
		privateLease: privateLease,
		rootID:       rootID,
		retain:       linuxfs.RetainQuarantineNoReplace,
	}, nil
}

// RootID returns the non-path identity of the trusted source root that
// selected this store. It cannot be used to derive a new layout authority.
func (store *Store) RootID() domain.TrustedRootID {
	if store == nil {
		return ""
	}
	return store.rootID
}

// Close revokes this store's future private-directory authority. It is
// idempotent and never mutates retained content.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	directory := store.directory
	store.directory = nil
	store.privateLease = nil
	store.retain = nil
	if directory == nil {
		return nil
	}
	return directory.Close()
}
