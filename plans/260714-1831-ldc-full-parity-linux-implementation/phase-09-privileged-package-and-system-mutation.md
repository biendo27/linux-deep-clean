---
phase: 9
title: "Privileged Package and System Mutation"
status: pending
priority: P1
effort: "4w"
dependencies: [8]
---

# Phase 9: Privileged Package and System Mutation

## Overview

Enable the Phase 8 reject-only helper one privileged action family at a time. Deliver fixed package-manager and journald execution, point-of-use re-simulation, typed drift comparison, bounded results, and mandatory reconciliation for uncertain outcomes. The unprivileged `ldclean` process remains the planner and presenter; it never runs as root and never gains a generic command primitive.

Authoritative context: [approved design](../reports/260714-1754-ldc-linux-native-design-report.md), [filesystem/privilege research](./research/02-filesystem-and-privilege-safety.md), [provider research](./research/03-linux-provider-contracts-and-qualification.md), and [scout report](./reports/scout-report.md).

## Requirements

### Functional

- Preserve immutable preview then explicit apply. `--yes` never implies `--apply`; `--dry-run` causes no authorization, mutation, or network access.
- Convert each executable-backed operation enum to exactly one compiled absolute executable and argument template. Build `argv` and a minimal environment from scratch; never use a shell, `PATH` lookup, caller flags, caller environment, or display text as authority.
- Permit at most one privileged `ActionFamily` in a helper request's `ExecutionActionIDs`. The request still carries the complete canonical plan body and subset digest frozen in Phase 8. Mixed-family, missing, duplicate, reordered, unselected, cross-plan, or dependency-ineligible execution IDs fail before authorization/execution; a family is enabled only after its validator, runner mapping, negative tests, reconciliation probe, and VM slice pass together.
- Re-probe distro/release, manager capability, lock/state, exact target identity, protected-package rules, and network disclosure immediately before dispatch.
- Normalize fresh evidence into the Phase 2 `domain.TransactionGraph`; compare exact nodes, edges, versions, architectures/scopes, reasons, maintainer-script disclosure, network requirement, and provider guarantee against the graph sealed in the plan. Observable difference returns exit 3/`drifted`; it never degrades to best effort.
- Classify any dispatched transaction whose terminal state cannot be proved as `indeterminate` with `reconciliation_required=true`. Never auto-retry it.
- Use only locally built `linux-deep-clean-test-*` native packages and local Flatpak/Snap fixtures in destructive VM tests. Never select an arbitrary guest package.
- Preserve the supported mutation gate: systemd, kernel 5.15+, approved distro/release/architecture, and local ext4/XFS/Btrfs only. Unsupported hosts remain read-only.

### Provider restrictions

| Family | Fresh evidence and only permitted apply | Explicit refusal rules |
|---|---|---|
| APT/dpkg removal | Re-query exact name, architecture, version, status, Essential/hold facts; immediately repeat C-locale `apt-get --simulate remove <name>:<arch>`; apply only fixed `/usr/bin/apt-get -y remove -- <name>:<arch>`. | No purge, autoremove, fix-broken, wildcard, option-looking ID, allow-remove-essential, allow-change-held-packages, or graph drift. Refuse Essential/held/protected/running-kernel/manager/helper dependencies. |
| APT cache | Bounded archive scan plus manager capability; apply only `/usr/bin/apt-get clean`. | No raw deletion under `/var/cache/apt`; never combine with package removal. |
| DNF5/RPM removal | Re-query exact NEVRA and protection facts. Create a root-owned 0700 private transaction directory, run fixed DNF5 remove storage with `--no-autoremove`, hash/parse the stored graph, then apply that same artifact only through DNF5 replay. | DNF4 unsupported. No generic upgrade, autoremove, `--skip-broken`, `--allowerasing`, protected/running-kernel removal, replay edits, or replay after installed-version drift/repository mismatch. |
| DNF5 cache | Apply only `/usr/bin/dnf5 clean packages`. | Never use `clean all`, expire metadata, or delete RPM/DNF databases directly. |
| Pacman removal | Re-query exact installed name/version; repeat fixed `pacman -R --print --print-format ... -- <name>`; apply only `/usr/bin/pacman -R --noconfirm -- <exact-name>`. | No recursive/cascade/unneeded flags (`-s`, `-c`), wildcard, system upgrade, ignored/protected/running-kernel removal, or touching `db.lck`. Dependency refusal is terminal, not permission to widen the graph. |
| Pacman cache | Require `/usr/bin/paccache`; compare `--dryrun --keep 3` candidates; apply exactly `--remove --keep 3`. | Missing `paccache` is unsupported. Never reduce keep count below three or delete cache entries directly. |
| Flatpak | Re-probe exact ref, commit, origin, installation, and `--user`/`--system` scope; invoke fixed `/usr/bin/flatpak uninstall <scope> --noninteractive -- <exact-ref>`. System scope uses the helper; user scope uses a closed main-process `ActionExecutor` as the trusted caller and never launches privilege. | No `--unused`, `--delete-data`, wildcard/partial ref, scope substitution, or commit drift. Absence of a real dry-run is disclosed as exact-target rather than transaction-bound preview. |
| Snap | Use the versioned snapd REST API on compiled `/run/snapd.socket`; map `SnapRemove` to a fixed method/path/body built from exact name, snap ID, and revision, then poll only the returned change ID to a terminal state. | No caller URL/body, CLI fallback, wildcard, `purge`, broad prune, or silent suppression of snapd's automatic data-snapshot effect. Lost/non-terminal change state is indeterminate. |
| journald | Re-probe C-locale disk use, run fixed `/usr/bin/journalctl --rotate`, then exactly one bounded `--vacuum-size=`, `--vacuum-time=`, or `--vacuum-files=` selector from the typed action; verify post-use. | Never delete journal files, edit retention config, combine selectors, stop/restart units, or promise an exact deletion list. Preview is explicitly a bound under concurrent writes. |

### Non-functional and security

- Default tests stay hermetic, offline, unprivileged, and workstation-safe. Real mutation requires `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, and a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token.
- Keep Phase 8 framing/auth/CBOR budgets frozen. Manager output is separately bounded by bytes and time, redacted, and represented by hash/length when truncated.
- Close all non-protocol descriptors; use cwd `/`, umask 077, explicit `LC_ALL=C`/`LANG=C`, conservative rlimits, and no proxy, `LD_*`, XDG, bus, pager/editor, Git, SSH, or language-runtime variables.
- A manager transaction is not reversible. DNF5 replay binds a planned operation; it is not rollback. History must say attempted, verified, drifted, interrupted, or unknown honestly.

## Architecture

### Apply flow

```text
immutable Plan + digest
  -> unprivileged family partition and explicit confirmation
  -> Phase 8 launcher/auth/framed full-plan + exact-family-subset request
  -> helper support + caller + action validation
  -> manager-specific fresh probe/simulation
  -> typed graph equality gate
  -> fixed operation registry (or fixed snapd request registry)
  -> bounded dispatch of one family
  -> provider-specific verification
  -> terminal Result OR indeterminate + typed reconciliation probe
  -> durable history; unresolved family is blocked from reapply
```

`managerexec.OperationRegistry` is the only executable authority. Its entries contain a constant operation enum, constant absolute path, constant flags, typed placement slots, allowed ID grammar, time/output budgets, and expected postcondition. Runtime executable discovery may report capability but never changes the compiled path. Snap uses a parallel closed `SnapRequestRegistry`; there is no general HTTP/Unix-socket client exposed to requests.

The shared `domain.TransactionGraph` uses stable typed fields rather than manager prose: operation, object kind, exact identity/version/scope, dependency edges and reasons, protected/essential state, script/restart/network disclosures, provider evidence digest, and guarantee. `managerexec` owns only fresh normalization and comparison. Comparison is order-independent but field-exact. Parser failure, missing required evidence, unknown operation, or a newly introduced graph node fails closed.

User-scoped Flatpak is deliberately outside the helper. `engine/usermanager.FlatpakExecutor` implements the Phase 4 executor port over a narrow `managerexec.UserFlatpak` interface that exposes only exact user-scope reprobe/uninstall/verify/reconcile. It runs with the original numeric caller, cannot select another scope or operation, and shares the same plan/graph/result gates as system scope.

### Family activation order

1. journald bounded vacuum;
2. manager-owned package caches (APT, DNF5, Pacman);
3. APT exact-package removal;
4. DNF5 stored-transaction replay;
5. Pacman exact-package removal;
6. Flatpak exact-ref removal (system scope privileged, user scope unprivileged);
7. Snap exact-revision asynchronous removal.

Do not start the next family until the preceding slice passes unit, negative integration, interruption/reconciliation, and applicable PR-VM tests. A helper build exposes only the already-qualified registry entries.

### Indeterminate state machine

`prepared -> dispatched -> verifying -> success|failed|drifted` is the normal path. A signal/timeout after dispatch, helper death, malformed/lost response, snapd non-terminal change, manager database recovery state, or unverifiable postcondition transitions to `indeterminate`. The durable record includes a manager-specific read-only `ReconciliationProbe`. While unresolved, the same family/target is blocked. Reconciliation may conclude `applied`, `not_applied`, or `manual_intervention`; only `not_applied` plus a newly generated plan can be attempted again.

## Related Code Files

All paths are repository-relative. Estimates guide review, not quotas.

| Action | File | Scope | Purpose and test impact |
|---|---|---:|---|
| Create | `internal/managerexec/operation.go` | ~120 LOC | Closed operation/family enums and typed command/request specs; exhaustive registry tests. |
| Create | `internal/managerexec/registry_linux.go` | ~180 LOC | Enum-to-absolute-executable/argv registry and fixed snapd request registry; authority-expansion negatives. |
| Create | `internal/managerexec/environment_linux.go` | ~100 LOC | Fresh locale/root environment, cwd/umask/FD/rlimit policy; exact environment tests. |
| Create | `internal/managerexec/runner_linux.go` | ~180 LOC | Direct bounded exec, cancellation, output hashing/redaction, dispatch facts; timeout/EOF tests. |
| Create | `internal/managerexec/graph.go` | ~160 LOC | Fresh-output normalization and exact comparison over `domain.TransactionGraph`; property and fuzz tests. |
| Create | `internal/managerexec/apt_linux.go` | ~220 LOC | Exact dpkg/APT reprobe, simulation normalization, apply and cache specs. |
| Create | `internal/managerexec/dnf5_linux.go` | ~240 LOC | Exact NEVRA probe, private stored transaction, artifact verification, replay and cache specs. |
| Create | `internal/managerexec/pacman_linux.go` | ~220 LOC | Exact package/cache preview, drift checks, apply specs. |
| Create | `internal/managerexec/flatpak_linux.go` | ~160 LOC | Exact ref/commit/origin/scope check and fixed uninstall spec. |
| Create | `internal/managerexec/snapd_linux.go` | ~200 LOC | Fixed Unix-socket REST remove/change client with bounds and exact identity validation. |
| Create | `internal/managerexec/journald_linux.go` | ~160 LOC | Disk-use parser, rotate/vacuum bounds, verification. |
| Create | `internal/managerexec/reconciliation.go` | ~180 LOC | Typed probe builders and result classification for every family. |
| Create | `internal/engine/usermanager/flatpak.go` | ~140 LOC | Closed unprivileged user-scope Flatpak `ActionExecutor`; never launches helper. |
| Create | `internal/engine/usermanager/flatpak_test.go` | ~180 LOC | UID/scope, exact-ref, drift, no-privilege, and reconciliation tests. |
| Create | `internal/managerexec/managerexec_test.go` | ~300 LOC | Registry, environment, runner, graph, drift, and result unit/property tests. |
| Create | `internal/managerexec/parsers_fuzz_test.go` | ~120 LOC | Fuzz all simulation/result parsers with bounded resources. |
| Modify | `internal/privilege/helperpolicy/registry.go` | small | Register one qualified family at a time; preserve closed default rejection. |
| Modify | `internal/privilege/helperpolicy/policy.go` | medium | Re-probe, graph equality, same-family, protected-object, and reconciliation gates. |
| Modify | `cmd/linux-deep-clean-helper/main.go` | small | Compose policy with bounded manager executor; no orchestration/provider imports. |
| Modify | `internal/engine/apply_coordinator.go` | medium | Partition privileged work by family and map action results without cross-family transaction claims. |
| Modify | `internal/engine/recovery_coordinator.go` | medium | Block unresolved family/targets and invoke typed read-only reconciliation probes. |
| Modify | `internal/application/use_cases.go` | small | Connect existing `ApplyPlan`/`Reconcile` use cases to manager-family results without presenter authority. |
| Create | `tests/contract/manager_operation_registry_test.go` | ~180 LOC | Prove enum coverage, fixed paths/flags/env, and absence of generic execution. |
| Create | `tests/integration/manager_drift_test.go` | ~220 LOC | Fixture-backed fresh-preview equality/drift and no-dispatch assertions. |
| Create | `tests/integration/manager_reconciliation_test.go` | ~180 LOC | Kill/EOF/timeout/lost-response state-machine tests without host mutation. |
| Create | `tests/fixtures/manager-apply/apt/manifest.json` | data | Sanitized exact/multiarch/held/essential/drift/lock cases with provenance. |
| Create | `tests/fixtures/manager-apply/dnf5/manifest.json` | data | Stored transaction/replay/protected/kernel/repository-drift cases. |
| Create | `tests/fixtures/manager-apply/pacman/manifest.json` | data | Print graph/cache/IgnorePkg/dependency/lock/drift cases. |
| Create | `tests/fixtures/manager-apply/flatpak/manifest.json` | data | Duplicate scope/ref/commit/active/lock cases. |
| Create | `tests/fixtures/manager-apply/snap/manifest.json` | data | REST/change/snapshot/daemon/interruption cases. |
| Create | `tests/fixtures/manager-apply/journald/manifest.json` | data | Versioned C-locale usage/vacuum/concurrent-write cases. |
| Create | `tests/vm/packages/debian/build-test-packages.sh` | ~120 LOC | Build only `linux-deep-clean-test-*` `.deb` dependency graphs locally. |
| Create | `tests/vm/packages/rpm/linux-deep-clean-test-packages.spec` | ~140 LOC | Build local leaf/dependent/protected-style RPM fixtures. |
| Create | `tests/vm/packages/arch/PKGBUILD` | ~120 LOC | Build local Arch test packages and cache versions. |
| Create | `tests/vm/packages/flatpak/build-test-repository.sh` | ~100 LOC | Build an offline local exact-ref/commit fixture. |
| Create | `tests/vm/packages/snap/build-test-snap.sh` | ~100 LOC | Build local `linux-deep-clean-test-snap` revisions. |
| Create | `tests/vm/mutation/package-and-system-test.sh` | ~300 LOC | Sentinel-gated locks, drift, removal, cache, Flatpak/Snap, journal, and interruption cases. |
| Delete | None | — | This phase removes no files. |

## Interfaces and Dependency Boundaries

### Function/interface checklist

- [ ] `OperationRegistry.Lookup(Operation) (CommandSpec, bool)` returns only compiled specs; no string/executable lookup API exists.
- [ ] `CommandSpec.Build(Target) (path string, argv []string, error)` accepts typed IDs and cannot accept caller flags.
- [ ] `FreshEnvironment(Operation, TrustedCaller) []string` returns an exact allowlist with deterministic ordering.
- [ ] `Runner.Run(ctx, CommandSpec) DispatchResult` records whether dispatch occurred and bounds time/stdout/stderr.
- [ ] `NormalizeSimulation(Provider, []byte) (domain.TransactionGraph, error)` is pure and bounded.
- [ ] `CompareTransactionGraph(planned, fresh domain.TransactionGraph) DriftReport` is order-independent and field-exact.
- [ ] `engine/usermanager.FlatpakExecutor` accepts only `remove_flatpak_ref` with user scope and the original caller, uses the narrow fixed Flatpak operation, and never calls the helper/launcher.
- [ ] `Preflight.Validate(Action, TrustedCaller) (ValidatedOperation, error)` repeats support/protection/lock/evidence checks.
- [ ] `Reconciler.Probe(ctx, ReconciliationProbe) ReconciliationResult` is read-only and never retries mutation.
- [ ] `SnapRequestRegistry` fixes socket, method, path grammar, body keys, poll endpoint, and budgets.
- [ ] Every enabled action enum has one validator, one operation mapping, one postcondition, one reconciliation probe, and one arbitrary-authority negative test.

### Dependency map

```text
Phase 2 domain/action/result/TransactionGraph schemas
  -> Phase 4 apply/history/recovery contracts
  -> Phase 5 read-only provider evidence and normalized fixtures
  -> Phase 8 protocol + trusted caller + reject-only registry
  -> Phase 9 managerexec normalization/comparison
       -> per-family helper activation for system scope
       -> closed usermanager executor for Flatpak user scope

helper main -> protocol, helperauth, helperpolicy, managerexec, capability/domain only
providers -> domain.TransactionGraph only
application/engine -> domain results + narrow executor ports
presenters -> application results only
```

The helper must not import `internal/application`, `internal/providers`, `internal/state`, `internal/presenters`, Cobra, Bubble Tea, or Lip Gloss. Providers and presenters must not call runner/protocol primitives or import `managerexec`. The main-process usermanager adapter receives only the closed user-Flatpak interface. `managerexec` must not expose `exec.Command`, arbitrary socket paths, or generic arguments through an interface.

## TDD Test Strategy

### RED -> GREEN -> REFACTOR

1. **RED — authority boundary first.** Add table/property tests proving unknown enums, relative/writable executables, leading-hyphen IDs, caller flags/env, shell metacharacters, mixed/mismatched execution subsets, partial-plan encoding, extra FDs, and snap URLs/bodies are rejected. Confirm the Phase 8 helper still rejects all requested actions.
2. **GREEN — minimal registry/runner.** Implement immutable specs, exact environment, direct exec/socket dispatch, output/time bounds, and dispatch facts until only the new tests pass. Keep all family validators disabled.
3. **REFACTOR — shared typed seams.** Extract ID grammars, graph normalization/comparison helpers over the existing domain type, and runner budgets without adding a generic command builder or second transaction-graph type. Run architecture/import tests after each extraction.
4. Repeat **RED -> GREEN -> REFACTOR** for each activation-order family: parser/golden and drift tests first; validator/spec/probe second; disposable-VM success/negative/interruption tests third; registry enablement last.
5. For Flatpak, add a separate RED/GREEN slice proving user scope runs through the unprivileged executor with no launcher, while system scope alone reaches the helper; both reuse the same domain graph and exact-target guarantee.
6. **RED — uncertainty.** Kill after dispatch, truncate helper response, hold locks, interrupt manager, and leave Snap changes non-terminal; assert `indeterminate`, durable reconciliation, and no retry.
7. **GREEN — reconciliation.** Implement read-only probes and target-family blocking. A failed pre-dispatch command remains `failed`; a post-dispatch unknown remains `indeterminate`.
8. **REFACTOR — result consistency.** Deduplicate stable error mapping only after all provider matrices pass; retain provider-specific guarantees in structured output.

### Exact test commands

```bash
go test ./internal/managerexec/... ./internal/privilege/helperpolicy/... ./tests/contract/...
go test -race ./internal/managerexec/... ./internal/privilege/... ./internal/application/...
go test -tags=integration ./tests/integration/... -run 'Manager(Drift|Reconciliation|Authority)'
go test ./internal/managerexec/... -run '^$' -fuzz 'Fuzz(Normalize|ManagerResult)' -fuzztime=60s
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go test -tags=vmtest ./tests/vm/mutation/... -run PackageAndSystem
```

The final command is run only inside the expected snapshot-backed guest after the harness validates the token-bound `/run/linux-deep-clean/disposable-guest`. Host-side, wrong-image, missing-token, or sentinel-mismatch execution must fail before invoking authorization or a manager.

### Test matrix

| Priority | Scenario | Fixture/target | Expected result |
|---|---|---|---|
| Critical | Unknown enum/path/argv/env/socket expansion | Contract tables + fuzz corpus | Rejected before dispatch; registry authority unchanged. |
| Critical | Planned vs fresh graph adds/removes/changes one node, edge, version, scope, script, or network fact | Every `manager-apply/*/manifest.json` | `drifted`, exit 3, zero dispatch. |
| Critical | Essential/protected/held/running-kernel/manager/helper dependency target | Local `linux-deep-clean-test-*` graph plus sanitized fixtures | Rejected even if main plan requests it. |
| Critical | Kill/timeout/EOF after dispatch | Deterministic subprocess fixture exercising the real runner contract; real manager in VM | `indeterminate`, reconciliation required, no automatic retry. |
| High | APT exact/multiarch remove and clean | Ubuntu/Debian PR VMs | Only exact package/cache effect; no purge/autoremove. |
| High | DNF5 stored replay and installed/repo drift | Fedora PR VM | Same artifact replays or fails closed; no fresh broadened solve. |
| High | Pacman dependency refusal and paccache keep-three | Arch PR VM | No cascade; lock untouched; missing paccache unsupported. |
| High | Flatpak duplicate user/system ref and commit drift | Offline local repo | Exact scope/ref only; system privilege separated. |
| Critical | User-scope Flatpak apply | Offline local user install plus launcher spy | Original caller only; zero pkexec/sudo/helper calls; exact-ref result/reconciliation. |
| High | Snap snapshot/change success, failure, daemon loss | Local snap revisions | Snapshot disclosed; exact revision; unknown terminal state indeterminate. |
| High | journald concurrent writes, permissions, all three bounds | Disposable persistent/volatile journals | Bounded rotate/vacuum only; verified or honestly bounded result. |
| Medium | Locale/output version/truncation/resource limit | Sanitized release fixtures | Deterministic parse or unsupported; no prose-derived authority. |
| Medium | Dry-run on every family | Instrumented launcher/network/filesystem guard | Zero prompts, network, manager dispatch, or writes. |

Fixture manifests must record distro, release, architecture, manager version, locale, capture command, sanitization, and SHA-256. Raw manager bytes live beside each manifest; production code has no fixture-specific branch.

## Implementation Steps

1. Freeze the Phase 8 protocol/auth baseline and add exhaustive disabled registry entries for every Phase 9 family. Prove the installed helper still rejects them.
2. Implement operation/family enums, typed IDs, exact executable/request registries, clean environment, bounded runner, and architecture tests. Review this as a privilege-surface change before any family activation.
3. Implement fresh normalization and exact comparison over Phase 2 `domain.TransactionGraph`, plus parser budgets and fixture provenance. Providers and helper share only that domain contract; neither imports the other or invents a second graph.
4. Implement dispatch-aware results and the durable reconciliation blocker/probes. Complete interruption tests before real mutation.
5. Activate journald: typed bound, fixed rotate/vacuum sequence, honest bounded preview, post-use verification, and disposable-VM tests.
6. Activate APT/DNF5/Pacman cache families separately. Native cleanup commands own caches; raw filesystem deletion remains impossible.
7. Build local native fixture packages. Activate APT exact removal with immediate re-simulation and full protected/hold/multiarch/lock/interruption coverage.
8. Activate DNF5 exact-NEVRA removal using a private stored transaction and replay. Verify artifact integrity and reject installed/repository/protection drift.
9. Activate Pacman exact removal with non-recursive semantics and dependency refusal. Verify cache keep-three and never manipulate `db.lck`.
10. Build local Flatpak and Snap fixtures. Activate user-scope Flatpak through the closed unprivileged executor first, system-scope Flatpak through the helper second, then Snap exact-revision REST removal and async reconciliation.
11. Integrate family partitioning with apply/history/structured results. Confirm mixed-family plans reuse the same complete plan body but require separate explicitly disclosed helper invocations with distinct subset digests, and unresolved targets block only their affected family/target.
12. Run focused, race, integration, fuzz, architecture, and four-VM PR tests. Perform a privilege/security review of every enabled enum and its negative test before Phase 10.

## Success Criteria

- [ ] Main `ldclean` refuses EUID 0; all discovery/preview remains unprivileged.
- [ ] Every executable operation is enum -> compiled absolute path/fixed argv/fresh environment; Snap has an equally closed socket request mapping.
- [ ] Shells, `PATH` lookup, inherited env, caller argv/URL/body, and arbitrary executable paths are impossible by interface and negative test.
- [ ] Helper accepts only one action family in each request's execution subset and only families whose complete TDD/VM slice passed; the full digest-bound plan may contain other-family or unprivileged actions.
- [ ] APT, DNF5, Pacman, Flatpak, Snap, and journald enforce every restriction in this phase.
- [ ] User-scope Flatpak executes as the original caller with zero authorization/helper calls; system scope alone uses the helper, and both share one domain transaction graph contract.
- [ ] APT/Pacman immediate re-simulation and DNF5 stored-transaction replay reject typed graph drift before dispatch.
- [ ] Only local `linux-deep-clean-test-*` packages/local app fixtures are mutated in VMs.
- [ ] Post-dispatch uncertainty is durable `indeterminate` with reconciliation required; no automatic retry or false rollback claim exists.
- [ ] Default tests are offline, hermetic, unprivileged, and safe; destructive tests require both `vmtest` and the validated guest sentinel.
- [ ] Focused tests, race tests, integration tests, parser fuzzing, architecture tests, and the four-VM PR lane pass.
- [ ] Structured output and history distinguish provider guarantee, estimated/attempted/verified effect, drift, failure, interruption, and unknown state.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback/containment |
|---|---|---|
| Fixed argv accidentally broadens manager behavior | Exhaustive enum/spec snapshots, option-looking ID rejection, per-family security review | Disable that registry entry; helper returns unsupported. Never replace with generic execution. |
| Preview/apply race changes dependency graph | Immediate APT/Pacman re-simulation; DNF5 stored replay; exact Flatpak/Snap reprobe | Return drift and require a new plan. No best effort. |
| Manager exits without known final state | Record dispatch point, bound output/time, persist typed reconciliation before response | Block reapply; run read-only reconciliation or direct user to manager-native repair. |
| Maintainer script/service effects exceed estimates | Include disclosed script/restart/network facts in graph; verify observable postcondition | Report partial/indeterminate. Package reinstall is guidance, never labeled rollback. |
| Parser changes with manager release | Version/capability gate, raw provenance fixtures, fuzzing, supported-VM qualification | Disable mutation for that manager version while retaining read-only discovery. |
| Mixed managers create misleading transactionality | One family per helper request; durable action-level results | Completed families remain recorded; no cross-manager rollback claim. |
| Test accidentally targets host packages | Build tag, opt-in env, fixed root-owned sentinel, package-name allowlist | Harness aborts before authorization/manager access; reset guest snapshot after each case. |

Rollback of the phase means disabling individual registry entries and shipping read-only/provider preview behavior. Do not delete history, auto-reinstall packages, or weaken a guard to restore availability.

## Phase Exit/Handoff

Phase 10 receives a reviewed manager executor, exact provider guarantees, stable reconciliation states, and enabled action-family registry. Handoff artifacts must include the passing four-VM report, fixture provenance hashes, per-family security-review checklist, and a list of any provider version left read-only. Phase 10 may compose these actions into parity commands, but must not add a new privileged enum or relax a provider rule without repeating this phase's validator/executor/reprobe/result/negative-test/VM slice.
