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

The authority maps each trusted source root plus one fixed layout kind to one
opaque directory lease. That mapping is the recovery lookup for a handle's
root and kind; it must remain stable for retained entries. Changing or losing
the mapping makes recovery drift/unsupported rather than allowing a scan or a
fallback path. The mapping must additionally supply the FDO metadata path
basis (home-absolute versus top-directory-relative) without exposing it as
apply-time path authority.

Until an authority proves a private same-mount quarantine location or a valid
Trash layout, irreversible work for that root is unsupported. The safety layer
never falls back to permanent deletion, copy-delete, or an arbitrary home
directory.

## Current metadata and descriptor boundary

`internal/trash` supplies a bounded parser and serializer for an LDC-owned
`.trashinfo` profile. It preserves raw filename bytes with canonical percent
encoding, treats `DeletionDate` as the Freedesktop local wall-clock value, and
accepts required keys only from the initial `[Trash Info]` group. Its parsed
`Path=` is metadata only: it cannot be resolved, restored, or used to choose a
source or destination.

`mounts.TrashAuthority` and `OpenTrustedTrash` add a descriptor-rooted
pre-selector boundary. An engine/helper opener transfers a fixed Trash-root,
`files`, `info`, and (when applicable) shared-parent descriptor bundle through
`NewTrashDescriptors`; that constructor normalizes opener-owned descriptors
away from 0, 1, and 2 before the bundle becomes authority. The lease
requalifies descriptor identities, ownership, modes, and mount binding before
a legacy pre-selector lease lends a duplicated `files`/`info` pair to
`linuxfs`.

Operations that need a literal Freedesktop layout require additional trusted
topology evidence. The engine/helper must supply a `TrashTopology` anchor and
transfer that anchor through `NewTrashTopologyDescriptors`; a legacy bundle
cannot carry an unbound anchor. A topology-qualified lease may lend the full
anchor/root/`files`/`info`/(shared-parent) descriptor set only to `linuxfs`.
`trash.ValidateTrashLayout` and
`trash.SelectTrashRoot` then require `linuxfs` to reopen fixed literal child
names with all required `openat2` constraints and compare the resulting
descriptor identities:

- a home-data anchor must contain `Trash`;
- a filesystem-top anchor must contain `.Trash-$uid` for a top-user layout;
- a filesystem-top anchor must contain sticky `.Trash`, whose literal `$uid`
  child is the top-shared layout; and
- every selected Trash root must contain literal `files` and `info` children.

The proof checks same-mount binding, distinct directory identities, private
owner/mode requirements for the user root and its `files`/`info` children, and
the shared parent's sticky bit. It is repeated whenever a qualified pair is
borrowed and again after metadata publication. It never discovers a home,
filesystem top, UID, or path from caller input. Missing or drifted topology is
unsupported/drifted rather than inferred or repaired. `linuxfs.OpenTrashDirectories`
remains the deliberately weaker metadata-only pre-selector API and must not
be used for a content move or restoration.

`linuxfs.PublishTrashInfoDurable` can publish one metadata record beneath a
requalified `info` descriptor. Publication requires a bounded `ldc-` plus
lowercase-hex token profile, uses no-follow and no-replace creation, writes and
syncs the record, syncs the directory, and reopens the name to verify its
identity and bytes. The token check prevents a foreign or arbitrary record
name at this boundary; it is not token generation, reservation, pair ownership,
or a durable intent record. Once creation may have succeeded, an error retains
the record for later reconciliation rather than attempting unsafe cleanup.

`trash.WriteTrashInfoDurable` is the metadata-only composition boundary. It
accepts a topology-qualified `mounts.TrashLease`, asks that lease to map a
source-relative byte path to its fixed metadata basis, reselects the literal
Trash topology, serializes the record, and delegates durable publication to
`linuxfs`. The mapped path is lexical metadata only: this operation does not
resolve, open, or prove a source exists. It must therefore be preceded by a
future source-bound durable intent and token reservation before any content
move can use it.

`quarantine.OpenPerMountQuarantine` is similarly an open-only boundary. It
accepts only a `LayoutPrivateQuarantine` lease and returns an opaque store with
the trusted root identity and idempotent close. It exposes neither a pathname
nor a descriptor and cannot retain, restore, scan, remove, or otherwise mutate
content.

No current API moves source content into `files`, restores content, removes
metadata, reconciles an orphan, or proves a metadata/content pair. Those
operations remain blocked on one-shot source-bound token reservation, durable
intent/reconciliation records, and descriptor-rooted no-replace move, retain,
and restore primitives.

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

Neither a Trash nor a same-UID private quarantine layout is exclusive removal
authority. They support move-and-retain only; they cannot enable permanent
`unlinkat` removal because another process with the same UID can replace a
classified name.
