---
date: 2026-07-14
session: LDC deep TDD implementation planning
status: complete
---

# Journal: 2026-07-14 — LDC deep implementation plan

## Context

The [deep implementation plan](../../plans/260714-1831-ldc-full-parity-linux-implementation/plan.md) translates the approved LDC product and architecture into an executable TDD sequence. Its goal is to build and qualify the independently authored `linux-deep-clean` product, installed only as `ldclean`, with semantic full parity through Linux-native providers and fail-closed safety contracts. This session produced planning artifacts only; no source, test, package, or configuration implementation was performed.

## What Happened

- Produced 11 sequential phases covering repository/toolchain setup, canonical domain and plan protocol, filesystem safety and Trash, policy/state/recovery, provider discovery, CLI/TUI presenters, user mutation, reject-only privilege protocol, privileged mutation, remaining parity, and release qualification.
- Made TDD exit gates explicit for each phase, with narrow tests first and full architecture, race, fuzz, VM, performance, packaging, provenance, and security gates before the parity claim.
- Preserved the approved architecture: one typed Go engine; immutable canonical plans before explicit apply; descriptor-anchored raw-byte filesystem authority; an unprivileged main process; a short-lived closed-vocabulary helper; provider-specific verification and reconciliation; and mutation only on the fixed 15-guest, systemd, kernel, and local-filesystem matrix.
- Preserved the approved exclusions and compatibility decisions, including `--whitelist`, `fingerprint` instead of Touch ID, no `ldc` alias, no Trash emptying, and no daemon, scheduler, downloaded rules, generic upgrades, unsafe tuning, or application-data deletion.

## Red-Team Corrections

The [full-tier review](../../plans/260714-1831-ldc-full-parity-linux-implementation/reports/red-team-review.md) accepted and resolved 10 draft defects without reversing an approved user decision:

| # | Correction |
|---:|---|
| 1 | Standardized destructive VM tests on the token-bound `/run/linux-deep-clean/disposable-guest` guard. |
| 2 | Isolated providers behind the read-only `providerapi.ReadTree` port and audited discovery adapter. |
| 3 | Assigned one dependency-free `domain.TransactionGraph` to Phase 2 for preview and helper comparison. |
| 4 | Assigned explicit-file `analyze` mutation to Phase 10 through the qualified Trash executor. |
| 5 | Added owned user- and system-scope completion installation contracts and executors. |
| 6 | Added a preserving, explicitly applied state-repair executor. |
| 7 | Added uninstall orchestration for manager removal and independently selected attributable residues. |
| 8 | Kept user-scoped Flatpak mutation unprivileged while reserving the helper for system scope. |
| 9 | Moved byte-mutating package signatures before final artifact freeze and exact-byte qualification. |
| 10 | Standardized raw path authority on `pathbytes.BytePath` inside typed domain targets. |

## Validation and Reflection

The [validation report](../../plans/260714-1831-ldc-full-parity-linux-implementation/reports/validation-report.md) checked 184 claims: 179 verified, 0 failed, and 5 left as explicit implementation entry gates. Those gates are the permanent remote/module path, strict TOML decoder selection, action-specific `statx` masks plus safe per-mount quarantine layout, signing identity/custody, and reference hardware/native aarch64 runners.

The final consistency sweep found no missing relative links, dependency-order errors, duplicate file ownership, stale rejected contracts, or unresolved cross-phase contradictions. The plan is therefore eligible for implementation, but eligibility is not completion: each entry gate remains a hard stop at its owning phase, and semantic full parity cannot be claimed before Phase 11 qualifies the exact artifacts across the complete matrix.

The privilege protocol was also tightened so each helper invocation carries the complete canonical plan plus an explicit ordered execution subset. The subset is separately digest-bound and may contain only one privileged action family; non-requested plan actions remain inert context, and partial-plan encodings cannot broaden authority or bypass the request-size ceiling.

## Next

Hand off to the TDD implementation workflow with:

`/ak:cook plans/260714-1831-ldc-full-parity-linux-implementation/plan.md --tdd`

Execute phases sequentially and stop at any unresolved entry gate. AgentWiki publishing was skipped because no AgentWiki CLI or MCP tool was provided.
