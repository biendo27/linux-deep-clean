---
phase: 5
title: "Provider Discovery and Read-only Inventory"
status: pending
priority: P1
effort: "18d"
dependencies: [4]
---

# Phase 5: Provider Discovery and Read-only Inventory

## Overview

Implement every read-only discovery path needed by `clean`, `uninstall`, `optimize`, `analyze`, `status`, `purge`, and `installer`. Providers turn raw Linux observations into Phase 2 domain candidates and Phase 4 preview inputs; they do not mutate files, acquire authorization, contact the network, or decide policy.

This phase establishes honest provider-specific guarantees. APT, DNF5, Pacman, Flatpak, Snap, journald, XDG/Trash, application, project, installer, disk-inventory, and metrics adapters must report the evidence they can prove and the limitations they cannot. Mutation remains disabled.

## Requirements

### Functional

- Select providers from Phase 4 capability probes, not distro names alone. Unsupported or missing tools produce a typed unavailable reason.
- Preserve raw pathname bytes through `pathbytes.BytePath` embedded in typed domain filesystem targets; provider APIs exchange those domain targets, and display strings/localized output are never authority.
- Implement these discovery/preview contracts:
  - **APT/dpkg:** exact name, architecture, installed version/status, Essential flag, registered ownership, bounded cache inventory, and normalized `apt-get --simulate remove <name>:<arch>` graph.
  - **DNF5/RPM:** exact installed NEVRA, ownership/file lists, install reason/origin, protected/running-kernel evidence, and exact-NEVRA `--no-autoremove` preview. Do not create a stored transaction in this read-only phase.
  - **Pacman/paccache:** exact name/version/ownership, `pacman -R --print --print-format` dependency preview, and `paccache --dryrun --keep 3` cache candidates. Missing `paccache` disables only that capability.
  - **Flatpak:** probe supported columns; inventory exact ref, commit, origin, installation, and user/system scope from versioned tabular output. Never infer a ref from display text.
  - **Snap:** prefer the versioned local snapd REST API; capture exact name, snap ID, revision, channel, confinement/status, and asynchronous-service availability. Never contact a remote store.
  - **journald/systemd:** report bounded journal usage/retention facts and supported vacuum capabilities. Mark `journalctl --disk-usage` as an estimate/bound, not an exact deletion list.
  - **XDG/cache and Trash:** resolve specification fallbacks; inventory only cache roots or exact manifest-backed recreatable caches; report Trash usage read-only and never enumerate it as cleanup candidates.
  - **Applications:** aggregate native, Flatpak, Snap, and exact user-install provenance. Separate manager removal proposals from independently evidenced residue candidates; never classify XDG data/state, documents, credentials, or active content as residue.
  - **Projects:** require configured local-root containment, nearest matching ecosystem marker, artifact-specific reproducibility evidence, and no link/mount traversal. Deployment outputs such as `dist` default unselected.
  - **Installers:** scan only XDG Downloads and explicit roots; require extension plus magic/package metadata or stronger provenance. Archives alone are not installers; private mail/cloud databases are excluded.
  - **Disk inventory:** report apparent and allocated bytes separately, bounded large-file/tree views, unsupported mount reasons, and cancellation-safe partial results.
  - **Metrics:** CPU, memory, load, filesystem capacity, disk I/O, network, process, power, thermal, cgroup context, and optional GPU observations from documented procfs/sysfs/service contracts.
- Bind process metrics to PID plus start time; handle counter reset/wrap, hotplug, sector-size differences, and permission loss.
- Represent missing metrics as `null` plus a capability reason. Never substitute zero.
- Produce a versioned, inspectable health-score input breakdown. Omit unavailable inputs and renormalize weights; do not reward or penalize missing sensors.
- Attach provider ID/version, source observation, evidence, precondition inputs, guarantee level, network disclosure, and parser/fixture version to each proposal.
- Apply the Phase 4 protection policy only after providers return data; providers may not expand trusted roots or select actions.

### Non-functional and safety

- Discovery, preview, `status`, and every test in the default suite are offline, unprivileged, and zero-mutation.
- External probes use compiled absolute executable paths, a fresh bounded environment with `LC_ALL=C`/`LANG=C`, no shell, no `PATH` lookup, bounded stdout/stderr, deadlines, and cancellation.
- Do not invoke manager transaction/apply verbs, `pkexec`, `sudo`, Trash moves, staging, history repair, or network clients from provider packages.
- Store fixture stdout/stderr as raw bytes. Every fixture manifest records schema version, distro/release/architecture, command and tool version, locale, capture source, sanitization, SHA-256, and expected parser contract.
- Default scanner limits: at most four workers and 128 open descriptors; cancellation reaches quiescence within one second.
- On the documented reference host with a warm cache: 100,000 entries complete within 2 seconds and 128 MiB RSS; the 1,000,000-entry deep scan completes within 60 seconds and 256 MiB RSS.
- Coverage gates: at least 85% statement coverage for every provider parser package, 80% for metrics and other behavior-bearing Phase 5 packages, while Phase 2/4 domain and engine packages remain at least 90%.

## Architecture

```text
application inventory/status use case
  -> Phase 4 capability registry
  -> compiled provider registry
  -> bounded read-only probe runner / descriptor-safe scanner
  -> raw observations + provenance
  -> provider parser and normalizer
  -> domain Candidate / TransactionGraph / MetricSnapshot
  -> Phase 4 policy, protection, deterministic plan preview
```

Providers are leaf adapters. A provider owns its machine-interface parser and evidence rules, but receives scanning, command, clock, and capability ports through constructors. A separate `internal/discoveryfs` adapter implements the read-only `providerapi.ReadTree` port by composing Phase 3 anchored resolution; concrete providers never import or receive `RootLease`, `linuxfs`, `mounts`, Trash, quarantine, or any mutation-capable interface. Tests use recorded bytes and disposable directory trees without adding test-only branches to production.

Manager previews use Phase 2 `domain.TransactionGraph`, not command prose. Nodes contain exact object IDs/versions/scopes; edges explain dependency consequences; metadata records how strongly preview binds a later apply. Phase 9 must re-probe and compare that same domain type before execution. Flatpak, Snap, and journald explicitly expose their weaker exact-target/bounded-effect guarantees.

Metrics use monotonic raw counters plus a sampler clock. Rate calculations occur outside parsers so fixture tests remain deterministic. The optional gopsutil v4 dependency stays deferred: add it only through an ADR-like evidence note in this phase if a named direct adapter cannot satisfy the matrix, accuracy, license, and resource gates.

## Related Code Files

### Create

- `internal/providers/probeexec/registry.go` — closed read-only probe vocabulary and absolute executable mapping.
- `internal/providers/probeexec/runner_linux.go` — bounded direct-exec adapter with fresh environment and cancellation.
- `internal/providers/probeexec/runner_test.go` — forbidden verb, environment, output-limit, timeout, and cancellation tests.
- `internal/discoveryfs/scanner_linux.go`, `internal/discoveryfs/scanner_test.go` — read-only implementation of `providerapi.ReadTree` over anchored Phase 3 resolution, with no mutation method in its interface.
- `internal/providers/apt/provider.go`, `internal/providers/apt/parser.go`, `internal/providers/apt/provider_test.go`, `internal/providers/apt/parser_test.go`.
- `internal/providers/dnf5/provider.go`, `internal/providers/dnf5/parser.go`, `internal/providers/dnf5/provider_test.go`, `internal/providers/dnf5/parser_test.go`.
- `internal/providers/pacman/provider.go`, `internal/providers/pacman/parser.go`, `internal/providers/pacman/provider_test.go`, `internal/providers/pacman/parser_test.go`.
- `internal/providers/flatpak/provider.go`, `internal/providers/flatpak/parser.go`, `internal/providers/flatpak/provider_test.go`, `internal/providers/flatpak/parser_test.go`.
- `internal/providers/snap/provider.go`, `internal/providers/snap/parser.go`, `internal/providers/snap/provider_test.go`, `internal/providers/snap/parser_test.go`.
- `internal/providers/journald/provider.go`, `internal/providers/journald/parser.go`, `internal/providers/journald/provider_test.go`, `internal/providers/journald/parser_test.go`.
- `internal/providers/xdg/provider.go`, `internal/providers/xdg/provider_test.go` — XDG cache boundaries and read-only Trash usage.
- `internal/providers/applications/provider.go`, `internal/providers/applications/provider_test.go` — manager/provenance aggregation and residue evidence.
- `internal/providers/projects/provider.go`, `internal/providers/projects/rules.go`, `internal/providers/projects/provider_test.go`.
- `internal/providers/installers/provider.go`, `internal/providers/installers/classifier.go`, `internal/providers/installers/provider_test.go`.
- `internal/providers/disk/provider.go`, `internal/providers/disk/scanner_linux.go`, `internal/providers/disk/provider_test.go`.
- `internal/providers/builtin.go`, `internal/providers/builtin_test.go` — construct the compiled provider set for the Phase 4 registry.
- `internal/metrics/provider.go`, `internal/metrics/procfs_linux.go`, `internal/metrics/sysfs_linux.go`, `internal/metrics/score.go`, `internal/metrics/provider_test.go`, `internal/metrics/score_test.go`.
- `tests/contract/provider_inventory_v1_test.go` — cross-provider domain/schema/evidence contract.
- `tests/integration/providers/read_only_host_test.go` — opt-in real-host probes with before/after mutation guard.
- `tests/performance/inventory_benchmark_test.go` — 100k/1M scan, RSS, worker, FD, and cancellation gates.
- `tests/fixtures/providers/v1/README.md` — raw-fixture format and recapture procedure.
- `tests/fixtures/providers/v1/manifest.schema.json` — fixture provenance schema.
- `tests/fixtures/providers/v1/{apt,dnf5,pacman,flatpak,snap,journald,procfs,sysfs}/manifest.json` — versioned capture indexes; each references raw `.stdout`, `.stderr`, or endpoint-body files under the same provider directory.

### Modify

- `internal/application/service.go` — compose built-in providers/metrics with Phase 4 registries through leaf interfaces.
- `internal/application/use_cases.go` — orchestrate capability-gated inventory/status collection through the Phase 4 use cases.
- `internal/application/events.go` — carry presenter-neutral inventory/metric events and unavailable reasons.
- `internal/capability/registry.go` — register exact provider/tool/service capabilities and unavailable reasons.
- `internal/engine/provider_registry.go` — register the compiled `providerapi.Provider` implementations.
- `internal/engine/planner.go` — accept provider preview guarantees without granting mutation authority.

### Delete

- None.

## Interfaces and Dependency Boundaries

```go
type Provider interface {
    Descriptor() providerapi.Descriptor
    Discover(context.Context, providerapi.DiscoverRequest, providerapi.CandidateSink) error
}

type CandidateSink interface {
    Offer(context.Context, domain.Candidate) error
}

type ProbeRunner interface {
    Run(context.Context, ProbeID, ProbeInput) (RawObservation, error)
}

type ReadTree interface {
    Walk(context.Context, domain.FilesystemTarget, ReadBudget, EntrySink) error
}

type MetricsProvider interface {
    Snapshot(context.Context, PreviousCounters) (domain.MetricSnapshot, error)
}
```

- Concrete `internal/providers/<name>` packages may import only the standard library, Phase 2 domain/path contracts, `internal/providerapi`, capability/read-only ports, and probe adapters. They must not import application, engine, state, presenters, `mounts`, `linuxfs`, Trash/quarantine, privilege protocol, or manager executors.
- `internal/discoveryfs` may import `providerapi`, domain/pathbytes, mounts/linuxfs read-only resolution, and `x/sys/unix`; architecture tests reject any call/import edge to staging, Trash, quarantine, unlink/rename, state writers, or external execution.
- `internal/providers/builtin.go` is the composition edge: it constructs concrete providers for `internal/engine/provider_registry.go`; it adds no discovery or policy logic.
- `probeexec` accepts a `ProbeID`, never an executable, argv, environment map, URL, or shell text from a provider or user.
- A `RawObservation` retains bytes, exit facts, command/tool version, locale, timestamp, and truncation status. Parsers reject truncated authority-bearing output.
- `domain.TransactionGraph.ProviderGuarantee` is one of `exact_graph_reprobe_required`, `exact_target_reprobe_required`, `bounded_effect_only`, or `read_only_inventory`; unknown guarantees fail schema validation.
- Scanner inputs are a compiled/Phase 4-approved `domain.FilesystemTarget` through `providerapi.ReadTree`; its path contains the Phase 2 `pathbytes.BytePath`. Providers cannot receive a root FD, reopen an arbitrary absolute root, select a new root, or cross a mount.
- Application orchestration may merge provider streams deterministically but may not reinterpret provider IDs, synthesize missing measurements, or turn a preview into an apply result.

## TDD Test Strategy

### RED — write failing contracts first

1. Add `TestProviderRegistry_HasEveryReadOnlyCapability` and `TestProviderPackages_HaveNoMutationImports` before provider implementations.
2. Add table tests named `TestParse_<Provider>_<Fixture>` against raw fixtures for all supported release families and important tool-version changes. Include malformed, truncated, localized, option-looking ID, counter-wrap, missing-field, permission-denied, and unknown-version cases.
3. Add contract tests:
   - `TestDiscoveryIsOfflineUnprivilegedAndMutationFree` records all exec/socket/write/rename/unlink attempts and requires zero.
   - `TestTransactionGraphUsesExactManagerIdentity`, `TestMissingEvidenceDefaultsToSkip`, and `TestResidueNeverIncludesUserData`.
   - `TestTrashUsageNeverProducesTrashAction` and `TestRecoveryRootsAreExcludedFromEveryProvider`.
   - `TestInvalidUTF8CandidateRoundTripsRawBytes` and `TestDisplayPathCannotBecomeAuthority`.
   - `TestMissingMetricIsNullWithReason`, `TestPIDReuseDoesNotMergeSamples`, and `TestHealthScoreRenormalizesMissingInputs`.
4. Add dynamic-tree tests for marker collisions, nested workspaces, symlinks, mount boundaries, hard links, sockets, permission changes, MIME/extension mismatch, malicious archives, and Downloads fallback.
5. Add `TestProbeRegistryRejectsApplyVerbs`, `TestProbeRunnerDropsCallerEnvironment`, `TestProbeRunnerBoundsOutput`, and `TestProbeCancellationQuiescesWithinOneSecond`.
6. Add performance benchmarks before optimizing: `BenchmarkInventory100K`, `BenchmarkDeepScan1M`, and `BenchmarkMetricSnapshot`.

Fixture minimum matrix:

| Contract | Required fixture variants |
|---|---|
| APT/dpkg | Ubuntu 22.04/24.04/26.04; Debian 12/13; multiarch; held/Essential/broken; dependency drift |
| DNF5/RPM | Fedora 43/44; protected package; running kernel; repository absent; transaction preview change |
| Pacman | current fully upgraded Arch; IgnorePkg; dependent refusal; versioned cache; missing paccache |
| Flatpak | user/system duplicate refs; runtime dependency; missing columns; disappearing ref |
| Snap | classic/strict; active service; daemon absent; asynchronous change metadata |
| journald | persistent/volatile; user/system; concurrent growth; permission denial; parser version change |
| procfs/sysfs | missing sensors; reset/wrap; hotplug; PID reuse; sector sizes; cgroup v2; permission loss |

### GREEN — smallest conforming implementation

1. Implement one provider parser at a time from recorded bytes; then add the production probe adapter and capability gate.
2. Return typed unavailable/partial observations instead of parser fallbacks or inferred data.
3. Reuse Phase 3 anchored read resolution only through `providerapi.ReadTree` and `internal/discoveryfs`; do not expose leases/mutation methods or write a path-string walker.
4. Implement direct procfs/sysfs readers and health-score normalization. Keep gopsutil absent unless the named evidence gate fails.
5. Register a provider only after its negative, fixture, zero-mutation, and cancellation tests pass.

### REFACTOR — improve behind frozen contracts

1. Share byte scanners and bounded probe plumbing only after at least two proven adapters need the same behavior; keep provider grammars separate.
2. Remove allocations and copies measured by the 100k/1M benchmarks without changing event ordering or raw-byte identity.
3. Consolidate fixture loaders and provenance validation; never consolidate distinct manager semantics into generic prose parsing.
4. Run import, coverage, race, and performance gates after each consolidation.

### Commands and gates

```bash
go test ./internal/providers/... ./internal/metrics/... ./tests/contract/...
go test -race ./internal/providers/... ./internal/metrics/... ./internal/application/...
go test -tags=integration ./tests/integration/providers/...
go test -run '^$' -bench 'Benchmark(Inventory100K|DeepScan1M|MetricSnapshot)' -benchmem ./tests/performance/...
go test -coverprofile=coverage.out ./internal/providers/... ./internal/metrics/...
go tool cover -func=coverage.out
```

VM fixture recapture is opt-in only: `LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go test -tags=vmtest ./tests/vm/providers/...`. The harness must validate a root-owned, non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches `LDCLEAN_VMTEST_TOKEN`; a missing opt-in, a missing/mismatched sentinel, or an unexpected image identity fails before probing. Phase 5 VM tests remain read-only.

## Implementation Steps

1. Freeze `Provider`, `providerapi.ReadTree`, `domain.TransactionGraph`, provenance, metric-null, and guarantee-level contracts in Phase 2/4 types; add import-boundary tests.
2. Define and test the raw-byte fixture manifest, checksum verification, version routing, and recapture rules. Capture from the supported release families without importing private/user data.
3. Implement the closed `probeexec` registry and bounded runner; prove no mutation/network/authorization vocabulary is reachable.
4. Implement APT/dpkg, DNF5/RPM, and Pacman/paccache parsers and previews in that order. Keep every backend's graph and guarantees explicit.
5. Implement Flatpak and local snapd discovery with exact scope/ref identity and graceful capability absence.
6. Implement journald/systemd usage/capability parsing with bounded-effect labeling.
7. Implement the read-only `internal/discoveryfs` adapter, then XDG/cache and Trash-usage discovery; blacklist config/state/cache indexes, Trash contents, staging, and recovery roots from candidates.
8. Implement application aggregation and exact-provenance residue classification.
9. Implement disk, project, and installer providers over the injected read-only tree port with evidence rules, byte paths, allocated/apparent sizes, and bounded concurrency.
10. Implement direct metrics adapters, rate sampling, process identity, unavailable reasons, and versioned health-score contributions.
11. Wire application use cases and deterministic provider event merging. Keep selection and policy in Phase 4.
12. Run fixture, property, race, coverage, performance, and opt-in read-only matrix tests; document any unavailable capability rather than widening support.

## Success Criteria

- [ ] Every Phase 5 provider is capability-gated, compiled, separately testable, and read-only.
- [ ] All proposals contain exact identity, evidence, provenance, guarantee level, and limitation disclosures.
- [ ] APT, DNF5, Pacman, Flatpak, Snap, and journald fixtures cover supported release families and negative/drift cases.
- [ ] XDG, Trash usage, applications, projects, installers, and disk analysis preserve raw bytes and never cross approved roots/mounts.
- [ ] Trash, quarantine, config, state, history, and recovery locations can never become cleanup candidates.
- [ ] Metrics emit missing data as `null` with reasons and the health score is inspectable and correctly renormalized.
- [ ] Default tests prove zero writes, renames, unlinks, authorization prompts, and network requests.
- [ ] Provider parser coverage is at least 85%; metrics/other Phase 5 behavior is at least 80%; upstream 90% gates remain green.
- [ ] 100k/1M scan, worker, FD, memory, and cancellation thresholds pass on the documented reference host.
- [ ] No provider imports Phase 3 mutation, presenters, privilege, state writers, or manager executor packages.
- [ ] Concrete providers receive only `providerapi.ReadTree`; architecture tests prove only `internal/discoveryfs` composes Phase 3 read resolution and has no mutation surface.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback |
|---|---|---|
| Localized or version-changing manager output | Force C locale where unavoidable; prefer machine/database APIs; version raw fixtures; reject unknown/truncated authority-bearing output | Disable only the affected capability and retain fixture/parser code for diagnosis |
| False application residue or project artifact | Require exact ownership/manifest/marker evidence; ambiguity and deployment output default unselected | Remove the rule/registry entry; no mutation has occurred in this phase |
| Metrics look valid when unavailable | Typed null + reason; PID/start-time identity; reset/hotplug tests | Disable the adapter and renormalize score inputs |
| Read-only preview accidentally writes cache/locks or reaches network | Closed probe vocabulary, isolated XDG/HOME in tests, syscall/network/write guard | Unregister the probe and mark provider unavailable |
| Scanner exceeds memory/FD limits | Streaming events, four-worker/128-FD caps, benchmarks and cancellation tests | Fall back to slower bounded implementation, never unbounded collection |

Rollback is code/config only: Phase 5 performs no user or system mutation and writes no migration that needs data rollback. Revert a provider registry entry or parser while preserving fixture evidence.

## Phase Exit/Handoff

Phase 6 receives stable application-level inventory/status streams, typed unavailable reasons, preview graphs, raw-byte paths, and versioned fixtures. It must present these contracts without importing provider implementations or changing their semantics. Phase 7 may consume only Phase 4 admitted actions derived from this inventory; it may not call provider internals or reinterpret raw discovery as deletion authority.
