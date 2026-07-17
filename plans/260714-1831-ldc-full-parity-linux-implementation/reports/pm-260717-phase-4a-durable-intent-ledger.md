# Phase 4A Durable Intent and Recovery Ledger Report

Date: 2026-07-17

## Outcome

Phase 4A is complete. It establishes the narrow private-state durability
prerequisite for later Trash and quarantine work while leaving layout
selection, content mutation, recovery probing, application composition, and
commands unimplemented.

## Delivered

- A domain-separated canonical action-binding digest, with strict
  action-plus-plan CBOR encoding and decoding.
- Descriptor-rooted LinuxFS private-ledger sessions over an existing qualified
  `LayoutPrivateState` lease. The session exposes neither paths nor raw file
  descriptors, holds an OS lock over lookup and durable no-replace publication,
  requalifies the held private directory, and rejects hostile, malformed,
  future-versioned, missing-lock, over-bound, and ambiguous retained state.
- Immutable, bounded canonical-CBOR recovery frames with deterministic record
  IDs, ledger-generated opaque tokens, predecessor digests, a closed event
  graph, and retained immutable source/root/action/precondition/destination
  facts.
- A single production `state.RecoveryLedger` constructor that accepts only a
  qualified private-directory lease. It supports reservation, typed fact
  transitions, exact-source outstanding lookup, bounded recovery listing, and
  fresh-session reload; it has no path/FD constructor, caller-selected backend,
  or production in-memory implementation.
- Import gates that restrict `internal/state` to the narrow ledger LinuxFS
  surface and contract documentation that makes Phase 3B's ledger consumption
  boundary explicit.

## Validation evidence

- `GOCACHE=/tmp/ldc-gocache make coverage` passed, including the new mandatory
  >=90% state/recovery gate and the existing LinuxFS gate (90.1%).
- `GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go test ./... -count=1` passed.
- `GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go test -race ./... -count=1`
  passed.
- `GOCACHE=/tmp/ldc-gocache GOTOOLCHAIN=local go vet ./...` passed.
- `govulncheck v1.6.0 ./...` completed with no vulnerabilities found.

## Handoff

Phase 3B may use only the opaque ledger port: reserve before durable Trash
metadata publication, record dispatch and verified/indeterminate facts around
later descriptor-rooted moves, and use bounded recovery lookup during later
reconciliation. A record never authorizes content work. Phase 3B remains
blocked on real engine/helper-owned layout authority and the existing
disposable-VM qualification gates.
