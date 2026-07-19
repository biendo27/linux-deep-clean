---
phase: "3B"
title: "Linux Filesystem Content Operations"
status: in-progress
priority: P1
effort: "remaining subset of Phase 3"
dependencies: ["3A", "4A"]
---

# Phase 3B: Linux Filesystem Content Operations

This formal subphase owns every remaining content operation from the composite
[Phase 3 execution document](./phase-03-linux-filesystem-safety-and-trash.md):
`ReserveTrashToken`, Trash/quarantine move and no-replace restore, retention,
and orphan/recovery reconciliation. It consumes only the opaque Phase 4A
ledger port and the existing descriptor-rooted safety APIs.

## Non-negotiable prerequisites

- An engine/helper-owned, requalified per-mount layout authority proves the
  fixed Trash/quarantine/staging topology. No source path, environment, or
  discovery scan is authority.
- Before every effect, persist the legal Phase 4A dispatch event; after it,
  persist a verified or indeterminate event. A ledger record never authorizes
  a cross-mount copy-delete fallback, a permanent deletion, or a weaker path
  operation.
- Run the existing disposable-VM guard and Phase 3 adversarial/ext4 smoke
  gates before claiming the content-operation exit. Phase 4A does not supply
  those proofs.

## Incremental progress

- [x] Metadata-only reconciliation for one current v2 Trash
  `metadata_indeterminate` ticket: its immutable authority-selected
  layout/mapping binding must match the supplied topology-qualified lease
  before an exact, read-only owned `<token>.trashinfo` probe records only
  absent or retained metadata as a closed not-applied ledger fact. Readable
  unbound v1 histories remain outstanding. This path does not scan, move,
  restore, delete, or clean up.
- [x] Positive-only reconciliation for one current v2 Trash
  `move_indeterminate` ticket: the immutable layout/mapping binding must match
  before any mapping or probe; exact owned metadata must bracket stable,
  freshly requalified descriptor-relative source absence and exact
  `files/<token>` post-move
  identity. Only that evidence appends an open `move_verified` fact. All other
  observations remain outstanding, and this path does not scan, move, restore,
  delete, or clean up.
- [x] Bounded live Quarantine retain: `linuxfs.RetainQuarantineNoReplace`
  requalifies an exact source parent and `LayoutPrivateQuarantine` authority,
  then uses same-mount no-replace rename, post-move identity verification, and
  directory sync. `quarantine.Retain` records only
  `intent_reserved -> move_dispatch_recorded -> move_verified` or
  `move_indeterminate`; every non-verified result stays outstanding. It does
  not scan, restore, reconcile, delete, clean up, or issue a recovery handle.
- [ ] Broad orphan/recovery reconciliation, high-level restore, Quarantine
  restore/reconciliation, retention removal, result composition, and the
  disposable-VM/adversarial exit gates remain pending. Quarantine has no
  durable layout binding, so its retained tickets cannot safely select a
  post-crash recovery layout yet.

## Exit gate

All remaining Phase 3 success criteria and focused/race/integration/VM gates
in the composite document pass, including zero outside-root mutation. Its
typed recovery results and replay format then become dependencies of Phase 4B.
