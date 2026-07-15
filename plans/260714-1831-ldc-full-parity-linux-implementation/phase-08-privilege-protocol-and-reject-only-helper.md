---
phase: 8
title: "Privilege Protocol and Reject-only Helper"
status: pending
priority: P1
effort: "12d"
dependencies: [7]
---

# Phase 8: Privilege Protocol and Reject-only Helper

## Overview

Build, stage-install, and adversarially qualify the privileged boundary before giving it any mutation authority. The installed `/usr/libexec/linux-deep-clean/helper` may authenticate its launcher/caller, read exactly one strictly framed canonical-CBOR request, independently validate the envelope and plan binding, return a bounded typed rejection, and exit. It has no action executor, manager runner, filesystem mutation, or generic command facility in this phase.

The unprivileged `ldclean` process displays the exact privileged action count, categories, plan digest, risk, and network disclosure before launch. Stock `pkexec` authorizes the executable before the helper reads stdin, so the trusted polkit prompt is deliberately static: `Authorize the LDC privileged helper.`

## Requirements

### Exact wire profile (protocol v1)

- Each direction carries exactly one frame and then closes. Header is 14 bytes:
  - bytes 0–7: ASCII magic `LDCHELP\x00`;
  - bytes 8–9: unsigned big-endian protocol version, exactly `1`;
  - bytes 10–13: unsigned big-endian CBOR payload length.
- Request and response payload ceilings are each 4,194,304 bytes (4 MiB), checked from the header before payload allocation. Zero-length, truncated, trailing, second-frame, wrong-magic, and unsupported-version input is rejected.
- Complete request read deadline is 5 seconds; reject-only helper lifetime is at most 10 seconds. A slow/trailing writer cannot keep a root process alive indefinitely.
- Use RFC 8949 deterministic/canonical CBOR through the approved strict `fxamacker/cbor/v2` mode. Decoder acceptance requires canonical re-encoding to equal the received payload byte-for-byte before digest or policy use.
- Exact structural limits:

| Property | Protocol v1 limit |
|---|---:|
| Complete plan actions | 1–1,024 |
| Nesting depth | 16 |
| Map pairs per map | 128 |
| Array elements per container | 2,048 |
| Any byte/text value | 1,048,576 bytes |
| BytePath components per action | 1,024 |
| Encoded BytePath per action | 262,144 bytes |
| Identifier text (`request`, `run`, `action`, provider/root IDs) | 128 UTF-8 bytes, with field-specific grammar |
| Bounded diagnostic detail in a response | 8,192 UTF-8 bytes |
| Total decoded retained request graph | 16 MiB |

- The request carries the complete canonical Phase 2 `PlanBody`, its digest, request/run/caller echoes, and a non-empty ordered `ExecutionActionIDs` subset. Every execution ID must be unique, exist in the plan, be selected, and satisfy the plan's dependency ordering. The full plan may also contain unprivileged or other-family actions; those remain digest-bound context and do not become helper execution authority.
- The helper response binds both the full plan digest and an `ExecutionSubsetDigest` computed over the ordered execution IDs. A request too large with the complete plan body must be reduced and re-planned; callers may not omit plan fields or split one family merely to bypass the 4 MiB ceiling. Later distinct family invocations reuse the same complete plan body and require separately disclosed authorization.
- BytePath components are CBOR byte strings. Apply the Phase 2 NUL/slash/empty/dot/dot-dot rules and later filesystem component limits; never accept an escaped display path as authority.
- Reject before policy/action allocation: indefinite lengths, duplicate keys, tags, floats, simple values outside the schema, bignums, invalid UTF-8 text, non-minimal integers/lengths, integer overflow, excessive counts/depth/bytes, unknown fields, missing fields, unsupported schema/protocol/action versions, and trailing bytes.
- All maps use the fixed v1 schema keys and field types from versioned fixtures. Decoder structs do not expose catch-all maps.
- Recompute the Phase 2 SHA-256 domain-separated canonical plan digest over the plan body excluding its digest field and compare it to request/history binding. The digest is audit/binding evidence, never authorization: helper policy independently validates every field.
- A response binds request ID, plan digest, execution-subset digest, helper build/protocol version, trusted caller UID, and terminal status. Phase 8's only well-formed requested-action response is `unsupported`/`no_actions_enabled`; authentication/protocol failures are `denied` or a bounded protocol error when it is safe to respond.
- Stdout contains only the one response frame. Diagnostics are bounded/redacted on stderr and never include raw paths, environment, full manager output, or request bytes.

### Launcher, caller, and installed-self checks

- Main invokes only absolute `/usr/bin/pkexec` with absolute `/usr/libexec/linux-deep-clean/helper`, or the explicit fallback `/usr/bin/sudo -- /usr/libexec/linux-deep-clean/helper`. No shell, `PATH` lookup, caller argv, dynamic polkit detail, or `NOPASSWD` policy is added.
- Prefer polkit when its packaged action and an agent are available; offer sudo fallback only through the user's existing policy. Denial is exit summary 5, never a safety fallback.
- Helper checks effective UID 0 before reading a request. Direct unprivileged execution rejects immediately.
- Helper verifies `/proc/self/exe` and `/usr/libexec/linux-deep-clean/helper` identify the expected regular file, owned by UID/GID 0, installed mode 0755, with no group/world write bit. It does not trust `argv[0]` or a caller path.
- Launcher mode is unambiguous:
  - polkit: canonical decimal `PKEXEC_UID` is present and `SUDO_UID`/`SUDO_GID` are absent;
  - sudo: canonical decimal `SUDO_UID` and `SUDO_GID` are present and `PKEXEC_UID` is absent;
  - missing, conflicting, signed, whitespace-padded, overflow, zero/root, nonexistent, or malformed numeric identities reject.
- `SUDO_USER`, `HOME`, `USER`, XDG values, request caller UID, parent PID, and `/proc` ancestry are not identity authority. Resolve passwd/group facts from the trusted numeric caller and compare the request's caller echo.
- A hostile root can spoof launcher markers and is outside scope. A compromised/buggy unprivileged main remains in scope and cannot expand helper authority.
- On entry, set umask 077 and cwd `/`; retain only stdin/stdout/stderr protocol descriptors with close-on-exec policy for everything else; ignore caller environment except the strictly parsed launcher markers. Phase 8 performs no external exec.

### Static polkit and installation contract

- Install helper as root:root 0755 at `/usr/libexec/linux-deep-clean/helper` and policy as root:root 0644 at `/usr/share/polkit-1/actions/org.linuxdeepclean.helper.policy` in native-package/staged-root layouts only.
- The polkit action binds the exact helper path and static message `Authorize the LDC privileged helper.` No request data is placed in argv/environment/policy variables to simulate a dynamic prompt.
- Generic/rootless archives exclude the helper, policy, sudo integration, and helper-install target output; privileged capabilities report unavailable.
- No daemon, socket, D-Bus service, setuid bit, sudoers file, resident root state, arbitrary file opener, or executor is installed.

### Reject-only authority and imports

- `helperpolicy` contains a closed registry and compiled root/action ID grammar, but every requested execution action validator terminates with `no_actions_enabled` after envelope/support checks. Non-requested plan actions are validated as canonical context only and never dispatched. The registry contains no function pointer or interface capable of mutation or exec.
- User-owned `trash_path`, `delete_recreatable_path`, and `quarantine_path` are explicitly rejected; they remain in the unprivileged Phase 7 process.
- The helper command dependency closure is limited to standard library, dependency-free domain types, strict privilege protocol, `helperauth`, reject-only `helperpolicy`, and the CBOR codec through protocol.
- Architecture tests reject imports of application, providerapi/providers, state/history, metrics, presenters/Cobra/Bubble Tea/Lip Gloss, Phase 7 userfs, managerexec/safeexec, network clients, or general command runners.
- Adding the first privileged action enum/validator/executor is Phase 9 work and requires a separate security review plus fixture/VM tests. Passing Phase 8 does not authorize it.

## Architecture

```text
ldclean confirmed privileged subset
  -> fixed launcher path (pkexec preferred, sudo fallback)
  -> installed helper self/caller/environment checks
  -> 14-byte bounded frame reader
  -> strict canonical CBOR decode + limits + byte equality
  -> Phase 2 plan digest/caller binding check
  -> reject-only helperpolicy registry
  -> one bounded canonical CBOR response frame
  -> exit
```

Validation order is security-significant: self/EUID/launcher identity, frame header and size, strict decode/resource limits, schema/canonicality, trusted-caller echo and digest binding, then reject-only policy. Invalid input must not reach action iteration, root lookup, or a future executor hook.

The full-plan/subset distinction is not a trust shortcut. Policy resolves each execution ID back to the canonical plan action, validates the subset digest and dependency facts, and rejects missing, duplicate, reordered, unselected, cross-plan, or Phase 8-enabled IDs. It never accepts an action object supplied only in a subset.

The client launches only after Phase 6 presents the dynamic privileged details and receives explicit apply confirmation. The polkit dialog is a second static authorization boundary, not a replacement for LDC's plan confirmation.

## Related Code Files

### Create

- `internal/privilege/protocol/frame.go`, `internal/privilege/protocol/limits.go`, `internal/privilege/protocol/codec.go`, `internal/privilege/protocol/schema.go`.
- `internal/privilege/protocol/frame_test.go`, `internal/privilege/protocol/codec_test.go`, `internal/privilege/protocol/codec_fuzz_test.go`, `internal/privilege/protocol/resource_test.go`.
- `internal/privilege/helperauth/caller_linux.go`, `internal/privilege/helperauth/selfcheck_linux.go`, `internal/privilege/helperauth/sanitize_linux.go`.
- `internal/privilege/helperauth/caller_test.go`, `internal/privilege/helperauth/selfcheck_test.go`, `internal/privilege/helperauth/sanitize_test.go`.
- `internal/privilege/helperpolicy/policy.go`, `internal/privilege/helperpolicy/registry.go`, `internal/privilege/helperpolicy/authority.go`, `internal/privilege/helperpolicy/policy_test.go`.
- `internal/privilege/client/launcher_linux.go`, `internal/privilege/client/launcher_test.go` — fixed absolute pkexec/sudo client and response-loss classification.
- `internal/architecture/helper_imports_test.go` — `go list -deps` allowlist and forbidden-authority checks.
- `packaging/common/org.linuxdeepclean.helper.policy` — static exact-path polkit action.
- `packaging/common/helper_install.mk` — DESTDIR-aware helper/policy layout; no sudoers/setuid behavior.
- `tests/contract/privilege_protocol_v1_test.go`, `tests/contract/reject_only_helper_test.go`, `tests/contract/rootless_helper_exclusion_test.go`.
- `tests/fixtures/protocol/v1/manifest.json`, `tests/fixtures/protocol/v1/valid_reject_request.cbor`, `tests/fixtures/protocol/v1/valid_reject_response.cbor`.
- `tests/fixtures/protocol/v1/fuzz/{duplicate_key,indefinite_map,nonminimal_integer,invalid_utf8,unknown_field,trailing_bytes,nesting_17,actions_1025,frame_4mib_plus_1,truncated_frame}.cbor`.
- `tests/integration/privilege/reject_only_helper_test.go`, `tests/integration/privilege/slow_frame_test.go`.
- `tests/vm/privilege/reject_only_helper_test.go` — installed ownership/mode, polkit/sudo identity/denial, malformed protocol, kill/response-loss tests.

### Modify

- `cmd/linux-deep-clean-helper/main.go` — replace the Phase 1 unconditional reject-only bootstrap with the authenticated framed reject-only server; no executor registration.
- `cmd/ldclean/main.go` — wire the fixed launcher client only after application confirmation; preserve root refusal.
- `internal/application/confirmation.go` — ensure dynamic privileged count/categories/digest/risk are rendered before static launcher authorization.
- `Makefile` — add safe DESTDIR staging and separate opt-in VM install target; normal build/test never installs or elevates.

### Delete

- None.

## Interfaces and Dependency Boundaries

```go
func ReadFrame(context.Context, io.Reader, Limits) ([]byte, error)
func WriteFrame(context.Context, io.Writer, []byte, Limits) error
func DecodeCanonicalRequest([]byte) (domain.HelperRequest, error)
func EncodeCanonicalResponse(domain.HelperResponse) ([]byte, error)

type TrustedCaller struct {
    UID, GID uint32
    Mode     LauncherMode
}

func DetectLauncherMode(lookup EnvLookup) (LauncherMode, error)
func DeriveTrustedCaller(LauncherMode, EnvLookup, UserLookup) (TrustedCaller, error)
func VerifyInstalledSelf(expectedPath string) error
func ValidateRequest(TrustedCaller, domain.HelperRequest) domain.HelperResponse
```

- Frame/codec APIs do not expose decoder modes or unbounded `Decode(any)` calls. Limits are protocol constants, not caller-configurable production options.
- `helperauth` reads only trusted process/file/launcher facts and returns numeric identity. It never accepts request identity as authority.
- `helperpolicy.ValidateRequest` returns a rejection response in Phase 8 and has no executor dependency. A compile-time test fails if an executor/runner field or import appears.
- `client` receives canonical request bytes from the Phase 2/4 plan binding, not arbitrary command text. Its argv is one of two exact templates.
- `domain.HelperRequest` carries one full canonical plan body plus execution IDs only; there is no second action body or partial-plan encoding. `ExecutionSubsetDigest` uses a versioned domain separator and stable plan action ordering.
- EOF, helper kill, invalid/missing response, or lost stdout after launch maps to `indeterminate` with `reconciliation_required`; never infer failed/safe-to-retry. In Phase 8 no action ran, but the shared result contract must already be conservative for Phase 9.
- The helper never imports unprivileged state/history; the caller records the bounded response or indeterminate transport fact.

## TDD Test Strategy

### RED — rejection and resource profile first

1. Freeze the 14-byte header and valid request/response bytes in golden tests before codec/server code.
2. Add a rejection table for every malformed form and every exact boundary: 4 MiB accepted/4 MiB+1 rejected before allocation, depth 16/17, 1,024/1,025 actions, map 128/129, array 2,048/2,049, value 1 MiB/+1, path 1,024/+1 components and 256 KiB/+1.
3. Add canonicality tests for duplicate keys, key order, indefinite lengths, non-minimal integers/lengths, tags, floats, bignums, invalid UTF-8, unknown/missing fields, trailing payload/frame, and semantic re-encode mismatch.
4. Add `FuzzReadFrame`, `FuzzCanonicalRequest`, `FuzzCallerMarkers`, and `FuzzRejectOnlyPolicy` with the checked-in corpus. Success requires no panic, hang, >16 MiB retained request graph, non-canonical acceptance, or policy/authority expansion.
5. Add caller/self tables for unprivileged direct run, root with no markers, polkit, sudo, conflicting markers, whitespace/sign/leading-zero/overflow/root/nonexistent UID, caller-echo mismatch, wrong owner/mode/type, alternate executable, symlink, and extra descriptors/environment.
6. Add `TestPolkitPolicyUsesStaticMessageAndExactPath`, `TestNoDynamicActionDataInLauncherArgv`, and `TestNoSudoersOrSetuidArtifact`.
7. Add `TestHelperRejectsEveryRequestedActionKind`, `TestFullPlanSubsetBindingRejectsMissingDuplicateReorderedOrCrossPlanIDs`, `TestUserOwnedActionsStayUnprivileged`, `TestHelperHasNoExecutorMapping`, and the `go list -deps` helper import allowlist.
8. Add process resource tests: slow/trailing writer terminated within 10 seconds; nesting/count bomb rejected within 250 ms on reference hardware; 4 MiB adversarial frame peak RSS at most 64 MiB; response never exceeds 4 MiB/8 KiB detail.
9. Add transport-loss tests requiring `indeterminate` plus reconciliation probe for EOF, kill, corrupt response, oversized response, and response-digest mismatch.

### GREEN — decode and reject only

1. Implement the bounded header reader/writer and deadline/poll behavior without CBOR.
2. Configure one strict canonical encoder/decoder mode and typed v1 schema; validate limits while decoding into bounded structures, then re-encode and compare bytes.
3. Implement installed-self, launcher-mode, trusted numeric caller, umask/cwd/FD/environment sanitation before request decoding.
4. Implement an empty-authority `helperpolicy` that validates envelope/binding and returns `no_actions_enabled` for every requested execution action.
5. Implement exact pkexec/sudo launch templates, static policy, DESTDIR install, and conservative transport-result mapping.
6. Wire one-frame server in the helper command. Do not add safeexec, managerexec, linuxfs mutation, or action hooks.

### REFACTOR — keep authority visibly absent

1. Consolidate common size checks only if boundary tests remain field-specific and no generic unbounded decoder appears.
2. Minimize helper dependency closure and allocations; keep authentication/codec/policy packages separately reviewable.
3. Make error categories stable and bounded without echoing hostile input.
4. Re-run corpus, fuzz, race, import, install-layout, resource, and VM tests after every protocol/auth change.

### Test matrix

| Axis | Cases |
|---|---|
| Framing | wrong magic/version, zero, truncated, exact 4 MiB, +1, trailing byte, second frame, slow writer |
| CBOR | canonical valid, duplicate/unknown/missing, indefinite, non-minimal, tag/float/bignum, invalid UTF-8, depth/count/size bombs |
| Identity | pkexec, sudo, direct user, root/no marker, conflicting/spoofed text, caller echo mismatch, missing passwd entry |
| Install | helper root:root 0755, policy root:root 0644, exact path/action, altered owner/mode/type, archive exclusion |
| Authority | every requested known/unknown/user action rejected; full-plan/subset mismatch and arbitrary executable/argv/env/root/path cannot be expressed |
| Transport | success rejection response, denial, timeout, kill, EOF, corrupt/oversized/mismatched response |

### Commands and gates

```bash
go test ./internal/privilege/... ./internal/architecture/... ./tests/contract/...
go test -race ./internal/privilege/... ./tests/integration/privilege/...
go test -fuzz=Fuzz -fuzztime=60s ./internal/privilege/protocol
go test -run 'Test.*Resource|Test.*Boundary' ./internal/privilege/protocol/...
go test -coverprofile=coverage.out ./internal/privilege/...
go tool cover -func=coverage.out
make build
make install-helper DESTDIR="$(mktemp -d)"
```

Default tests are hermetic, offline, unprivileged, and install only beneath a temporary `DESTDIR`; privileged cases use process seams or skip. Real root/polkit/sudo/install-mode tests require `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token, and the expected snapshot-backed guest identity. Missing any guard aborts before installation. Protocol/helper packages must meet 90% statement coverage.

## Implementation Steps

1. Freeze protocol-v1 schema, header, all numerical limits, complete-plan/execution-subset binding, canonical fixture bytes, stable errors, and rejection profile in tests.
2. Implement bounded framing with pre-allocation length checks, exact-one-frame semantics, deadlines, and response ceilings.
3. Implement the typed strict canonical codec and canonical re-encode equality; seed and run fuzz/resource campaigns.
4. Implement EUID/self/install verification, unambiguous launcher detection, trusted numeric caller derivation, request-echo comparison, and process sanitation.
5. Implement the closed reject-only authority registry; prove every requested execution action and every root/path, executable/argv, or environment expansion attempt rejects.
6. Implement client launch templates and conservative indeterminate transport mapping. Keep dynamic plan details in the Phase 6 confirmation.
7. Add static polkit policy and DESTDIR installer; prove rootless archive layout excludes all privileged artifacts.
8. Wire the helper command to read, validate, reject, respond once, and exit. Re-run dependency allowlist to prove no executor exists.
9. Run default, race, coverage, fuzz, corpus, resource, staged-install, and disposable-VM identity/polkit/sudo tests. Obtain security review before Phase 9.

## Success Criteria

- [ ] Protocol v1 framing, canonical CBOR schema, complete-plan/execution-subset digest binding, and every exact resource limit are frozen by golden/boundary tests.
- [ ] All malformed, non-canonical, unknown, duplicate, over-budget, trailing, and unsupported input rejects before authority/action allocation.
- [ ] Helper derives caller only from one trusted launcher mode after EUID/self checks; request/environment display fields cannot authorize.
- [ ] Polkit policy uses the exact helper path and static prompt; dynamic count/categories/digest remain in unprivileged confirmation.
- [ ] The installed helper/policy pass owner/mode/type checks; no setuid, sudoers, daemon, service, socket, or rootless-archive helper exists.
- [ ] Every requested execution action—including any attempted Phase 7 user-owned action—is rejected with no executor, exec, write, rename, unlink, network, or state access; non-requested plan actions remain inert digest-bound context.
- [ ] Helper dependency closure passes the strict import allowlist.
- [ ] Fuzz/resource/slow-frame tests show no panic, hang, unbounded allocation, non-canonical acceptance, or authority expansion.
- [ ] Lost/invalid helper responses map to `indeterminate` and `reconciliation_required`, never safe retry.
- [ ] Default tests remain hermetic/offline/unprivileged; real launcher/install tests require both VM guards.
- [ ] Protocol/helper statement coverage is at least 90%, and all earlier race/coverage gates remain green.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback |
|---|---|---|
| Decoder differential or resource exhaustion | Strict typed profile, canonical byte equality, exact pre-allocation/count/depth limits, fuzz/resource gates | Remove/disable helper installation; privileged capabilities become unavailable |
| Caller identity spoof through request/env | EUID/self checks, one launcher marker set, numeric lookup, request echo only, no parent-PID authority | Reject launcher mode and fall back only to preview/guidance |
| Helper becomes generic root primitive | Empty executor registry, closed IDs, no exec/syscall imports, architecture allowlist | Uninstall helper/policy package files; rootless `ldclean` continues read-only/user-owned work |
| Users expect dynamic polkit details | Exact dynamic subset in LDC confirmation; static prompt text documented/tested | Disable polkit launch if policy/UI contract cannot be met; do not smuggle data via argv |
| Transport loss is mistaken for failure | Mandatory indeterminate/reconciliation mapping | Block future apply until typed reconciliation completes |

Rollback removes the helper and static policy or unregisters privileged capability. It does not change user-owned Phase 7 behavior, delete state, or infer anything about an interrupted future transaction. Because Phase 8 executes nothing, rollback has no privileged data mutation to undo.

## Phase Exit/Handoff

Phase 9 receives a frozen frame/schema/limit profile, trusted caller, installed-self checks, static launcher contract, reject-only authority registry, bounded result transport, fuzz corpus, resource gates, and helper import allowlist. It may add exactly one privileged action family at a time by introducing its validator, compiled root/operation mapping, fixed executor, re-probe, postcondition/reconciliation, negative arbitrary-argv test, and disposable-VM qualification together. It must not loosen Phase 8 limits/authentication or enable user-owned actions in the helper.
