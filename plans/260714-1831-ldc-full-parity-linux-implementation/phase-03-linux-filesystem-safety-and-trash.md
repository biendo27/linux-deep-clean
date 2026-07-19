---
phase: 3
title: "Linux Filesystem Safety and Trash"
status: in-progress
priority: P1
effort: "15-22 engineer-days"
dependencies: [2]
---

# Phase 3: Linux Filesystem Safety and Trash

> **Composite reference document.** Formal execution nodes are now
> [Phase 3A](./phase-03a-safety-foundation.md) and
> [Phase 3B](./phase-03b-content-operations.md). This document preserves the
> original complete requirements and is not an additional dependency node.

## Overview

Implement the only filesystem mutation primitives later phases may use: trusted-root leases, mount qualification, `openat2` parent resolution, action-specific `statx` comparison, same-filesystem no-replace staging, descriptor-relative removal, Freedesktop Trash, quarantine, restoration, and crash reconciliation. No provider or user command may mutate yet.

The enforceable claim is narrow: traversal cannot escape held root/staging descriptors; a raced final entry is never permanently deleted unless its post-stage identity matches; ambiguity becomes drift/unsupported/retained. Context: [filesystem safety research](./research/02-filesystem-and-privilege-safety.md).

## Approved 3A/4A/3B Boundary

Phase 3A delivered the checked safety foundation marked complete below. The
user-approved [Phase 4A ledger](./phase-04a-durable-intent-ledger.md) now
precedes Phase 3B solely to resolve the durable-intent dependency cycle.
Phase 4A records private state only; it cannot register a root or layout,
select a Trash/quarantine location, move content, restore content, reconcile
content, or enable a command. Phase 3B may use the completed 4A ledger port
for `ReserveTrashToken`, move/restore, retention, and reconciliation only
after a real engine/helper-owned layout authority and the existing VM gates
are independently satisfied.

## Requirements

### Functional

- `OpenTrustedRoot` derives a root through an engine/helper-supplied authority registry, reopens it at apply time, verifies mount namespace/mountinfo/device/inode/filesystem/ownership/mode, and holds the FD for the action. No request absolute path is authority.
- Mutation requires Linux >=5.15, local ext4/XFS/Btrfs, and all `openat2` flags: `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS|RESOLVE_NO_XDEV`. Reject unsupported syscall/layout/flags, network/FUSE/overlay/ZFS/removable mounts, bind/nested crossing, and mount-namespace drift; never fall back to `realpath` or prefix checks.
- `ResolveParent` opens every intermediate component and returns a held parent FD plus exactly one validated final basename. `renameat2`/`unlinkat` receive only that basename and directory FDs; they have no resolution flags and never receive a multi-component planned path.
- `SnapshotFD` uses `statx(fd, "", AT_EMPTY_PATH, ...)`, checks returned mask bits, and compares a named action-specific required mask. Baseline identity: device, inode, type, UID/GID, mode; add link count/size/mtime only when the action requires them. Never compare atime. Treat Linux 5.15 mount ID as drift evidence, not persistent/global identity; use unique mount ID only as optional strengthening.
- Irreversible work stages with `renameat2(..., RENAME_NOREPLACE)` into a private same-mount directory, reopens the token, and compares identity. Mismatch is restored only via no-replace or retained; it is never deleted. Recursive removal walks held FDs and one basename at a time; generic cleanup rejects symlinks, devices, sockets, FIFOs, special files, and unexpected types.
- Trash uses same-filesystem atomic rename and valid `.trashinfo`; home Trash only on the home filesystem, otherwise qualified `.Trash/$uid` or `.Trash-$uid`. Never copy-delete, cross devices, overwrite a pair, silently switch to permanent deletion, or empty Trash.
- Publish `.trashinfo` durably before move; fsync metadata and affected directories. Reconcile metadata-only/file-only/interrupted pairs without deleting user content. Restore validates the anchored destination and uses no-replace.
- Quarantine is private, same-mount, retention-bounded, excluded from discovery, visible via a recovery handle, and never counted as freed space. If a supported/safe per-mount location is not proven, irreversible deletion is unsupported for that root.

### Non-Functional

- Preserve same-UID threat boundary: another UID/tree/environment/mount layout is hostile; a malicious same-UID peer may disrupt caller-owned staging but must not gain outside-root or root authority. Test both actors and document the limitation.
- Add bounded `openat2` `EAGAIN` retries; exhaustion is drift. Cancellation reaches quiescence <=1 second except an in-progress non-interruptible syscall, then records recoverable state.
- Every promised durable success requires successful sync; otherwise return interrupted/indeterminate/reconciliation as facts allow.
- Default tests use only test-owned temporary roots, are offline/unprivileged, and do not mount. Root/mount/race campaigns require `integration` or both `vmtest` guards.
- Reach >=90% coverage for validation/state machines and execute all adversarial behaviors; release remains blocked until Phase 11 runs >=10,000 completed attempts per ext4/XFS/Btrfs (30,000 total), zero outside mutation. PR smoke is >=1,000 ext4 attempts.

## Architecture

### Point-of-use state machine

```text
derive root ID -> lease/qualify root -> resolve parent -> open/snapshot target
permanent/quarantine: private same-mount stage -> rename NOREPLACE -> reopen/compare
Trash: validate/reserve -> publish+fsync info -> rename NOREPLACE into files/ -> reopen/compare
mismatch: restore NOREPLACE OR retain + reconciliation handle; never delete
match: FD-walk removal OR finalize Trash/quarantine durability
-> verify postcondition/effect -> fsync -> typed result/reconciliation
```

`STATX_MNT_ID` is cross-checked against the current `/proc/self/mountinfo`, device, inode, filesystem type, and mount namespace while the FD is held. Mount IDs may be reused after unmount; equality never authorizes a new root.

### Planned interface/function/type checklist

- [x] `mounts.RootAuthority`, `OpenTrustedRoot`, `RootLease.Close`, `InspectMount`, `CheckSupportedFilesystem`, `CheckMountNamespace`; registry implementations remain engine/helper-owned, not provider-owned.
- [x] `mounts.LayoutAuthority`, `OpenTrustedLayout`, and opaque layout leases bind one source root plus fixed layout kind to a same-mount directory without path authority.
- [x] `linuxfs.ResolveParent(lease, BytePath) (ParentLease, basename)`, `OpenTargetHandle`, `SnapshotFD`, `RequiredStatMask(ActionKind)`, `ComparePrecondition`.
- [x] `linuxfs.OpenPrivateStaging`, `StageNoReplace`, `VerifyStagedIdentity`, `RestoreNoReplace`, and `RemoveStagedTree`; public staging consumes only a requalified `LayoutPrivateStaging` lease, and the mismatch API exposes no delete operation. `VerifyPostcondition` remains pending with an executor-owned action contract.
- [x] `linuxfs.PublishFileDurable` accepts only a held private-directory lease plus one basename, uses no-follow/no-replace semantics, verifies the final entry identity and bytes, and syncs file and directory. `ReplaceFileDurable` remains blocked on a durable intent/reconciliation design.
- [x] `mounts.TrashAuthority`, `TrashRegistry`, `OpenTrustedTrash`, and `linuxfs.OpenTrashDirectories` bind an engine/helper-selected descriptor bundle and lend only requalified `files`/`info` duplicates to `linuxfs`; `PublishTrashInfoDurable` durably publishes one bounded LDC metadata record. The legacy API remains a metadata-only pre-selector.
- [x] `trash.SelectTrashRoot`, `ValidateTrashLayout`, and `linuxfs.OpenTopologyQualifiedTrashDirectories` require engine/helper-attested anchor evidence, prove literal Home/`.Trash-$uid`/`.Trash/$uid` plus `files`/`info` relationships with descriptor-rooted `openat2`, and requalify the proof at point of use. They do not discover a Trash location.
- [x] `WriteTrashInfoDurable` maps a lease-attested lexical metadata path, reselects topology, serializes bounded metadata, and durably publishes it. It does not resolve the source, reserve a token, move content, or issue a recovery handle.
- [x] `recoveryport.Ledger.Reserve`, `linuxfs.MovePublishedTrashNoReplace`, and `trash.MoveToTrash` provide a ledger-backed, receipt-bound no-replace Trash move. `linuxfs.RestoreTrashNoReplace` provides a descriptor-rooted low-level restoration that deliberately retains metadata.
- [x] v2 `recoveryport.Ledger.Reserve` binds each Trash intent to an opaque authority-selected layout/mapping identity; `linuxfs.ProbeOwnedTrashInfo` and `trash.ReconcileIndeterminateTrashMetadata` require that exact binding before mapping, selecting, and running an exact, read-only probe of one current `<token>.trashinfo`. They record only absent or retained metadata as a closed not-applied fact. Readable v1 histories stay unbound and read-only, so their metadata tickets remain outstanding rather than being reconciled against a guessed layout. This path does not scan, move, restore, delete, or clean up.
- [ ] `trash.RestoreFromTrash`, `ReconcileTrashOrphans`, and content-state reconciliation for interrupted moves/restores, including a recovery-safe metadata disposition after restoration.
- [x] `quarantine.OpenPerMountQuarantine` accepts only a requalified `LayoutPrivateQuarantine` lease and exposes root metadata plus idempotent close; it exposes no path, descriptor, or content mutation.
- [ ] `Retain`, `RestoreNoReplace`, `ApplyRetention`, `ReconcileRetained`; retention removal still uses verified staged-tree primitives.
- [x] `domain.RecoveryHandle` identifies root/token/original `BytePath`/date without absolute-path authority; `domain.ActionResult` distinguishes restored, retained, drifted, interrupted, and indeterminate outcomes.
- [ ] Wire those result types into actual Trash/quarantine retain, restore, and reconciliation operations after their trusted layout authorities and durable intent records exist.

### Dependency/import constraints

- `mounts` may import domain/pathbytes, `x/sys/unix`, and standard library only.
- `linuxfs` may import mounts/domain/pathbytes and `x/sys/unix`; it never imports providers, presenters, state, application, manager execution, or privilege protocol.
- `trash` and `quarantine` may import linuxfs/mounts/domain/pathbytes and the data-only `recoveryport` only. They cannot call shell commands or reconstruct absolute apply paths.
- Providers/presenters cannot import mounts/linuxfs/trash/quarantine. Later executors and the private state store may compose these APIs; architecture tests prohibit direct `renameat2`, `unlinkat`, or equivalent pathname mutation outside this safety layer.

## Related Code Files

| Action | Exact repo-relative paths | Purpose |
|---|---|---|
| Create | `docs/contracts/filesystem-safety.md` | Supported claim, syscall algorithm, threat boundary |
| Create | `docs/contracts/trash-and-quarantine.md` | Trash ordering, restoration, orphan/recovery semantics |
| Modify | `go.mod`, `go.sum` | Add audited/pinned `golang.org/x/sys` |
| Modify | `internal/architecture/import_rules_test.go` | Safety-layer import and raw-syscall prohibitions |
| Create | `internal/mounts/authority.go`, `internal/mounts/root_lease_linux.go` | Trusted registry contract and retained root FD |
| Create | `internal/mounts/mountinfo_linux.go`, `internal/mounts/filesystem_linux.go` | Namespace/mountinfo/statfs qualification |
| Create | `internal/mounts/root_lease_test.go`, `internal/mounts/mountinfo_test.go` | Root/mount parser and fail-closed tests |
| Create | `internal/linuxfs/resolve_linux.go`, `internal/linuxfs/snapshot_linux.go` | `openat2` parent/basename and FD `statx` |
| Create | `internal/linuxfs/stage_linux.go`, `internal/linuxfs/remove_linux.go` | No-replace staging/restoration and FD-recursive removal |
| Create | `internal/linuxfs/verify_linux.go`, `internal/linuxfs/durable_file_linux.go`, `internal/linuxfs/errors.go` | Postconditions, rooted durable file publication, stable errors |
| Create | `internal/linuxfs/resolve_test.go`, `internal/linuxfs/snapshot_test.go` | Traversal and action-mask tests |
| Create | `internal/linuxfs/stage_test.go`, `internal/linuxfs/remove_test.go`, `internal/linuxfs/durable_file_test.go` | Mismatch, special-type, hard-link, durable publication tests |
| Create | `internal/trash/select.go`, `internal/trash/layout.go` | Home/top-directory selection and layout validation |
| Create | `internal/trash/metadata.go`, `internal/trash/write.go`, `internal/trash/move.go` | Token validation, metadata serialization/publication, move |
| Create | `internal/trash/restore.go`, `internal/trash/reconcile.go` | No-replace restoration and crash orphan repair |
| Create | `internal/trash/select_test.go`, `internal/trash/metadata_test.go` | Mount/layout/raw-byte contract tests |
| Create | `internal/trash/move_test.go`, `internal/trash/reconcile_test.go` | Collision/durability/crash-state tests |
| Create | `internal/quarantine/quarantine.go`, `internal/quarantine/retention.go` | Private same-mount store authority/retention |
| Create | `internal/quarantine/reconcile.go`, `internal/quarantine/quarantine_test.go` | Retained-object recovery tests |
| Create | `tests/integration/linuxfs_integration_test.go` | Unprivileged real-syscall integration lane |
| Create | `tests/integration/trash_crash_integration_test.go` | Process-kill durability/reconciliation lane |
| Create | `tests/vm/filesystem_race_test.go` | Barrier-based ext4/XFS/Btrfs adversarial campaign |
| Create | `tests/vm/filesystem_mount_test.go` | Bind/remount/namespace/unsupported-mount VM cases |
| Create | `tests/fixtures/filesystem/README.md` | Dynamic hazard/replay seed provenance; no static host paths |
| Delete | None | Safety core is greenfield |

## TDD-First Test Strategy

### Test matrix

| Priority | Exact tests | Scenario | Allowed result/gate |
|---|---|---|---|
| Critical | `TestResolveParentRejectsSymlinkMagicLinkAndMountCrossing` | Intermediate/final symlink, proc magic link, nested/bind mount | `unsupported`/`drifted`; never weaker fallback |
| Critical | `TestRequiredStatMaskFailsClosed`, `TestMountIDAloneNeverAuthorizes` | Missing mask bits, reused mount ID, namespace drift | Reject; action-specific comparison only |
| Critical | `TestStageMismatchNeverDeleted`, `TestRestoreNeverOverwrites` | Swap between snapshot/stage; occupied original name | Restore no-replace or retained handle |
| Critical | `TestRemoveStagedTreeStaysBeneathFD` | Deep tree, parent rename, symlink/special/hard-link injection | Planned identity only; no outside change/false freed bytes |
| Critical | `TestTrashInfoDurabilityOrder`, `TestTrashCrashReconciliation` | Kill before/after metadata, rename, directory sync | Valid pair, safe orphan cleanup, or reconciliation; user content retained |
| Critical | `TestAdversarialSwapCampaign` | Barrier schedules: symlink/rename/inode/hard-link/bind/remount/parent swap | 1k PR ext4; 10k per FS release; zero sentinel change |
| High | `TestTrashRootValidationRejectsHostileLayouts` | Symlinked `.Trash`, wrong owner/sticky/mode, collision, EXDEV | Refuse; never copy-delete/permanent fallback |
| High | `TestInvalidUTF8TrashRoundTrip` | Home/top Trash raw byte path | Correct percent metadata and exact restore |
| High | `TestQuarantineNotFreedAndExcluded` | Retained staged object | Zero verified savings; discovery-exclusion marker/handle |
| Medium | `TestCancellationLeavesRecoverableState` | Cancel during walk/restore | <=1 s quiescence and typed retained/reconciliation result |

### RED -> GREEN -> REFACTOR

1. **RED — root/mount qualification:** add parser and real-syscall negative tests for mount namespace, filesystem, unsupported flags, and mount-ID reuse.
2. **GREEN — root lease:** implement minimal held-FD qualification with no mutation API.
3. **REFACTOR — explicit evidence:** separate registry-derived location, current mount record, and optional unique mount ID; remove any equality shortcut.
4. **RED — parent/snapshot/stage:** write traversal, required-mask, swap, restore-collision, hard-link, and special-file tests.
5. **GREEN — anchored staging:** implement all four `openat2` flags, bounded `EAGAIN`, one-basename `renameat2`, staged reopen/compare, and restore/retain.
6. **REFACTOR — FD ownership:** make leases close-safe, eliminate reconstructed paths, and expose no delete-on-mismatch call.
7. **RED — Trash/quarantine durability:** enumerate every crash point, hostile layout, token collision, raw-byte path, and restore collision before move code.
8. **GREEN — ordered durable operations:** implement compliant selection/metadata/fsync/move/reconcile and private quarantine.
9. **REFACTOR — shared primitives:** reuse only validated stage/restore/sync helpers; keep Trash policy distinct from quarantine; rerun architecture gates.
10. **RED/GREEN — adversarial harness:** first make outside-sentinel assertions fail with an intentionally non-mutating harness self-check, then run production APIs only; no test-only authority path.

### Commands and behavioral gates

```bash
GOTOOLCHAIN=local go test ./internal/mounts ./internal/linuxfs ./internal/trash ./internal/quarantine -count=1
GOTOOLCHAIN=local go test -race ./internal/mounts ./internal/linuxfs ./internal/trash ./internal/quarantine
GOTOOLCHAIN=local go test -tags=integration ./tests/integration -run 'LinuxFS|TrashCrash' -count=1
GOTOOLCHAIN=local go test ./internal/architecture -run TestArchitectureImportAllowlists -count=1
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" GOTOOLCHAIN=local go test -tags=vmtest ./tests/vm -run TestAdversarialSwapCampaign -count=1
```

Default/focused tests require no root, mounts, network, or authorization. VM command is valid only inside the Phase 1 guarded disposable guest. Coverage: >=90% for validation/state-machine branches in mounts/linuxfs/trash/quarantine. Non-coverage gates: no outside sentinel change, no post-stage mismatch deletion, no Trash overwrite/copy-delete, no false durable success, and no leaked FDs (`<=128` during campaign).

## Implementation Steps

1. Audit/pin `x/sys`; record minimum syscall/kernel behavior. Survey safe same-mount stage/quarantine layouts on every supported distro/root class. Document selected locations; mark an unproven root irreversible-mutation-unsupported.
2. Add root/mount RED tests, then implement `RootLease`, mountinfo parser, namespace/filesystem/ownership checks, and held-FD lifetime.
3. Add resolution/snapshot/staging RED tests, then implement parent+basename resolution, action masks, no-replace stage, post-stage compare, restore/retain, and descriptor walk.
4. Prove `renameat2` and `unlinkat` call sites are limited to `internal/linuxfs` and accept one component only; Trash/quarantine/state compose the rooted API rather than issuing raw syscalls. Update architecture scan.
5. After Phase 4A, add Trash/quarantine RED tests and crash-state table. Consume its durable source-bound reservation/transition port; then implement layout validation, metadata percent encoding/date, ordered fsync, move, restore, retention, and reconciliation without adding a second state store.
6. Build deterministic barrier actors and outside sentinels. Record kernel/arch/FS/mount options/schedule/seed/result/quarantine for replay. Use only production APIs.
7. Run focused/race/integration gates. In disposable VMs run PR ext4 smoke; defer the required 30,000 total supported-filesystem release campaign to Phase 11 but keep the same harness and recorded seeds.
8. Review every error path for FD closure and one of: verified success, explicit drift, safe no-replace restoration, retained handle, unsupported, or indeterminate reconciliation.

## Success Criteria

- [ ] All descendant opens use required `openat2` flags; unsupported guarantees fail closed with no string-path fallback.
- [ ] Mount namespace/device/inode/fs type/mountinfo are checked while root FD remains held; mount ID alone never authorizes.
- [ ] Mutating syscalls receive directory FDs and one validated basename; staged mismatch is never deleted or overwritten on restore.
- [ ] Recursive removal cannot traverse links/mounts/special files and cannot overstate hard-link/reflink savings.
- [ ] Trash selection, raw metadata, collision handling, fsync order, restore, and orphan reconciliation satisfy the documented non-atomic-pair contract; LDC has no empty-Trash operation.
- [ ] Quarantine is same-mount/private/excluded/retention-bounded and never counted freed; unproven locations disable irreversible deletion.
- [ ] Focused/race/integration/architecture/coverage gates and 1,000-attempt ext4 VM smoke pass with zero outside-root mutation.

## Risk Assessment and Rollback

| Risk | Mitigation |
|---|---|
| `openat2` mistaken for final rename/unlink identity | Mandatory stage/reopen/compare; one-basename syscall API |
| Optional `statx`/mount ID creates false confidence | Required masks, mountinfo/device/inode/fs checks, held lease |
| Crash splits Trash file/metadata | Ordered fsync and explicit safe orphan reconciliation; never claim pair atomicity |
| Same-UID peer disrupts staging | 0700 caller-owned stage, drift/retain behavior, documented trust boundary |
| Quarantine silently consumes disk | Retention/history handle, discovery exclusion, never counted freed |

Rollback disables all filesystem mutation call sites and removes Phase 3 packages/dependency before any release; raw contract types remain. If a test crash leaves a test-owned Trash/quarantine entry, run the tested reconciliation path inside the disposable root—never manually broaden deletion. Persisted user recovery handles, once introduced later, must be migrated rather than discarded.

## Phase Exit and Hand-Off

Phase 3A hands the Phase 4A ledger only immutable safety types, action-specific masks, and descriptor-rooted private-directory publication; the ledger may persist facts but cannot add a weaker filesystem path. Phase 3B exits only after the unprivileged gates and PR ext4 smoke pass and the exact safe quarantine layout decision is recorded. It then hands Phase 4B immutable safety APIs, typed recovery handles, supported/unsupported reasons, crash reconciliation states, and race replay format.
