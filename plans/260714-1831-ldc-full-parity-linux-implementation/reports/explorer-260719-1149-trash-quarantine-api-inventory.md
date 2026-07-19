# Trash and quarantine API inventory

Scope: initial read-only inspection of
`internal/{trash,quarantine,linuxfs,mounts,recoveryport,state}`, followed by
the narrow current-v2 move-reconciliation increment it identified. The report
records the resulting API surface; it does not claim broader Phase 3B
completion.

## Existing capabilities

### Trash policy API

- `trash.MoveToTrash(ctx, ledger, action, planDigest, source, trashLease, deletedAt)` is a real high-level content-move orchestration. It validates an exact `ActionTrashPath` precondition and source `ParentLease`, derives a topology-bound layout identity, reserves a durable ticket, publishes and verifies owned `.trashinfo`, records dispatch facts, and calls `linuxfs.MovePublishedTrashNoReplace`.
- `trash.ReconcileIndeterminateTrashMetadata(ctx, ledger, ticket, trashLease)` is deliberately narrow: it reloads a metadata-indeterminate ticket, requires an equal durable layout binding before mapping/selecting/probing, then probes exactly `<token>.trashinfo` and records only `MetadataAbsent` or `MetadataRetained`/`OutcomeNotApplied`.
- `trash.ReconcileIndeterminateTrashMove(ctx, ledger, ticket, source, trashLease)` is a positive-only path for one current-v2 move-indeterminate ticket. It requires the exact bound layout, matching held source parent, exact owned metadata around two stable source-absence proofs that freshly requalify the immutable parent beneath its retained trusted root, and exact post-move `files/<token>` identity before recording the still-open `OutcomeMoveVerified` fact. It never restores, cleans up, scans, or resolves a broad orphan state.
- `trash.WriteTrashInfoDurable` creates durable owned metadata only. `MetadataPublication` exposes only `RootID()` and `Token()`; it is the receipt used by the move path.
- `trash.SelectTrashRoot` and `trash.ValidateTrashLayout` select topology-qualified `linuxfs.TrashDirectories`. Metadata encoding/parsing is internal (`trashInfo`), and parsed metadata is intentionally not a restore authority.

### Descriptor-rooted LinuxFS primitives

- `linuxfs.MovePublishedTrashNoReplace` **already exists**. It takes a validated source `ParentLease`, basename, topology-qualified `TrashDirectories`, exact metadata receipt, and immutable filesystem precondition. It performs `RENAME_NOREPLACE` into `files/<token>`, verifies destination identity and metadata/topology, fsyncs both parents, and returns `TrashMoveNotApplied`, `TrashMoveIndeterminate`, or `TrashMoveVerified`.
- `linuxfs.RestoreTrashNoReplace` also exists as a low-level primitive. It restores an owned `files/<token>` entry to the exact original basename without replacement, validates the original identity, fsyncs both parents, and intentionally retains `.trashinfo`.
- Supporting APIs are `OpenTopologyQualifiedTrashDirectories`, `PublishTrashInfoDurable`, read-only `ProbeOwnedTrashInfo`, exact `ProbeOwnedTrashContent`, and two-ENOENT `ProbeResolvedTargetAbsence`. `mounts.TrashLease` supplies only qualified data/descriptor capabilities (`RootID`, layout facts, `MetadataReconciliationIdentity`, `MetadataPathFor`, and package-audited descriptor duplication).
- The private staging building blocks exist independently: `OpenPrivateStaging`, `StageNoReplace`, `VerifyStagedIdentity`, `RestoreNoReplace`, and `RemoveStagedTree`.

### Quarantine and recovery APIs

- `quarantine.OpenPerMountQuarantine(layout)` returns a `Store` that exposes only `RootID()` and `Close()`. The package explicitly has no retain, move, restore, list, scan, remove, or reconciliation operation.
- `recoveryport.Ledger` supports `Reserve`, `Transition`, `FindOutstanding`, `ListOutstanding`, and `Reload`; its closed event vocabulary already includes move and restore facts for both Trash and Quarantine destinations. `state.RecoveryLedgerPort` is the concrete adapter.
- The state graph permits a Quarantine reservation to enter move dispatch directly, and a verified move to enter restore dispatch. New v2 Trash reservations require a nonzero layout binding; Quarantine reservations require the zero binding.

## Gaps evidenced by the current API surface

1. There is no high-level `trash` restore operation that binds a durable move-verified ticket to a newly resolved original parent, records restore dispatch/result facts, and invokes `linuxfs.RestoreTrashNoReplace`. The raw LinuxFS primitive should not be treated as the recovery policy layer.
2. There is no generic content-side reconciliation API and no restore-indeterminate reconciler. The narrow move reconciler can establish `OutcomeMoveVerified` only for a current v2 ticket with exact retained content, freshly requalified stable source absence, and stable metadata; source presence or every other inconclusive state remains outstanding.
3. There is no Trash metadata lifecycle/orphan API: no safe owned-record deletion, paired content-and-metadata reconciliation, scanning/listing, or generic Trash cleanup. A successful raw restore deliberately leaves metadata intact.
4. Quarantine has no policy/content operation at all. Its `Store` intentionally does not expose a directory capability, so there is currently no route from a Quarantine action/ticket to `StageNoReplace`, `RestoreNoReplace`, or any reconciliation operation.
5. `RemoveStagedTree` is not a production cleanup path yet: its own guard rejects current staging objects because no constructor grants exclusive-removal authority that excludes same-UID replacement races.
6. No public engine/composition entry point in this scope resolves a plan action into a `ParentLease` or opens all required authorities. This inventory does not infer where that composition belongs.

## Test seams and patterns

- `internal/trash/move.go`: private `moveToTrashWith` takes `trashMoveOperations` closures; tests use a fake `recoveryport.Ledger`/ticket and record the strict reserve → metadata → move ordering. No public mock authority is introduced.
- `internal/trash/reconcile.go`: private data-only `trashMetadataReconcileAuthority`, `reconcileIndeterminateTrashMetadataWithAuthority`, and operation closures test reload-before-map, layout-binding rejection, exact-token probes, and ticket-substitution handling without fabricating a lease.
- `internal/trash/content_reconcile.go`: reuses the same private data-only authority pattern and adds closure seams for exact metadata/content/source-absence evidence. Tests enforce the metadata → source absence → content → source absence → metadata ordering and retain the ticket on every non-positive fact.
- `internal/trash/write.go`: private `trashMetadataAuthority`, `trashInfoPublicationDirectory`, and `writeTrashInfoDurableWith` let tests fake only metadata mapping and receipt publication.
- `internal/linuxfs`: private hooks (`trashMoveHooks`, `trashRestoreHooks`, `trashInfoPublishHooks`, `stageHooks`, `restoreHooks`, `removalHooks`) inject rename/fsync/open/read behavior. Tests use descriptor-backed temporary topology fixtures for success and syscall ambiguity/error cases; the hooks are package-private.
- `internal/quarantine`: `openPerMountQuarantineWith` uses a private minimal `privateDirectory` seam (`RootID`, `Kind`, `Close`), with tests proving fail-closed kind/root handling and idempotent concurrent close. There is no content-operation seam because there is no content API.
- `recoveryport.Ledger` is intentionally the fakeable boundary for policy tests; `state.RecoveryLedgerPort` tests its opaque ticket/transition adapter separately.

## Validation

Executed successfully:

```sh
GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local \
  go test ./internal/trash ./internal/quarantine ./internal/linuxfs \
  ./internal/mounts ./internal/recoveryport ./internal/state -count=1
```

Useful narrow commands for subsequent work:

```sh
GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go test ./internal/trash -run 'TestMoveToTrash|TestReconcileIndeterminateTrash' -count=1
GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go test ./internal/linuxfs -run 'TestMovePublishedTrash|TestRestoreTrash|TestProbeOwnedTrashContent|TestProbeResolvedTargetAbsence' -count=1
GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go test ./internal/quarantine ./internal/state -count=1
```

## Low-conflict ownership suggestion

- **Trash policy owner:** `internal/trash/content_reconcile.go` and its test now own the completed narrow move proof. A future `restore.go` must stay separate and define its own durable metadata disposition; it must not treat an open `move_verified` fact as cleanup authority.
- **Quarantine policy owner:** new `internal/quarantine` operation files/tests, with the minimal necessary change to `quarantine.go` only if a safe opaque bridge to an existing LinuxFS primitive is designed. Do not expose raw descriptors or turn `Store` into generic filesystem authority.
- **LinuxFS owner:** the exact content and source-absence probes now live beside existing Trash primitives. Future restore/orphan work should reuse their read-only evidence only where the authority and race boundary actually match.
- **State/recoveryport owner:** leave untouched initially; the current event graph already models move/restore states. Change it only if the intended new operation needs a durable fact the existing closed graph cannot represent.

Status: DONE
Summary: The repository now has a durable high-level Trash move, a low-level restore primitive, and a narrow positive-only move reconciliation proof. High-level restore, generic/orphan reconciliation, restore reconciliation, and all Quarantine content policy remain absent.
Concerns/Blockers: Later operations still need their own explicit evidence and authority design; neither metadata nor a move-verified ticket grants cleanup authority.
