# Schema v1 strict-CBOR rejection corpus

Each entry in this corpus describes a malformed or non-deterministic CBOR
shape that the schema-v1 plan and result decoders must reject before typed
plan/action allocation. The executable cases live in
`internal/planproto/strict_decode_test.go` and `internal/planproto/fuzz_test.go`;
the short byte sequences below are retained as reviewable fixture provenance.

| Case | CBOR hex | Required rejection |
| --- | --- | --- |
| duplicate map key | `a2616100616100` | duplicate keys are forbidden |
| out-of-order map keys | `a2617a00616100` | deterministic bytewise key order is required |
| indefinite map | `bf6161f6ff` | indefinite lengths are forbidden |
| tag | `c100` | tags are forbidden |
| float | `f93c00` | floats/simple values outside booleans/null are forbidden |
| bignum tag | `c24101` | bignums/tags are forbidden |
| non-minimal integer | `1817` | integer and length arguments are minimally encoded |
| invalid text UTF-8 | `61ff` | text strings must be valid UTF-8 |
| trailing item | `f600` | exactly one complete frame is required |

These fixtures are original LDC protocol test values, not compatibility data
copied from another implementation. A future schema version may add fixtures,
but must never reinterpret or replace the v1 entries.
