---
phase: "4A"
title: "Durable Intent and Recovery Ledger"
status: completed
priority: P1
effort: "3-5 engineer-days"
dependencies: ["3A"]
---

# Phase 4A: Durable Intent and Recovery Ledger

## Overview

Resolve the Phase 3/4 durability dependency cycle without starting the policy
engine, application, or content operations. Build only a private,
LDC-owned intent/recovery ledger over a qualified
`LayoutPrivateState` lease. It records what a later descriptor-rooted
Trash/quarantine operation intends to do and whether its pre-/post-effect
boundaries are known; it never performs, selects, or authorizes that operation.

## Scope and hard boundary

In scope:

- Versioned, canonical, bounded immutable ledger records beneath an existing
  `linuxfs.PrivateDirectoryLease` for `LayoutPrivateState`.
- A no-follow descriptor-backed **ledger session** plus bounded record
  read/enumeration needed to reserve a token, reload outstanding intent after
  a crash, and prevent concurrent duplicate reservations. Only the opaque
  session may perform lookup, conflict checking, or publication while the
  OS-held lock is live.
- A narrow `internal/state` port for reservation, transition recording, exact
  source lookup, and bounded outstanding-recovery lookup.

Out of scope:

- XDG/config discovery, root/layout registration, providers, capability
  probes, planner, engine/application composition, executor registration,
  commands, target/layout discovery, content move/restore/remove/reconcile,
  retention cleanup, or VM qualification.
- Any raw descriptor/path exported outside `linuxfs`, arbitrary filesystem
  read/list/write authority, replacement/delete state APIs, and an in-memory
  production implementation.

## Requirements

### Durable record contract

- A reservation accepts one validated typed filesystem action plus a plan
  digest, then derives the trusted root ID, exact raw-component source
  `BytePath`, filesystem action kind/ID, action-specific precondition,
  canonical action-binding digest, and closed destination semantics (`trash`
  or `quarantine`) itself. The source in the precondition must equal the
  action target and separately stored source/root facts. The action-binding
  digest is computed by a pure domain-separated `planproto` helper; it binds
  this supplied action to the plan digest but does not claim that the action is
  a member of a verified full plan, which remains Phase 4B work.
- Tokens are ledger-generated opaque `ldc-` plus lowercase-hex values with at
  least the Trash contract's entropy floor. A caller cannot choose a token,
  file name, state directory, or destination path.
- Each transition repeats the immutable reservation identity and moves only
  through a closed typed event graph. `intent_reserved` is first. For Trash,
  only `metadata_dispatch_recorded -> metadata_verified` or
  `metadata_indeterminate` may follow; both destinations then require
  `move_dispatch_recorded`, followed by exactly one of `move_verified` or
  `move_indeterminate`. A verified retained move may later use
  `restore_dispatch_recorded -> restore_verified` or
  `restore_indeterminate`. An indeterminate dispatch may be followed only by
  `reconciliation_resolved`, whose closed outcome is `not_applied`,
  `move_verified`, or `restore_verified`. `reconciliation_resolved(not_applied)`
  is also legal directly from `intent_reserved` or `metadata_verified` for a
  cancellation before a content dispatch; it carries the closed metadata fact
  `absent` or `retained`. `metadata_indeterminate` may resolve only through
  that same retained/absent no-effect reconciliation. Quarantine skips all
  metadata events. Each event carries an explicit ordinal and predecessor
  digest. Events record facts only—never a retry instruction, destination
  path, cleanup instruction, or content authority—and 4A itself emits no
  content-side event.
- Records use a fixed v1 canonical-CBOR envelope with bounded byte/frame,
  component, record-count, and lookup-result limits. Future versions and
  malformed/noncanonical/corrupt entries are read-only failures; they are
  retained and never silently repaired, overwritten, or removed.
- Reservation and transition publication use existing durable no-replace
  semantics. Any error after creation may have succeeded is `interrupted` and
  requires lookup/reconciliation; the ledger never treats it as a clean retry.
- Ledger record IDs are deterministic, LinuxFS-validated names derived from
  the action-binding digest, the ledger-generated reservation token, and a
  fixed transition ordinal. This permits a later legal reservation of the
  same resolved action/plan while preserving one outstanding source intent.
  Replay has an explicit canonical ordering and validates a predecessor
  digest/ordinal; it never relies on directory enumeration order. A
  post-create interruption returns its opaque reservation identifier alongside
  `interrupted` when possible, so it can be found by exact record ID; after a
  process crash, bounded exact-source lookup finds it rather than retrying a
  new reservation blindly.

### Concurrency, lookup, and crash behavior

- Reserve/read/transition sequencing runs inside one opaque no-follow
  private-state LinuxFS ledger session. The session holds a cross-process
  lock across lookup, conflict checking, and durable publication; it
  rechecks the private lease before use and closes without mutating retained
  records. State cannot issue unlocked ledger reads or publications.
- A write session may initialize only the fixed, LinuxFS-owned 0600 lock entry
  before its first reservation; it is excluded from ledger enumeration and is
  never an intent record. A read-only session never creates it: a missing,
  hostile, or unverifiable lock entry fails closed and leaves the private
  directory unchanged.
- Exact-source lookup is bounded and rejects duplicate/conflicting outstanding
  reservations. A bounded outstanding lookup returns typed recovery facts only;
  `limit + 1` records, any malformed/future/corrupt relevant record, a chain
  gap, or conflicting replay makes the ledger read-only and blocks every new
  reservation/transition. It never skips a bad tail, returns an ambiguous
  valid prefix as complete, rewrites, truncates, or repairs records.
- A process crash before durable reservation creates no intent; after a
  successfully published reservation, its durable facts remain discoverable;
  a crash around transition publication becomes `interrupted`/unknown rather
  than authorizing replay. No recovery reader mutates state.
- The ledger is configuration-private and has no content mount authority. A
  future Phase 3B cross-mount/layout mismatch is a drift/unsupported fact and
  must prevent copy-delete or fallback behavior; recording intent never makes
  a cross-mount move legal.

### Package boundaries

- `internal/state` may import `domain`, `pathbytes`, `planproto` where needed
  for canonical bindings, and narrow `linuxfs` ledger primitives only. It may
  not import `mounts`, raw `unix`/`os` filesystem APIs, engine, provider,
  application, presenter, helper, or command packages.
- `internal/linuxfs` owns all descriptor operations and accepts only a
  qualified `PrivateDirectoryLease` plus ledger-owned validated identifiers.
  It returns bytes/names/facts within fixed bounds; it exports no path or FD.
- The raw LinuxFS record-ID/session port is architecture-gated to
  `internal/state`; no other production package may construct an identifier,
  open a session, or bypass state-owned token generation and canonical-frame
  validation.
- Every LinuxFS ledger session primitive verifies `LayoutPrivateState` itself
  before locking, reading, enumerating, or publishing. Thus `internal/state`
  need not import `mounts`, and staging/quarantine/private-directory leases
  fail before any state entry is touched.
- The only exported production ledger constructor accepts a qualified
  `*linuxfs.PrivateDirectoryLease`; there is no path/FD constructor,
  caller-selected storage interface, `NewWithStore`, or `MemoryStore`.
  State unit tests use pure frame/reducer inputs; any fault seam is
  unexported and package-local.

## Planned files

| Action | Paths | Purpose |
|---|---|---|
| Modify | `internal/linuxfs/durable_file_linux.go` and focused tests | Add a narrowed private-record read/enumeration and opaque no-follow ledger session alongside existing durable publication. |
| Modify | `internal/domain/identifiers.go`, `internal/planproto/action_digest.go` and tests | Add a semantic action-binding digest and its pure canonical, domain-separated derivation. |
| Create | `internal/state/recovery_ledger.go` | Typed reservation, transition, lookup, schema validation, bounded canonical encoding. |
| Create | `internal/state/recovery_ledger_test.go` | TDD state-machine, binding, corruption, capacity, crash/duplicate reservation tests. |
| Modify | `internal/architecture/import_rules_test.go` | Add a strict `state` import allowlist and preserve raw-syscall authority boundaries. |
| Modify | `docs/contracts/trash-and-quarantine.md` | State the implemented ledger prerequisite and Phase 3B consumption boundary. |

## TDD-first test matrix

1. **RED — linuxfs private ledger session:** a qualified test-owned private
   lease can enter one OS-held session that locks, publishes, reads, and
   bounded-enumerates only regular 0600
   records; nil/closed/wrong-kind leases, symlink/special/oversized/hostile
   entries, bad identifiers, busy/cancelled locks, and limit overflow fail
   closed without a path/FD escape. Two independently opened leases admit one
   reservation session at a time and closing releases only the lock.
2. **GREEN — descriptor-only primitives:** implement the narrow no-follow
   read/enumeration/session operations with held-descriptor requalification,
   deterministic record ordering, and bounded allocation before decoding.
3. **RED — ledger binding:** reject mismatched root/path/precondition,
   unrecognized action/destination/state, caller token, zero digest,
   malformed or noncanonical frame, duplicate reservation, invalid
   transition, replay order/chain gaps, and conflicting transition history.
4. **GREEN — immutable ledger:** generate a token under the opaque lock,
   publish the deterministic reservation first, append only valid deterministic
   transitions, and rebuild recovery facts from bounded retained records.
5. **REFACTOR — fault semantics:** inject publication/read failures to prove
   pre-create rejection has no record, post-create uncertainty is found by
   its exact deterministic ID after a fresh ledger instance, readers never
   repair, and capacity/corruption fails closed. Pure state tests never need
   a production memory/path backend.

## Acceptance criteria

- Every accepted reservation and transition binds the required source/action/
  precondition/digest/token/destination facts exactly and can be read back
  through a qualified private lease only.
- No ledger API can select a path, mutate user content, register authority, or
  claim a content effect. Cross-mount/content questions remain Phase 3B facts.
- Duplicate/outstanding intents, malformed/future/corrupt records, ambiguous
  publication, replay conflicts, and capacity exhaustion block a new unsafe
  reservation; a fresh concrete ledger instance proves its result comes from
   retained records rather than process memory. Layer this proof: LinuxFS
   reopens a fresh session over its existing test-only qualified lease, while
   state replays retained-frame fixtures through a fresh reducer/ledger
   instance; never add cross-package path authority just for a test.
- Focused LinuxFS/state tests, `go test -race`, architecture import tests, and
  the full hermetic repository suite pass before the Phase 3B handoff.

## Handoff

Phase 3B receives an opaque ledger port: reserve before durable Trash metadata
publication, record pre-effect immediately before a no-replace move, record
verified or indeterminate post-effect facts, and use bounded lookup during
reconciliation. It does not receive a state path, raw descriptor, arbitrary
token, or permission to bypass layout qualification or VM proof.
