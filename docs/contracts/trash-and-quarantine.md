# Trash and quarantine contract

Trash and quarantine are recovery policies layered over descriptor-rooted
Linux filesystem primitives. They do not import raw syscalls, reconstruct an
absolute path, or turn a plan caller UID, environment variable, or metadata
`Path=` field into filesystem authority.

## Authority prerequisites

A fully compliant Trash or quarantine operation needs an engine/helper-owned
per-mount layout authority. It supplies already-opened, validated, syncable
directory leases and trusted context for metadata encoding. A target root may
be a subdirectory, so it cannot safely walk through `..` to discover a
filesystem-top `.Trash*` directory. The plan's caller echo is audit data only,
not a trusted UID for `.Trash/$uid`.

Until an authority proves a private same-mount quarantine location or a valid
Trash layout, irreversible work for that root is unsupported. The safety layer
never falls back to permanent deletion, copy-delete, or an arbitrary home
directory.

## Intended Trash ordering

For a validated candidate, the ordered durable sequence is:

1. Verify the source precondition and reserve a bounded opaque token.
2. Create the owned `.trashinfo` file with no-replace/no-follow semantics;
   write and fsync the file, then fsync the `info` directory.
3. Rename the source to `files/<token>` using no-replace semantics.
4. Reopen and verify the destination identity with the post-rename stable
   mask, then fsync `files` and the source parent.
5. Return a Trash recovery handle with zero verified freed space.

The `files` and `info` entries are not an atomic pair. A collision never
overwrites either entry. Restore moves the file back no-replace and syncs both
changed directories before removing and syncing the info record. Metadata is
display/validation data only; the trusted recovery handle plus held destination
lease controls restoration.

## Reconciliation and retention

Unknown rename or fsync state requires a durable private intent/state record
and an indeterminate reconciliation probe. A generic scan of a user's Trash
cannot establish LDC ownership. Therefore malformed, file-only, collision, and
unknown orphan entries are retained and reported by default. Metadata-only
entries may be cleaned only when a durable LDC-owned intent proves ownership
and the corresponding cleanup is verified.

Quarantine follows the same no-replace move/verify/sync rules but uses a
separate authority-attested, private same-mount store. Retained content is
visible through a recovery handle, excluded from discovery by authority policy,
and never counted as freed. Retention cleanup may remove only owned, verified
entries through staged descriptor-walking removal; malformed or unknown entries
remain retained.
