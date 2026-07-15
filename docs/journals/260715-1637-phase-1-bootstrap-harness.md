---
date: 2026-07-15
session: Phase 1 repository, toolchain, and contract harness
status: complete
---

# Journal: 2026-07-15 — Phase 1 bootstrap harness

## Context

This session completes only Phase 1 of the [11-phase implementation plan](../../plans/260714-1831-ldc-full-parity-linux-implementation/plan.md). The goal was a small, safe bootstrap repository for LDC, not cleanup functionality or a claim of full parity.

## What Happened

- Fixed the permanent public identity: the `origin` remote is `git@github.com-personal-biendo27:biendo27/linux-deep-clean.git` and the Go module is `github.com/biendo27/linux-deep-clean`.
- Added the Apache-2.0 bootstrap project, provenance policy, thin Make targets, and the deliberately limited `ldclean` CLI: offline help/version, an EUID-0 refusal before dispatch, and an uninstalled helper that rejects every request.
- Pinned Go 1.26.5 in `.go-version`; automation uses `GOTOOLCHAIN=local`, with the audited Cobra v1.10.2 dependency confined to the CLI presenter boundary.
- Established default, integration, performance, fuzz, and VM suite locations. The default lane is source-gated against network, host mutation, privilege, shell, and unsafe process behavior; integration and VM lanes remain opt-in, and the VM lane fails closed without its environment/token/sentinel guards.
- Added CI that primes and verifies the module cache, then runs default/race/coverage/VM-guard work in a network-disabled namespace rather than treating unavailable isolation as success.

## Reflection

The bootstrap is intentionally restrictive because it establishes the boundaries that later phases must preserve. It contains no discovery, authorization, cleanup, or host mutation; Phase 1 provides trustworthy execution and test lanes rather than product breadth.

## Decisions Made

| Decision | Rationale | Impact |
|---|---|---|
| Keep the module and remote identity permanent from the start. | The module path is a public compatibility contract. | Future phases build under `github.com/biendo27/linux-deep-clean`; no temporary path is carried forward. |
| Clear `GOROOT` before Make and CI launch the pinned local Go toolchain. | An ambient `GOROOT` could redirect the bootstrap compiler. | `GOTOOLCHAIN=local` selects the installed pinned toolchain without silent download. |
| Permit `runtime.GOROOT()` only as the child Go executable path inside that already-trusted bootstrap process. | Default-lane Go subprocesses must not resolve `go` through ambient `PATH`. | Each child environment clears `GOROOT`, fixes `PATH=/usr/bin:/bin`, sets `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOWORK=off`, and empty `GOFLAGS`, and empties both VM-test opt-in variables. |
| Make host-sensitive test lanes explicit and fail closed. | Default tests must be safe on a developer host. | Integration is tagged; VM tests require the separate environment, token, and sentinel guard before their bodies run. |

## Next Steps

- Begin Phase 2: canonical domain types and the plan protocol, without weakening the Phase 1 import, toolchain, root-guard, or test-lane contracts.
- Treat the remaining phases as pending; completing Phase 1 does not complete the 11-phase plan.
- AgentWiki publishing was skipped because no AgentWiki CLI or MCP integration is available in this environment.
