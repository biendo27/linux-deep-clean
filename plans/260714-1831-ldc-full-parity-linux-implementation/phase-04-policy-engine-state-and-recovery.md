---
phase: 4
title: "Policy Engine, State, and Recovery"
status: pending
priority: P1
effort: "15-20 engineer-days"
dependencies: [3]
---

# Phase 4: Policy Engine, State, and Recovery

## Overview

Build the presenter-neutral application core: capability/provider registries, monotonic policy, deterministic selection/planning, an explicit-apply coordinator, private XDG configuration/state/history, and crash reconciliation. With no executors registered, apply remains deliberately fail-closed/preview-only at phase exit.

This phase creates real durable state and pure policy/planning behavior, not dummy providers or mutation mocks. Context: [configuration/state design](../reports/260714-1754-ldc-linux-native-design-report.md#configuration-state-and-privacy), [scout](./reports/scout-report.md#phase-4--policy-engine-state-and-recovery).

## Requirements

### Functional

- Capability probes produce typed facts and reasons; support is never inferred from distro name alone. Snapshots bind kernel/distro/release/architecture/systemd/procfs/mount/backend/privilege facts and distinguish read-only, mutation-supported, and unavailable.
- Providers implement a leaf `providerapi.Provider` contract: descriptor plus offline/read-only `Discover(ctx, request, sink)`. Output is data only. Central policy alone classifies/admit/rejects candidates.
- Protection entries (`--whitelist` compatibility name, “protection list” internally) may remove candidates/actions only. They cannot add roots, providers, capabilities, action kinds, or mutate risk/reversibility; adding protection is monotonic.
- Planner deterministically resolves conflicts/dependencies, rejects unsupported/ambiguous/unselected non-interactive targets, freezes capability/config digests and provider guarantees, separates size kinds, canonicalizes through Phase 2, and emits an immutable plan before any authorization/apply.
- `ApplyPlan` requires an explicit apply request and exact plan digest. `--yes` will suppress only the later second confirmation; it never means apply. Dry-run routes only through inventory/plan and guarantees zero state mutation, authorization, network, or executor calls.
- Apply coordinator rechecks config digest, capabilities, expiry, outstanding recovery, action preconditions, and executor support at point of use; persists run intent before dispatch and terminal/reconciliation facts after. No executor in this phase means `unsupported`, never a simulated success.
- Cancellation stops before the next safe dependency group, records completed actions, and marks a dispatched-but-unverifiable action `indeterminate` with reconciliation. Partial independent continuation follows explicit action dependencies only.
- Persist private XDG layout: config/protection at config home; plans/results/history/recovery at state home; rebuildable indexes at cache home; ephemeral authorization material at runtime. Respect specification fallbacks, modes 0700/0600, no-follow/rooted writes, and never classify these paths as cleanup candidates.
- History is append-oriented/versioned and private. Reads report a corrupt/truncated tail without changing it. Repair is a separate previewed `repair_state` action that preserves the original and would publish a verified replacement only after a later explicit apply.
- `LDCLEAN_NO_HISTORY=1` or config may disable optional history, but never the minimal recovery ledger needed to avoid unsafe replay. Store no credentials/tokens and make no telemetry/network request.

### Non-Functional

- Before adding TOML, select/audit a decoder for license, maintenance, vulnerability history, fuzzing, duplicate/unknown-field behavior, and strict decoding. Unknown keys/type coercions reject; do not silently ignore misspelled policy.
- Hash the canonical typed effective configuration, not TOML formatting. Equivalent config yields the same digest; a semantic change expires the plan.
- Use Phase 3 held-private-directory durable publication for state records. Atomic temp/write/fsync/rename/fsync operations and locks are real filesystem behavior under `t.TempDir`; no in-memory production store.
- State records use bounded, versioned canonical CBOR frames. Check length before allocation; retain corrupt originals; unsupported future versions fail read-only.
- Default tests are hermetic/offline/unprivileged. No test invokes a package manager, authorization, external network, or non-test-root mutation.
- Coverage: >=90% engine policy/planner/apply/recovery and state; >=80% capability/application. Add property, crash-subprocess, race, and non-quadratic planning gates.

## Architecture

### Control and persistence flow

```text
CapabilityRegistry -> CapabilitySnapshot
ProviderRegistry(providerapi) -> CandidateSink -> Policy -> bounded selection
-> Planner -> canonical immutable Plan -> PlanStore
explicit ApplyRequest -> digest/config/capability/recovery checks -> Run intent
-> ActionExecutor.Revalidate -> Apply -> Verify -> Result/Recovery ledger -> History
```

`engine` owns policy and orchestration ports, not adapters. `state` implements store ports without engine importing state. `application.Service` composes capability, provider, engine, and state implementations and emits presenter-neutral events. Phase 5 adds real providers; Phases 7/9 add real executors.

### Planned interface/function/type checklist

- [ ] `capability.Probe.Probe`, `Registry.Register`, `Registry.Snapshot`, `Snapshot.Digest`, and typed unavailable/degraded reasons; duplicate probe IDs reject.
- [ ] `providerapi.Provider.Descriptor`, `Provider.Discover(context.Context, DiscoverRequest, CandidateSink) error`, `CandidateSink.Offer`; request fixes roots, budgets, offline policy, and cancellation.
- [ ] `engine.ProviderRegistry.Register/List/Discover` preserves deterministic provider ordering and backpressure; it cannot execute actions.
- [ ] `engine.Policy.Evaluate`, `Policy.Admit`, `ProtectionList.Protects`, `Selection.Resolve`; conflicts fail closed and protections are monotonic.
- [ ] `engine.Planner.Build` validates action dependencies, support/guarantees, totals, capability/config digest, and calls `planproto` once to seal the plan.
- [ ] `engine.ActionExecutor.Revalidate/Apply/Verify`, `ExecutorRegistry`, `ApplyCoordinator.Apply`; registry is empty at phase exit and unknown action kinds reject.
- [ ] `engine.RecoveryCoordinator.Outstanding/Reconcile/BuildRepairPlan`; unresolved target/provider probes block overlapping reapply.
- [ ] `state.ResolveLayout`, `LoadConfig`, `EffectiveConfig.Digest`, `PlanStore.Put/Get/Expire`, `RunStore.Begin/Finish`, `HistoryStore.Append/Query/InspectTail`, `RecoveryStore.Put/List/Resolve`.
- [ ] `application.Service.Inventory/CreatePlan/ApplyPlan/QueryHistory/Reconcile`, `EventSink.Emit`, and `ConfirmationRequest` containing plan digest, privileged subset/count/categories/risk; presenters cannot change it.

### Dependency/import constraints

- `providerapi` is a leaf: standard library + domain only. Concrete providers import it/domain, never engine/state/application/presenters.
- `capability` imports domain only. Probe adapters added later may use fixed read-only facilities but not presenters/state/mutation.
- `engine` imports domain, planproto, capability, and providerapi; it never imports state, presenters, concrete providers/executors, Cobra, TUI, or raw syscalls.
- `state` imports domain, planproto, pathbytes, linuxfs durable-file APIs, and the selected TOML decoder; no engine/provider/presenter/helper imports.
- `application` composes domain/capability/providerapi/engine/state. Presenters later import application/domain only.
- Architecture tests forbid provider-to-engine/state, presenter authority, and any state/provider direct raw mutation syscall.

## Related Code Files

| Action | Exact repo-relative paths | Purpose |
|---|---|---|
| Create | `docs/contracts/configuration.md`, `docs/contracts/state-and-recovery.md` | Typed config, XDG layout, event/recovery semantics |
| Modify | `go.mod`, `go.sum` | Add only the audited TOML decoder |
| Modify | `Makefile`, `.github/workflows/ci.yml` | Engine/state coverage, race, crash, fuzz gates |
| Modify | `internal/architecture/import_rules_test.go` | Provider/engine/state/application boundary allowlists |
| Create | `internal/capability/probe.go`, `internal/capability/registry.go`, `internal/capability/snapshot.go` | Typed capability collection/digest |
| Create | `internal/capability/registry_test.go`, `internal/capability/snapshot_test.go` | Determinism/duplicate/degraded tests |
| Create | `internal/providerapi/contracts.go`, `internal/providerapi/contracts_test.go` | Leaf provider/sink/request contract and budget checks |
| Create | `internal/engine/provider_registry.go`, `internal/engine/policy.go`, `internal/engine/planner.go` | Discovery orchestration, monotonic policy, immutable plans |
| Create | `internal/engine/executor.go`, `internal/engine/apply_coordinator.go`, `internal/engine/recovery_coordinator.go` | Deny-by-default executor/apply/reconciliation ports |
| Create | `internal/engine/provider_registry_test.go`, `internal/engine/policy_test.go`, `internal/engine/planner_test.go` | Ordering, protection, determinism tests |
| Create | `internal/engine/apply_coordinator_test.go`, `internal/engine/recovery_coordinator_test.go` | Explicit apply, drift, cancellation, outstanding recovery tests |
| Create | `internal/state/layout.go`, `internal/state/config.go`, `internal/state/lock.go` | XDG resolution, strict config, process lock |
| Create | `internal/state/atomic_file.go`, `internal/state/plan_store.go`, `internal/state/run_store.go` | Rooted durable plan/run records |
| Create | `internal/state/history_store.go`, `internal/state/recovery.go` | Append/query/corrupt-tail and mandatory recovery ledger |
| Create | `internal/state/layout_test.go`, `internal/state/config_test.go`, `internal/state/atomic_file_test.go` | Permissions/symlink/config/durability tests |
| Create | `internal/state/plan_store_test.go`, `internal/state/history_store_test.go`, `internal/state/recovery_test.go` | Schema, crash tail, locking, reconciliation tests |
| Create | `internal/application/service.go`, `internal/application/use_cases.go` | Composition root and use-case contracts |
| Create | `internal/application/events.go`, `internal/application/confirmation.go` | Presenter-neutral progress and immutable confirmation data |
| Create | `internal/application/service_test.go`, `internal/application/confirmation_test.go` | Preview-only, dry-run, confirmation binding tests |
| Create | `tests/contract/engine_contract_test.go`, `tests/contract/state_contract_test.go` | Cross-package plan/apply/state invariants |
| Create | `tests/performance/planner_benchmark_test.go` | 50k/100k deterministic growth/RSS benchmark |
| Create | `tests/fixtures/config/minimal-v1.toml`, `tests/fixtures/config/unknown-field-v1.toml` | Reviewed strict config fixtures |
| Create | `tests/fixtures/state/history-valid-v1.cborlog`, `tests/fixtures/state/history-truncated-v1.cborlog` | Reviewed append/tail-recovery fixtures |
| Delete | None | No legacy policy/state exists |

## TDD-First Test Strategy

### Test matrix

| Priority | Exact tests | Scenario | Gate |
|---|---|---|---|
| Critical | `TestProtectionMonotonicity` | Property-generated candidates plus added protections | Accepted actions can only stay/remove; never add/change authority |
| Critical | `TestPlannerDeterministicAcrossProviderOrder` | Same typed candidates in all orderings | Byte-identical plan/digest/actions/totals |
| Critical | `TestUnsupportedAndAmbiguousNeverAdmitted` | Missing capability, conflicting ownership, provider bounded guarantee | Explicit reason; no action or upgraded guarantee |
| Critical | `TestApplyRequiresExplicitRequestAndExactDigest` | yes-only, dry-run, stale digest/config/capability | No executor/state intent; exit mapping invalid/drift/unsupported |
| Critical | `TestUnknownExecutorAndOutstandingRecoveryReject` | Empty registry; unresolved same target/provider | Fail closed; no simulated success/reapply |
| Critical | `TestDispatchedCancellationBecomesIndeterminate` | Cancel before group vs after dispatch record | Completed facts retained; unknown requires typed reconciliation |
| Critical | `TestHistoryCorruptTailIsReadOnly`, `TestRepairPlanPreservesOriginal` | Valid/truncated fixtures and crash subprocess | Query prefix+warning; no implicit truncate; separate action |
| High | `TestXDGLayoutModesAndNoFollow` | unset/custom vars, symlink/wrong owner/mode | Exact fallbacks, 0700/0600, reject unsafe layout |
| High | `TestConfigStrictDecodeAndSemanticDigest` | unknown/duplicate/type errors; formatting-only changes | Reject invalid; equivalent effective config digest equal |
| High | `TestStateCommitCrashPoints` | kill after temp write/fsync/rename/dir sync | Old or new valid record, never accepted partial |
| High | `TestNoHistoryStillPersistsRecovery` | config/env combinations | Optional history absent; safety ledger remains |
| Medium | `BenchmarkPlanner50k100k` | Original typed contract candidates, bounded sink | Deterministic, no quadratic growth or unbounded retention |

Tests construct validated domain values and use real temporary directories/subprocess crashes. Do not add production fake providers, stores, or executors. Registry validation uses descriptors; apply denial uses the real empty registry.

### RED -> GREEN -> REFACTOR

1. **RED — config/layout:** write strict TOML, XDG fallback/mode/no-follow, semantic digest, and history-disable/recovery-preservation tests.
2. **GREEN — private state foundation:** after decoder audit, implement typed config/layout/locks and rooted durable file wrapper with the minimum v1 schema.
3. **REFACTOR — one persistence path:** consolidate length/budget/version/fsync handling; keep optional history separate from mandatory recovery.
4. **RED — policy/planner:** add protection property tests, ordering permutations, conflicts, capability/provider guarantee, totals, and golden digest expectations.
5. **GREEN — deterministic engine:** implement leaf provider API, registries, monotonic policy/selection, and planner over Phase 2 constructors.
6. **REFACTOR — bounded flow:** add backpressure/cancellation, stable ordering, checked arithmetic, and remove any provider-specific policy branch.
7. **RED — apply/recovery:** test explicit apply/dry-run/stale plan/empty executor/cancellation/partial dependency groups/outstanding reconciliation before coordinator code.
8. **GREEN — deny-by-default lifecycle:** persist intent, enforce all gates, return unsupported with empty executor registry, and produce reconciliation facts without mutation.
9. **REFACTOR — application ports:** compose use cases/events/confirmation, ensure presenters cannot supply privileged counts/categories, and rerun import gates.
10. **RED/GREEN — state corruption:** add subprocess crash points, corrupt-tail reads, repair-plan generation, locking, and schema-version rejection; never auto-repair.

### Commands and behavioral gates

```bash
GOTOOLCHAIN=local go test ./internal/capability ./internal/providerapi ./internal/engine ./internal/state ./internal/application ./tests/contract -count=1
GOTOOLCHAIN=local go test -race ./internal/capability ./internal/providerapi ./internal/engine ./internal/state ./internal/application
GOTOOLCHAIN=local go test ./internal/engine ./internal/state -coverprofile=coverage-engine-state.out
GOTOOLCHAIN=local go test ./internal/engine -run 'TestProtectionMonotonicity|TestPlannerDeterministicAcrossProviderOrder' -count=100
GOTOOLCHAIN=local go test ./internal/state -run 'TestStateCommitCrashPoints|TestHistoryCorruptTailIsReadOnly' -count=20
GOTOOLCHAIN=local go test ./internal/architecture -run TestArchitectureImportAllowlists -count=1
GOTOOLCHAIN=local go test ./tests/performance -run '^$' -bench BenchmarkPlanner50k100k -benchmem
```

Coverage gates: engine and state >=90%; capability/application >=80%. Behavioral gates: zero mutation/authorization/network on dry-run or rejected apply, byte-identical plans under ordering permutations, no automatic corrupt-tail change, no optional-history setting disabling recovery, no plan build worse than near-linear (100k time/allocations <2.5x 50k on the documented benchmark host).

## Implementation Steps

1. Run the TOML decoder decision gate; record license/version/audit and strict-mode proof. Add only the selected dependency.
2. Write config/layout RED tests; implement XDG derivation, permissions, locks, effective config, protection parsing, semantic digest, and config-root exclusions.
3. Write state RED tests; implement bounded canonical plan/run/history/recovery records using Phase 3 durable publication. Persist boot/run/action states before risky transitions.
4. Define capability and leaf provider contracts. Add deterministic registries with duplicate-ID rejection, explicit offline/budget/cancellation requests, and capability reasons.
5. Write policy/planner RED tests and property inputs. Implement central support/protection/conflict/selection logic and canonical sealed plans; never upgrade provider guarantees.
6. Write apply/recovery RED tests. Implement explicit request/digest/config/capability/expiry/lock/outstanding-recovery gates, dependency grouping, and empty-executor rejection.
7. Implement corrupt-tail reporting and repair-plan generation. Preserve original bytes and defer actual repair mutation to a later executor/apply phase.
8. Compose application use cases and event/confirmation values. Dynamic privileged detail belongs to LDC confirmation; stock polkit limitation is carried forward to Phase 8.
9. Run focused, repeated-property/crash, race, coverage, architecture, and planner benchmark gates. Review every persisted transition and cancellation window.

## Success Criteria

- [ ] Capability/provider ports are deterministic, bounded, offline by default, and cannot mutate or import policy/state/presenters.
- [ ] Protection is property-tested monotonic; unsupported/ambiguous/provider-limited facts never become broader actions or guarantees.
- [ ] Equivalent inputs produce byte-identical plans/digests; config/capability/action drift expires a plan before apply.
- [ ] Apply requires explicit request+digest, persists transitions, rejects unknown executors, and records partial/indeterminate/reconciliation accurately.
- [ ] XDG layout, modes, no-follow writes, locking, canonical bounded records, optional history, mandatory recovery, corrupt-tail reads, and repair-plan behavior pass.
- [ ] No phase-exit code discovers via fake provider or mutates via fake executor; the system is honestly preview-only.
- [ ] Focused/race/coverage/architecture/crash/performance gates pass.

## Risk Assessment and Rollback

| Risk | Mitigation |
|---|---|
| Policy order changes plan/digest | Stable IDs/sorts, ordering permutations, canonical sealing once |
| Protection accidentally grants authority | Monotonic property gate and no root/action fields in protection output |
| Config typo silently weakens safety | Strict unknown/type rejection and semantic digest |
| Crash loses attempted/unknown fact | Intent-before-dispatch, durable recovery independent of optional history |
| Corrupt history is auto-truncated | Read-only tail warning and separately previewed preserving repair |
| Engine couples to adapters/UI/state | Leaf ports, application composition, architecture allowlists |

Rollback disables application wiring and returns to Phase 3 safety libraries. Before release, Phase 4 state directories may be removed only from test roots. Once real user state exists, never delete or silently downgrade it: preserve versioned files, ship a read-only compatible reader, and plan an explicit migration/repair.

## Phase Exit and Hand-Off

Exit only when every use case is preview/read-only and apply is demonstrably deny-by-default with an empty executor registry. Hand Phase 5 `providerapi`, capability snapshots, bounded candidate sink, policy/planner inputs, state ports, application events, and exact guarantee/recovery result contracts. Provider work may add read-only adapters only; it cannot register an executor or bypass policy. The generated state-repair plan remains non-executable through Phase 9; Phase 10 alone adds the narrow retain-and-publish executor and must reuse this phase's approved digest/schema/recovery invariants without making a history read mutate.
