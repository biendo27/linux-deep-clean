---
phase: 10
title: "Optimization, Fingerprint, and Remaining Semantic Parity"
status: pending
priority: P1
effort: "3w"
dependencies: [9]
---

# Phase 10: Optimization, Fingerprint, and Remaining Semantic Parity

## Overview

Close the public command contract without weakening Linux safety. Add only evidence-backed, reversible optimization recipes; implement `fingerprint` as the safe Linux replacement for Touch ID; finish exact-package `update`/`remove`; complete the deferred user-owned `analyze`, uninstall-residue, completion-install, and state-repair actions; qualify optional-provider behavior; and enforce a command-by-command semantic parity ledger. This phase does not add generic tuning, arbitrary service changes, general upgrades, or a self-updater.

Authoritative context: [approved design](../reports/260714-1754-ldc-linux-native-design-report.md), [provider research](./research/03-linux-provider-contracts-and-qualification.md), and the qualified Phase 9 manager/helper contracts.

## Requirements

### Optimization

- `optimize`/`optimise` first performs a read-only health audit and reports evidence, expected effect, reversibility, and unsupported reasons. It never equates “cleanup” with a speed boost.
- New Phase 10 recipes must be allowlisted, measurable, user-approved, idempotent, and reversible from an anchored pre-change snapshot. If rollback cannot be proved, the recipe is recommendation-only.
- Initial mutable recipes are limited to caller-owned generated caches with an owner-provided rebuild tool:
  - `rebuild_user_fontconfig_cache`: propose only when `fc-cat`/metadata validation proves an invalid or stale cache wholly beneath the caller-owned fontconfig cache root; stage the prior cache, run fixed `/usr/bin/fc-cache -f` as the caller with a fresh derived XDG environment, verify with `fc-cat`, and restore no-replace on failure/rollback.
  - `rebuild_user_desktop_database`: propose only when the generated `mimeinfo.cache` disagrees with parseable caller-owned desktop entries beneath `$XDG_DATA_HOME/applications`; stage the generated cache only, run fixed `/usr/bin/update-desktop-database <validated-root>`, verify exact generated records, and restore no-replace on failure/rollback.
- Phase 9 package-cache and bounded-journal actions may appear in optimize results with their true `recreatable`/`irreversible` labels; they are not called reversible recipes.
- Never change sysctls, CPU governors, I/O schedulers, firewall/network settings, swappiness, page cache, service enablement, timers, package selections, firmware, kernels, or arbitrary configuration. Never promise a generic speed boost.

### Fingerprint

- `fingerprint status` is read-only and probes fprintd D-Bus availability, a usable device, enrollment for the trusted caller, PAM profile-manager capability, current feature state, and a verifiable password path.
- Enrollment is a user-session handoff: show the exact supported enrollment tool/steps, return to a fresh status probe, and never pass biometric data through LDC or the privileged helper.
- Debian/Ubuntu enable/disable uses only the installed fprintd profile through absolute `/usr/sbin/pam-auth-update --enable|--disable <compiled-profile>`; never `--force` and never edit PAM files.
- Fedora enable/disable first requires successful absolute `/usr/bin/authselect check` and `authselect test`; use only the current supported profile plus `with-fingerprint`, request an authselect-managed backup, and verify/restore through authselect on failure.
- Arch is status plus guidance only for v1. Missing hardware, enrollment, fprintd, profile, managed authselect state, backup support, or password fallback makes mutation unsupported.
- Before enable and after enable/disable, prove a password authentication path remains configured. The real-hardware qualification lane must prove password sudo still works when fingerprint is unavailable. Any ambiguity fails closed and attempts manager-owned restore.

### Update and remove

- Detect installation kind from exact package ownership/build metadata: Debian package, RPM, Arch package, or rootless archive. Ambiguous/unowned binaries receive guidance only.
- `ldclean update` may query only the installed LDC channel. Native apply targets exactly `linux-deep-clean` and the dependency graph disclosed by the manager preview; never issue a general upgrade. Reuse Phase 9 APT re-simulation, DNF5 stored replay, and Pacman drift/reconciliation guarantees.
- `ldclean remove` previews and asks the native manager to remove exactly `linux-deep-clean`, without purge/autoremove/cascade. It preserves all user XDG config/state/cache/history.
- Rootless archive update verifies signed release metadata/artifact and prints an exact manual replacement procedure; it never overwrites the running executable. Rootless remove prints the resolved running binary/archive root and manual removal guidance; it never self-deletes.
- `--dry-run` remains offline. An explicit update check may use the network; package apply may use only the manager network requirement disclosed in its immutable plan.

### Deferred user-owned parity actions

- `analyze` deletion is admitted only for an explicitly selected, caller-owned regular file discovered beneath an explicit analysis root. It reuses Phase 7's Trash-only `trash_path` executor; directories, special files, permanent deletion, unsupported filesystems, display-path authority, and bulk implicit selection are rejected.
- `uninstall` composes the exact manager action from Phase 9 with separately selected, attributable residue actions. Residues require provider evidence that binds the exact installed application identity to each caller-owned cache/log/leftover target. Config, credentials, documents, histories, databases, and ambiguous “app data” are excluded. An uninstall plan orders manager removal before selected residue actions; an already-orphaned evidenced residue remains independently selectable.
- Completion generation remains pure Phase 6 behavior. User installation publishes generated Bash/Zsh/Fish bytes only to compiled per-shell destinations derived from the trusted caller's home/XDG facts, with bound shell/version/digest and durable no-follow publish/backup semantics. Callers cannot supply a destination. System installation is a separate privileged action over root-owned, versioned package assets and compiled destinations; it is unavailable in rootless archives.
- The separately selected `history` state-repair action preserves the explicit Phase 4 recovery-plan contract. It is surfaced through the existing history selection/plan/apply flow—no new command, alias, or implicit mutation. Apply retains the original corrupt/old state, durably publishes only the already-approved rebuilt state, verifies reopen/digest/schema invariants, and reports a recovery handle. It accepts no arbitrary source/destination/delete request and is never implicit in a read-only history query.

### Remaining parity and optional providers

- Every public command/alias/mode in the approved contract is present in a machine-readable parity ledger with owner package, read/write boundary, required capability, unsupported behavior, JSON/NDJSON contract test, interactive/non-interactive test, and release gate.
- Optional Flatpak, Snap, fprintd, UPower, thermal, GPU, and `paccache` capability absence yields a typed unavailable reason or `null` metric. It never yields fabricated success/zero or removes the command.
- Retain `--whitelist` only as the compatibility-facing protection-list name. It can subtract candidates/actions only.
- Preserve independent Apache-2.0 provenance. The ledger is derived from the approved report/public behavior, not upstream source, fixtures, prose, rule tables, or TUI layout.

### Test safety

- Default tests are hermetic, offline, unprivileged, and host-safe. PAM, manager, root, and hardware mutation requires `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, and a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token.
- A separate real-hardware fingerprint lane is opt-in, attended, snapshot/recovery documented, and not emulated as success when hardware is absent.

## Architecture

### Optimization recipe boundary

```text
read-only Recipe.Audit
  -> evidence + exact generated outputs + expected measurable postcondition
  -> central policy/protection/selection
  -> immutable plan + explicit apply
  -> anchored same-filesystem Stage prior generated output
  -> fixed owner executable as trusted caller
  -> Recipe.Verify
  -> success + RollbackHandle OR restore-no-replace/retained recovery
```

`Recipe` is not a generic command descriptor. The compiled registry owns roots, probe, executable, fixed flags, output set, snapshot mask, postcondition, and rollback implementation. Recipe outputs cannot escape the derived caller-owned XDG root. A provider cannot inject a recipe or argv.

### Fingerprint flow

The unprivileged capability adapter derives the numeric caller and queries fprintd. It emits status/guidance or a typed enable/disable action. The Phase 8/9 helper independently re-probes caller enrollment and distro profile authority, verifies password fallback, executes one fixed profile-manager action, verifies the manager result and password path, and returns the manager-owned backup handle. It never accepts a PAM path/profile name from the request.

### Installation channel flow

`InstallationDetector` returns a typed `NativeAPT`, `NativeDNF5`, `NativePacman`, `RootlessArchive`, or `Unknown` record bound to the running executable identity. Native update/remove becomes a dedicated self-package action family so generic uninstall protection cannot be bypassed. Archive metadata verification is pure and bounded; Phase 11 supplies signed production metadata and artifacts.

### Semantic parity ledger

| Public surface | Required Linux semantic outcome | Mutation/qualification gate |
|---|---|---|
| `ldclean` | TTY capability menu; non-TTY help and no mutation | Phase 6 no-TTY/PTY goldens; apply remains explicit. |
| `clean` | Recreatable caches, bounded logs, manager cache APIs, exact leftovers; Trash usage read-only | Phases 5, 7, 9; never empty Trash. |
| `uninstall` | Exact native/Flatpak/Snap app plus separately evidenced, individually selected caller-owned residues | Phase 9 manager guarantees plus Phase 10 user-filesystem admission; app data never selected. |
| `optimize`, `optimise` | Health audit, the two reversible owned-cache recipes, and honestly labeled bounded maintenance | Recipe idempotency/rollback plus Phase 9 gates; no generic tuning. |
| `analyze`, `analyse` | Explicit-root disk explorer, apparent/allocated size, refresh/large-file/JSON; selected ordinary files may move to Trash | Phase 10 admission over the Phase 7 `trash_path` executor; no directory/permanent/implicit deletion. |
| `status` | CPU/memory/load/disk/I/O/network/process/power/thermal/optional GPU with explainable score | Read-only; unavailable metrics `null` with reason. |
| `history` | Query runs/actions/results/reconciliation with filters and human/JSON/NDJSON; explicit repair plan/apply for recoverable state | Reads stay read-only; Phase 10 repair executor preserves the original and verifies durable replacement. |
| `purge` | Marker/evidence-backed project artifacts under configured roots | Trash default; permanent only explicit recreatable action. |
| `installer` | Extension + magic/package metadata in Downloads/explicit roots | Trash only; no private mail/cloud scans. |
| `fingerprint` | Status, enrollment handoff, safe enable/disable where supported | Debian/Ubuntu and Fedora rules above; Arch guidance only. |
| `completion` | Bash/Zsh/Fish generation; compiled-destination user install by default | Phase 10 user publisher plus typed system helper action; rootless system install is unsupported. |
| `update` | Exact LDC channel/package update or verified archive guidance | No self-replacement/general upgrade. |
| `remove` | Exact native package removal or archive guidance | Preserve XDG state; no self-delete/purge/autoremove. |
| `--help`, `--version` | Stable, offline, fast informational output | Read-only golden/performance contract. |
| shared `--dry-run`, `--apply`, `--yes`, `--json`, `--ndjson`, `--debug`, `--whitelist` | Approved shared interaction semantics and exit codes 0–6 | Ledger and command-tree exhaustiveness tests. |

The release claim is blocked if any ledger row lacks its named behavior and test evidence. “Unavailable with reason” passes only for genuinely optional capability; it cannot excuse a required supported-matrix backend.

## Related Code Files

All paths are repository-relative. Estimates guide review, not quotas.

| Action | File | Scope | Purpose and test impact |
|---|---|---:|---|
| Create | `internal/optimization/recipe.go` | ~140 LOC | Sealed recipe/audit/apply/verify/rollback contracts. |
| Create | `internal/optimization/registry.go` | ~120 LOC | Compiled two-recipe allowlist and output/root authority. |
| Create | `internal/optimization/fontconfig_linux.go` | ~200 LOC | Read-only evidence, fixed rebuild, anchored output verification/rollback. |
| Create | `internal/optimization/desktop_database_linux.go` | ~180 LOC | Generated MIME cache comparison, fixed rebuild, verification/rollback. |
| Create | `internal/optimization/recipe_test.go` | ~260 LOC | Idempotency, containment, failure restore, retained recovery, no-generic-recipe tests. |
| Create | `internal/fingerprint/capability_linux.go` | ~160 LOC | fprintd/device/enrollment/profile/password status model. |
| Create | `internal/fingerprint/fprintd_linux.go` | ~180 LOC | Bounded D-Bus probes and enrollment handoff facts; no biometric transport. |
| Create | `internal/fingerprint/pam_auth_update_linux.go` | ~180 LOC | Debian/Ubuntu fixed profile validation and postcondition. |
| Create | `internal/fingerprint/authselect_linux.go` | ~220 LOC | Fedora managed-profile check/test/backup/feature/restore flow. |
| Create | `internal/fingerprint/guidance.go` | ~100 LOC | Typed absent/unsupported/Arch/manual-enrollment guidance. |
| Create | `internal/fingerprint/fingerprint_test.go` | ~300 LOC | Distro, enrollment, unmanaged PAM, backup, password fallback, rollback tests. |
| Create | `internal/selfpackage/installation_linux.go` | ~160 LOC | Exact running-binary/package ownership and archive-origin detection. |
| Create | `internal/selfpackage/native.go` | ~180 LOC | Exact `linux-deep-clean` preview/apply requests using Phase 9 graph contracts. |
| Create | `internal/selfpackage/archive.go` | ~180 LOC | Signed metadata/artifact verification and manual update/remove guidance. |
| Create | `internal/selfpackage/selfpackage_test.go` | ~260 LOC | Ambiguous ownership, exact-package graph, no self-write/delete, state preservation. |
| Create | `internal/completion/install.go` | ~160 LOC | Bound user/system completion-install proposals with compiled destinations and digests. |
| Create | `internal/completion/install_test.go` | ~220 LOC | Destination injection, shell/version/digest drift, publish recovery, and rootless-system rejection. |
| Create | `internal/state/repair_action.go` | ~160 LOC | Narrow explicit repair executor that retains old state and durably publishes the approved rebuild. |
| Create | `internal/state/repair_action_test.go` | ~220 LOC | Wrong digest/schema, collision, interruption, retain/reopen, and no-arbitrary-path tests. |
| Create | `internal/managerexec/fingerprint_linux.go` | ~180 LOC | Fixed pam-auth-update/authselect operation specs and postconditions. |
| Create | `internal/managerexec/selfpackage_linux.go` | ~200 LOC | Dedicated exact-LDC APT/DNF5/Pacman update/remove operation specs. |
| Create | `internal/managerexec/completion_linux.go` | ~120 LOC | Fixed system completion asset/destination operation with no request-derived path. |
| Modify | `internal/privilege/helperpolicy/registry.go` | medium | Enable fingerprint, self-package, and system-completion families only after full Phase 9-style slices. |
| Modify | `internal/privilege/helperpolicy/policy.go` | medium | Independently validate enrollment/profile/password/package origin and completion asset/version/destination. |
| Modify | `internal/engine/userfs/validate.go` | medium | Admit only selected analyze files and attributable uninstall residues into existing safe actions. |
| Modify | `internal/engine/userfs/validate_test.go` | medium | Reject directory/permanent analyze requests, implicit sets, app data, ambiguous residues, and wrong app identity. |
| Modify | `internal/engine/apply_coordinator.go` | small | Compose reversible recipe rollback and family-specific apply ordering. |
| Modify | `internal/engine/recovery_coordinator.go` | small | Reconcile retained recipe snapshots and profile-manager backup outcomes. |
| Modify | `internal/application/use_cases.go` | medium | Expose optimize/fingerprint/update/remove plus analyze deletion, residue cleanup, completion install, and state-repair use cases. |
| Modify | `internal/state/recovery.go` | small | Hand an approved repair plan to the explicit repair executor without making reads mutate. |
| Modify | `internal/presenters/cli/commands.go` | small | Connect the already-frozen command names/aliases to completed use cases; do not change grammar or exit mapping. |
| Create | `docs/semantic-parity.md` | docs | Human-readable command ledger, intentional Linux substitutions, exclusions, and capability limits. |
| Create | `tests/contract/parity-ledger.yaml` | data | Machine-readable exhaustive public command/alias/options/provider matrix. |
| Create | `tests/contract/semantic_parity_test.go` | ~240 LOC | Compare ledger to Cobra tree, use cases, action registry, schemas, aliases, and test IDs. |
| Create | `tests/contract/no_ldc_executable_test.go` | ~80 LOC | Reject binary, alias, completion, package path, or docs claiming `ldc`. |
| Create | `tests/integration/optimization_rollback_test.go` | ~180 LOC | Temp-XDG rebuild, cancellation, rollback, and containment tests. |
| Create | `tests/integration/self_update_remove_test.go` | ~180 LOC | Isolated real package-database fixtures plus signed production-shaped metadata; exact target, network/dry-run, and no self-mutation tests. |
| Create | `tests/integration/remaining_user_parity_test.go` | ~240 LOC | Analyze Trash, residue ordering, completion publish recovery, and explicit state-repair flows. |
| Create | `tests/fixtures/fingerprint/manifest.json` | data | Sanitized D-Bus/pam-auth-update/authselect/unsupported fixtures with provenance. |
| Create | `tests/fixtures/selfpackage/manifest.json` | data | Native ownership/graph and signed archive metadata cases. |
| Create | `tests/vm/mutation/fingerprint-test.sh` | ~220 LOC | Sentinel-gated PAM manager, backup/restore, password fallback tests. |
| Create | `tests/vm/mutation/self-package-test.sh` | ~180 LOC | Sentinel-gated exact package update/remove and XDG state preservation. |
| Create | `tests/vm/mutation/completion-test.sh` | ~140 LOC | Sentinel-gated fixed system completion install, drift rejection, and package-asset verification. |
| Delete | None | — | This phase removes no files. |

## Interfaces and Dependency Boundaries

### Function/interface checklist

- [ ] `Recipe.Audit(ctx, TrustedCaller) (Proposal, error)` is read-only and returns evidence plus measurable postcondition.
- [ ] `Recipe.Apply(ctx, ApprovedAction) (RollbackHandle, Result)` can touch only compiled generated outputs beneath a derived caller-owned root.
- [ ] `Recipe.Verify` and `Recipe.Rollback` are recipe-specific; no arbitrary callback/command/path field exists.
- [ ] `FingerprintStatus(ctx, TrustedCaller) Status` reports device, enrollment, manager, current feature, password fallback, and reason.
- [ ] `FingerprintPlanner.Enable|Disable(Status)` accepts no PAM path/profile from the presenter.
- [ ] `InstallationDetector.Detect(runningExecutable)` uses package database/build identity and returns `Unknown` on disagreement.
- [ ] `SelfPackagePlanner.CheckUpdate|PlanUpdate|PlanRemove` targets only `linux-deep-clean` and records manager dependency/network effects.
- [ ] `ArchiveVerifier.VerifyMetadata|VerifyArtifact` rejects expiry, wrong channel/platform/version/digest/signature, rollback version, and oversized input.
- [ ] `AnalyzeAdmission.Admit(SelectedCandidate)` accepts one evidenced caller-owned regular file and emits only the existing `trash_path` action.
- [ ] `UninstallResidueAdmission.Admit(AppIdentity, SelectedCandidate)` requires exact attribution, excludes user data, and emits only existing user-filesystem actions.
- [ ] `CompletionInstaller.PlanUser|PlanSystem(TrustedCaller, Shell, GeneratedAsset)` derives a compiled destination and binds shell/version/digest; it accepts no destination.
- [ ] `StateRepairExecutor.Apply(ApprovedRepairPlan)` preserves the original, publishes the approved rebuild durably, and accepts no arbitrary path/delete field.
- [ ] `ParityLedger.Validate(CommandTree, UseCaseRegistry, ActionRegistry, SchemaRegistry)` fails missing, extra, untested, or incorrectly mutating surfaces.

### Dependency map

```text
Phases 3/4 anchored staging + rollback/recovery
  -> Phase 7 unprivileged mutation
  -> Phase 10 optimization recipes

Phase 5 capability/metrics/package ownership
  -> fingerprint + installation detection + optional-provider results

Phase 8 helper auth/policy + Phase 9 fixed executor/reconciliation
  -> fingerprint profile changes + exact self-package update/remove

Phase 7 safe userfs actions + Phase 5 attribution evidence
  -> analyze selected-file Trash + uninstall residue cleanup

Phase 6 completion bytes + trusted-caller destinations
  -> Phase 10 user publisher OR fixed system helper action

Phase 4 explicit repair plan + durable state invariants
  -> Phase 10 state repair executor

Phase 6 command tree/presenters + all use cases
  -> machine parity ledger
  -> Phase 11 public release qualification
```

Optimization and fingerprint packages do not import Cobra/TUI. `internal/fingerprint` cannot write PAM; only the helper's fixed manager executor can. `internal/selfpackage/archive.go` cannot open the running executable for write or unlink it. Analyze and uninstall residue flows do not introduce a second filesystem mutator: they must pass existing Phase 7 action validation and execution. Completion install and state repair expose narrow typed publishers, not general copy/write/path APIs. Ledger tests inspect registries; the ledger does not grant runtime authority.

## TDD Test Strategy

### RED -> GREEN -> REFACTOR

1. **RED — recipe boundary.** Write tests that attempt arbitrary executable/path/output injection, cross-root outputs, symlink/mount escape, non-idempotent second apply, rollback overwrite, and recommendation without measurable evidence.
2. **GREEN — two recipes only.** Implement read-only audits and anchored stage/rebuild/verify/rollback for fontconfig and desktop database in temp XDG roots. Keep every other recipe ID unknown.
3. **REFACTOR — shared lifecycle, not generic authority.** Extract proposal/result mechanics while leaving roots, executable, outputs, and postconditions sealed per recipe.
4. **RED — fingerprint lockout.** Add fixture tests for no device/enrollment, unmanaged PAM/authselect, missing profile, authselect check/test failure, backup failure, missing password path, injected profile/path, and failed post-verification.
5. **GREEN — status then distro slice.** Implement read-only status/guidance first; enable Debian/Ubuntu, pass VM restore/password tests, then independently enable Fedora. Arch stays guidance-only.
6. **REFACTOR — common typed facts.** Share only status/result types. Keep distro executors separate and fixed.
7. **RED — self-package scope.** Attempt a second package, generic upgrade, purge/autoremove/cascade, self-overwrite/unlink, stale/downgrade metadata, ambiguous installation, and XDG state removal.
8. **GREEN — exact native/archive behavior.** Reuse Phase 9 drift/replay/reconciliation; implement signed archive verification and manual instructions without self-mutation.
9. **RED — deferred parity authority.** Attempt analyze directory/permanent/display-path deletion, implicit residue selection, app-data cleanup, caller-supplied completion destinations/assets, rootless system install, and repair with an arbitrary path/delete or mismatched approved digest.
10. **GREEN — reuse narrow executors.** Route analyze/residues through Phase 7 actions, publish bound user completions, enable the fixed system-completion slice, and apply only Phase 4-approved state repairs with durable retain/reopen verification.
11. **REFACTOR — keep admission separate from authority.** Share typed selection/evidence facts, but leave filesystem mutation, privileged execution, and state publication inside their existing narrow owners.
12. **RED — parity ledger.** Seed one missing alias, wrong mutation flag, absent structured test ID, optional metric zero, and forbidden `ldc`; require actionable failures.
13. **GREEN/REFACTOR — exhaustive ledger.** Populate all rows, bind them to code/test registries, and generate the human doc from reviewed ledger data without making generated data authoritative for execution.

### Exact test commands

```bash
go test ./internal/optimization/... ./internal/fingerprint/... ./internal/selfpackage/...
go test ./internal/completion/... ./internal/state/... ./internal/engine/userfs/...
go test ./tests/contract/... -run 'SemanticParity|NoLDCExecutable'
go test -race ./internal/optimization/... ./internal/fingerprint/... ./internal/selfpackage/... ./internal/completion/... ./internal/state/... ./internal/engine/...
go test -tags=integration ./tests/integration/... -run 'OptimizationRollback|SelfUpdateRemove|RemainingUserParity'
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go test -tags=vmtest ./tests/vm/mutation/... -run 'Fingerprint|SelfPackage|Completion'
go test ./...
```

The VM command must abort unless both opt-in controls pass. Real fingerprint hardware/password fallback uses a separate attended lane; absence is an explicit skip with no support claim for that hardware result.

### Test matrix

| Priority | Scenario | Fixture/target | Expected result |
|---|---|---|---|
| Critical | Recipe path/executable/output authority injection | Temp XDG + malicious byte paths | Rejected before staging/exec; no outside change. |
| Critical | Rebuild failure/cancel/rollback collision | Both recipe implementations | Old output restored no-replace or retained; never overwritten/deleted. |
| High | Valid recipe twice, then rollback twice | Temp XDG golden states | Idempotent apply; exact verified state; second rollback safely skipped. |
| Critical | Fingerprint enable with no password fallback/unmanaged config | PAM/authselect fixtures + VMs | Unsupported; zero mutation. |
| High | Debian/Ubuntu enable/disable and manager failure | Ubuntu 24.04, Debian 13 PR VMs | Fixed profile only; restore on failure; password remains. |
| High | Fedora authselect check/test/backup/feature/restore | Fedora 44 PR VM | Managed feature only; backup verified; password remains. |
| High | Arch fingerprint request | Current Arch PR VM | Status/guidance only; no privileged action. |
| Critical | Update adds unrelated upgrade/removal or package changes after preview | Native manager fixtures/VMs | Drift/reject; exact LDC graph only. |
| Critical | Archive tries overwrite/self-delete or bad metadata | `selfpackage` fixtures | Manual guidance only; verification failure is terminal. |
| High | Native remove with populated XDG config/state/history | Package VM fixture | Package files removed; user data unchanged; reinstall works. |
| Critical | Analyze deletion selects directory, special file, implicit set, or permanent mode | Userfs fixtures + temp XDG | Admission rejected; valid explicit regular file uses Trash only. |
| Critical | Uninstall residue lacks exact app attribution or is user data | Provider evidence fixtures | Rejected individually; no broad app-data/root sweep. |
| Critical | Completion request injects destination/asset or changes after preview | Temp XDG + system VM | Rejected; only compiled destination and bound bytes publish durably. |
| Critical | State repair plan changes digest/schema/path or publication is interrupted | Corrupt/recoverable state fixtures | Original retained; no unapproved replacement; recovery remains explicit. |
| Critical | Command/alias/options/exit/schema registry differs from ledger | Mutated contract fixture | Contract test identifies exact missing/extra mismatch. |
| High | Optional provider absent/permission denied/malformed | Flatpak/Snap/fprintd/UPower/GPU/paccache fixtures | Typed unavailable or `null`; no fabricated zero/success. |
| Medium | Human/JSON/NDJSON and TTY/no-TTY for every ledger row | Golden CLI harness | Same semantic plan/result; no structured-output decoration/prompts. |

## Implementation Steps

1. Add the machine parity ledger schema and a failing exhaustiveness test against the Phase 6 command tree, use-case registry, action registry, aliases, shared flags, schemas, and test IDs.
2. Implement the sealed optimization recipe interface and negative authority tests. Add the fontconfig recipe, prove audit/idempotency/rollback, then add desktop database with the same gate. Leave all unsafe/non-measurable ideas recommendation-only or absent.
3. Add fprintd/device/enrollment/password/profile capability status and structured guidance. Do not add mutation yet.
4. Add fixed Debian/Ubuntu pam-auth-update enable/disable with fresh helper validation, VM backup/restore, and password fallback tests; enable its registry entry only after review.
5. Add fixed Fedora authselect check/test/backup/enable/disable/restore, then its VM and real-hardware password-fallback gates. Keep Arch read-only guidance.
6. Implement installation-origin detection and exact native update/remove planning. Add the dedicated self-package helper family using the complete Phase 9 security slice and manager-specific drift/replay rules.
7. Implement bounded archive release-metadata/artifact verification and exact manual replacement/removal guidance. Prove no code path writes/unlinks the running artifact.
8. Add analyze deletion and uninstall-residue admission over the existing Phase 7 action types. Prove explicit selection, exact app attribution, manager-before-residue ordering, user-data exclusions, and independent cleanup of already-orphaned evidenced residues.
9. Add the completion installer: compiled caller destinations and durable user publication first, then a separate fixed system-helper slice over root-owned versioned assets. Reject all caller destinations and rootless system requests.
10. Add the explicit state-repair executor over Phase 4 approved repair plans. Retain the original, publish/reopen/verify durably, exercise interruption recovery, and prove history reads remain non-mutating.
11. Fill the ledger row by row, including aliases, optional-provider reasons, structured/interactive modes, exit statuses, exclusions, and exact owning test. Generate/review `docs/semantic-parity.md`.
12. Run focused, contract, race, integration, default full-suite, and four-VM PR tests. Record attended fingerprint hardware evidence separately; never convert an absent lane into a pass.
13. Freeze the public command/parity contract for Phase 11. Any missing required supported-matrix row blocks packaging qualification.

## Success Criteria

- [ ] `optimize`/`optimise` offers only evidence-backed health results, the two sealed reversible recipes, and honestly labeled Phase 9 maintenance.
- [ ] Both recipes are idempotent, confined to caller-owned generated outputs, verify measurable postconditions, and rollback/retain safely under failure.
- [ ] No sysctl/page-cache/governor/service/network/firewall/general-upgrade/speed-boost behavior exists.
- [ ] Fingerprint status/enrollment guidance works without privilege; enable/disable uses only pam-auth-update or authselect where qualified.
- [ ] Password fallback is verified before and after mutation; Arch and ambiguous/unmanaged hosts remain guidance-only.
- [ ] Native update/remove targets only `linux-deep-clean`, preserves user state, and inherits exact graph drift/reconciliation guarantees.
- [ ] Rootless archive update/remove never overwrites or deletes itself and accepts only verified production-shaped metadata/artifacts.
- [ ] Analyze deletes only an explicitly selected evidenced regular file through the existing Trash action; no directory, special-file, permanent, display-path, or implicit-set authority exists.
- [ ] Uninstall may remove only separately selected attributable residues, orders them after exact manager removal, and never selects config, credentials, documents, histories, databases, or ambiguous app data.
- [ ] User completion install publishes bound generated bytes only at compiled caller destinations; system install is a fixed privileged slice and is unavailable to rootless archives.
- [ ] State repair applies only an explicit approved Phase 4 plan, retains the original, verifies durable replacement, and never makes a history read mutate.
- [ ] Every approved command, alias, shared option, exit code, interactive/structured mode, optional capability, and exclusion has a passing parity-ledger row.
- [ ] No `ldc` executable, alias, package path, completion, or misleading documentation exists.
- [ ] Default suite remains hermetic/offline/unprivileged; VM/hardware mutation is doubly gated and disposable.
- [ ] Focused, contract, race, integration, full default, and four-VM PR gates pass with no unresolved indeterminate state.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback/containment |
|---|---|---|
| “Optimization” becomes folklore or broad tuning | Two sealed recipes, evidence/postcondition/rollback required, explicit exclusions | Disable recipe ID; retain read-only audit/recommendation. Never add a generic runner. |
| Cache rebuild escapes XDG root or loses old output | Phase 3 anchored staging, fixed output set, no-replace restore, integration races | Restore from handle or retain recovery item; report no verified benefit. |
| Fingerprint change locks out sudo/login | Require enrollment + managed profile + backup + password path; VM and attended hardware tests | Manager-owned restore; if unverifiable mark indeterminate/manual intervention and disable distro slice. |
| Update broadens to unrelated packages | Exact self-package family and typed graph comparison | Drift/re-plan; no general upgrade fallback. |
| Helper/package removes itself before response | Dispatch-aware durable result and post-removal manager reconciliation | Report indeterminate if response lost; user state remains; use manager query/manual reinstall. |
| Archive metadata/signing changes | Versioned bounded verifier and rollback-version check | Refuse update, keep current binary, print verification error. |
| Completion install becomes arbitrary privileged copy | Compiled shell destinations, package-owned versioned assets, digest binding, no request path | Reject drift/injection; retain prior user file or leave system package asset unchanged. |
| State repair destroys the only recoverable copy | Explicit approved digest/schema, retain-before-publish, fsync/reopen verification | Preserve original/recovery handle; report indeterminate and require a new plan. |
| Ledger says parity while behavior is untested | Executable command/use-case/action/schema/test-ID cross-check | Release blocked until row and evidence are complete; no partial full-parity claim. |

Rollback is capability-granular: disable the affected recipe, distro fingerprint mutation, package channel, or optional provider while retaining truthful read-only status. Do not undo explicit exclusions or silently mark unsupported behavior as success.

## Phase Exit/Handoff

Phase 11 receives a frozen public command tree and semantic parity ledger, qualified optional-capability behavior, tested fingerprint/self-package families, completed analyze/residue/completion/repair slices, and no unresolved supported-matrix gaps. Handoff includes recipe rollback evidence, distro fingerprint capability table, exact update/remove graphs, archive verification fixtures, deferred user-action recovery evidence, and the ledger-to-test report. Packaging may expose only capabilities represented in this ledger; a support/full-parity claim remains prohibited until Phase 11 completes every required package, VM, supply-chain, race, and performance gate.
