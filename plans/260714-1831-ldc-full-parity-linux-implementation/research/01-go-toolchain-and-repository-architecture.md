---
type: research
date: 2026-07-14
subject: Go toolchain, repository boundaries, testing, and packaging
status: complete
---

# Go toolchain and repository architecture research

## Recommendation

Use one Go module, a thin build wrapper, and internal packages arranged around authority boundaries rather than commands. The permanent module path must be selected from the eventual repository remote before `go mod init`; this repository has no remote yet, so the implementation phase must not invent an owner or temporary module path.

Go 1.26.5 is the current stable patch on the research date. Record `go 1.26.0` as the module language/toolchain floor, pin 1.26.5 in CI and release builders with `GOTOOLCHAIN=local`, and re-check the current supported patch when Phase 1 starts. This makes local and release builds deterministic without turning a planning-time patch number into a permanent maintenance policy.

## Repository shape

The initial repository should use:

```text
cmd/
  ldclean/
  linux-deep-clean-helper/
internal/
  application/
  architecture/
  capability/
  domain/
  engine/
  linuxfs/
  managerexec/
  metrics/
  presenters/
  privilege/
  providers/
  state/
tests/
  contract/
  fixtures/
  integration/
  performance/
  vm/
packaging/
  arch/
  archive/
  common/
  debian/
  rpm/
```

Keep unit tests beside their packages. Use `tests/` only for cross-package contracts, black-box integration, performance campaigns, fixtures, and disposable-VM qualification. Do not create a public `pkg/` tree until a genuine external consumer exists.

Phase 1 should create `go.mod`, `go.sum`, `LICENSE`, a thin `Makefile`, both command entry points, architecture tests, CI definitions, and test-suite directories. The privileged helper binary may initially be a real unconditional reject-only bootstrap; no privileged executor belongs in repository bootstrap.

## Dependency boundaries

Dependencies have narrow roles:

| Dependency | Permitted boundary | Constraint |
|---|---|---|
| Cobra | CLI command wiring and completion | Never imported by engine, providers, or helper |
| Bubble Tea v2 | Interactive presenter | Never authoritative for plan or apply semantics |
| Lip Gloss v2 | TUI styling | Presenter-only |
| `golang.org/x/sys/unix` | Linux syscall and capability adapters | Linux build-tagged code only |
| `fxamacker/cbor/v2` | Canonical helper wire codec candidate | Locked to the approved strict CBOR profile |
| TOML decoder, to be selected | Configuration adapter | Select and audit in the phase that establishes configuration |
| gopsutil v4, deferred | Metrics adapter | Add only if direct procfs/sysfs adapters prove insufficient |

The helper command and packages may depend only on dependency-free domain types, strict privilege protocol and validation, apply-time capability re-probes, `linuxfs`, and `managerexec`. They must not depend on application orchestration, discovery providers, state/history, metrics, presenters, Cobra, Bubble Tea, or Lip Gloss. An architecture test should run `go list -deps` and fail on a forbidden import.

Presenters may depend only on application use-case and domain contracts. JSON, NDJSON, and no-TTY behavior must precede the TUI so terminal rendering never becomes the product contract.

## Test and fixture strategy

The default `go test ./...` suite must remain hermetic, offline, unprivileged, and safe on a developer workstation. Reserve two opt-in build tags:

- `integration` for host-isolated integration tests that do not mutate the host;
- `vmtest` for disposable supported-matrix VMs, additionally guarded by an explicit environment variable and a guest sentinel created by the VM harness.

Use raw byte fixtures captured from supported distributions, with provenance metadata that records distribution, release, command version, locale, and sanitization. Filesystem hazards such as non-UTF-8 names, symlinks, bind mounts, hard links, sockets, mount crossings, and race actors should be generated dynamically. Package mutation tests should install locally built packages named `linux-deep-clean-test-*`; they must never select arbitrary packages from a test host.

Target statement coverage of at least:

- 90% for domain, policy/planning engine, privilege protocol, and recovery state;
- 85% for provider parsers and structured presenters;
- 80% for other behavior-bearing packages.

Coverage is subordinate to the behavioral release gates: `go test -race`, `go vet`, `govulncheck`, parser/protocol fuzzing, golden structured-output contracts, and the fixed filesystem adversarial campaign.

## Build and release approach

Use direct `go build -trimpath` commands behind a thin Makefile. Start native packaging with `dpkg-buildpackage`, RPM tooling (`rpmbuild` and an isolated builder such as Mock), and `makepkg`; do not introduce GoReleaser or nFPM unless native build repetition later justifies it. The rootless `tar.zst` must exclude the privileged helper and polkit/sudo integration.

The distribution channel is not a code bootstrap decision. Phase 11 should produce reproducible LDC-owned release artifacts and repository metadata first; acceptance into official distribution repositories is a separate downstream governance effort with potentially different Go and vendoring policies.

## Phase ordering implications

The safest dependency order is repository/toolchain, canonical domain and schemas, Linux capability/filesystem proof, engine/state, read-only providers, structured presenters/TUI, user-owned mutation, reject-only helper/protocol, privileged manager mutation, remaining parity, then packaging and full-matrix qualification. Package-manager preview followed by an external manager apply is not atomic, so each backend must define typed simulation evidence, drift checks, bounded execution, and an indeterminate/reconciliation result before mutation is enabled.

## Primary sources

- [Go version endpoint](https://go.dev/VERSION?m=text)
- [Go release history](https://go.dev/doc/devel/release)
- [Go toolchain selection](https://go.dev/doc/toolchain)
- [`cmd/go` documentation](https://pkg.go.dev/cmd/go)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Go race detector](https://go.dev/doc/articles/race_detector)
- [Debian package build flow](https://www.debian.org/doc/manuals/maint-guide/build.en.html)
- [Arch `PKGBUILD(5)`](https://man.archlinux.org/man/PKGBUILD.5.en)

## Open implementation gates

- Establish the permanent repository remote and Go module path before creating `go.mod`.
- Re-check the Go patch and direct dependency versions when implementation begins.
- Select the TOML decoder only after a license, maintenance, fuzzing, and unknown-field behavior review.
- Keep official distribution submission outside the first release gate; LDC-owned native artifacts are sufficient for initial support claims.
