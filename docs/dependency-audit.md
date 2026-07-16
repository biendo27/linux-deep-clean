# Bootstrap toolchain and dependency audit

Date: 2026-07-16

## Go toolchain

The [official Go version endpoint](https://go.dev/VERSION?m=text) reported
`go1.26.5`; the bootstrap host reports the same version. The module language floor is `go 1.26.0`, while
[`.go-version`](../.go-version) pins the accepted builder patch to `1.26.5`.
Automation uses `GOTOOLCHAIN=local` so a build never silently downloads a
different compiler.

## Bootstrap command provenance and test boundary

The Make `GO_ENV` and CI command environments clear ambient `GOROOT` before
launching the local toolchain, then set `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOWORK=off`, and an empty `GOFLAGS`. CI also verifies the selected Go version
against [`.go-version`](../.go-version) before its offline lanes run.

After that bootstrap trust is established, the default-lane contract permits a
child Go command only through `filepath.Join(runtime.GOROOT(), "bin", "go")`.
Its final command environment clears `GOROOT`, disables proxy, workspace, and
toolchain downloads, clears `GOFLAGS`, fixes `PATH` to `/usr/bin:/bin`, and
explicitly empties both VM-test opt-in variables.

A hostile bootstrap toolchain or environment is outside this in-process
test-lane boundary. It must be addressed at the pinned CI/Make entry point,
not by the default-lane source checks.

## Direct dependency

Phase 3 has three direct runtime dependencies:

| Module | Pin | Purpose | License / review |
| --- | --- | --- | --- |
| `github.com/fxamacker/cbor/v2` | [`v2.9.1`](https://github.com/fxamacker/cbor/releases/tag/v2.9.1) | RFC 8949 deterministic plan/result codec | MIT; release tag commit `63d1c6649d4235ae97b78c40888d9b2a0b426878`; confined by architecture tests to `internal/planproto` |
| `github.com/spf13/cobra` | [`v1.10.2`](https://github.com/spf13/cobra/releases/tag/v1.10.2) | Bootstrap CLI parsing and help rendering | Apache-2.0; release tag commit `88b30ab89da2d0d0abb153818746c5a2d30eccec` |
| `golang.org/x/sys` | [`v0.47.0`](https://github.com/golang/sys/releases/tag/v0.47.0) | Linux descriptor, `openat2`, `statx`, and `renameat2` bindings | BSD-3-Clause; release tag commit `9e7e939dcafac07e8ab4cffa6e5fc74908413f00`; requires Go 1.25 and is confined by architecture tests to `internal/mounts` and `internal/linuxfs` |

Its Go module declares `go 1.15` and the audited transitive graph is pinned in
`go.sum`. Cobra remains presenter-only; architecture tests reject it anywhere
outside the CLI path and reject all non-standard-library helper dependencies.

CBOR is confined to the protocol boundary. `internal/pathbytes` and
`internal/domain` remain standard-library-only (apart from domain's local
raw-path import), while `internal/planproto` is the sole project package
allowed to import `fxamacker/cbor/v2`. The codec uses an explicit deterministic
encode profile and a bounded reject-first decode profile; a plan digest binds
canonical bytes for audit and drift detection, never for authorization.

`x/sys/unix` is not a general host-mutation capability: production imports are
admitted only to the Phase 3 mount-qualification and rooted-filesystem safety
packages. Architecture and default-lane contract tests reject it everywhere
else, reject raw syscall/mount escapes, and continue to reject string-path
mutation APIs globally.

## Vulnerability check

The bootstrap graph was scanned with:

```bash
GOROOT= GOTOOLCHAIN=local GOWORK=off GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

The scan reported no vulnerabilities. This audit command is an explicit
networked review operation; it is not part of the hermetic default test lane.

## Re-audit triggers

Repeat this review before changing the pinned Go patch, adding a direct
dependency, changing Cobra's or CBOR's use boundary, or cutting a release.
