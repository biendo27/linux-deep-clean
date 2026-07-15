---
type: scout-report
date: 2026-07-14
status: complete
scope: full repository and every planned phase
---

# LDC implementation-plan scout report

## Repository baseline

The repository is greenfield. There is no Go module, source tree, test suite, packaging metadata, CI configuration, or configured Git remote. The only authoritative product material is the approved Linux-native design report and its journal. The new plan scaffold and research notes are untracked; the earlier design artifacts are committed in `059f6a9`.

Authoritative inputs:

- `plans/reports/260714-1754-ldc-linux-native-design-report.md`
- `docs/journals/2026-07-14-ldc-design-decisions.md`
- `plans/260714-1831-ldc-full-parity-linux-implementation/research/01-go-toolchain-and-repository-architecture.md`
- `plans/260714-1831-ldc-full-parity-linux-implementation/research/02-filesystem-and-privilege-safety.md`

No overlapping unfinished implementation plan existed before this plan was created. The design report is an input, not a competing plan.

## Environment observations

- Local compiler: Go 1.26.5.
- Host: Debian-family Linux with kernel 6.12; it is not a substitute for the supported 15-VM matrix.
- Available locally include Docker, QEMU x86 tooling, ShellCheck, and Bats.
- AArch64 QEMU support, libvirt tooling, Podman, Packer, nFPM, GoReleaser, Syft, and Cosign were not found in the initial tool scan.
- Missing host tools are setup work for the relevant phase, not reasons to weaken validation.

## Per-phase scout

### Phase 1 — repository and contract harness

- Existing files: none of the planned source/build files exist.
- Create: module/build/license/CI files; `cmd/ldclean`; reject-only helper entry point; `internal/architecture`; cross-suite skeletons.
- Interfaces: command construction, version metadata, build-info output, architecture import allowlists.
- Risky dependencies: module path and Go patch pin; all third-party dependencies must be role-limited.
- Test gap: no tests exist. First red tests must prove executable name, no-root main-process invariant, hermetic default suite, and forbidden imports.

### Phase 2 — canonical domain and plan protocol

- Existing files: design-only action/result/config contracts.
- Create: dependency-free domain types, byte paths, canonical plan encoding/digest, schema fixtures, contract tests and fuzz targets.
- Interfaces: `BytePath`, `TrustedRootID`, `Action`, `Precondition`, `Plan`, `PlanDigest`, `Result`, `Outcome`, `ReconciliationState`.
- Risky dependencies: CBOR library and JSON representation of non-UTF-8 paths.
- Test gap: no golden schemas, round-trip properties, or compatibility fixtures exist.

### Phase 3 — Linux filesystem safety and Trash

- Existing files: no syscall implementation; the safety research defines required Linux behavior.
- Create: capability probes, mount inspection, descriptor-relative resolver, action-specific snapshots, staging/quarantine, Trash adapter, recovery records, adversarial tests.
- Interfaces: `OpenTrustedRoot`, `RootLease`, `ResolveParent`, `SnapshotFD`, `ComparePrecondition`, `StageNoReplace`, `RestoreNoReplace`, `RemoveStagedTree`, `SelectTrashRoot`, `WriteTrashInfoDurable`, `MoveToTrash`.
- Risky dependencies: `openat2`, `statx`, `renameat2`, `/proc/self/mountinfo`, ext4/XFS/Btrfs semantics.
- Test gap: all path-swap, mount, crash, hard-link, hostile-Trash, and 30,000-attempt release campaigns are absent.

### Phase 4 — policy engine, state, and recovery

- Existing files: no engine or persistent state.
- Create: provider/action registries, config policy, plan builder, immutable apply coordinator contracts, history/state store, cancellation and recovery state machine.
- Interfaces: `Provider`, `Candidate`, `Planner`, `Policy`, `Executor`, `StateStore`, `HistoryStore`, `RecoveryCoordinator`.
- Risky dependencies: configuration decoder and durable write/locking semantics.
- Test gap: no determinism, conflict-resolution, stale-plan, cancellation, crash-recovery, or schema-migration tests.

### Phase 5 — provider discovery and read-only inventory

- Existing files: no parsers or distribution adapters.
- Create: APT, DNF5, Pacman, Flatpak, Snap, XDG cache, journald, project-artifact, kernel/package metadata, and metrics discovery adapters plus raw fixtures.
- Interfaces: capability-gated `Discover`, typed manager transaction graph, byte-size estimator, provider provenance.
- Risky dependencies: locale-sensitive CLI output, manager locks, incomplete metadata, optional tools, Btrfs allocation estimates.
- Test gap: no fixtures from the supported distribution/release/architecture matrix and no read-only host guards.

### Phase 6 — CLI, structured output, and TUI

- Existing files: command surface exists only in the design report.
- Create: Cobra wiring, stable exit/error contract, JSON and NDJSON presenters, no-TTY behavior, Bubble Tea/Lip Gloss TUI, shell completion.
- Interfaces: application use cases for every public command; presenter-neutral progress/events; confirmation request/response.
- Risky dependencies: terminal capability/width, cancellation, broken pipes, localization, accidental presenter authority.
- Test gap: no golden outputs, PTY tests, accessibility/no-color tests, latency benchmarks, or command parity checks.

### Phase 7 — user-owned mutation and installer cleanup

- Existing files: no mutation implementation.
- Create: unprivileged executor composition, Trash/permanent purge flows, installer-artifact provider, apply confirmation, cancellation/recovery integration.
- Interfaces: typed action executors composed only from Phase 3 primitives; apply receipt and reconciliation commands.
- Risky dependencies: same-UID race boundary, hard links, open files, cross-device Trash, interrupted staging.
- Test gap: no proof that dry-run is side-effect free or that mutation cannot escape approved roots.

### Phase 8 — privilege protocol and reject-only helper

- Existing files: no protocol or packaged helper.
- Create: strict framed canonical CBOR, caller derivation, launcher/self checks, root/action registries, static polkit/sudo integration, reject-only helper and architecture gates.
- Interfaces: codec, request validator, `TrustedCaller`, `HelperPolicy`, response/result binding.
- Risky dependencies: `pkexec` authorization order, sudo environment, framing limits, parser resource exhaustion.
- Test gap: no malformed protocol corpus, fuzzing, root-spoof matrix, prompt-boundary tests, or installed-file permission tests.

### Phase 9 — privileged package and system mutation

- Existing files: discovery contracts only after Phase 5; no manager executor.
- Create: fixed manager command vocabulary, clean environment builder, transaction graph comparator, one validated action family at a time for APT/DNF5/Pacman, privileged journald/package cleanup, interruption reconciliation.
- Interfaces: operation enum to compiled absolute command/argv; bounded runner; preflight re-simulation; outcome reconciliation.
- Risky dependencies: manager concurrent state, locks, maintainer scripts, reboot needs, partial transactions.
- Test gap: no disposable-package fixtures or negative VM tests proving authority cannot expand.

### Phase 10 — optimization, fingerprint, and remaining parity

- Existing files: no parity features implemented.
- Create: conservative user-approved optimization recipes, read-only fingerprint capability adapter, update/remove orchestration, optional backend parity, parity ledger tests.
- Interfaces: reversible optimization proposal/apply/rollback; fingerprint status/enrollment handoff; signed update metadata contract.
- Risky dependencies: distro configuration variance, fprintd enrollment UX, archive self-update trust, service restart behavior.
- Test gap: no idempotency/rollback checks, unsupported-host behavior, or command-by-command Mole semantic parity suite.

### Phase 11 — packaging, release, and VM qualification

- Existing files: no package or release infrastructure.
- Create: Debian/RPM/Arch/archive packaging, polkit/helper install rules, SBOM/checksum/signature/provenance generation, VM images/harness, support-matrix and performance gates.
- Interfaces: package lifecycle scripts, release manifest, artifact verification, disposable-VM sentinel.
- Risky dependencies: native packager policy differences, cross-architecture runners, signing custody, official-repository requirements.
- Test gap: all 15 supported VM combinations, package upgrade/remove behavior, rootless archive exclusions, 100k/1M inventory performance, and full filesystem adversarial release gates are absent.

## Cross-phase dependency finding

The critical path is Phase 1 → 2 → 3 → 4. Read-only provider and presenter work can overlap only after domain/application contracts stabilize. No mutation phase may bypass Phase 3 primitives. No privileged executor may begin before the installed reject-only helper passes Phase 8. Full support and parity claims remain blocked until Phase 11 qualifies every supported VM and architecture.

## Questions discoverable only later

The module path depends on the future remote; reference performance hardware depends on CI capacity; signing custody and official distribution submission depend on release governance. These are explicit phase entry gates, not blockers to writing the implementation plan.
