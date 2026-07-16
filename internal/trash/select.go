//go:build linux

package trash

import (
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
)

// SelectTrashRoot consumes an engine/helper-selected Trash lease and proves
// its literal Freedesktop layout before returning descriptor-rooted
// directories. It does not search HOME, XDG data, a filesystem top, or any
// caller-provided path: a missing topology-qualified lease is unsupported.
func SelectTrashRoot(lease *mounts.TrashLease) (*linuxfs.TrashDirectories, error) {
	return ValidateTrashLayout(lease)
}
