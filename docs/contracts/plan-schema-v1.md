# Plan and result schema v1

Schema v1 is the closed, typed interchange contract between discovery, policy,
execution, history, and the future privileged helper. It represents a reviewed
proposal or a recorded outcome; it does not grant authority, select an
executable, or authorize a mutation.

## Authority model

Filesystem authority is always a pair of a compiled `TrustedRootID` and an
ordered `BytePath` of raw relative components. Components reject empty values,
`.` and `..`, NUL, and slash. No schema field stores an authority-bearing
absolute pathname. Human display is one-way only.

Manager objects are typed `ProviderID`, `ManagerObjectID`, and explicit
user/system scope values. Command text, caller argv, shell fragments, and
executable paths are not schema values.

## Plan body

The digest-excluded plan body has these required fields:

| Field | Meaning |
| --- | --- |
| `schema_version` | Must be integer `1`. Other versions reject. |
| `command` | Closed product command enum. |
| `caller` | Run ID, UID, interactive flag, and selected candidate IDs; audit echo only. |
| `capabilities` | Sorted closed support facts and probe revisions. |
| `config_digest` | Exactly 32 nonzero bytes identifying configuration facts. |
| `actions` | Intentional execution order of validated typed actions. |
| `totals` | Apparent/allocated and estimated/verified size facts with explicit availability. |
| `creation` | UTC creation instant representable as an `int64` Unix-nanosecond value, tool version, and host profile. |

The enclosing plan carries the same body plus a 32-byte `digest`. The digest is
always excluded when encoding the canonical body.

Action kinds are closed in v1: `trash_path`, `delete_recreatable_path`,
`quarantine_path`, `restore_trash_path`, `restore_quarantine_path`,
`repair_state`, `remove_native_package`, `remove_flatpak_ref`, `remove_snap`,
`clean_package_cache`, `vacuum_journal`, `run_owned_cache_rebuild`,
`install_completion`, `configure_fingerprint_auth`, `update_ldclean_package`,
and `remove_ldclean_package`.

Each action contains an ID, typed target, typed evidence and point-of-use
precondition, ordered dependency IDs, risk and reversibility classes, estimated
size facts, required capability, provider guarantee, and expected postcondition.
No enum implies that an executor exists or that an action is permitted.

Action order and each action's dependency sequence are declared semantic order
and are preserved. Candidate IDs, capability facts, graph nodes/edges, and
guarantee object sets are order-insensitive facts and canonicalized by their
stable identities before serialization. A manager graph identity is the pair
of its object ID and explicit scope, so the same object ID may safely occur in
both user and system scope. Duplicate, self, unknown, and cyclic action
dependencies reject.

## Evidence, preconditions, and outcomes

Evidence and preconditions are closed discriminated unions: filesystem identity,
package transaction, manager object, capability, project marker, installer
metadata, and supported profile manager. Unknown kinds reject.

Filesystem preconditions declare exactly the stat facts needed for that action:
device, inode, type, UID, GID, mode, link count, size, modification time,
change time, and mount ID. Atime is not a generic drift field. Size effects keep
apparent and allocated bytes apart; a nonexclusive or unknown hard link cannot
claim allocated bytes freed. Aggregate totals never claim allocated savings:
they have no per-entry proof ledger to establish exclusive ownership.

An action's evidence, precondition, and provider guarantee must bind its typed
target. Manager-object guarantees use scoped graph identities: bounded claims
cannot exceed matching transaction evidence, exact-target claims cover only the
target, and exact-graph claims match the complete graph node set. Filesystem
actions carry only a read-only-inventory guarantee.

Result action terminals are only `success`, `skipped`, `drifted`, `denied`,
`unsupported`, `failed`, `interrupted`, and `indeterminate`. An
`indeterminate` outcome must set `reconciliation_required` and carry a typed
reconciliation probe. It must not be silently converted to a retryable failure.
Recovery handles carry only a trusted-root ID, typed token, relative raw path,
and an `int64` Unix-nanosecond-representable UTC recording time; they never
carry an absolute path. Recovery disposition is
explicitly `not_applicable`, `retained`, or `restored`; retained/restored
outcomes cannot claim freed space as a rollback shortcut.

Ordinary drift is pre-dispatch and therefore has `attempted: false`. A
conclusively detected post-stage identity mismatch is the narrow exception: it
is `drifted` with `attempted: true` and either `restored` or `retained`
recovery. A retained mismatch carries its recovery handle; a restored mismatch
does not. Both have zero verified effect and require no reconciliation because
the safety state is known. An ambiguous post-stage state remains
`indeterminate` with reconciliation instead.

## Canonical CBOR and digest

`internal/planproto` is the sole CBOR boundary. It uses RFC 8949 deterministic
encoding with byte-string raw path components and no map insertion-order
dependence. Decode first applies explicit frame, nesting, map, array, scalar,
action, and path budgets; then it rejects duplicate keys, indefinite lengths,
tags (including bignums), floats, invalid UTF-8 text, non-minimal numeric or
length encodings, unknown fields, unknown enum values, unsupported versions,
and trailing data. Decoded values are validated, canonically re-encoded, and
must byte-match the received input.

The plan digest is:

```text
SHA-256("ldclean.plan.digest.v1\\0" || canonical-plan-body-cbor)
```

It is binding audit evidence for preview/apply/history consistency and drift
detection. It is not a MAC, signature, capability, authorization decision, or
substitute for helper-side validation.

## Exact JSON

The protocol's JSON DTOs are versioned and explicit; they are not presenter
output. An accepted document has the complete v1 object shape: every schema
field is present even when its value is `false`, zero, empty, or `null`.
Whitespace, object member order, and equivalent JSON string escaping are not
semantic, but duplicate, unknown, and omitted fields reject.
Malformed or unpaired UTF-16 surrogate escapes reject before a JSON decoder can
normalize them; valid surrogate pairs decode to their exact UTF-8 bytes.

A raw path contains display text plus authoritative component entries. Each
component contains exactly one of a valid UTF-8 string or canonical padded
base64; its inactive union field is omitted. The display field is never decoded
into a `BytePath`; only the exact component entries can construct one.

## Compatibility

Schema v1 values are immutable contracts. Adding a field, enum, or semantic
interpretation requires a new schema version, reviewed golden fixtures, a
reader/writer compatibility decision, and any needed state migration. Existing
v1 bytes are never silently reinterpreted.

Before any persisted or released v1 reader existed, the post-stage-drift rule
above was clarified using the existing result fields and enum values. It does
not change the DTO shape or golden bytes, but validators from earlier source
must be upgraded before they accept this known-safe state combination.
