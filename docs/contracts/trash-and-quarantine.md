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
resolve, open, or prove a source exists. Phase 4A now supplies its durable
prerequisite through `state.RecoveryLedger`: over an already qualified private
state lease it binds a validated Trash/quarantine action, plan digest, exact
root/source/precondition, ledger-generated opaque token, closed destination,
and pre-/post-effect facts in immutable records. The ledger is not a layout,
source, or content authority. Phase 3B must consume it before a content move;
the metadata-only API cannot do so by itself.

`recoveryport.Ledger` is the data-only Phase 4A bridge used by content policy.
It reserves the opaque LDC token and persists the closed pre-/post-effect fact
graph, but exposes no descriptor, layout, or content-mutation authority.
`state.RecoveryLedgerPort` is its production adapter.

`trash.MoveToTrash` is the current high-level content composition boundary. It
accepts an already-resolved source parent, a topology-qualified Trash lease,
and a validated immutable Trash action; it verifies that the held parent was
resolved for the exact planned target before reserving the ledger token or
publishing metadata. It then records metadata and move dispatch facts before
their respective effects, binds the durable metadata receipt to the move, and
records a verified or indeterminate outcome. A known no-replace collision
retains metadata and durable history rather than deleting either entry. It
never copies, overwrites, or reports freed space.

`linuxfs.MovePublishedTrashNoReplace` is the descriptor-rooted lower-level
move primitive consumed by that facade. It requires a topology-qualified
directory pair and the exact metadata receipt, proves the source identity and
rejects protected Trash structure, then uses `RENAME_NOREPLACE`, post-move
identity verification, directory sync, and topology reproof.
`linuxfs.RestoreTrashNoReplace` is likewise a descriptor-rooted no-replace
primitive for a known ticket token and original source parent. It verifies the
retained content before restoring it and verifies and syncs a successful
restore, but deliberately retains the `.trashinfo` record. It is not a
ledger-closing recovery operation: current same-UID Trash authority cannot
safely unlink a name after classifying it.

`quarantine.OpenPerMountQuarantine` accepts only a
`LayoutPrivateQuarantine` lease and returns an opaque store with the trusted
root identity and idempotent close. Its sole content operation is the bounded
`quarantine.Retain` composition: it validates an exact already-resolved
`quarantine_path` action and source, reserves a zero-Trash-binding ledger
ticket, persists move dispatch, and invokes
`linuxfs.RetainQuarantineNoReplace`. That primitive requalifies both
descriptor authorities, uses same-mount `RENAME_NOREPLACE`, proves the
post-move identity, and syncs the destination then source-parent directories.

After a dispatch reaches the filesystem effect, the only resulting ledger
paths are `intent_reserved -> move_dispatch_recorded -> move_verified` and
`intent_reserved -> move_dispatch_recorded -> move_indeterminate`. A known
post-dispatch no-effect result also remains `move_indeterminate`: this narrow
operation does not perform reconciliation. An interrupted ledger append
returns the last candidate or prior ticket without retrying the effect. The
store exposes neither a
pathname nor a descriptor and has no restore, scan, removal, generic mutation,
or cleanup operation. It issues neither a `RecoveryHandle` nor an
`ActionResult`.

There is no high-level Trash restoration, generic/orphan reconciliation API,
descriptor-rooted orphan probe, Quarantine restore/reconciliation, or
`domain.RecoveryHandle`/`domain.ActionResult` composition. A durable ledger
record alone never authorizes or performs a content operation; the bounded
Quarantine retain path also needs the supplied live, qualified store. The
narrow read-only Trash move reconciliation described below is not restoration,
cleanup, or a generic recovery scan.

## Required eventual Trash ordering

The sequence below is the completion contract, not a claim about the current
low-level restore primitive. Its final metadata disposition remains blocked on
an authority that can prove safe removal or retention without a same-UID
replacement race.

For a validated candidate, the ordered durable sequence is:

1. Verify the source precondition and reserve a bounded opaque token through
   the durable recovery ledger.
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
and an indeterminate reconciliation probe. Phase 4A implements the private
record and closed fact graph. The narrow
`trash.ReconcileIndeterminateTrashMetadata` path first reloads and validates a
current v2 durable `EventMetadataIndeterminate` ticket before requiring an
exact match between its opaque immutable layout/mapping binding and the
supplied topology-qualified Trash lease. Only then does it map metadata,
reload again before selecting descriptors, and read-only probe the exact
ticket-owned `<token>.trashinfo`. Readable legacy v1 tickets lack that binding
and remain unsupported and outstanding. It may close a v2 ticket only as
`EventReconciliationResolved` with
`OutcomeNotApplied` and `MetadataAbsent` or `MetadataRetained`; malformed,
mismatched, or uncertain metadata leaves the ticket outstanding.

`trash.ReconcileIndeterminateTrashMove` is a separate positive-only path for
one current v2 `EventMoveIndeterminate` ticket. It requires the same exact
layout binding plus a held source parent that matches the ticket's immutable
source precondition. After a first exact owned `<token>.trashinfo` probe, it
requires a stable two-lookup absence proof for the original source basename
beneath a freshly requalified held-parent identity, proves exact post-move
identity of `files/<token>`, requires source absence a second time, and
requires byte-identical metadata on its final exact probe.
Only then can it append `EventReconciliationResolved` with
`OutcomeMoveVerified`; that fact deliberately remains open for a later restore
boundary. Source presence, content absence, malformed or changed metadata,
identity/layout drift, and every uncertain probe leave the ticket outstanding.
The checks are point-in-time and cannot create an atomic pair against a
malicious same-UID actor, so they never authorize cleanup or deletion.

Neither reconciler has scan, cleanup, deletion, restoration, rename, or other
content-operation authority. A generic scan of a user's Trash cannot establish
LDC ownership. Therefore malformed, file-only, collision, and unknown orphan
entries are retained and reported by default. Metadata-only entries may be
cleaned only when a durable LDC-owned intent proves ownership and the
corresponding cleanup is verified.

The implemented Quarantine retain path follows the same no-replace
move/verify/sync rules but uses a separate authority-attested, private
same-mount store. It records only the move fact and never reports freed space.
Because Quarantine tickets have no durable Quarantine-layout binding, no
post-crash restore or exact-token reconciliation may select a reopened layout
from those tickets. Retention cleanup, scans, deletion, recovery handles, and
result composition remain unimplemented; malformed, unknown, and ambiguous
entries remain retained.

Neither a Trash nor a same-UID private quarantine layout is exclusive removal
authority. They support move-and-retain only; they cannot enable permanent
`unlinkat` removal because another process with the same UID can replace a
classified name.
