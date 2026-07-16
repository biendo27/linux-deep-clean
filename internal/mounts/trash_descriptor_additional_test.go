//go:build linux

package mounts

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNewTrashDescriptorsRejectsInvalidTransferredDescriptorsAndClosesOwnedInputs(t *testing.T) {
	trashRoot := openAdditionalTrashDirectory(t)
	info := openAdditionalTrashDirectory(t)

	descriptors, err := NewTrashDescriptors(trashRoot, -2, info, -1)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("NewTrashDescriptors() error = %v, want ErrInvalidAuthority", err)
	}
	if descriptors != emptyTrashDescriptors() {
		t.Fatalf("NewTrashDescriptors() descriptors = %+v, want released bundle", descriptors)
	}
	assertTrashFDClosed(t, "invalid-bundle Trash root", trashRoot)
	assertTrashFDClosed(t, "invalid-bundle Trash info", info)
}

func TestNewTrashDescriptorsRejectsAliasedOwnershipAndClosesEachDescriptorOnce(t *testing.T) {
	trashRoot := openAdditionalTrashDirectory(t)
	info := openAdditionalTrashDirectory(t)

	descriptors, err := NewTrashDescriptors(trashRoot, trashRoot, info, -1)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("NewTrashDescriptors() error = %v, want ErrInvalidAuthority", err)
	}
	if descriptors != emptyTrashDescriptors() {
		t.Fatalf("NewTrashDescriptors() descriptors = %+v, want released bundle", descriptors)
	}
	assertTrashFDClosed(t, "aliased Trash root", trashRoot)
	assertTrashFDClosed(t, "aliased Trash info", info)
}

func TestNewTrashDescriptorsCleansEarlierNormalizationOnLaterDescriptorFailure(t *testing.T) {
	trashRoot := openAdditionalTrashDirectory(t)
	info := openAdditionalTrashDirectory(t)
	const invalidFilesDescriptor = 1 << 30

	descriptors, err := NewTrashDescriptors(trashRoot, invalidFilesDescriptor, info, -1)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("NewTrashDescriptors() error = %v, want ErrInvalidAuthority", err)
	}
	if descriptors != emptyTrashDescriptors() {
		t.Fatalf("NewTrashDescriptors() descriptors = %+v, want released bundle", descriptors)
	}
	assertTrashFDClosed(t, "normalization-failure Trash root", trashRoot)
	assertTrashFDClosed(t, "normalization-failure Trash info", info)
}

func TestTrashLeaseOwnerUIDAndOpenWrapperFailClosed(t *testing.T) {
	var nilLease *TrashLease
	if got := nilLease.OwnerUID(); got != 0 {
		t.Fatalf("nil TrashLease.OwnerUID() = %d, want 0", got)
	}
	lease := &TrashLease{ownerUID: 4242}
	if got := lease.OwnerUID(); got != 4242 {
		t.Fatalf("TrashLease.OwnerUID() = %d, want 4242", got)
	}

	if _, err := OpenTrustedTrash(nil, nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("OpenTrustedTrash(nil, nil) error = %v, want ErrInvalidAuthority", err)
	}
}

func openAdditionalTrashDirectory(t *testing.T) int {
	t.Helper()
	fd, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open temporary Trash directory: %v", err)
	}
	if fd < 3 {
		t.Fatalf("open temporary Trash directory returned reserved descriptor %d", fd)
	}
	return fd
}
