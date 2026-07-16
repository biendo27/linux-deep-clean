//go:build linux

package trash

import (
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
)

// ValidateTrashLayout proves that an engine/helper-selected Trash lease has
// the literal Freedesktop parent/child relationships required for content
// operations. It never discovers a home, filesystem top, or user ID from a
// caller-controlled value; a lease without trusted topology evidence is
// unsupported.
func ValidateTrashLayout(lease *mounts.TrashLease) (*linuxfs.TrashDirectories, error) {
	return linuxfs.OpenTopologyQualifiedTrashDirectories(lease)
}
