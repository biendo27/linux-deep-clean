//go:build linux

package trash

import (
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
)

func TestSelectTrashRootRejectsMissingTrustedTopologyLease(t *testing.T) {
	directories, err := SelectTrashRoot(nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("SelectTrashRoot(nil) error = %v, want linuxfs.ErrUnsupported", err)
	}
	if directories != nil {
		defer directories.Close()
		t.Fatal("SelectTrashRoot(nil) returned descriptor authority")
	}
}

func TestValidateTrashLayoutRejectsMissingTrustedTopologyLease(t *testing.T) {
	directories, err := ValidateTrashLayout(nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("ValidateTrashLayout(nil) error = %v, want linuxfs.ErrUnsupported", err)
	}
	if directories != nil {
		defer directories.Close()
		t.Fatal("ValidateTrashLayout(nil) returned descriptor authority")
	}
}
