---
title: "LDC full-parity Linux implementation"
description: "Implement the independently authored, plan-first Linux Deep Clean product and qualify its complete Linux-native command contract."
status: in-progress
priority: P1
effort: "32-48 engineer-weeks"
branch: main
tags: [feature, backend, infra, security, critical]
blockedBy: []
blocks: []
created: 2026-07-14
---

# LDC full-parity Linux implementation

## Fixed Scope

Build an independent Apache-2.0 Linux-native product named **LDC — Linux Deep Clean**. The repository/package slug is `linux-deep-clean`; the only user executable is `ldclean`—never `ldc`. Deliver semantic full parity with Mole's public command families through Linux-native providers and explicit provider-specific guarantees, not copied internals or unsafe approximations.

The fixed public families are no-command menu/help, `clean`, `uninstall`, `optimize`/`optimise`, `analyze`/`analyse`, `status`, `history`, `purge`, `installer`, `fingerprint`, `completion`, `update`, `remove`, `--help`, and `--version`; `--whitelist` remains the compatibility-facing protection-list name. `fingerprint` is the Linux-native replacement for Touch ID—never add a `touchid` command.

Mutation support is limited to systemd hosts with kernel >=5.15 and local ext4, XFS, or Btrfs paths on these 15 guests: Ubuntu 22.04/24.04/26.04 x86_64+aarch64; Debian 12/13 x86_64+aarch64; Fedora 43/44 non-atomic x86_64+aarch64; current Arch x86_64. Other environments may be read-only; `--force` never bypasses a support boundary.

Authoritative inputs:

- [Approved product and architecture design](../reports/260714-1754-ldc-linux-native-design-report.md)
- [Approved decision journal](../../docs/journals/2026-07-14-ldc-design-decisions.md)
- [Repository/toolchain research](./research/01-go-toolchain-and-repository-architecture.md)
- [Filesystem/privilege safety research](./research/02-filesystem-and-privilege-safety.md)
- [Provider/qualification research](./research/03-linux-provider-contracts-and-qualification.md)
- [Greenfield scout report](./reports/scout-report.md)

## Goals

1. One typed Go engine for CLI, TUI, JSON, NDJSON, history, and both privileged/unprivileged execution paths.
2. Immutable canonical preview/plan before explicit apply; point-of-use revalidation; fail-closed drift and unsupported results.
3. Descriptor-anchored filesystem mutation with raw-byte paths, durable Trash/recovery, and no path-string authority.
4. Unprivileged `ldclean` plus a short-lived, independently validating, closed-vocabulary helper only in native packages.
5. Provider-specific inventory, simulation, apply, verification, `indeterminate`, and reconciliation contracts.
6. Complete approved command/alias parity and release qualification across the fixed matrix.

## Non-Goals

No GUI, daemon, scheduler, unattended cleanup, plugins/downloaded rules, general upgrades, firmware/kernel/package-orphan automation, unsafe tuning, block operations, filesystem repair, perceptual duplicates, Trash emptying, browser-history/cookie/credential/application-data deletion, hand-edited PAM, telemetry, accounts, cloud sync, curl-pipe installer, or v1 AppImage/Flatpak/Snap/container package. Native-manager side effects are disclosed but never expanded by LDC. Distribution-owned repository submission is downstream governance; initial signed LDC-owned artifacts are sufficient.

## Architecture and Authority Boundaries

```text
presenters -> application use cases -> engine/policy -> providers (discovery only)
                                      -> immutable canonical Plan
                                      -> point-of-use revalidation
                                      -> unprivileged executors OR helper client
                                      -> verification -> private local state/history

helper -> dependency-free domain + strict protocol + helper policy + linuxfs + managerexec
```

- Presenters may import only application use cases and dependency-free domain view contracts; they never discover, plan, authorize, or mutate.
- Providers emit typed evidence/candidates only; they never import presenters, state, mutation executors, or helper protocol.
- No provider/presenter directly calls `renameat2`, `unlinkat`, `execve`, or privilege framing.
- Helper imports are allowlisted to dependency-free domain types, strict privilege validation/protocol, apply-time capability probes, `linuxfs`, and `managerexec`. It must not import application, discovery providers, state/history, metrics, presenters, Cobra, Bubble Tea, or Lip Gloss.
- Filesystem authority is `TrustedRootID + BytePath` raw components. Canonical CBOR carries byte strings; display/JSON text never reconstructs authority.

## Dependency Graph

```text
Phase 1 -> 2 -> 3A -> 4A -> 3B -> 4B -> 5 -> 6 -> 7 -> 8 -> 9 -> 10 -> 11
                 safety    ledger   content                  user mutation
                 foundation          primitives              / first apply
```

Later implementation may parallelize read-only adapters behind stable Phase 4 interfaces, but phase acceptance is sequential except for the narrowly approved Phase 4A interphase dependency below. No mutation bypasses Phase 3 primitives; no privileged executor exists before Phase 8's installed reject-only helper passes.

## Approved Phase 3/4 Interphase Boundary

The original sequential order contains a verified dependency cycle: Phase 3
content moves and reconciliation need a durable source-bound intent, while
Phase 4 owns that mandatory recovery state. The user approved this exact,
limited order:

```text
Phase 3A safety foundation -> Phase 4A durable intent ledger
-> Phase 3B content-operation primitives -> Phase 4B remainder
-> Phases 5/6 -> Phase 7 first production apply
```

Phase 4A is only the [durable intent and recovery ledger](./phase-04a-durable-intent-ledger.md).
It owns a configuration/private-state-backed, versioned, bounded,
descriptor-rooted ledger and no more. It may bind source facts and record
pre-/post-effect transitions, but it may not add a root/layout registry,
XDG discovery/composition, providers, planner, application service,
executor, command, target/layout discovery, content mutation, arbitrary path
authority, or an in-memory production store. It does not satisfy Phase 3's
per-mount layout-authority or disposable-VM gates.

Phase 3B may consume only that narrow ledger port to implement the already
planned Trash/quarantine content primitives. Actual production root/layout
registration, explicit-plan application, and VM mutation qualification remain
with their established later owners; a ledger record never authorizes an
operation by itself.

## Phases

| # | Phase | Status | Exit gate |
|---:|---|---|---|
| 1 | [Repository, toolchain, and contract harness](./phase-01-start.md) | Completed | Permanent remote/module selected; hermetic harness and import gates pass |
| 2 | [Canonical domain and plan protocol](./phase-02-canonical-domain-and-plan-protocol.md) | Completed | BytePath, schema, deterministic CBOR/digest, and result states frozen |
| 3A | [Linux filesystem safety foundation](./phase-03a-safety-foundation.md) | Completed | Descriptor-rooted safety primitives and metadata-only composition pass without content mutation |
| 4A | [Durable intent and recovery ledger](./phase-04a-durable-intent-ledger.md) | Completed | Bounded descriptor-rooted intent/recovery facts persist and reload without content mutation |
| 3B | [Linux filesystem content operations](./phase-03b-content-operations.md) | Pending | Anchored ledger-backed Trash/quarantine content operations and adversarial smoke gates pass |
| 4B | [Policy engine, state, and recovery](./phase-04-policy-engine-state-and-recovery.md) | Pending | Deterministic planning, monotonic policy, private durable state, recovery pass |
| 5 | [Provider discovery and read-only inventory](./phase-05-provider-discovery-and-read-only-inventory.md) | Pending | Supported provider parsers and inventory contracts pass without mutation |
| 6 | [CLI, structured output, and TUI](./phase-06-cli-structured-output-and-tui.md) | Pending | Exact command/alias, schema, no-TTY, and presenter-authority gates pass |
| 7 | [User-owned mutation and installer cleanup](./phase-07-user-owned-mutation-and-installer-cleanup.md) | Pending | Explicit apply, Trash/default purge, installer, cancellation, recovery pass |
| 8 | [Privilege protocol and reject-only helper](./phase-08-privilege-protocol-and-reject-only-helper.md) | Pending | Installed helper rejects all execution and malformed/expanded authority |
| 9 | [Privileged package and system mutation](./phase-09-privileged-package-and-system-mutation.md) | Pending | Per-provider validator+executor+drift+reconciliation VM slices pass |
| 10 | [Optimization, fingerprint, and remaining parity](./phase-10-optimization-fingerprint-and-remaining-parity.md) | Pending | Every approved command has honest supported/degraded behavior |
| 11 | [Packaging, release, and VM qualification](./phase-11-packaging-release-and-vm-qualification.md) | Pending | Native/rootless artifacts and all release gates pass on 15 guests |

Phase 1 completion evidence: [bootstrap handoff report](./reports/pm-260715-2132-phase-1-bootstrap.md).

Phase 2 completion evidence: [canonical protocol handoff report](./reports/pm-260716-1254-phase-2-canonical-protocol.md).

Phase 3A safety-foundation evidence: [in-progress handoff report](./reports/pm-260716-phase-3-safety-foundation.md). The composite Phase 3 requirements remain preserved in [the original execution document](./phase-03-linux-filesystem-safety-and-trash.md); its completed safety work is formalized by 3A and its remaining content work by 3B. Phase 3B remains gated on an engine/helper-owned per-mount layout authority and disposable-VM qualification; Phase 4A does not relax either gate or enable a production command/composition.

Phase 4A completion evidence: [durable intent and recovery ledger report](./reports/pm-260717-phase-4a-durable-intent-ledger.md). The next node is Phase 3B; Phase 4A itself adds no content operation or production composition.

## Decision and Entry Gates

| Gate | Must be resolved before |
|---|---|
| Create/configure a permanent Git remote and derive the permanent Go module path; never invent an owner or temporary module | Phase 1 `go mod init` |
| Re-check current supported Go 1.26 patch and audit/pin every direct dependency; CI/release use `GOTOOLCHAIN=local` | Phase 1 dependency lock |
| Select a TOML decoder only after license, maintenance, fuzzing, and strict unknown-field review | Phase 4 config implementation |
| Freeze action-specific `statx` required masks and prove a safe per-mount quarantine location; unsupported roots reject | Each Phase 3/7 action kind |
| Record stock `pkexec` limitation: LDC displays dynamic privileged details; polkit prompt remains static | Phase 8 UX/protocol |
| Add any helper action enum only with validator, fixed executable/argv mapping, negative authority tests, VM proof, and security review | Phases 9-10 |
| Select reference hardware, signing custody, and cross-architecture VM runners | Phase 11 release qualification |

## Whole-Plan Quality Gates

- Default `go test ./...` is hermetic, offline, unprivileged, host-safe, and never invokes authorization. Host-isolated integration uses `-tags=integration`; destructive tests require `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, and a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches that token.
- VM package tests use only locally built `linux-deep-clean-test-*` packages. No arbitrary host package is ever a mutation fixture.
- Minimum statement coverage: 90% domain/policy/planning/protocol/recovery; 85% provider parsers/structured presenters; 80% other behavior-bearing packages. Behavioral gates override headline coverage.
- Required gates: `go test -race`, `go vet`, `govulncheck`, fuzz corpora, architecture/import tests, golden JSON/NDJSON/help contracts, native package lint/signature/SBOM/provenance checks.
- PR VMs: Ubuntu 24.04 x86_64, Debian 13 x86_64, Fedora 44 x86_64, current Arch x86_64. Nightly/release: all 15 supported guests.
- Release filesystem campaign: >=10,000 completed mutation race attempts on each ext4/XFS/Btrfs (30,000 total), every race class represented, zero outside-root mutation; PR smoke: >=1,000 ext4 attempts.
- Performance: help/version p95 <=150 ms; TUI first paint p95 <=300 ms; 100k inventory <=2 s/128 MiB; 1M deep scan <=60 s/256 MiB; <=4 scan workers; <=128 FDs; cancellation <=1 s except a non-interruptible manager transaction; dry-run has zero mutation, authorization, and network; controlled ext4 estimate within max(5%, 64 MiB).

## Product Acceptance Criteria

- [ ] Every approved command and alias has interactive and non-interactive behavior backed by the same engine.
- [ ] Every mutation is represented in an immutable canonical plan, explicitly applied, revalidated, verified, and recorded.
- [ ] Unsupported/ambiguous hosts and provider drift fail closed; `--force` cannot broaden authority.
- [ ] Results distinguish estimated, attempted, verified, skipped, drifted, denied, unsupported, failed, interrupted, and indeterminate effects; unknown completion requires reconciliation.
- [ ] Main process refuses effective UID 0; helper accepts no shell, path authority, executable, argv, environment, or root mapping from the caller.
- [ ] Rootless archive excludes helper and polkit/sudo integration; native deb/rpm/Arch packages include their audited privileged integration.
- [ ] All 15 VM targets, filesystem races, package lifecycle, performance, provenance, dependency, package, and security reviews pass.
- [ ] No excluded behavior or `ldc` executable/alias is present; LDC never empties Trash.

## Red Team Review

Full-tier adversarial review sampled 184 cross-phase claims through security/fact-checking, failure-flow, scope/assumption, and contract/complexity lenses. It accepted 10 draft defects (1 critical, 7 high, 2 medium), rejected 7 unsupported/style-only concerns, and preserved every approved user decision. All accepted findings are resolved; see the [evidence and resolution report](./reports/red-team-review.md).

Key corrections standardized the token-bound VM guard, isolated providers behind a read-only tree port, established one Phase 2 transaction graph, assigned all deferred parity executors, kept user Flatpak unprivileged, and moved byte-mutating package signing before final artifact freeze.

### Whole-Plan Consistency Sweep

- Files reread: `plan.md` and all 11 `phase-*.md` files.
- Decision deltas checked: 14, including the 10 accepted findings plus file-action, history-flow, real-fixture, and complete-plan/execution-subset protocol clarifications.
- Reconciled stale references: 14; active-plan searches find no old sentinel, duplicate graph type, wrong BytePath symbol, implicit history-repair command, partial-plan helper authority, or pre-freeze qualification claim.
- Unresolved contradictions: 0.

## Validation Log

### Session 1 — 2026-07-14

**Trigger:** `/ak:plan --tdd --deep` after the user approved the independent-LDC design and recommended constraints.

**Questions asked:** 0 new. The two prior approval gates already fixed the product-level choices; no remaining question would change implementation rather than merely resolve a named phase-entry gate.

#### Confirmed Decisions

- Independent Apache-2.0 Linux-native product; package/repository slug `linux-deep-clean`; executable `ldclean`, never `ldc`.
- Semantic full parity with the approved Linux substitutions, explicit exclusions, and truthful optional-capability degradation.
- Go-first architecture, unprivileged main process, short-lived closed-vocabulary helper, immutable plan before explicit apply, and fail-closed drift handling.
- Mutation qualification on the fixed 15-VM/systemd/kernel/filesystem matrix; rootless archives exclude privileged integration.
- TDD phase order and full release evidence are mandatory; a missing matrix or safety gate blocks the parity claim.

#### Verification Results

- Detailed evidence: [validation report](./reports/validation-report.md).
- Tier: Full (11 phases; all four verification roles).
- Claims checked: 184.
- Verified: 179; failed: 0; unverified: 5 explicit implementation entry gates.
- Entry gates: permanent remote/module path, TOML decoder selection, safe per-mount quarantine layout, signing custody, and reference hardware/runners.

#### Impact and Consistency

- Phases 1, 2, and 4–11 received the accepted contract corrections; Phase 3 remained the unchanged filesystem authority baseline.
- Plan schema validation, relative-link resolution, sequential dependency checks, duplicate-create checks, and stale-term searches pass.
- Whole-plan sweep: 12 plan/phase files reread; unresolved contradictions: 0. The plan is eligible for implementation, with each named entry gate remaining a hard stop at its owning phase.

## Handoff

After plan validation and adversarial review, execute sequentially with:

`/ak:cook plans/260714-1831-ldc-full-parity-linux-implementation/plan.md --tdd`

Implementation must stop at any unresolved decision/entry gate. Phase files are the executable contracts; this index does not authorize implementation shortcuts or a full-parity claim before Phase 11.

<!-- slug: ldc-full-parity-linux-implementation -->
