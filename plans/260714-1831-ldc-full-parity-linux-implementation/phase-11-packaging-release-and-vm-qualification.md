---
phase: 11
title: "Packaging, Release, and VM Qualification"
status: pending
priority: P1
effort: "5w"
dependencies: [10]
---

# Phase 11: Packaging, Release, and VM Qualification

## Overview

Produce installable native `.deb`, `.rpm`, and Arch packages plus a reduced-capability rootless `.tar.zst`; generate reproducible and verifiable release evidence; and qualify the complete public contract on the approved Linux matrix. No artifact or documentation may claim full parity or mutation support until package lifecycle, supply-chain, performance, destructive VM, and 10,000-attempt-per-filesystem race gates all pass.

Official distribution-repository acceptance is out of scope. This phase publishes LDC-owned native artifacts and signed repository metadata without weakening downstream distro governance requirements.

## Requirements

### Artifact and installation contract

| Artifact | Required contents | Forbidden contents/behavior |
|---|---|---|
| Debian/Ubuntu `.deb` | `/usr/bin/ldclean`; root-owned `/usr/libexec/linux-deep-clean/helper`; static polkit action; `ldclean(1)`; Bash/Zsh/Fish completions; Apache-2.0 notices | No `/usr/bin/ldc` or alias, setuid bit, writable helper/policy, daemon/service, sudoers `NOPASSWD`, home-directory mutation, purge of XDG state, curl-pipe installer |
| Fedora `.rpm` | Same native ownership and paths, expressed through RPM policy/macros | No DNF4 compatibility claim, generic privilege integration, or scriptlet that starts a service/mutates user state |
| Arch package | Same native ownership and paths through `PKGBUILD`; current fully upgraded x86_64 only | No broad pacman transaction, AUR/official-repo acceptance claim, or mutation support outside the qualified current image |
| Rootless `.tar.zst` | `ldclean`, man page, Bash/Zsh/Fish completions, README/license/notices, verification instructions | No helper, polkit policy, sudo integration/config, package scripts, system-path installer, self-update/self-remove, or privileged-capability claim |

- Package name/repository slug is `linux-deep-clean`; executable is always `ldclean`, never `ldc`.
- Native files have deterministic root ownership/modes. The helper is an ordinary root-owned non-group/world-writable executable, never setuid. Package removal deletes package-owned system files and preserves user XDG config/state/cache/history.
- Generate man page and completions from the frozen Phase 10 command tree. Fail if generated output differs from checked release assets or mentions a nonexistent/forbidden command.
- Rootless archive startup reports privileged/system mutation unavailable. It must not discover or use a helper left by a native install unless its own verified installation origin is native.

### Reproducibility, integrity, and provenance

- Pin the supported Go patch and every release tool by version and verified digest at phase entry. Re-check current official documentation before pinning; never install tools with curl-pipe-shell.
- Derive version and `SOURCE_DATE_EPOCH` from an annotated release tag/commit. Build with fixed locale/timezone, `GOTOOLCHAIN=local`, `-trimpath`, no host path/timestamp, deterministic archive order/ownership/mtime, and no untracked source.
- Build each unsigned/native payload candidate twice in independent clean builders. Byte-for-byte mismatch blocks release and produces a `diffoscope` report; do not bless one output manually. If a package format requires an embedded signature that changes package bytes, also compare its extracted payload/file manifest to the reproduced unsigned candidate.
- Apply every byte-mutating package-format signature before the final artifact digest set is frozen. Then emit SHA-256 checksums for the final bytes, per-artifact SPDX JSON SBOM, dependency/license inventory, signed checksum manifest, and signed in-toto/SLSA-style provenance binding source commit, builder identity, commands, materials, and final artifact digests.
- Use a reviewed release identity/custody mechanism. CI keyless signing may use the repository's protected OIDC identity; local release signing requires an approved hardware/offline key. PR artifacts use explicitly marked ephemeral test signatures and cannot be promoted.
- Verify checksum signature/identity, SBOM-artifact binding, provenance subject digest, package signature/repository metadata, final package payload identity, and archive contents before publishing.
- Generate signed LDC-owned APT `Release`/Packages metadata, RPM repodata, and Arch repository database for the native artifacts. Official Debian/Fedora/Arch submission remains a later governance process.

### CI and VM lanes

- Pull requests gate exactly these four mutation VMs: Ubuntu 24.04 x86_64, Debian 13 x86_64, Fedora 44 x86_64, and current fully upgraded Arch x86_64.
- Nightly and release candidates gate all 15 approved VMs:

| Distro/release | x86_64 | aarch64 |
|---|:---:|:---:|
| Ubuntu 22.04 LTS | required | required |
| Ubuntu 24.04 LTS | required | required |
| Ubuntu 26.04 LTS | required | required |
| Debian 12 | required | required |
| Debian 13 | required | required |
| Fedora 43 non-atomic | required | required |
| Fedora 44 non-atomic | required | required |
| Arch current, fully upgraded | required | unsupported by approved matrix |

- Every destructive guest starts from a clean snapshot and validates: `vmtest` build tag, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token, expected image identity/digest, and a separate outside-root sentinel. Failure aborts before privilege or mutation.
- Exercise only production binaries/protocol/policies and locally built `linux-deep-clean-test-*` packages/local Flatpak/Snap fixtures. Reset the snapshot after each destructive case.
- On every native-package guest test install -> exact-package update -> remove -> reinstall, ownership/modes, helper/polkit denial and success, completions/man page, preserved XDG state, and actual command/provider behavior.
- Test locks, typed graph drift, forced interruption/reboot, indeterminate reconciliation, Trash/quarantine recovery, journald, optional-provider absence, unsupported environment rejection, and structured/TUI parity.
- The PR filesystem smoke lane completes at least 1,000 ext4 adversarial attempts. Nightly and release each complete at least 10,000 attempts on ext4, 10,000 on XFS, and 10,000 on Btrfs: **30,000 completed mutation attempts total**, all race classes represented, zero outside-root mutation.

### Performance and public claim gates

On documented reference hardware with the specified warm/cold cache state:

| Gate | Required threshold |
|---|---:|
| `ldclean --help` / `--version` p95 | <= 150 ms |
| TUI first paint p95 | <= 300 ms |
| 100,000-entry inventory | <= 2 s and <= 128 MiB RSS |
| 1,000,000-entry deep scan | <= 60 s and <= 256 MiB RSS |
| Default scanning resources | <= 4 workers and <= 128 open FDs |
| Cancellation to quiescence | <= 1 s, excluding an already-running non-interruptible manager transaction |
| Dry-run side effects | zero filesystem mutation, auth prompts, and network requests |
| Controlled ext4 freed-space estimate | within `max(5%, 64 MiB)` of verified allocated-byte change |

- Package/provider behavior may be I/O-bound, but memory/FD/cancellation limits still apply.
- Full-parity/support publication requires a signed qualification manifest with a green result for all 15 VM identities, all three native package families, both architectures where approved, the rootless exclusions, supply-chain verification, performance gates, and 30,000-attempt race campaign.
- A missing/failed/flaky lane is not “unsupported” by convenience and cannot shrink the user-approved matrix. It blocks the full-parity release until fixed and rerun. Pre-release/read-only artifacts must state that they are unqualified.

## Architecture

### Release pipeline

```text
clean tagged source + pinned tool manifest
  -> deterministic Go binaries (x86_64/aarch64)
  -> generated man/completions
  -> native deb/rpm/Arch + rootless tar.zst
  -> independent unsigned/native-payload rebuild and byte comparison
  -> canonical payload/file-manifest extraction
  -> any byte-mutating package signature
  -> verify signed payload identity, then freeze final package/archive bytes
  -> SBOM + license inventory + checksums + signed provenance/repository metadata
  -> package lint/content/permission/contract tests over final bytes
  -> PR 4-VM / nightly-release 15-VM qualification over final bytes/metadata
  -> ext4/XFS/Btrfs race + performance gates
  -> verify from an empty trust workspace
  -> create/sign qualification manifest without changing artifact bytes
  -> publish the verified frozen set
```

No artifact byte may change after its final digest is frozen. Embedded or otherwise byte-mutating package signatures occur before that freeze; all later checksum, provenance, qualification, and repository signatures are detached from the package/archive bytes or apply only to newly generated metadata. Every package/content/VM/repository qualification lane consumes the exact frozen final bytes. Publication consumes verified digests, never a rebuild or re-sign, and repository indexes refer to those same package bytes.

### VM harness trust boundary

The host controller is read-only with respect to the developer host and sends artifacts only to snapshot-backed guests. Inside a guest, `SentinelGuard` checks the opt-in env, sentinel inode/type/owner/mode/content nonce, image manifest digest, distro/release/architecture, systemd, kernel >=5.15, and expected test filesystem. Destructive helpers receive only harness-created roots/packages. The outside sentinel lives on a separate mount and is hashed/inspected after every attempt.

Each result bundle records source/artifact/image digests, kernel, architecture, manager versions, filesystem/mount options, test seed/schedule, completed attempt counts per filesystem/race class, quarantine/reconciliation state, timings/RSS/FDs, and logs with secrets/path data redacted.

### Support manifest

`qualification.json` is generated from signed machine results, not manually edited. Its schema lists every approved target and gate. The publisher rejects missing/duplicate targets, stale source/artifact digests, reruns without seed evidence, aggregate-only race counts, or an unsigned/failed result. Runtime support policy stays compiled and fail-closed; this manifest is release evidence, not a way to dynamically grant authority.

## Related Code Files

All paths are repository-relative. Estimates guide review, not quotas.

| Action | File | Scope | Purpose and test impact |
|---|---|---:|---|
| Modify | `Makefile` | medium | Deterministic build/test/package/release/verify targets with no hidden installer. |
| Create | `packaging/common/release-tools.lock` | data | Pinned release-tool versions, sources, licenses, and SHA-256 digests. |
| Create | `packaging/common/files-manifest.yaml` | data | Canonical native/archive source-to-destination ownership/mode/exclusion contract. |
| Create | `packaging/common/ldclean.1` | generated | Reviewed deterministic man page from frozen command tree. |
| Create | `packaging/common/completions/ldclean.bash` | generated | Bash completion artifact. |
| Create | `packaging/common/completions/_ldclean` | generated | Zsh completion artifact. |
| Create | `packaging/common/completions/ldclean.fish` | generated | Fish completion artifact. |
| Create | `packaging/debian/debian/changelog` | small | Native release version/distribution metadata. |
| Create | `packaging/debian/debian/control` | small | Package metadata and constrained runtime/build dependencies. |
| Create | `packaging/debian/debian/copyright` | medium | Apache-2.0 and dependency provenance. |
| Create | `packaging/debian/debian/rules` | small | Deterministic Go/native package build. |
| Create | `packaging/debian/debian/source/format` | tiny | Explicit Debian source format. |
| Create | `packaging/debian/debian/linux-deep-clean.install` | small | Exact binary/helper/policy/completion files. |
| Create | `packaging/debian/debian/linux-deep-clean.manpages` | tiny | `ldclean(1)` installation. |
| Create | `packaging/rpm/linux-deep-clean.spec` | medium | Reproducible RPM build, exact files/modes, no service/scriptlet side effects. |
| Create | `packaging/arch/PKGBUILD` | medium | Reproducible Arch package with exact files/modes and checksums. |
| Create | `packaging/archive/build-rootless-archive.sh` | medium | Deterministic sorted `.tar.zst` containing only rootless manifest entries. |
| Create | `packaging/repositories/build-metadata.sh` | medium | LDC-owned signed APT/RPM/Arch metadata from frozen package bytes. |
| Create | `internal/release/manifest.go` | ~180 LOC | Release/qualification manifest schemas, canonical validation, artifact bindings. |
| Create | `internal/release/manifest_test.go` | ~220 LOC | Missing/duplicate/stale/failed target and digest/signature binding tests. |
| Create | `tools/release/main.go` | ~260 LOC | Deterministic manifest/SBOM/checksum/provenance orchestration; never publishes implicitly. |
| Create | `scripts/release/build.sh` | ~180 LOC | Clean builder wrapper and native/archive build dispatch. |
| Create | `scripts/release/verify.sh` | ~220 LOC | Empty-workspace signature/SBOM/provenance/package/archive verification. |
| Create | `scripts/release/publish.sh` | ~120 LOC | Publish only a previously verified frozen digest set after explicit release invocation. |
| Create | `tests/contract/package_contents_test.go` | ~220 LOC | Exact native file ownership/modes and archive exclusions, including no `ldc`. |
| Create | `tests/contract/generated_docs_test.go` | ~120 LOC | Man/completion reproducibility and command-ledger agreement. |
| Create | `tests/integration/reproducible_build_test.go` | ~180 LOC | Compare two isolated builds and emit diffoscope path on mismatch. |
| Create | `tests/integration/release_verification_test.go` | ~200 LOC | Tampered checksum/signature/SBOM/provenance/qualification negative tests. |
| Create | `tests/performance/reference-machine.yaml` | data | CPU/RAM/storage/kernel/cache-state definition and calibration record. |
| Create | `tests/performance/release_gates_test.go` | ~260 LOC | Exact latency/RSS/FD/concurrency/cancellation/estimate thresholds. |
| Create | `tests/vm/matrix.yaml` | data | Exact 4-PR and 15-nightly/release image identities/digests and backend expectations. |
| Create | `tests/vm/cmd/vmtest/main.go` | ~300 LOC | Snapshot controller, artifact injection, suites, reset, signed result collection. |
| Create | `tests/vm/harness/sentinel_linux.go` | ~160 LOC | Double opt-in, root-owned sentinel/image/filesystem/outside-root checks. |
| Create | `tests/vm/harness/result.go` | ~180 LOC | Canonical per-guest/race/performance evidence and redaction. |
| Create | `tests/vm/qualification/package-lifecycle-test.sh` | ~260 LOC | Install/update/remove/reinstall and XDG preservation for each native format. |
| Create | `tests/vm/qualification/full-parity-test.sh` | ~260 LOC | Execute Phase 10 ledger behavior against production artifacts. |
| Create | `tests/vm/filesystem/race_campaign_test.go` | ~300 LOC | Deterministic barrier schedules and exact per-filesystem counters. |
| Create | `.github/workflows/vm-pr.yml` | medium | Required four-x86_64-VM mutation lane plus 1,000 ext4 smoke attempts. |
| Create | `.github/workflows/vm-nightly.yml` | large | Scheduled 15-VM suite and 30,000-attempt full filesystem campaign. |
| Create | `.github/workflows/release.yml` | large | Tagged build, independent rebuild, full qualification, signing, verify, explicit publish. |
| Modify | `.github/workflows/ci.yml` | small | Default hermetic/race/vet/vulnerability/license/contract gates; no VM mutation. |
| Create | `docs/release-process.md` | docs | Signing custody, reproducible build, qualification, rollback/revocation, and support-claim procedure. |
| Create | `docs/supported-platforms.md` | docs | Exact approved matrix, prerequisites, degraded read-only behavior, exclusions. |
| Modify | `docs/semantic-parity.md` | small | Link each public parity claim to release qualification evidence. |
| Delete | None | — | This phase removes no files. |

`packaging/common/org.linuxdeepclean.helper.policy` is created and negative-tested in Phase 8; this phase packages the exact reviewed bytes rather than generating a different policy.

## Interfaces and Dependency Boundaries

### Function/interface checklist

- [ ] `FileManifest.Validate(ArtifactKind)` proves native required paths/modes and rootless forbidden paths before packaging.
- [ ] `GenerateReleaseManifest(source, artifacts, tools)` is deterministic and binds exact digests.
- [ ] `VerifyRelease(trustPolicy, manifest, artifacts, sboms, provenance)` works in an empty workspace and fails closed.
- [ ] `SentinelGuard.Validate()` requires tag + env + compiled sentinel path + image identity + supported host facts; none are caller-overridable paths.
- [ ] `VMController.Restore/Boot/Inject/Run/Collect/Destroy` never executes destructive guest commands on the host.
- [ ] `QualificationResult.ValidateMatrix()` requires exactly the approved 15 unique targets and all mandatory suites.
- [ ] `RaceResult.Validate()` requires >=10,000 completed attempts separately for ext4, XFS, and Btrfs, every race class, and zero outside-root changes.
- [ ] `PerformanceResult.Validate(reference)` enforces fixed user-approved thresholds; CI config cannot supply looser values.
- [ ] `Publisher.Publish(VerifiedDigestSet)` cannot build, mutate, or substitute artifacts.

### Dependency map

```text
Phases 1-10 frozen source/contracts/tests
  -> deterministic binaries + generated docs/completions
  -> native/rootless packages
  -> package content/lifecycle qualification
  -> 4-VM PR evidence and 15-VM nightly/release evidence
  -> race/performance/supply-chain verification
  -> signed qualification manifest
  -> explicit publication and support claim
```

Packaging scripts may consume built binaries/assets but cannot change command behavior or helper policy. VM harness code is test-only and cannot be imported by production packages. Release signing/publishing has no runtime dependency in `ldclean`. Repository metadata and rootless update metadata bind the same frozen artifacts consumed by Phase 10 verification.

## TDD Test Strategy

### RED -> GREEN -> REFACTOR

1. **RED — manifest before packages.** Write exact content/mode tests for each native format and a rootless denylist test covering helper, polkit, sudo, package scripts, system installers, and every `ldc` path/name.
2. **GREEN — one native format at a time.** Implement Debian packaging and lifecycle tests, then RPM, then Arch. Generate man/completions from the command tree and make drift fail.
3. **REFACTOR — common file manifest only.** Deduplicate source assets through `files-manifest.yaml`; keep packager-native metadata/build tools rather than replacing them with nFPM/GoReleaser.
4. **RED — reproducibility/supply chain.** Add different-workdir/timezone/locale/umask builder tests plus tampered checksum, signature identity, SBOM digest, provenance subject, package payload, and repository metadata tests. Include a test that fails if any package byte changes after final digest freeze or qualification uses a pre-signing candidate.
5. **GREEN — deterministic release tooling.** Normalize timestamps/order/ownership/build flags, pin tools, require identical unsigned/native payload rebuilds, apply byte-mutating format signatures before freeze, bind evidence to final bytes, and verify from an empty workspace.
6. **REFACTOR — frozen final digest set.** Separate reproduce, format-sign, freeze, evidence-sign, qualify, and publish stages so nothing can rebuild, re-sign, or substitute package/archive bytes after freeze.
7. **RED — VM safety/matrix.** Test missing/writable/symlink/wrong-nonce sentinel, wrong image/distro/arch/fs, host invocation, duplicate/missing matrix row, arbitrary package name, and snapshot-reset failure.
8. **GREEN — PR lane first.** Bring up the four required x86_64 VMs and pass package lifecycle, manager/helper, parity, interruption, unsupported-host, and 1,000-attempt ext4 smoke tests.
9. **GREEN — full matrix.** Add all aarch64 and prior-release guests, run identical production suites, then enable nightly/release only after all 15 are stable.
10. **RED/GREEN — fixed gates.** Make each performance threshold and each filesystem attempt count fail one below its fixed value. Implement full 10,000 ext4 + 10,000 XFS + 10,000 Btrfs deterministic campaign and zero-escape validation.
11. **REFACTOR — evidence aggregation.** Aggregate only signed canonical results; retain per-guest/per-filesystem counts, seeds, and logs. Never turn flaky/missing into passed.

### Exact test and release commands

```bash
go test ./tests/contract/... -run 'PackageContents|GeneratedDocs|SemanticParity|NoLDCExecutable'
go test -tags=integration ./tests/integration/... -run 'ReproducibleBuild|ReleaseVerification'
go test -race ./...
go vet ./...
govulncheck ./...
make package-all
make verify-packages
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go run -tags=vmtest ./tests/vm/cmd/vmtest --lane=pr --matrix=tests/vm/matrix.yaml
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go run -tags=vmtest ./tests/vm/cmd/vmtest --lane=release --matrix=tests/vm/matrix.yaml
LDCLEAN_VMTEST=1 LDCLEAN_VMTEST_TOKEN="$RUN_TOKEN" go test -tags=vmtest ./tests/vm/filesystem/... -run TestAdversarialRaceCampaign -args -filesystems=ext4,xfs,btrfs -attempts-per-filesystem=10000
go test -tags=performance ./tests/performance/... -run TestReleaseGates
make release-build SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"
make release-rebuild-compare
make release-verify
```

VM commands run only through the double-gated snapshot controller. Release publication is intentionally a separate explicit command after human review of the verified digest set; ordinary tests and `make release-build` never publish.

### Qualification matrix

| Priority | Scenario | Lane | Required outcome |
|---|---|---|---|
| Critical | Native exact contents, ownership, modes, no setuid/`ldc` | all package builders/VMs | Exact file manifest; helper/policy immutable by non-root. |
| Critical | Rootless archive denylist and privileged capability probe | x86_64 + aarch64 archive tests | No helper/polkit/sudo/package scripts; privileged actions unavailable. |
| Critical | Install -> update -> remove -> reinstall with existing XDG state | every supported native-package VM | Correct versions/files each step; user state unchanged; commands work after reinstall. |
| Critical | Two independent clean builds | every published artifact/arch | Unsigned/native payload bytes are identical; any embedded-signed payload matches that manifest; mismatch blocks with diff report. |
| Critical | Byte mutation or candidate substitution after final digest freeze | release orchestrator + VM artifact injection | Rejected; every qualification result binds the exact final package/archive digest. |
| Critical | Tampered artifact/checksum/signature/SBOM/provenance/repository/qualification | empty-workspace verifier | Every mutation rejected before publish/install guidance. |
| Critical | Missing/wrong guest sentinel/image/package allowlist | harness negatives | Abort before helper/manager; developer host unchanged. |
| High | Full command/parity/provider/helper/interruption suite | 4 PR + 15 nightly/release VMs | Every required row passes; optional absence is typed. |
| Critical | Race campaign | PR: >=1,000 ext4; nightly/release: >=10,000 each ext4/XFS/Btrfs | Zero outside mutation; every class/count/seed recorded. |
| High | Unsupported FUSE/overlay/network/removable/WSL/container/chroot/non-systemd/kernel<5.15 | disposable negatives | Read-only/degraded or exit 6; `--force` cannot enable mutation. |
| High | Help/version/TUI/inventory/deep scan/resources/cancel/dry-run/estimate | reference performance lane | Every fixed threshold passes without CI override. |
| High | Man pages/completions/help/ledger agreement | all artifacts | Deterministic exact `ldclean` surface; no stale/extra command. |
| Medium | Package lint and clean removal | lintian/rpmlint/namcap + VMs | No hidden errors; package-owned system files removed, XDG state retained. |

## Implementation Steps

1. Freeze the Phase 10 command/parity schema and select/pin release builders, SBOM, signing, provenance, package-lint, repository, and diff tools from current official sources. Record tool digests/licenses and signing trust policy.
2. Add the canonical file manifest and failing package/rootless content tests. Generate and review man/completion assets; enforce deterministic regeneration.
3. Implement Debian packaging with native `dpkg-buildpackage`; pass content/lint and install/update/remove/reinstall on Ubuntu/Debian snapshots.
4. Implement RPM packaging with `rpmbuild` plus an isolated builder; pass content/lint and lifecycle on Fedora snapshots.
5. Implement Arch `PKGBUILD` with `makepkg`; pass content/namcap and lifecycle on a fully upgraded current Arch snapshot.
6. Implement deterministic rootless `.tar.zst` with sorted paths, fixed epoch/ownership/modes, single-thread deterministic compression, and strict denylist/capability tests.
7. Implement release manifest, double-build payload comparison, pre-freeze package-format signing, final-digest freeze, SBOM/license/checksum/provenance generation, repository metadata, and empty-workspace verification. Make later signatures detached/metadata-only and keep reproduce/sign/freeze/qualify/publish stages separate.
8. Implement the sentinel-guarded snapshot VM controller and exact matrix. Bring up the four PR guests first; exercise production packages, helpers, local test packages, parity, recovery, and unsupported negatives.
9. Add the remaining 11 nightly/release targets, including native aarch64 execution. Pin image identity/digest and record manager/kernel versions without assuming distro name alone proves support.
10. Implement package lifecycle suites across all applicable guests. Confirm native removal preserves XDG state and rootless behavior cannot acquire native helper authority.
11. Implement deterministic barrier-driven filesystem campaigns. Gate PR on >=1,000 ext4 attempts and nightly/release on >=10,000 completed attempts separately for ext4, XFS, Btrfs (>=30,000 total), every race class, zero escape.
12. Implement the fixed performance gates on documented reference hardware and retain raw measurements. Optimize implementation if a threshold fails; do not loosen it without a new user decision.
13. Run the full 15-VM release candidate, supply-chain verification, provenance/license/security review, and clean-room provenance audit. Generate the signed qualification manifest from passing results only.
14. Perform a dry-run publication, verify install/update/remove from LDC-owned repository metadata, then explicitly publish the frozen digest set. Update support docs only with the signed evidence link.

## Success Criteria

- [ ] `.deb`, `.rpm`, Arch, and x86_64/aarch64 rootless `.tar.zst` artifacts build from clean tagged source and pass exact content/mode tests.
- [ ] Native packages install `/usr/bin/ldclean`, the reviewed non-setuid helper/polkit policy, man page, and Bash/Zsh/Fish completions; no artifact claims or installs `ldc`.
- [ ] Rootless archives contain no helper, polkit, sudo integration/config, package script, system installer, self-mutation, or privileged capability.
- [ ] Install -> exact-package update -> remove -> reinstall passes on each applicable supported VM/package family; user XDG state survives removal.
- [ ] Every published final byte has a checksum, SPDX SBOM, license inventory, verified signature, and source/builder/material/final-artifact-bound provenance.
- [ ] Independent clean unsigned/native payload rebuilds are byte-identical; embedded-signed packages retain that exact payload; verification succeeds from an empty workspace before publication.
- [ ] All byte-mutating signatures precede final digest freeze, every qualification lane binds the exact frozen final bytes, and no post-freeze step rebuilds or re-signs an artifact.
- [ ] LDC-owned signed APT/RPM/Arch metadata refers to the same frozen package digests; official distro acceptance is not claimed.
- [ ] The four required PR VMs pass on every pull request and all 15 approved VMs pass nightly and for the release candidate.
- [ ] VM mutation is impossible without `vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, a validated root-owned sentinel whose exact content matches that token, expected image identity, and harness-owned targets.
- [ ] PR smoke completes >=1,000 ext4 attempts; nightly/release complete >=10,000 ext4 + >=10,000 XFS + >=10,000 Btrfs (>=30,000 total), every race class, zero outside-root mutation.
- [ ] Every fixed latency, memory, FD, concurrency, cancellation, dry-run, and freed-space-estimate threshold passes on documented reference hardware.
- [ ] Full semantic parity ledger, helper/security, fuzz/race, package lint, vulnerability, license, and provenance reviews are green.
- [ ] The signed qualification manifest contains all 15 unique target results and exact source/artifact/image digests; no failed, missing, stale, or flaky lane is treated as passed.
- [ ] Full parity and mutation support are claimed only after every criterion above passes.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback/containment |
|---|---|---|
| Package path/mode exposes helper or collides with `ldc` | Canonical exact file manifest, package extraction tests, installed self-check | Block/unpublish candidate metadata; publish corrected version. Never add alias/setuid workaround. |
| Rootless archive accidentally carries privilege files | Explicit allowlist plus recursive denylist and capability test | Do not sign/publish; discard candidate and rebuild from reviewed manifest. |
| Non-reproducible/native tool variance | Pinned clean builders, fixed epoch/locale/order/flags, two-build comparison, diffoscope | Block release and fix entropy source; never waive digest mismatch. |
| Embedded signing changes bytes after qualification | Format signing before final freeze, payload-manifest verification, digest-bound VM injection, no post-freeze re-sign | Reject candidate and rerun qualification over the newly frozen bytes; never transfer evidence between digests. |
| Signing identity/key compromise or unavailable signer | Protected trust policy, least privilege, offline/hardware option, verifier identity constraints | Stop publication, revoke/rotate per documented procedure, sign a new manifest; never reuse suspect evidence. |
| VM harness mutates host or wrong guest | Double opt-in, fixed sentinel, image identity, harness-owned package/root allowlists, snapshot API boundary | Abort before privilege; destroy guest and investigate. No host fallback. |
| Matrix image/toolchain drift | Pinned image digests plus scheduled refresh qualification | Keep prior qualified release; update image/tool fixtures and rerun all gates. |
| Race escape or retained-object corruption | Deterministic barriers, outside sentinels, per-attempt assertions, recorded seeds | Immediate release block; preserve image/result; return to filesystem phase. Do not reduce attempts. |
| Performance gate fails on fixed reference | Raw evidence and fixed thresholds in code | Optimize implementation or block release; changing threshold needs explicit user approval. |
| Package lifecycle removes user state | No home-touching scripts, populated-state assertions | Block artifact; correct packaging. Never recreate fake state or hide failure. |
| One approved VM remains flaky/unavailable | Retry only with the same artifact/image/seed evidence and diagnose infrastructure | Full-parity release remains blocked; do not silently shrink the 15-VM matrix. |
| Published artifact later proves unsafe | Signed repository metadata, retained provenance/results, documented revocation/yank path | Remove/yank affected repository version, publish advisory/fixed version, preserve audit evidence; never rewrite signed history. |

## Phase Exit/Handoff

This is the release gate. Exit only with the immutable published digest set, signatures, SBOMs, provenance, package/repository metadata, empty-workspace verification report, performance report, four-VM PR configuration, green 15-VM release report, per-filesystem race reports showing at least 10,000/10,000/10,000 completed attempts, and signed qualification manifest. If any item is absent, the project may distribute clearly labeled unqualified pre-release/read-only artifacts, but it must not claim full semantic parity or supported mutation. Future releases rerun the same gates; official distro-repository submission is a separate plan.
