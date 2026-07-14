---
date: 2026-07-14
session: LDC Linux-native product and architecture design
status: approved
---

# Journal: 2026-07-14 — LDC design decisions

## Context

The [approved LDC design report](../../plans/reports/260714-1754-ldc-linux-native-design-report.md) defines the end-state product and architecture for a safe Linux-native maintenance tool. This entry records the decisions and their impact; it is not an implementation plan or schedule.

## What Happened

- Established LDC as an independent Linux product with its own identity and Linux-native behavior.
- Selected a Go-first architecture and a single typed engine shared by every interface.
- Made immutable previews, point-of-use revalidation, and a narrow privilege boundary the basis of all mutation.
- Bounded mutation to an explicit tested platform matrix and documented unsupported environments and non-goals.

## Reflection

The design favors semantic integrity and provable safety over superficial compatibility. Full parity preserves Mole's user jobs, not its Apple-specific mechanisms or implementation. That choice creates a broad validation burden, but it also gives LDC one coherent contract: unsupported or ambiguous operations fail closed instead of being simulated or forced.

## Decisions Made

| Decision | Rationale | Impact |
|---|---|---|
| Build **LDC — Linux Deep Clean** as an independent product, not a port or fork. | Linux needs distinct platform semantics, branding, and independently authored implementation. | LDC keeps a separate identity and provenance while targeting equivalent user outcomes. |
| Distribute as `linux-deep-clean` and install only `ldclean`. | `ldc` is already the established LLVM D compiler command across supported distributions. | Packaging and documentation must never claim an `ldc` executable or alias. |
| Target semantic full parity with Mole's public command families. | Users need equivalent jobs, not copied Apple-only internals or unsafe Linux approximations. | Linux-native providers may report real capability gaps; unavailable behavior is never silently simulated. |
| Use a Go-first architecture with compiled providers and one typed domain engine. | Go best balances delivery breadth, Linux syscall access, packaging, and contributor maintainability. | TUI, CLI, JSON, and NDJSON share discovery, policy, planning, validation, execution, and results. |
| Require plan/preview first for every mutation. | Discovery state can drift and destructive intent must be explicit. | Actions enter an immutable plan; apply requires an explicit request and point-of-use revalidation. |
| Anchor filesystem mutations to approved directory descriptors. | String paths, prefix checks, and `realpath`-then-delete cannot close race and traversal hazards. | Supported mutation uses `openat2` resolution constraints, `statx` preconditions, and descriptor-relative operations; missing guarantees fail closed. |
| Keep the main process unprivileged and use a short-lived helper only for selected native-package actions. | A resident or generic root process would expand the attack surface. | The helper accepts a closed typed action vocabulary, independently validates the plan, exposes no shell or arbitrary command runner, and exits after one request. |
| Enable mutation only on the tested Ubuntu, Debian, Fedora non-atomic, and Arch matrix with the required kernel, systemd, architecture, and local filesystem constraints. | Safety claims require empirical platform coverage. | Other Linux systems may receive safe read-only behavior; immutable hosts, WSL, containers, chroots, non-systemd systems, and unsupported mounts remain mutation-disabled, and `--force` cannot bypass that boundary. |
| Preserve explicit exclusions such as daemons, schedules, arbitrary plugins, generic tuning, package upgrades, block-device work, private-data cleanup, and hand-edited PAM. | These capabilities weaken ownership, reversibility, or the stated threat model. | The product remains terminal-first, user-driven, evidence-backed, and narrowly authoritative. |
| Defer implementation planning to a later session. | The approved report settles product and architecture without authorizing milestones or task decomposition. | Future planning must use the report as fixed design input; changing identity, support, privilege, safety, parity, or exclusions requires a new user decision. |

## Next

Implementation planning remains explicitly deferred. No schedule, phase breakdown, dependency pin, or implementation task is authorized by this journal.
