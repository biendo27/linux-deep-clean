# Provenance policy

LDC is an independently authored Linux product. Apache-2.0 applies to original
project contributions; it does not grant permission to copy material from
other projects or sources.

## Prohibited sources

Do not copy or closely adapt Mole source code, prose, test fixtures, artwork,
cleanup-rule tables, distinctive layouts, or unpublished artifacts. Do not
use a decompiler, source-to-source translation, or an upstream fixture as a
shortcut for LDC behavior.

## Allowed inputs

Public specifications, documented command interfaces, operating-system
standards, and independently captured behavior may inform an implementation.
Record the source, license, date, exact version, and the independent design
decision it informed. Public facts describe a contract; they are not a license
to reproduce protected expression.

Every third-party dependency, fixture, icon, documentation excerpt, or copied
test input must have a compatible license and recorded attribution. Store raw
fixture provenance and sanitization metadata as described in
[`tests/fixtures/README.md`](../tests/fixtures/README.md).

## Review checklist

Before merge, contributors must confirm that new material is original or has a
traceable compatible source, that no sensitive capture data remains, and that
the change preserves LDC's separate `linux-deep-clean`/`ldclean` identity.
When a source is uncertain, exclude it until a maintainer records a clear
license and provenance decision.
