# Phase 2 Canonical Protocol Completion Report

Date: 2026-07-16

## Outcome

Phase 2 is complete. It freezes the dependency-light domain vocabulary and the
strict v1 plan/result interchange boundary that later discovery, policy,
filesystem, state, presenter, and helper phases must use without expanding
authority.

## Delivered milestones

- Added immutable raw-byte `BytePath` values with defensive copies, bounded
  exact JSON/base64 and Trash percent codecs, and one-way display text.
- Added closed typed identities, targets, evidence, preconditions, actions,
  size facts, plans, results, reconciliation probes, and the shared scoped
  transaction graph/provider-guarantee model.
- Added deterministic RFC 8949 CBOR and explicit strict JSON DTOs with
  domain-separated SHA-256 plan digests, canonical re-encoding, malformed
  surrogate rejection, and caller-provided decode budgets.
- Added reviewed v1 CBOR/JSON/reconciliation fixtures, hostile CBOR corpus,
  import constraints, dependency audit, contract documentation, package
  coverage floors, and scheduled fuzz/repeated-corpus CI gates.

## Validation evidence

Before publication, local validation passed:

- `make check` (default tests, `-race`, `go vet`, and 91.3% `pathbytes`,
  92.4% `domain`, and 90.4% `planproto` statement coverage)
- `go test ./internal/planproto -run
  'TestStrictDecoderRejectsNonCanonicalCorpus|TestPlanV1GoldenCompatibility'
  -count=100`
- Ten-second smoke runs for `FuzzBytePathCodecs`, `FuzzCanonicalCBOR`, and
  `FuzzPlanDigest`
- `go mod verify`, `make build`, Ruby parsing of the GitHub Actions workflow,
  and `git diff --check`
- An independent Phase 2 review, including a focused CBOR protocol audit

## Risks and handoff

Phase 3 receives raw relative path authority, target-bound filesystem evidence,
the immutable plan/result vocabulary, and strict codec/digest APIs. It must
implement Linux filesystem semantics through those contracts; it must not add
absolute-path authority, treat display data as a path, or relax canonical
decode validation.

## Unresolved questions

None for the Phase 2 exit gate.
