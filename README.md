# LDC — Linux Deep Clean

LDC is an independently authored, Apache-2.0-licensed Linux-native terminal
maintenance tool. Its package and repository slug is `linux-deep-clean`; the
only user executable is `ldclean`. It never installs, documents, or aliases
`ldc`, which is an established LLVM D compiler command.

This is a greenfield implementation with no legacy or upstream codebase.

## Development status

This repository is in its bootstrap phase. The current binary provides only
offline `--help` and `--version` behavior, rejects a main process started with
effective UID 0, and contains an uninstalled helper that rejects every request.
It does not discover files, call package managers, request authorization,
perform cleanup, or mutate host state.

Future LDC behavior is constrained by an immutable plan-before-apply model,
point-of-use revalidation, Linux-native provider contracts, and a narrow
privilege boundary. Unsupported or ambiguous mutation is intended to fail
closed; `--force` will not broaden that boundary. See the approved
[design report](plans/reports/260714-1754-ldc-linux-native-design-report.md)
and [implementation plan](plans/260714-1831-ldc-full-parity-linux-implementation/plan.md).

## Bootstrap development

The pinned toolchain is Go 1.26.5. Normal Make development targets clear an
ambient `GOROOT` and use `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOWORK=off`, and
an empty `GOFLAGS`, so they neither select an ambient workspace nor download
dependencies. Default-lane checks that need a child Go command invoke the
already-running toolchain's `filepath.Join(runtime.GOROOT(), "bin", "go")`
directly and give that child a fixed `PATH=/usr/bin:/bin`.
Those child environments also explicitly empty `LDCLEAN_VMTEST` and
`LDCLEAN_VMTEST_TOKEN`, so an ordinary host test cannot inherit VM-test
authorization.

On a fresh checkout, deliberately prime and verify the audited module cache
before using the offline Make targets. The first command is the deliberate
networked cache-prime exception:

```bash
GOROOT= GOTOOLCHAIN=local GOWORK=off GOFLAGS= go mod download
GOROOT= GOTOOLCHAIN=local GOPROXY=off GOWORK=off GOFLAGS= go mod verify
```

```bash
make build
make test
make test-race
make vet
make coverage
```

`make integration` is an explicit, non-mutating integration lane. `make
vmtest-unit` exercises the VM guard with injected test dependencies and is
safe to run on a development host. `make vmtest` is intentionally rejected
unless a disposable guest has both `LDCLEAN_VMTEST=1` and a nonempty
`LDCLEAN_VMTEST_TOKEN`, plus the fixed
`/run/linux-deep-clean/disposable-guest` sentinel: it must be a root-owned,
regular, non-group/world-writable file whose content exactly matches the
token. It does not enable a destructive test by itself.

The helper binary is a development-only reject-all artifact. It is not
installed and has no polkit, sudo, or other privilege integration.

## Provenance

LDC is not a Mole port or fork. Its implementation, documentation, tests,
fixtures, rules, and visual identity must be independently authored. Do not
copy Mole source, prose, fixtures, artwork, cleanup tables, or distinctive
layouts. See [the provenance policy](docs/provenance-policy.md) before adding
third-party material.
