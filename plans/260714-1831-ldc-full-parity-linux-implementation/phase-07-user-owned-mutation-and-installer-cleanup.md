---
phase: 7
title: "User-owned Mutation and Installer Cleanup"
status: pending
priority: P1
effort: "14d"
dependencies: [6]
---

# Phase 7: User-owned Mutation and Installer Cleanup

## Overview

Enable the first real apply paths: selected user-owned actions for `clean`, `purge`, and `installer`. Every action must originate in a persisted immutable Phase 4 plan and execute only through the Phase 3 trusted-root, descriptor-resolution, same-filesystem staging, Trash, quarantine, and recovery primitives.

This phase does not enable package managers, system journals, fingerprint/PAM, system completion installation, update/remove, privileged files, or a generic filesystem executor. `analyze` file mutation remains preview-only for a later parity phase. LDC may report Trash usage but must never empty Trash.

## Requirements

### Admission by command

- **`clean`:** admit only caller-owned, evidence-backed recreatable XDG caches, bounded user logs, and exact-provenance inert user integration/cache residue. Exclude XDG configuration, data, state, documents, credentials, browser history/cookies, active application content, manager-owned caches, LDC state/cache/recovery paths, and Trash.
- **`purge`:** admit only artifact rules proven by Phase 5 project-root containment plus the matching nearest ecosystem marker. Default action is `trash_path`; permanent `delete_recreatable_path` is a separate, explicitly selected irreversible action. Deployment outputs such as `dist` remain unselected by default.
- **`installer`:** admit only Phase 5 evidence-backed ordinary installer files from XDG Downloads/configured roots. The only allowed mutation is `trash_path`; directories, special files, manager caches, private mail/cloud databases, ambiguous archives, and MIME/extension mismatches are rejected.
- No command may synthesize a target from a display pathname, a current scan, a basename, a glob, a CLI string, or a protection-list entry at apply time.

### Plan/apply and support gates

- Preview completes and displays the immutable plan before apply. Interactive confirmation or explicit `--apply` is mandatory; `--yes` never implies apply.
- Load the exact stored plan by digest/run ID. Recompute and compare canonical digest, caller, command, config digest, capability snapshot, selected action set, dependencies, and expiration inputs before executing.
- Run only as the original unprivileged numeric caller; the main process still refuses effective UID 0.
- Mutation support requires an approved 15-matrix platform identity, systemd, kernel 5.15+, local ext4/XFS/Btrfs target, and every Phase 3 root/mount/resolution guarantee. During development, only guests already qualified for the action family are enabled; all other hosts remain preview-only.
- Reject WSL, containers, chroots, live/rescue media, immutable/atomic hosts, non-systemd, network/FUSE/overlay/ZFS/removable filesystems, and mount crossings. `--force` does not override these decisions.
- Protection lists are monotonic: re-evaluate them immediately before apply; newly protected action returns drift/skip, never a broadened replacement.

### Execution and recovery

- Register only the closed unprivileged action kinds `trash_path`, `delete_recreatable_path`, and `quarantine_path`. Each kind has a distinct validator, Phase 3 call graph, precondition mask, expected postcondition, and result mapping.
- Derive trusted root/provider authority from compiled registries and the trusted caller. Requests never supply an authoritative absolute root.
- Resolve the parent with all required `openat2` flags, retain the root/parent descriptors, accept exactly one validated final basename for `renameat2`, and compare the action-specific `statx` preconditions.
- For irreversible deletion, stage with `RENAME_NOREPLACE` to a private same-mount location, reopen and verify staged identity, then remove only inside staging through the descriptor-anchored walker. Never unlink a post-stage mismatch.
- For Trash, use Phase 3 home/top-directory selection, collision-safe paired token, durable `.trashinfo` ordering, same-filesystem rename, and no-replace restore handle. Refuse cross-device copy/delete or permanent fallback.
- Restore a mismatch only with no-replace semantics. Otherwise retain quarantine and return a recovery handle; retained/quarantined bytes are not reported as freed.
- Handle cancellation/crash before metadata, after metadata, after rename, during staged removal, during verification, and during restore. Phase 4 recovery must be idempotent and preserve uncertain objects.
- Return per-action terminal facts including attempted, skipped/drifted/failed/interrupted/indeterminate, verified postcondition, actual apparent/allocated effect, and a real Trash/quarantine handle when present.
- Hard-link removal never claims allocated bytes were freed unless verified. Success is not durable until required metadata/directory syncs complete.

### Dry-run and privacy

- `--dry-run` is an absolute zero-side-effect path: no filesystem write/rename/unlink/fsync, no plan/history/debug/state write, no authorization/launcher call, no network/socket connection, and no external transaction command.
- Dry-run may read configuration/capabilities/providers and render an in-memory immutable preview. It must not create XDG directories merely by resolving them.
- Normal apply records private Phase 4 receipts/history with 0700 directories and 0600 files unless history is disabled. Debug and result output use redaction rules.
- LDC never empties, prunes, compacts, or treats any Trash `files/`/`info/` content as a cleanup target. There is no `empty_trash` action enum, executor, flag, or hidden TUI operation.

## Architecture

```text
Phase 5 Candidate
  -> Phase 4 Policy/Planner -> immutable selected Plan + digest
  -> Phase 6 confirmation / --apply contract
  -> Phase 4 ApplyCoordinator
       -> support/config/protection/digest recheck
       -> userfs action registry
       -> Phase 3 RootLease + ResolveParent + SnapshotFD
       -> MoveToTrash OR StageNoReplace + RemoveStagedTree
       -> VerifyPostcondition
       -> Phase 4 receipt/history/recovery
```

The user-owned executor is composition, not a parallel syscall implementation. It maps a validated action kind to a fixed sequence of Phase 3 interfaces. It never imports providers or presenters, and providers/presenters never import it. The Phase 4 coordinator owns dependency ordering, cancellation, partial completion, receipt persistence, and reconciliation entry/exit gates.

Normal preview may persist a plan through Phase 4. Dry-run uses a no-write state sink selected before any state-layout creation. Apply refuses when unresolved recovery from a previous action overlaps the same root/target.

## Related Code Files

### Create

- `internal/engine/userfs/executor.go` — closed unprivileged action dispatcher over Phase 3 interfaces.
- `internal/engine/userfs/validate.go` — caller/root/action/evidence/support/precondition admission.
- `internal/engine/userfs/results.go` — Phase 3 outcome to Phase 2/4 result and verified-byte mapping.
- `internal/engine/userfs/executor_test.go`, `internal/engine/userfs/validate_test.go`, `internal/engine/userfs/results_test.go`.
- `internal/application/dryrun.go`, `internal/application/dryrun_test.go` — no-write/no-auth/no-network dependency graph.
- `tests/contract/user_owned_action_boundary_test.go` — exact commands/action kinds/imports and no-empty-Trash invariant.
- `tests/contract/dry_run_zero_side_effects_test.go` — black-box CLI/application side-effect audit.
- `tests/integration/userfs/clean_test.go`, `tests/integration/userfs/purge_test.go`, `tests/integration/userfs/installer_test.go`.
- `tests/integration/userfs/recovery_test.go` — deterministic crash-point and idempotent reconciliation cases.
- `tests/integration/userfs/outside_root_sentinel_test.go` — synchronized target/parent replacement with immutable external sentinel.
- `tests/vm/userfs/user_owned_mutation_test.go` — opt-in ext4/XFS/Btrfs Trash/staging/apply/recovery qualification.
- `tests/performance/user_owned_apply_benchmark_test.go` — cancellation and controlled ext4 verified-space gate.

### Modify

- `internal/engine/apply_coordinator.go` — register user-owned executors, enforce digest/support/recovery gates, and preserve dependency ordering.
- `internal/engine/recovery_coordinator.go` — reconcile Trash metadata, staged/quarantined objects, and interrupted receipts without deleting uncertain data.
- `internal/application/use_cases.go` — route `clean`, `purge`, and `installer` apply requests through the coordinator; other mutation remains unavailable.
- `internal/application/confirmation.go` — require immutable plan confirmation and dry-run override.
- `internal/state/recovery.go` — persist/reload private typed Trash/quarantine handles and reconciliation state.
- `internal/providers/projects/rules.go` — mark which evidenced artifacts may offer a separately selected irreversible action; keep deployment outputs unselected.

### Delete

- None.

## Interfaces and Dependency Boundaries

```go
type UserActionExecutor struct {
    Roots       linuxfs.TrustedRootOpener
    Resolver    linuxfs.ParentResolver
    Stager      linuxfs.Stager
    Trash       trash.Mover
    Quarantine quarantine.Store
    Remover     linuxfs.StagedTreeRemover
}

func (e *UserActionExecutor) Revalidate(context.Context, domain.Action) (domain.Revalidation, error)
func (e *UserActionExecutor) Apply(context.Context, domain.Action) (domain.ActionResult, error)
func (e *UserActionExecutor) Verify(context.Context, domain.Action, domain.ActionResult) (domain.Verification, error)
```

- `internal/engine/userfs` implements the Phase 4 `ActionExecutor` contract and may import only domain, capability snapshots, and Phase 3 linuxfs/trash/quarantine ports.
- It must not import provider implementations, presenters, Cobra/TUI, privilege protocol/helper, managerexec, external command runners, or concrete state stores.
- `trash_path` calls only `SelectTrashRoot`, `ValidateTrashLayout`, `ReserveTrashToken`, `WriteTrashInfoDurable`, `MoveToTrash`, and verification/recovery primitives.
- `delete_recreatable_path` calls only trusted-root lease, action-specific snapshot comparison, `StageNoReplace`, staged identity verification, `RemoveStagedTree`, and postcondition verification.
- `quarantine_path` never reports freed bytes and returns a retained handle. It is a fail-safe outcome, not a silent substitute for Trash or deletion.
- Phase 4 is the only package allowed to persist receipts/history and decide safe continuation after one action fails. The executor returns facts; it does not rewrite the plan.
- No interface accepts arbitrary absolute paths, executable/argv, shell strings, presenter selections, or an “ignore safety” boolean.

## TDD Test Strategy

### RED — prove the boundary before enabling an executor

1. Add `TestUserOwnedExecutor_ClosedActionSet` and require exactly `trash_path`, `delete_recreatable_path`, and `quarantine_path`; assert every privileged/manager/system kind is rejected.
2. Add `TestNoEmptyTrashActionExists` across domain enums, registry, Cobra tree, TUI messages, and executor mappings. Add candidates beneath home and top-directory Trash and require zero admitted actions.
3. Add command admission tables:
   - clean: only exact recreatable user cache/bounded log/inert-provenance cases;
   - purge: marker/root/rule evidence, Trash default, permanent action separately selected, `dist` unselected;
   - installer: ordinary evidenced file and Trash only.
4. Add `TestDryRunZeroSideEffects` with recording implementations for state, filesystem, history, debug log, authorization launcher, network dialer, and external runner. Assert zero calls and no XDG directory creation for every mutating command/flag combination.
5. Add stale-plan cases for digest, caller UID, config/protection, capability snapshot, action set, root/mount, target metadata, provider evidence, and elapsed validity; all reject before staging with exit summary 3 where applicable.
6. Add synchronized races at resolution, snapshot, stage, staged verification, removal, and restore. Allowed outcomes are verified planned mutation, drift, no-replace restore, or retained quarantine; outside sentinels never change.
7. Add Trash cases for home/top-directory layouts, sticky/ownership violations, symlinked components, collision pairs, invalid UTF-8, metadata encoding, cross-device refusal, crash ordering, restore collision, and orphan reconciliation.
8. Add hard-link, open-file, permission/ACL/type change, special-file, mount-crossing, unsupported-filesystem, read-only-mount, cancellation, and partial-result cases.
9. Add crash-point recovery tests and run reconciliation twice to prove idempotence and no deletion of indeterminate objects.
10. Add controlled ext4 apparent/allocated accounting before implementation; require verified delta within `max(5%, 64 MiB)` of the reported estimate.

### GREEN — one action family at a time

1. Implement validation and `trash_path` for one ordinary user file. Pass Trash durability/recovery cases before registering purge/installer.
2. Register installer Trash only after classifier evidence is revalidated at apply time.
3. Register purge Trash; then add the separately selected staged permanent path for rules explicitly marked recreatable.
4. Register clean's staged permanent path only for exact caller-owned recreatable cache/log rules. Never route manager cache through userfs.
5. Wire receipts, actual byte verification, cancellation, and recovery blocking through the Phase 4 coordinator.
6. Select a no-write dependency graph at CLI/application entry for dry-run, before state layout initialization.

### REFACTOR — retain Phase 3 as sole mutation authority

1. Deduplicate executor sequencing only through Phase 3 interfaces; reject any convenience wrapper that reopens a string path or calls `renameat2`/`unlinkat` directly.
2. Keep action-specific precondition masks and result mapping separate so later loosening in one action cannot weaken another.
3. Improve batched plan scheduling only if it preserves dependency order, four-worker/128-FD limits, cancellation, and per-action receipts.
4. Re-run import, dry-run, adversarial, recovery, race, coverage, and VM gates after every executor refactor.

### Test matrix

| Axis | Cases |
|---|---|
| Command | clean user cache/log; purge Trash/permanent; installer Trash |
| Filesystem | ext4, XFS, Btrfs; reject FUSE, overlay, ZFS, network, removable, read-only |
| Identity | invalid UTF-8, hard links, inode replacement, symlink/magic link, owner/mode/type/link drift |
| Trash | home, `.Trash/$uid`, `.Trash-$uid`, collision, hostile layout, cross-device, metadata/file crash window |
| Recovery | pre-stage, post-metadata, post-rename, recursive removal, verification, restore collision, repeated reconcile |
| Interaction | preview only, interactive apply, `--apply`, `--yes` without apply, dry-run, cancellation, partial/indeterminate |

### Commands and gates

```bash
go test ./internal/engine/userfs/... ./internal/application/... ./tests/contract/...
go test -race ./internal/engine/userfs/... ./internal/linuxfs/... ./internal/state/... ./tests/integration/userfs/...
go test -tags=integration ./tests/integration/userfs/...
go test -run '^$' -bench 'Benchmark(UserApply|ControlledExt4Accounting)' -benchmem ./tests/performance/...
go test -coverprofile=coverage.out ./internal/engine/... ./internal/state/...
go tool cover -func=coverage.out
```

Default tests use temporary ordinary directories only and remain hermetic, offline, unprivileged, and safe. Filesystem/mount/kill tests require `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token, and the expected snapshot-backed guest identity. Missing any guard aborts before mutation. PR smoke exercises at least the qualified Ubuntu 24.04 x86_64 ext4 guest; Phase 11 owns the full matrix claim.

## Implementation Steps

1. Freeze the exact user-owned action/command matrix, dry-run side-effect contract, action-specific preconditions, and recovery states in failing tests.
2. Implement support/caller/config/protection/plan-digest revalidation around Phase 4 `ApplyCoordinator`.
3. Compose and register `trash_path` only; pass home/top-directory Trash, byte-path, crash, restore, and no-empty-Trash tests.
4. Enable installer apply for evidenced ordinary files, then purge Trash for evidenced artifacts.
5. Compose `delete_recreatable_path` through same-mount staging and staged identity verification; enable it separately for approved purge and clean rules.
6. Implement verified effect accounting, hard-link handling, retained quarantine results, cancellation, and per-action receipts.
7. Integrate Phase 4 reconciliation, block overlapping applies while recovery is unresolved, and prove repeated recovery is safe.
8. Implement dry-run dependency selection before any state or XDG writer construction; run black-box zero-side-effect audit.
9. Run default, race, coverage, integration, performance, and disposable-VM gates. Leave unqualified hosts preview-only.

## Success Criteria

- [ ] Only `clean`, `purge`, and `installer` user-owned actions are enabled; all privileged/manager/system actions remain unavailable.
- [ ] Every apply consumes an unchanged immutable plan and requires explicit apply plus resolvable selection.
- [ ] `--dry-run` causes zero filesystem/state/history/debug writes, authorization prompts, external transactions, and network requests.
- [ ] No provider, presenter, or command directly invokes mutation; the executor uses Phase 3 interfaces exclusively.
- [ ] Trash is never emptied or offered as a cleanup target; no empty/prune action exists anywhere.
- [ ] Installer actions are Trash-only; purge defaults to Trash; permanent purge/clean deletion is separately labeled, selected, staged, and evidence-backed.
- [ ] Unsupported platform/filesystem/mount conditions fail closed and `--force` cannot bypass them.
- [ ] Every staged mismatch is restored no-replace or retained; it is never deleted.
- [ ] Recovery is idempotent, preserves uncertain objects, blocks unsafe reapply, and records real handles/status.
- [ ] Apparent/allocated/estimated/verified effects and hard-link behavior are accurate; quarantine is never counted freed.
- [ ] Engine/recovery safety code meets the 90% coverage gate and other Phase 7 behavior meets at least 80%.
- [ ] Default and opt-in VM adversarial tests show zero mutation outside disposable approved roots.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback |
|---|---|---|
| Candidate becomes a pathname guess | Immutable root+byte path+evidence; apply-time revalidation; no display-string API | Unregister the affected rule/executor; leave discovery preview-only |
| Race moves/deletes wrong entry | Held descriptors, basename-only stage, post-stage identity check, no-replace restore/retain | Disable irreversible kind; preserve retained recovery objects |
| Trash crash leaves orphan metadata/object | Ordered fsync protocol, typed orphan reconciliation, never delete uncertain content | Disable apply and run read-only reconciliation report; retain data |
| Dry-run initializes XDG state or prompts | No-write dependency graph chosen at entry; recording-port contract tests | Route command to pure in-memory preview only |
| Savings are overstated | Verify allocated delta, hard-link rules, no freed bytes for quarantine | Report verified effect as unknown/zero; never infer success |

Rollback is capability withdrawal: remove the executor registration for the affected command/action and return to preview-only mode. Do not delete recovery records or quarantined/Trashed objects. Recovery handles remain readable across the rollback; any restoration still requires the same no-replace validation.

## Phase Exit/Handoff

Phase 8 must not reuse this unprivileged executor in the helper: user Trash and ordinary user cleanup remain in `ldclean`. Phase 9 may add privileged manager/system action families only after the reject-only helper gate, and must leave Phase 7 registries closed. Phase 10 may later enable analyze-selected file Trash by reusing this exact plan/apply boundary, never by adding a TUI-local delete path.
