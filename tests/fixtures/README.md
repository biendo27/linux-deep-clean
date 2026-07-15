# Test fixtures

Fixtures are deterministic evidence for offline tests. A fixture is the exact
byte sequence consumed by a parser or protocol test; it is not a hand-written
approximation of a command's output.

## Required provenance

Every versioned fixture set must include a manifest beside its data. For each
raw stdout, stderr, file, or endpoint-body byte stream, the manifest records:

- fixture schema version and stable fixture identifier;
- provider or parser contract version;
- capture date and capture method;
- distribution ID, release, architecture, and image or guest identity;
- exact command and arguments, plus the command/tool version;
- `LANG`, `LC_ALL`, `LANGUAGE`, and any other locale-affecting environment;
- capture source, ownership/license status, and SHA-256 of the committed bytes;
- expected parser outcome; and
- every sanitization transformation, including its reason and replacement
  token.

Record command output as separate raw `.stdout` and `.stderr` files when both
exist. Preserve bytes exactly after the documented sanitization step: do not
normalize encoding, whitespace, line endings, quoting, terminal escapes, or
invalid UTF-8. Parsed expectations belong in separate files and never replace
the captured bytes.

## Sanitization

Never commit credentials, tokens, private paths, account names, machine IDs,
hostnames, addresses, package credentials, or other personal/sensitive data.
When a capture contains such data, use a deterministic byte-preserving
sanitizer before committing it. Document the sanitizer version, each
replacement rule, and the resulting SHA-256 in the manifest. Do not manually
edit, reformat, truncate, invent, or "clean up" output; if a safe sanitization
cannot preserve the parser-relevant structure, do not use that capture.

Raw source captures that cannot be published remain outside the repository in
the approved evidence store. The repository contains only the exact sanitized
byte stream named and hashed by its manifest. Tests must consume those recorded
bytes and must not recapture fixtures from a host, invoke a package manager,
request privilege, or contact a network service.

## Attribution and independent authorship

Prefer captures made by this project on an identified supported guest. If a
fixture is derived from a public specification, sample output, or other
third-party material, record its source URL, license, and attribution in the
manifest and include it only when redistribution is permitted. Do not copy
Mole source, prose, artwork, cleanup rules, test fixtures, or distinctive
layouts. A fixture without traceable provenance, a compatible license, and a
documented sanitization history must not be added.
