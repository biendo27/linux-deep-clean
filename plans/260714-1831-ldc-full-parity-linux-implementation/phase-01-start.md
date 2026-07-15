---
phase: 1
title: "Repository, Toolchain, and Contract Harness"
status: completed
priority: P1
effort: "4-6 engineer-days"
dependencies: []
---

# Phase 1: Repository, Toolchain, and Contract Harness

## Overview

Create the smallest real Go repository that fixes product identity, licensing, build reproducibility, layer boundaries, and safe test lanes. This phase delivers only offline help/version/bootstrap behavior and an uninstalled helper binary that rejects every request; it does not discover or mutate anything.

Entry gate satisfied: the permanent Git remote was configured and the permanent module path was chosen from that remote **before** `go mod init`. No owner or temporary module path was invented.

Context: [design report](../reports/260714-1754-ldc-linux-native-design-report.md), [toolchain research](./research/01-go-toolchain-and-repository-architecture.md), [scout report](./reports/scout-report.md).

## Requirements

### Functional

- Build `/usr/bin`-targeted command `ldclean`; never produce, install, document, or alias an `ldc` executable.
- Make `ldclean --help` and `ldclean --version` fast, offline, deterministic, and side-effect free. With no command in the bootstrap phase, non-TTY behavior is help only.
- Refuse execution when the main process effective UID is 0 before command dispatch or state access.
- Build `linux-deep-clean-helper` as a reject-only development artifact. It accepts/executes no request, is not installed, and has no privilege integration until Phase 8.
- Establish unit, contract, integration, performance, fuzz, and VM suite locations without placeholder success behavior.
- License original code Apache-2.0 and document clean-room-style provenance constraints without copying Mole code, prose, fixtures, artwork, rule tables, or distinctive layouts.

### Non-Functional

- Re-check the current supported Go 1.26 patch on phase entry. Record `go 1.26.0` as the language floor, pin the accepted patch (research baseline: 1.26.5) in `.go-version` and CI/release builders, and set `GOTOOLCHAIN=local` in automation.
- Audit and pin Cobra before adding it. Do not add Bubble Tea, Lip Gloss, `x/sys`, CBOR, TOML, gopsutil, release, or packaging dependencies before their owning phase.
- Use direct `go build -trimpath` commands behind a thin Makefile. No GoReleaser/nFPM abstraction.
- Default `go test ./...` remains hermetic, offline, unprivileged, and host-safe. `integration` and `vmtest` are opt-in build tags; `vmtest` additionally requires `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, and a root-owned disposable-guest sentinel created by the VM harness whose content exactly matches that token.
- CI must run with the module cache populated, `GOPROXY=off`, and a network-disabled test namespace. A skipped network-isolation step is a failure.

## Architecture

### Initial dependency map

```text
cmd/ldclean -> internal/presenters/cli -> internal/application -> internal/domain
cmd/linux-deep-clean-helper -> standard library reject path only
internal/architecture tests -> `go list -deps` over both commands
tests/contract -> built binaries as black boxes
```

The main command owns only process startup. Cobra stays in `internal/presenters/cli`. Bootstrap application code exposes build information and the non-root guard; it does not know Cobra. The helper is deliberately isolated so later protocol work starts from a deny-all boundary.

### Planned interface/function/type checklist

- [x] `application.BuildInfo` contains semantic version, commit, build time, Go version, and dirty flag; `Validate()` rejects missing release fields while allowing an explicit development form.
- [x] `application.RequireUnprivileged(euid int) error` is pure/testable; production startup passes only `os.Geteuid()` and offers no flag/env override.
- [x] `application.Bootstrap` exposes only `BuildInfo()` and `RequireUnprivileged()` to the presenter.
- [x] `cli.NewRootCommand(application.Bootstrap) *cobra.Command` wires help/version without discovery, state, network, or mutation.
- [x] `cli.Execute(context.Context, application.Bootstrap, io.Writer, io.Writer) int` maps bootstrap errors to stable process summaries without calling `os.Exit` below `main`.
- [x] Helper `main` closes over no application object and returns one bounded rejection on stderr plus a non-zero exit without parsing caller data.

### Import constraints

- `internal/domain`: standard library only; imports no internal package.
- `internal/application`: may import `internal/domain`; no Cobra, presenter, provider, filesystem, helper, or state imports.
- `internal/presenters/...`: may import only `internal/application` and `internal/domain` from project packages.
- `cmd/ldclean`: may import application and CLI presenter only.
- `cmd/linux-deep-clean-helper`: Phase 1 allowlist is standard library only.
- The architecture test fails on dependency cycles, a `pkg/` tree, shell-script runtime logic, or any forbidden import. Later phases modify the explicit allowlist; they never disable the test.

## Related Code Files

| Action | Exact repo-relative path | Purpose |
|---|---|---|
| Create | `.github/workflows/ci.yml` | Offline default, race, vet, coverage, and tagged-lane compile gates |
| Create | `.gitignore` | Go/build/test artifacts only; never ignore plans or provenance |
| Create | `.go-version` | Accepted Go patch pin |
| Create | `LICENSE` | Apache License 2.0 text |
| Create | `Makefile` | Thin build/test/check entry points |
| Create | `README.md` | Distinct identity, non-claims, and bootstrap developer entry |
| Create | `docs/provenance-policy.md` | Independent-authorship and third-party origin rules |
| Create | `go.mod` | Permanent module path and Go language floor; only after entry gate |
| Create | `go.sum` | Tool-generated checksums for audited direct/transitive dependencies |
| Create | `cmd/ldclean/main.go` | Unprivileged main entry point |
| Create | `cmd/linux-deep-clean-helper/main.go` | Reject-only, uninstalled helper entry point |
| Create | `internal/application/bootstrap.go` | Root guard and bootstrap contract |
| Create | `internal/application/build_info.go` | Validated build metadata |
| Create | `internal/domain/doc.go` | Dependency-free domain package boundary |
| Create | `internal/presenters/cli/root.go` | Cobra bootstrap help/version wiring |
| Create | `internal/architecture/import_rules_test.go` | Main/helper/presenter import allowlists |
| Create | `internal/application/bootstrap_test.go` | Root-guard tests |
| Create | `internal/application/build_info_test.go` | Build metadata tests |
| Create | `internal/presenters/cli/root_test.go` | Help/version/no-command tests |
| Create | `tests/contract/binary_contract_test.go` | Black-box naming, output, and helper rejection contracts |
| Create | `tests/contract/default_suite_contract_test.go` | Default-lane safety/tag checks |
| Create | `tests/fixtures/README.md` | Raw fixture provenance/locale/sanitization contract |
| Create | `tests/integration/suite_test.go` | `integration`-tag opt-in guard; no mutation behavior |
| Create | `tests/performance/bootstrap_benchmark_test.go` | Help/version startup benchmark harness |
| Create | `tests/vm/guard_test.go` | `vmtest` environment+sentinel fail-closed guard |
| Delete | None | Greenfield phase; no legacy artifacts |

## TDD-First Test Strategy

### Test matrix

| Priority | Exact test | Fixture/setup | Expected gate |
|---|---|---|---|
| Critical | `TestRequireUnprivilegedRejectsRoot` / `TestRequireUnprivilegedAcceptsNonRoot` | Table of effective UIDs | No bypass field; root rejected before dispatch |
| Critical | `TestHelperRejectsEveryInput` | Empty, valid-looking, 4 MiB, and truncated stdin byte cases | No parsing/execution; bounded rejection and non-zero exit |
| Critical | `TestArchitectureImportAllowlists` | `go list -deps -json ./cmd/...` | Exact main/helper/presenter allowlists; no forbidden dependency |
| Critical | `TestVMTestRequiresBothGuards` | Missing env, missing sentinel, wrong owner/mode/token | VM lane refuses before test-body execution |
| High | `TestBuiltExecutableIdentity` | `go build -trimpath` to temp dir | Output is `ldclean`; no `ldc` artifact or alias |
| High | `TestDefaultSuiteContract` | `go list` with no tags and network-disabled namespace | No integration/vmtest packages or authorization/network path runs |
| High | `TestRootCommandHelpGolden` / `TestRootCommandVersionGolden` | In-test expected contract; no copied upstream fixture | stdout only, deterministic, no mutation/network/state |
| Medium | `BenchmarkHelp` / `BenchmarkVersion` | Locally built binary, warm cache | Capture baseline; Phase 11 enforces p95 <=150 ms |

### RED -> GREEN -> REFACTOR

1. **RED — identity and denial contracts:** write `bootstrap_test.go`, `root_test.go`, architecture tests, and black-box contract tests first. Confirm they fail because the module/binaries do not exist. Commit no golden output copied from Mole.
2. **GREEN — minimal behavior:** after the permanent remote/module gate, initialize the module; implement only the root guard, validated build info, help/version root command, and unconditional helper rejection needed to pass.
3. **REFACTOR — boundaries and deterministic build:** remove duplicate startup logic, centralize build flags in the Makefile, tighten import allowlists, and prove the behavior is unchanged.
4. **RED — lane guards:** add failing tagged-suite checks for default exclusion and both VM guards.
5. **GREEN — safe harness:** implement tag files/CI jobs and network-disabled execution; do not create fake VM success tests.
6. **REFACTOR — quality surface:** consolidate commands into `make build`, `make test`, `make test-race`, `make vet`, `make coverage`, `make integration`, and `make vmtest`; each prints the exact underlying Go command.

### Commands and behavioral gates

```bash
GOTOOLCHAIN=local go test ./internal/application ./internal/presenters/cli ./tests/contract
GOTOOLCHAIN=local GOPROXY=off go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go test ./internal/architecture -run TestArchitectureImportAllowlists -count=1
GOTOOLCHAIN=local go test -tags=integration ./tests/integration -count=1
GOTOOLCHAIN=local go test -tags=vmtest ./tests/vm -run TestVMTestRequiresBothGuards -count=1
```

Run the default suite inside CI's network-disabled namespace after dependencies are cached. `make vmtest` must fail unless both guards pass; no destructive VM case exists yet. Phase-specific coverage gate: 100% branch coverage for root guard, build-info validation, and helper denial; no aggregate percentage is accepted as a substitute for the black-box/import/lane gates.

## Implementation Steps

1. Record the permanent remote URL/module path decision in the implementation PR. Configure the remote; verify module path ownership. Stop if unresolved.
2. Re-check Go stable/support status and Cobra license, maintenance, vulnerabilities, transitive graph, and version. Record pins; set `.go-version`, `go 1.26.0`, and `GOTOOLCHAIN=local` automation.
3. Add RED tests and exact expected exit/output contracts. Ensure tests fail for missing behavior, not harness errors.
4. Initialize the module and implement the minimum command/bootstrap/helper behavior. Generate `go.sum`; never hand-edit checksums.
5. Add the import allowlist test using `go list -deps`; list every permitted project/dependency edge explicitly.
6. Add thin Make targets and CI jobs. Cache/download dependencies before entering the network-disabled default test namespace.
7. Add tagged suite guards. Define the later VM harness contract as `LDCLEAN_VMTEST=1` plus `LDCLEAN_VMTEST_TOKEN` and a root-owned, non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` sentinel whose content exactly matches that token; absence/mismatch aborts.
8. Add original README/provenance policy. State `ldclean` identity, Apache-2.0 scope, greenfield status, unsupported mutation claims, and no upstream-source copying.
9. Run focused tests, then default/race/vet/import gates. Inspect built artifacts and `go version -m` output for module/version reproducibility.

## Success Criteria

- [x] Permanent Git remote and module path are explicitly selected before `go mod init`; no invented owner/path exists.
- [x] Only `ldclean` is built as the user executable; repository search and black-box tests find no executable/alias contract named `ldc`.
- [x] `ldclean` rejects EUID 0 before dispatch and provides deterministic offline help/version as an unprivileged process.
- [x] Helper builds but rejects all inputs, imports only the standard library, and is neither installed nor authorized.
- [x] Default tests are demonstrably offline, unprivileged, and exclude integration/VM lanes; VM lane requires both independent guards.
- [x] Import allowlists, race, vet, focused coverage, and reproducible `-trimpath` builds pass.
- [x] Apache-2.0 and provenance policy are present; no copied upstream artifact or unsupported product claim is introduced.

## Risk Assessment and Rollback

| Risk | Mitigation |
|---|---|
| Temporary module path becomes a public contract | Hard stop before module initialization; select from permanent remote only |
| Toolchain auto-download hides builder drift | `.go-version` pin plus `GOTOOLCHAIN=local`; fail on mismatch |
| Bootstrap helper becomes accidental authority | Deny all input, standard-library-only import gate, no install/polkit metadata |
| Default tests touch host/network | Tagged suites, network namespace CI, no authorization code, fail-closed guard tests |
| Architecture test becomes a broad exception list | Exact per-binary/project-package allowlists reviewed with every new edge |

Rollback is deletion/reversion of Phase 1-created bootstrap files before any release. There is no user data or system mutation to migrate. Do not roll back a verified permanent module path after publication; treat a later path change as a separate compatibility decision.

## Phase Exit and Hand-Off

Exit only when all success criteria and commands pass from a clean clone using the pinned toolchain. Hand Phase 2 the permanent module path, dependency audit, build metadata contract, exact import allowlists, and hermetic suite/tag conventions. Phase 2 may add domain/CBOR code but must not weaken main/helper boundaries or initialize mutation.
