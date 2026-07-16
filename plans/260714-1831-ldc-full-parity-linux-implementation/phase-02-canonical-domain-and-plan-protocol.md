---
phase: 2
title: "Canonical Domain and Plan Protocol"
status: completed
priority: P1
effort: "8-12 engineer-days"
dependencies: [1]
---

# Phase 2: Canonical Domain and Plan Protocol

## Overview

Freeze the dependency-light domain vocabulary and immutable plan/result schemas used by every presenter, provider, executor, state store, and the later helper. Linux paths remain raw bytes under compiled trusted-root IDs; deterministic canonical CBOR and a domain-separated digest bind preview, apply, result, and history without being treated as authorization.

This phase performs no discovery or mutation. Context: [approved domain model](../reports/260714-1754-ldc-linux-native-design-report.md#core-domain-model) and [safety corrections](./research/02-filesystem-and-privilege-safety.md#canonical-cbor-profile).

## Requirements

### Functional

- Model `Candidate`, `Evidence`, `Precondition`, `Action`, `Plan`, `PlanDigest`, `Result`, `Outcome`, and `ReconciliationState` as closed typed values—no `map[string]any`, caller argv, executable path, shell text, or authority-bearing absolute path.
- Represent filesystem targets as `TrustedRootID` plus `BytePath` ordered raw-byte components. Reject NUL, slash within a component, empty, `.`, and `..`; enforce filesystem component limits at point of use, not through a universal stored `PATH_MAX`.
- CBOR encodes components as byte strings. JSON carries a separately escaped display plus exact component data; invalid UTF-8 uses base64. Decoders never turn display text back into path authority.
- Define explicit evidence/precondition variants for filesystem identity, package transaction state, manager objects, capabilities, project markers, installer metadata, and supported profile managers.
- Define one dependency-free `domain.TransactionGraph` for both provider preview and helper re-simulation. It carries exact typed objects, versions, scopes, dependency edges/reasons, protected/essential facts, maintainer-script/restart/network disclosures, provider evidence digest, and a provider guarantee; no executor package owns or extends the graph.
- Freeze schema v1 plan fields: schema version, command, caller echo, capability snapshot, config digest, ordered actions/dependencies, size totals, creation context, and digest.
- Compute SHA-256 over a domain-separated canonical plan body with the digest field excluded. Validate canonical bytes before digest comparison; document that the digest is binding/audit evidence, not a MAC or helper authorization.
- Result terminals include `success`, `skipped`, `drifted`, `denied`, `unsupported`, `failed`, `interrupted`, and `indeterminate`. Unknown completion sets `reconciliation_required` with a typed probe and never becomes automatically retryable failure.

### Non-Functional

- Keep `internal/pathbytes` and `internal/domain` standard-library-only. Put the audited CBOR dependency exclusively in `internal/planproto`.
- Audit/pin `fxamacker/cbor/v2`; configure an RFC 8949 deterministic profile that rejects duplicate keys, indefinite lengths, tags, floats, bignums, invalid UTF-8 text, non-minimal integers, trailing bytes, unsupported schema versions, and unknown fields.
- Make decode budgets explicit inputs. Phase 8 will apply the privileged profile (4 MiB frames, depth 16, 1,024 actions, 128 map pairs, 2,048 array items, 1 MiB scalar, 1,024 path components, 256 KiB encoded path); no decoder may allocate an advertised container before checking its budget.
- Schema changes require new golden fixtures and compatibility decisions. Never silently reinterpret schema v1 fields or action enum values.
- Reach >=90% statement coverage for domain/path/protocol behavior, plus property/fuzz gates; coverage cannot excuse accepting non-canonical or authority-expanding inputs.

## Architecture

### Data and codec flow

```text
raw Linux components -> pathbytes.New -> domain.FilesystemTarget
typed evidence/preconditions -> domain.Action -> domain.PlanBody
PlanBody -> planproto.CanonicalEncode -> SHA-256(domain || bytes) -> domain.Plan
domain.Plan/Result -> explicit JSON DTOs for contracts (not presenter styling)
```

`Plan` construction goes through validated constructors; public fields are immutable-by-convention values with defensive copies for slices/bytes. No consumer receives mutable backing storage. Canonical ordering rules cover action IDs, dependencies, capability facts, evidence, and totals; a map's insertion order cannot alter encoded bytes.

### Planned interface/function/type checklist

- [x] `pathbytes.BytePath`, `New(components [][]byte)`, `Components() [][]byte`, `Equal`, `Display`, `EncodeJSONExact`, `DecodeJSONExact`, `PercentEncodeTrashPath`, `PercentDecodeTrashPath`.
- [x] `domain.ProviderID`, `TrustedRootID`, `CandidateID`, `ActionID`, `RunID`, `PlanDigest`, and manager object IDs use validated constructors and stable encoded forms.
- [x] `domain.Candidate` owns provider, typed target, evidence, size facts, confidence, and discovery precondition; it grants no authority.
- [x] `domain.TransactionGraph`, `TransactionNode`, `TransactionEdge`, and `ProviderGuarantee` use closed variants and stable identifiers; `Validate` rejects unknown fields/kinds, duplicate identities/edges, dangling references, or a guarantee broader than the evidence.
- [x] `domain.Evidence` and `domain.Precondition` are discriminated unions with a closed `Kind`; filesystem preconditions carry required-field mask plus device/inode/type/UID/GID/mode/link/size/time/mount evidence as applicable.
- [x] `domain.ActionKind` initially names `trash_path`, `delete_recreatable_path`, `quarantine_path`, `restore_trash_path`, `restore_quarantine_path`, `repair_state`, `remove_native_package`, `remove_flatpak_ref`, `remove_snap`, `clean_package_cache`, `vacuum_journal`, `run_owned_cache_rebuild`, `install_completion`, `configure_fingerprint_auth`, `update_ldclean_package`, and `remove_ldclean_package`; definition does not enable execution.
- [x] `domain.Action` includes typed target, evidence, precondition, dependency IDs, risk/reversibility classes, estimated effect, required capability, provider guarantee, and expected postcondition.
- [x] `domain.PlanBody`, `NewPlan`, `Plan.Validate`, `Plan.CanonicalBody`, `PlanDigest.Verify`; constructor sorts only fields declared order-insensitive and rejects duplicate/unknown dependency references.
- [x] `domain.Result`, `ActionResult`, `RecoveryHandle`, `Outcome`, `ReconciliationState`, `ReconciliationProbe`, `ExitSummary`; recovery handles bind root/token/original `BytePath`/date without absolute-path authority, and validation rejects `indeterminate` without reconciliation or false rollback/freed-space claims.
- [x] `domain.SizeFacts` separates apparent, allocated, estimated, verified, and unavailable values; hard-link semantics cannot claim allocated bytes freed from one removed link.
- [x] `planproto.EncodePlan`, `DecodePlan`, `EncodeResult`, `DecodeResult`, `ComputeDigest`, `VerifyDigest`, and `DecodeLimits` use strict canonical re-encode-and-byte-compare.

### Dependency/import constraints

- `internal/pathbytes`: standard library only; imports no project package.
- `internal/domain`: may import `internal/pathbytes`; no CBOR/JSON presenter, application, engine, provider, filesystem, state, or privilege packages.
- `internal/planproto`: may import domain/pathbytes and `fxamacker/cbor/v2`; no application/engine/provider/state/presenter/helper imports.
- Presenters/providers/helper code do not exist in this phase except Phase 1 bootstrap. The architecture allowlist adds `planproto -> domain/pathbytes`, never the reverse.
- Exact JSON DTOs live in `planproto`; human display remains presenter-owned in Phase 6.

## Related Code Files

| Action | Exact repo-relative path | Purpose |
|---|---|---|
| Create | `docs/contracts/plan-schema-v1.md` | Public field, canonicalization, compatibility, and digest contract |
| Modify | `go.mod` | Add audited/pinned CBOR direct dependency |
| Modify | `go.sum` | Tool-generated dependency checksums |
| Modify | `internal/architecture/import_rules_test.go` | Add domain/pathbytes/planproto allowlist edges |
| Create | `internal/pathbytes/byte_path.go` | Raw-component immutable value and validation |
| Create | `internal/pathbytes/json_codec.go` | Display plus exact UTF-8/base64 JSON representation |
| Create | `internal/pathbytes/trash_percent_codec.go` | Raw-byte Trash `Path=` percent codec |
| Create | `internal/pathbytes/byte_path_test.go` | Component and defensive-copy tests |
| Create | `internal/pathbytes/json_codec_test.go` | Exact/display separation tests |
| Create | `internal/pathbytes/trash_percent_codec_test.go` | Trash encoding round trips |
| Create | `internal/pathbytes/fuzz_test.go` | Raw-byte/JSON/percent fuzz targets |
| Create | `internal/domain/identifiers.go` | Validated stable IDs/digests |
| Create | `internal/domain/candidate.go` | Candidate and typed target model |
| Create | `internal/domain/transaction_graph.go` | Shared dependency-free preview/re-simulation graph and guarantee model |
| Create | `internal/domain/evidence.go` | Closed evidence variants |
| Create | `internal/domain/precondition.go` | Closed action-specific preconditions/masks |
| Create | `internal/domain/action.go` | Closed action/risk/reversibility vocabulary |
| Create | `internal/domain/capability.go` | Capability snapshot and provider guarantee values |
| Create | `internal/domain/plan.go` | Immutable plan body and validation |
| Create | `internal/domain/result.go` | Terminal outcomes/reconciliation/exit summary |
| Create | `internal/domain/size.go` | Apparent/allocated/estimated/verified arithmetic |
| Create | `internal/domain/plan_test.go` | Determinism, dependency, and immutability tests |
| Create | `internal/domain/transaction_graph_test.go` | Exact identity, graph validation, ordering, and guarantee tests |
| Create | `internal/domain/result_test.go` | State-machine and exit-summary tests |
| Create | `internal/domain/size_test.go` | Overflow/unavailable/hard-link tests |
| Create | `internal/planproto/limits.go` | Checked decode budgets/profiles |
| Create | `internal/planproto/cbor.go` | Strict canonical plan/result CBOR codec |
| Create | `internal/planproto/digest.go` | Domain-separated SHA-256 binding |
| Create | `internal/planproto/json.go` | Versioned explicit contract JSON DTOs |
| Create | `internal/planproto/cbor_test.go` | Canonical/non-canonical corpus tests |
| Create | `internal/planproto/digest_test.go` | Stability and semantic-change properties |
| Create | `internal/planproto/compatibility_test.go` | Golden schema v1 decode/re-encode tests |
| Create | `internal/planproto/fuzz_test.go` | Bounded strict decoder fuzzing |
| Create | `tests/contract/plan_schema_contract_test.go` | Cross-package JSON/CBOR contract gate |
| Create | `tests/fixtures/contracts/plan-v1.cbor` | Reviewed canonical binary golden |
| Create | `tests/fixtures/contracts/plan-v1.json` | Reviewed schema v1 JSON golden |
| Create | `tests/fixtures/contracts/result-indeterminate-v1.cbor` | Required reconciliation golden |
| Create | `tests/fixtures/contracts/noncanonical-cbor-corpus/README.md` | Corpus provenance/expected rejection catalog |
| Delete | None | No obsolete schema exists |

## TDD-First Test Strategy

### Test matrix

| Priority | Exact tests | Cases/fixtures | Gate |
|---|---|---|---|
| Critical | `TestBytePathRejectsUnsafeComponents`, `TestBytePathAllRawBytesRoundTrip` | Every non-NUL byte; slash/dot/dot-dot/empty; invalid UTF-8 | Exact bytes survive; invalid components rejected |
| Critical | `TestDisplayNeverDecodesAsAuthority` | Escapes, replacement rune, bidi/control, invalid UTF-8 | Only exact component field constructs `BytePath` |
| Critical | `TestCanonicalPlanStableAcrossInsertionOrder` | Permuted map/input/action construction | Identical semantic plan => identical bytes/digest |
| Critical | `TestTransactionGraphRejectsAuthorityOrGuaranteeExpansion` | Extra node/edge, version/scope/script/network change, dangling/duplicate IDs, broadened guarantee | Exact typed difference remains visible and invalid broadening rejects |
| Critical | `TestStrictDecoderRejectsNonCanonicalCorpus` | Duplicate keys, indefinite, tags, floats, bignums, non-minimal ints, trailing, unknown fields | Every corpus entry rejected before action allocation |
| Critical | `TestDigestExcludesOnlyDigestField`, `TestSemanticChangeChangesDigest` | Golden v1 plan, one-field mutations | Stable domain-separated SHA-256 binding |
| Critical | `TestUnknownCompletionRequiresReconciliation` | Interrupted, EOF/lost response, malformed response | `indeterminate` + typed probe; never retryable failure |
| High | `TestFilesystemPreconditionRequiresNamedMask` | Missing `statx` bits, atime-only drift, action-specific fields | Missing required bit fails; no compare-everything helper |
| High | `TestSizeFactsNeverOverclaimHardLinkSavings` | link counts 1/2+, sparse/reflink unknown | Verified allocated effect remains unavailable unless measured |
| High | `TestPlanV1GoldenCompatibility` | Three exact contract fixtures | Decode, validate, canonical re-encode byte-equal |
| Medium | Fuzz targets `FuzzBytePathCodecs`, `FuzzCanonicalCBOR`, `FuzzPlanDigest` | Seed corpus plus boundary sizes | No panic, unbounded allocation, non-canonical acceptance, or authority expansion |

### RED -> GREEN -> REFACTOR

1. **RED — raw paths:** write BytePath/component/display/exact JSON/Trash percent tests; confirm unsafe components and display-authority cases fail.
2. **GREEN — minimal BytePath:** implement immutable raw components, validation, defensive copies, exact JSON, and percent codec.
3. **REFACTOR — one validation path:** remove duplicate validators; keep display generation separate from exact decoding.
4. **RED — domain invariants:** add plan/action/result/size/transaction-graph tests for closed variants, immutability, dependency validity, guarantee non-expansion, indeterminate reconciliation, and no false savings.
5. **GREEN — typed domain:** implement only constructors/value objects needed by tests, including the single shared manager graph; action enum values remain disabled capabilities.
6. **REFACTOR — canonical ordering:** centralize stable ID ordering and checked arithmetic without generic reflection-based domain maps.
7. **RED — protocol:** add goldens and the full rejection corpus before configuring CBOR; prove permissive library defaults fail the contract.
8. **GREEN — strict profile:** configure deterministic encode, pre-allocation limits, decode/validate/re-encode equality, and digest.
9. **REFACTOR — compatibility surface:** isolate v1 DTOs, document every field/enum, and rerun fuzz/property/import gates.

### Commands and behavioral gates

```bash
GOTOOLCHAIN=local go test ./internal/pathbytes ./internal/domain ./internal/planproto ./tests/contract -count=1
GOTOOLCHAIN=local go test -race ./internal/pathbytes ./internal/domain ./internal/planproto ./tests/contract
GOTOOLCHAIN=local go test ./internal/pathbytes ./internal/domain ./internal/planproto -coverprofile=coverage-domain.out
GOTOOLCHAIN=local go test ./internal/planproto -run 'TestStrictDecoderRejectsNonCanonicalCorpus|TestPlanV1GoldenCompatibility' -count=100
GOTOOLCHAIN=local go test ./internal/architecture -run TestArchitectureImportAllowlists -count=1
GOTOOLCHAIN=local go test ./internal/pathbytes -fuzz=FuzzBytePathCodecs -fuzztime=60s
GOTOOLCHAIN=local go test ./internal/planproto -fuzz=FuzzCanonicalCBOR -fuzztime=60s
```

Coverage gate: each of `pathbytes`, `domain`, and `planproto` >=90% statement coverage. Nightly fuzzing runs each target >=10 minutes with the checked-in seeds. A passing percentage is insufficient if any rejection corpus value is accepted, any decoded allocation escapes its budget, or canonical fixture bytes change without a schema-version decision.

## Implementation Steps

1. Audit the CBOR library/license/version/default modes; record the selected pin in the implementation PR and update module checksums.
2. Add RED raw-path tests and property generators. Generate components dynamically; do not use host paths as fixture authority.
3. Implement `pathbytes` and exact codecs. Ensure callers cannot retain/mutate constructor or accessor backing slices.
4. Add RED typed-domain tests, then implement validated IDs, targets, evidence, action-specific preconditions, actions, plans, size facts, and results.
5. Add the shared transaction graph and provider-guarantee model; prove provider preview and future helper re-simulation can bind the same canonical type without an executor import.
6. Assign stable numeric/text encodings for every v1 enum. Reject unknown values; never map them to an `other` executor path.
7. Add RED canonical protocol/golden tests and malicious corpus. Keep fixture provenance in the corpus README.
8. Implement strict encoding/decoding and budget accounting. Decode, validate, canonical re-encode, and require byte equality before computing/verifying a digest.
9. Generate/review v1 golden files from original LDC contract values. Check them in only after field-by-field human review.
10. Update import architecture gates and public schema documentation. Run focused, race, coverage, repeated-corpus, and fuzz commands.

## Success Criteria

- [x] Every filesystem target is a validated `TrustedRootID + BytePath`; no display/absolute path is authority.
- [x] Raw-byte CBOR and exact JSON/Trash codecs round-trip invalid UTF-8 without normalization or ambiguity.
- [x] Domain variants and action enums are closed, typed, versioned, and grant no execution capability.
- [x] Provider preview and helper re-simulation use the same dependency-free `domain.TransactionGraph`; executor packages cannot redefine or broaden it.
- [x] Canonical plan/result bytes are deterministic; one semantic change changes the digest; digest is documented/tested as non-authorizing.
- [x] Strict decoder rejects every forbidden form, over-budget structure, unknown field/version, and trailing byte before authority use.
- [x] Interrupted/lost-response results become `indeterminate` with `reconciliation_required`; exit summaries preserve partial/unknown state.
- [x] Golden compatibility, architecture, race, >=90% coverage, property, and fuzz gates pass.

## Risk Assessment and Rollback

| Risk | Mitigation |
|---|---|
| Go strings/UI normalize byte paths | Raw components are authoritative; display is one-way; invalid UTF-8 fixtures/fuzzing |
| CBOR library accepts broader RFC forms | Locked strict profile plus canonical byte-equality and malicious corpus |
| Digest is mistaken for authorization | Domain separation and binding-only documentation; later helper independently validates fields |
| Generic unions permit authority expansion | Closed discriminants and exhaustive validators; unknown values reject |
| Schema churn strands state/history | Freeze v1 goldens; changes require explicit version/migration decision |

Rollback before publication may remove the new codec/dependency and restore Phase 1. After any plan schema is persisted or released, do not rewrite v1; add a new reader/version and migration plan. Preserve golden fixtures for backward compatibility.

## Phase Exit and Hand-Off

Exit only after schema documentation and goldens match exact encoded bytes and all gates pass. Hand Phase 3 the `BytePath`, trusted-root target, filesystem precondition masks, action/postcondition/result types, strict codec, and digest API. Phase 3 may implement Linux semantics but must not add path authority or weaken canonical validation.
