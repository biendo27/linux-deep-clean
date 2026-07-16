# Filesystem safety contract

The filesystem safety layer is descriptor-rooted. It is a foundation for later
executors, not a general path-mutation API and not an authority grant. No
provider, presentation package, plan field, or request may supply an absolute
apply path.

## Supported boundary

Filesystem mutation is supported only when an engine/helper-owned registry can
reopen a typed trusted root on Linux 5.15 or newer and qualify its live mount.
The registry opener returns a newly owned
`O_RDONLY|O_DIRECTORY|O_CLOEXEC` descriptor; `O_PATH`, write-capable,
non-directory, or non-CLOEXEC descriptors reject. The registry records the
complete mountinfo evidence (mount ID, parent ID, device, root, mount point,
filesystem, and source), then rechecks descriptor and mount-namespace evidence
on both sides of reading the current mount table. It also requires explicit
fixed-local-device and bind-free provenance attestations from its trusted
owner; both default to false.

The held root descriptor is the only source of descendant authority. A mount
ID is drift evidence only: it never authorizes a new root by itself. Mountinfo
can reject a subtree mount origin and an ambiguous second current full-root
view of the same device/filesystem/root. Linux does not expose a reliable flag
that distinguishes every full-filesystem-root bind from an independent mount
of the same filesystem, however, so topology checks never replace the trusted
bind-free attestation. Descendant `RESOLVE_NO_XDEV`, retained root evidence,
and requalification are required in addition to mountinfo checks.

An unqualified root, unsupported kernel/filesystem/layout, missing fixed-local
attestation, or lost evidence is unsupported or drifted. There is no
`realpath`, prefix, copy-delete, or string-path fallback.

## Descriptor algorithm

`ResolveParent` accepts a trusted-root lease and a validated relative
`BytePath`. It resolves every intermediate component with exactly:

```text
RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS | RESOLVE_NO_XDEV
```

It retains a read-only, syncable parent-directory descriptor and one validated
final basename. `openat2` retries `EAGAIN` at most three times; exhaustion is
drift. A final target is opened under the same flags and must be a regular file
or directory. Symlinks, devices, sockets, FIFOs, and other special files
reject before generic cleanup can act.

The resolved lease records the trusted root ID and the exact planned relative
path. A staging request must bind that recorded path and root ID to the typed
filesystem precondition, preventing a same-root caller from substituting a
different parent or hard-link name.

`renameat2` and `unlinkat` receive held directory descriptors and one basename
only. They never receive a multi-component plan path.

## Identity and staging

`SnapshotFD` reads `statx` facts using `AT_EMPTY_PATH`, rejects missing mask
bits, and compares only the action's named mask. The baseline is device,
inode, type, UID/GID, mode, and mount ID. Destructive path actions additionally
require link count, size, modification time, and change time; restore actions
use the baseline. Atime is never an authorization field.

Before a destructive move, all requested fields are compared. A successful
rename changes inode ctime, so post-rename identity verification compares the
same requested mask except `ChangedAt`. This exception is limited to the
post-move verification step; ctime remains a pre-move freshness check.

`StageNoReplace` requires an opaque, qualified private same-mount staging
lease, not merely another parent directory lease, and uses `RENAME_NOREPLACE`.
The current foundation accepts only a stable, executor-owned exact-`0700`
directory that is bound to the same trusted root. Its constructor remains
internal until an engine/helper-owned layout registry can provide that
authority. Staging reopens the token and compares post-move identity. If
verification fails, the staged object is retained and reports both drift and
retention; it exposes no generic delete capability. `RestoreNoReplace` first
revalidates the staged object and never overwrites an occupied original name.
Unknown rename/restore outcomes are interrupted and require reconciliation,
not a blind retry.

Directory descriptors used for durability are syncable. A required directory
sync failure means durable completion is not claimed; unsupported descriptor or
filesystem behavior rejects, while an uncertain sync result is interrupted.
The current private staging lease does not authorize irreversible recursive
removal: exact `0700` does not exclude a malicious process with the same UID,
and Linux `unlinkat` cannot atomically bind a name to a just-classified FD. In
the absence of a future engine/helper authority that proves an exclusive staged
namespace, removal returns `unsupported` and retains the staged object.

## Threat boundary and qualification

The layer prevents traversal outside held descriptors and rejects cross-mount
resolution. It does not make a malicious same-UID actor trustworthy: that
actor can disrupt caller-owned staging. The safe response is drift, retention,
or reconciliation; never deletion of uncertain content.

The default suite exercises only test-owned temporary roots. It does not mount
filesystems or perform privileged work. Real ext4/XFS/Btrfs race and mount
qualification remain VM-gated; no local default-lane result certifies a root
for production mutation.
