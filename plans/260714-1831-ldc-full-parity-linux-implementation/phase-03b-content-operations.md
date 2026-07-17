---
phase: "3B"
title: "Linux Filesystem Content Operations"
status: pending
priority: P1
effort: "remaining subset of Phase 3"
dependencies: ["3A", "4A"]
---

# Phase 3B: Linux Filesystem Content Operations

This formal subphase owns every remaining content operation from the composite
[Phase 3 execution document](./phase-03-linux-filesystem-safety-and-trash.md):
`ReserveTrashToken`, Trash/quarantine move and no-replace restore, retention,
and orphan/recovery reconciliation. It consumes only the opaque Phase 4A
ledger port and the existing descriptor-rooted safety APIs.

## Non-negotiable prerequisites

- An engine/helper-owned, requalified per-mount layout authority proves the
  fixed Trash/quarantine/staging topology. No source path, environment, or
  discovery scan is authority.
- Before every effect, persist the legal Phase 4A dispatch event; after it,
  persist a verified or indeterminate event. A ledger record never authorizes
  a cross-mount copy-delete fallback, a permanent deletion, or a weaker path
  operation.
- Run the existing disposable-VM guard and Phase 3 adversarial/ext4 smoke
  gates before claiming the content-operation exit. Phase 4A does not supply
  those proofs.

## Exit gate

All remaining Phase 3 success criteria and focused/race/integration/VM gates
in the composite document pass, including zero outside-root mutation. Its
typed recovery results and replay format then become dependencies of Phase 4B.
