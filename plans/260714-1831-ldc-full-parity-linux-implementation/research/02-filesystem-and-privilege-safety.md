---
type: research
date: 2026-07-14
subject: filesystem and privilege safety
status: complete
---

# Filesystem and privilege safety research

## Summary

The approved safety direction is implementable on Linux 5.15+ if apply-time authority is a trusted directory descriptor, never a caller path, and every irreversible pathname operation is reduced to a final basename beneath an already-open parent. `openat2` supplies race-resistant traversal constraints; it does not make `renameat2` or `unlinkat` operate on an already-verified inode. Same-filesystem staging is therefore mandatory before irreversible deletion. A post-stage mismatch must never be deleted and may be restored only with no-replace semantics.

The privileged helper should remain short-lived and unconditionally distrust the main process. `PKEXEC_UID` or `SUDO_UID` identifies the launcher caller only after the helper proves it is already root through that launcher; the request's UID and digest are not authority. Stock `pkexec` cannot put request-derived action counts and categories into its authorization dialog because authorization precedes delivery and validation of the stdin request. LDC must show the exact privileged subset itself immediately before a static polkit/sudo prompt.

Four plan-critical corrections are required:

1. `renameat2` must receive only a validated final basename and directory FDs; it has no `openat2` resolution flags.
2. `statx` mount IDs on the Linux 5.15 floor are drift evidence, not globally unique persistent identities.
3. Dynamic action counts/categories cannot be trusted in a stock `pkexec` prompt; keep them in LDC's own confirmation view.
4. Helper results need `indeterminate`/`reconciliation_required` for interrupted manager transactions or a lost helper response.

## Scope and fixed decisions

This report preserves these approved constraints:

- immutable plan before apply;
- main process refuses effective UID 0;
- privileged helper handles only a closed action vocabulary;
- no shell, arbitrary executable, arbitrary argv, downloaded rule, or caller-authorized root;
- fail closed when the required kernel, filesystem, mount, Trash, launcher, or validation property is unavailable;
- ext4, XFS, and Btrfs only for mutation; no crossing mount boundaries;
- same-UID peers are inside the caller's OS trust domain, while another UID, candidate contents, environment, mount layout, and concurrent managers are hostile.

## Linux syscall findings

### Byte paths

Linux pathname components are byte sequences. Slash separates components and NUL terminates a syscall pathname; UTF-8 validity is not a kernel invariant. Go strings can hold arbitrary bytes, but JSON text and UI rendering cannot be the authority. See [`path_resolution(7)`](https://man7.org/linux/man-pages/man7/path_resolution.7.html) and [`pathnames(7)`](https://man7.org/linux/man-pages/man7/pathnames.7.html).

Required `BytePath` contract:

- store a trusted-root ID plus ordered raw-byte components;
- reject NUL, slash within a component, empty components, `.` and `..`;
- never recover authority from an escaped display string;
- CBOR uses byte strings; JSON uses a display value plus base64 for exact bytes when UTF-8 round-trip is not exact;
- enforce the mounted filesystem's per-component limit at use time; do not assume a universal `PATH_MAX` as a storage contract;
- pass one component at a time after safe parent resolution where a mutating syscall has no resolution policy argument.

### `openat2`

`openat2` is the correct traversal primitive. The apply path requires all four flags: `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS`, `RESOLVE_NO_MAGICLINKS`, and `RESOLVE_NO_XDEV`. `NO_XDEV` also rejects bind-mount traversal. `BENEATH` rejects absolute paths and escapes through `..`; its current magic-link side effect must not replace explicit `NO_MAGICLINKS`. The kernel may return `EAGAIN` when it cannot prove a safe resolution during a concurrent rename; retry only a small bounded number, then classify drift. Unknown flags or an unsupported `open_how` layout fail with `EINVAL`/`E2BIG` and must disable mutation. See [`openat2(2)`](https://man7.org/linux/man-pages/man2/openat2.2.html).

Use `O_PATH|O_CLOEXEC` for identity handles and add `O_DIRECTORY` for directories. Resolve the target parent with `openat2`, keep that FD open, and validate the target as exactly one basename. Never pass a multi-component planned relative path to `renameat2` or `unlinkat`.

### `statx` and mount identity

Read identity from the opened FD with `statx(..., "", AT_EMPTY_PATH, mask, ...)`. The requested mask is a request, not a guarantee: inspect `stx_mask` before using every optional field. The stable comparison baseline is device major/minor, inode, file type, UID/GID, mode, link count where policy needs it, and action-specific size/time facts. Do not compare atime. Do not call the snapshot "complete": fields may be unsupported, and directory size/timestamps can legitimately drift. See [`statx(2)`](https://man7.org/linux/man-pages/man2/statx.2.html).

`STATX_MNT_ID` identifies a mount in the caller's mount namespace and corresponds to `/proc/self/mountinfo`; ordinary mount IDs can be reused after unmount. `STATX_MNT_ID_UNIQUE` is newer than the 5.15 minimum. Therefore:

- treat the planned mount ID as a drift signal, not a durable capability;
- reopen an action-kind-derived root at apply time and compare mount ID, device, inode, filesystem type, and the expected `/proc/self/mountinfo` record;
- reject an unexpected mount namespace or supported-filesystem mismatch;
- hold the validated root FD for the entire action so later operations remain anchored even if the pathname is renamed or detached;
- use `RESOLVE_NO_XDEV` for all descendant opens;
- use `STATX_MNT_ID_UNIQUE` only as an optional strengthening on kernels that return it.

Mount-info semantics are documented by [`proc_pid_mountinfo(5)`](https://man7.org/linux/man-pages/man5/proc_pid_mountinfo.5.html). No combination of a stored 5.15 mount ID and pathname proves persistent mount identity across separate runs.

### `renameat2`

`RENAME_NOREPLACE` atomically refuses an existing destination and cross-filesystem rename fails with `EXDEV`. It does not accept `openat2` resolution flags and does not mean "rename the inode I previously opened." See [`rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html).

Safe use is narrowly defined:

- old directory FD is the already-open target parent;
- old name is one validated basename;
- new directory FD is an already-open private staging/Trash directory on the same validated mount;
- new name is an executor-generated collision-resistant token, reserved with no-replace behavior;
- after rename, reopen the staged name beneath the staging FD and compare its FD snapshot to the planned identity;
- on mismatch, never unlink it; restore only with `RENAME_NOREPLACE` into the still-open original parent, otherwise retain and report a recovery handle.

This can transiently move a raced-in object before detecting the mismatch. Linux exposes no rename-by-open-inode primitive. Parent permissions and private staging prevent another UID from arranging that race in supported roots; a malicious same-UID peer can still cause disruption and is outside the isolation guarantee.

### `unlinkat`

`unlinkat(parent_fd, basename, 0)` removes a non-directory name and `AT_REMOVEDIR` removes an empty directory. It has no expected-inode parameter and no general `AT_EMPTY_PATH` form for unlinking an already-open object. See [`unlinkat(2)`](https://man7.org/linux/man-pages/man2/unlinkat.2.html).

Consequences:

- irreversible unlink happens only inside validated private staging;
- recursive removal walks through directory FDs, never reconstructed absolute paths;
- every child is opened without symlink or mount traversal, classified, and unlinked by one basename;
- symlinks, device nodes, sockets, FIFOs, and unexpected types fail unless an action kind explicitly permits removing the symlink entry itself; generic cleanup permits none of these;
- a hard link is an entry, not unique storage. Recheck link count and never report its allocated bytes as freed merely because one link was removed;
- restoration and cleanup use no-replace semantics and record retained quarantine on collision.

### What Linux cannot guarantee

Linux cannot atomically assert that a name still denotes a previously opened inode and then unlink or rename that name. It cannot provide a durable globally unique mount identity with the 5.15 `STATX_MNT_ID`. Two directories cannot be atomically updated together to create a crash-proof Trash file/metadata pair. A same-UID peer can ptrace the main process where allowed, alter caller-owned staging, mutate writable opened files, or race names; LDC cannot isolate that peer without changing the host security model.

The enforceable claim is narrower: an untrusted tree cannot cause LDC traversal outside the held trusted-root/staging FDs; a raced final entry is never permanently deleted unless its post-stage identity satisfies the action's required snapshot; privileged deletion occurs only inside helper-owned staging; and every inability to establish these facts returns drift/unsupported instead of weakening resolution.

## Point-of-use mutation algorithm

Each filesystem action uses this state machine:

1. **Derive root:** map `action_kind + provider_id + root_id` through a compiled registry. Never accept an absolute root from the request.
2. **Lease root:** open the fixed root descriptor-safely; inspect `statx`, `statfs`, mount namespace, ownership, mode, and mountinfo; retain the FD.
3. **Resolve parent:** use `openat2` with all required resolve flags. Reject unsupported filesystems, ACL/mode conditions that give an untrusted UID mutation authority, and mount crossing.
4. **Snapshot target:** open one basename with `O_PATH|O_NOFOLLOW`; call `statx(AT_EMPTY_PATH)` and compare only the required action-specific fields whose mask bits are present. Revalidate provider evidence.
5. **Lease staging:** open/create a 0700 executor-controlled directory on the same mount. Privileged staging is root-owned; unprivileged staging is caller-owned and inherits the stated same-UID limitation.
6. **Stage:** `renameat2(parent_fd, basename, stage_fd, token, RENAME_NOREPLACE)`. `EXDEV`, collision, or any unexpected error fails closed.
7. **Verify staged identity:** reopen `token`, snapshot by FD, and compare. A mismatch is `drifted_retained` or a no-replace restoration; never delete it.
8. **Apply inside staging:** Trash stops after metadata/move completion. Permanent removal performs an FD-anchored walk inside staging. Manager-owned data is not handled by this path.
9. **Verify:** check expected absence/presence, retained handles, and actual allocated-space delta where meaningful.
10. **Durability and result:** `fsync` written metadata and affected directories where the product promises crash recovery. A successful syscall without required sync is not a durable success; see [`fsync(2)`](https://man7.org/linux/man-pages/man2/fsync.2.html).

The plan must define which snapshot fields are hard preconditions per action. A single "compare everything" helper would cause false drift and invite later weakening.

## Trash and quarantine

The [Freedesktop Trash specification](https://specifications.freedesktop.org/trash/latest/) is the authority for user Trash behavior.

Required behavior:

- use `$XDG_DATA_HOME/Trash` only for objects on the home-trash filesystem;
- otherwise use an eligible top-directory Trash (`.Trash/$uid` with required sticky/ownership checks, or `.Trash-$uid`) on the object's filesystem;
- validate every Trash component as a real directory, owned and permissioned as the specification requires, without following symlinks;
- refuse if a compliant same-filesystem Trash cannot be established; never copy-then-delete and never silently convert to permanent removal;
- allocate a collision-free shared token for `files/<token>` and `info/<token>.trashinfo`;
- create and durably publish `.trashinfo` before moving the file, remove it if the move fails, and tolerate/reconcile crash-left metadata without deleting user content;
- encode `Path=` by percent-encoding the original raw pathname bytes; use an absolute original path for home Trash and a path relative to the top directory for top-directory Trash;
- emit the required deletion date; reject unrepresentable or ambiguous metadata rather than normalizing the filename;
- record original `BytePath`, Trash root identity, token, and deletion date as the restore handle;
- restore only after anchored destination-parent validation and with no-replace behavior.

The specification does not make `files/` plus `info/` one atomic transaction. LDC can promise ordered, synced operations and recoverable orphan handling, not atomic pair creation.

Quarantine is not Trash. It must be per-mount, private, excluded from discovery, retention-bounded, and visible in history. Retained quarantine is not freed space and is not called reversible unless the original destination can be safely reconstructed. If no safe same-mount quarantine exists, permanent deletion is unsupported for that root.

## Privileged helper design

### Lifecycle and caller identity

1. Main builds the complete plan, displays the exact privileged subset, and receives explicit apply confirmation.
2. Main invokes an absolute launcher and absolute helper directly, never through a shell.
3. `pkexec` mode uses the packaged polkit action for `/usr/libexec/linux-deep-clean/helper`; sudo fallback executes that exact helper through the user's existing sudo policy and adds no `NOPASSWD` rule.
4. Helper first requires effective UID 0, verifies its installed executable is the expected root-owned, non-group/world-writable regular file, sets umask 077 and a fixed working directory, and rejects ambiguous launcher markers.
5. In polkit mode, caller UID comes from the decimal `PKEXEC_UID` set by `pkexec`. In sudo mode it comes from decimal `SUDO_UID`; `SUDO_GID` may supply the launch GID, while `SUDO_USER` is display-only. Request UID, `HOME`, `USER`, and path text are never identity authority.
6. Helper resolves passwd/group facts from the numeric UID, compares the request's caller echo, and derives any user-scoped root from this trusted UID plus compiled policy.
7. Helper reads exactly one bounded request, independently validates every action, executes the dependency groups, writes exactly one bounded result, and exits.

`pkexec` deliberately runs programs in a minimal environment and sets `PKEXEC_UID`; it requires an absolute program path and authorizes an action associated with that executable. See [`pkexec(1)`](https://polkit.pages.freedesktop.org/polkit/pkexec.1.html). Sudo documents `SUDO_UID`, `SUDO_GID`, and `SUDO_USER` as variables set for the invoking user; see [`sudo(8)`](https://www.sudo.ws/docs/man/sudo.man/).

These variables are trustworthy only because a non-root direct invocation cannot make the non-setuid helper root. A hostile root can spoof them and is out of scope. Do not add fragile mandatory parent-PID checks: sudo may use a monitor process, PIDs race, and `/proc` ancestry is not an authorization channel.

### Polkit prompt correction

Stock `pkexec` authorizes the helper executable before the helper decodes stdin. Its action message is policy metadata, not a trusted rendering of the later CBOR request. Therefore the approved statement that "the authorization prompt lists the privileged action count and categories" is infeasible with the selected stock mechanism.

Minimum correction: LDC's unprivileged confirmation screen lists the plan digest, privileged count, categories, and risk; the polkit prompt uses a static message such as "Authorize the LDC privileged helper." Do not put action data in argv merely to influence the prompt. A genuinely dynamic trusted polkit prompt would require a separately designed D-Bus authorization client/details contract and is YAGNI for v1.

### Canonical CBOR profile

Use [RFC 8949 deterministic CBOR](https://www.rfc-editor.org/rfc/rfc8949.html) as a strict application profile, not merely a library's default mode.

Protocol limits to name in the plan:

| Property | Required limit/behavior |
|---|---|
| Framing | fixed magic and protocol version, unsigned 32-bit big-endian length, exactly one request/response |
| Frame size | 4 MiB request and 4 MiB response maximum before allocation |
| Nesting | maximum 16 |
| Actions | maximum 1,024 privileged actions per request |
| Maps/arrays | maximum 128 map pairs and 2,048 array elements per container |
| Byte/text values | maximum 1 MiB each; tighter field-specific limits; aggregate remains within frame |
| Paths | byte-string components only; maximum 1,024 components and 256 KiB encoded path per action, also checked against filesystem limits |
| CBOR forms | reject indefinite lengths, duplicate keys, tags, floats, bignums, invalid UTF-8 text, non-minimal integers, and trailing bytes |
| Schemas | reject unsupported versions and unknown fields; version additions require explicit decoder changes |
| Canonicality | decode, validate, canonical re-encode, and require byte equality before digest use |

Hash a domain-separated canonical plan body excluding its digest field, using a fixed algorithm named by protocol version. The digest binds preview/history/request bytes and detects accidental drift. It is not a MAC or authorization token: a compromised main process can recompute it. The helper's compiled validators remain the authority.

### Executable and environment sanitation

Every external operation maps one action enum to one compiled executable path and argument template. Before exec:

- validate the executable path/root ownership and absence of group/world write permission;
- build argv from scratch; argv element zero and every flag are fixed;
- validate manager IDs against manager-specific grammar and installed database state; reject leading hyphens and use a documented `--` delimiter where supported;
- call exec directly, never `/bin/sh`, `env`, or a PATH lookup;
- construct a fresh environment such as `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `LC_ALL=C`, `LANG=C`, and fixed root identity variables only when required;
- inherit no `LD_*`, language runtime, proxy, Git, SSH, XDG, desktop-bus, pager/editor, or caller configuration variables;
- close all non-protocol descriptors, set `CLOEXEC`, fixed cwd `/`, umask 077, bounded output capture, context deadline, and conservative resource limits;
- never return unbounded/raw manager output; retain bounded redacted diagnostics plus hashes/lengths for audit.

`execve` receives argv and envp exactly as supplied and performs no shell parsing; see [`execve(2)`](https://man7.org/linux/man-pages/man2/execve.2.html).

### Fixed action validation

The helper must reject user-owned Trash and ordinary user cleanup; those stay in the unprivileged process. Each privileged validator must independently enforce:

- action kind is compiled and allowed on the probed distro/release/filesystem;
- root/provider IDs map to compiled authority; no request absolute path;
- caller UID is allowed for the action and target ownership matches policy;
- plan preconditions and action-specific numeric bounds still hold;
- package/service object ID is exact, installed, and not essential/protected;
- manager lock/state and a fresh simulation produce the same typed transaction graph, versions, dependency consequences, scope, and network disclosure as the plan;
- fixed executable/arguments correspond exactly to that graph;
- fingerprint actions use only the distro-supported profile manager and preserve a verified password path;
- completion/system-file destinations are exact packaged allowlist locations;
- any state difference returns drift and requires a new plan.

Package manager maintainer scripts are an inherent effect of an approved native transaction, not arbitrary LDC commands. LDC must surface that manager-owned boundary and must not claim transactionality or rollback.

### Result model

Each action or manager transaction group returns:

- request ID, plan digest, action ID/kind, helper/protocol version;
- terminal status: `success`, `skipped`, `drifted`, `denied`, `unsupported`, `failed`, `interrupted`, or `indeterminate`;
- stable error category plus bounded/redacted detail;
- attempted flag, start/end times, manager exit/signal/timeout facts;
- expected and verified postcondition, verified apparent/allocated effect, and any Trash/quarantine handle;
- `reconciliation_required` with a typed probe when final state is not known.

`indeterminate` is mandatory. If a manager was interrupted, the helper died after dispatch, stdout framing failed, or the main process lost the response, LDC cannot safely infer failure or success. History records attempted/unknown, exit summary is partial, and the next operation must reconcile manager/filesystem state before reapply.

## Interfaces the implementation plan must name

| Logical module | Critical contract/function names to preserve in the later plan |
|---|---|
| `pathbytes` | `BytePath`, component validation, display/base64 codec, Trash percent codec |
| `mounts` | `OpenTrustedRoot`, `RootLease`, `InspectMount`, `CheckSupportedFilesystem`, `CheckMountNamespace` |
| `linuxfs` | `ResolveParent`, `OpenTargetHandle`, `SnapshotFD`, `ComparePrecondition`, `StageNoReplace`, `RestoreNoReplace`, `RemoveStagedTree`, `VerifyPostcondition` |
| `trash` | `SelectTrashRoot`, `ValidateTrashLayout`, `ReserveTrashToken`, `WriteTrashInfoDurable`, `MoveToTrash`, `RestoreFromTrash`, orphan reconciliation |
| `quarantine` | `OpenPerMountQuarantine`, retention policy, retained-object recovery, discovery exclusion |
| `protocol` | framed reader/writer, strict canonical decoder/encoder, plan digest, size/depth/count budgets |
| `helperauth` | launcher mode detection, trusted numeric caller derivation, executable self-check, caller-root derivation |
| `helperpolicy` | action enum registry, per-action validator, root authority registry, support probe, dependency grouping |
| `safeexec` | executable verifier, fixed argv builder, fresh environment, FD closure, timeout/output limiter |
| `manager` | simulation normalizer, typed transaction graph comparison, lock/reprobe, post-transaction reconciliation |
| `results` | terminal state machine, `indeterminate`, reconciliation probe, exit-summary mapping, redacted diagnostics |

No provider or presenter may call `renameat2`, `unlinkat`, `execve`, or helper protocol primitives directly.

## TDD and validation design

### Contract-first unit tests

Write failing tests before each implementation slice:

- all raw-byte component round trips, including every non-NUL byte and invalid UTF-8;
- rejection of slash/NUL/dot/dot-dot and display-string reuse as authority;
- action-specific stat masks and comparisons; missing required fields fail closed;
- mount ID reuse model never treated as equality authority;
- target mutation API accepts a basename, not a relative path;
- post-stage mismatch can only restore-no-replace or retain;
- hard links never over-report freed bytes;
- protection lists and helper validators are monotonic: adding protection cannot add authority/actions;
- deterministic plan bytes/digest across map insertion order;
- every non-canonical/duplicate/over-budget CBOR form is rejected before action allocation;
- every action enum has exactly one validator, executor mapping, result postcondition, and negative arbitrary-argv test;
- interrupted/EOF helper outcomes become `indeterminate`, never `failed` or retryable success.

### Property and fuzz tests

- Property: decode/encode of any valid `BytePath` preserves exact bytes.
- Property: successful resolution remains beneath the held root and on its mount.
- Property: no request field can change executable path, fixed flags, root mapping, or environment keys.
- Property: canonical plan bytes are stable; one semantic field change changes the digest.
- Property: Trash token allocation never overwrites either member of an existing pair.
- Fuzz strict CBOR framing/decoder, BytePath/JSON codecs, Trash info parser/encoder, root/action registry lookup, manager simulation normalizers, and result/history recovery.
- Seed corpora include duplicate keys, truncated frames, 4 MiB boundaries, nesting bombs, huge counts, invalid UTF-8, NULs, option-looking IDs, and integer overflow values.

Fuzz success means no panic, unbounded allocation, authority expansion, or accepted non-canonical input—not merely parser survival.

### Adversarial filesystem scenarios

Run synchronized attacker/executor tests for:

- final symlink and intermediate symlink/magic-link insertion;
- target rename-away/replacement and parent rename;
- inode churn/reuse after plan and between snapshot/stage;
- bind mount insertion, lazy unmount/remount, root mount replacement, and nested mount;
- hard-link injection to an outside sentinel;
- mode, owner, ACL, type, link-count, size, and timestamp drift;
- special files, sockets, FIFOs, devices, immutable/append-only files, read-only mounts;
- staging/Trash token collision, hostile symlinked Trash directories, cross-device staging;
- invalid UTF-8 and maximum-length/deep paths;
- cancellation/crash before metadata, after metadata, after rename, during recursive removal, and during restore;
- another UID plus same-UID peer processes, including open directory/file descriptors held across staging.

Allowed outcomes are verified success for the planned identity, explicit drift, safe no-replace restoration, or retained quarantine. Never permit outside mutation, permanent deletion of a post-stage mismatch, silent cross-device copy/delete, or false verified savings.

### 10,000-swap release gate

Use a deterministic barrier-based race harness, not an unsynchronized stress loop:

1. Place immutable test sentinels outside the trusted root and record names, contents, metadata, and allocated blocks.
2. Prepare a planned target and synchronize the attacker at resolution, snapshot, rename, and staged-verification windows.
3. Rotate symlink, rename, inode-reuse, hard-link, bind-mount, unmount/remount, and parent-swap schedules with recorded random seeds.
4. Assert after every attempt that all outside sentinels and namespace entries are unchanged; a mismatched object is restored-no-replace or retained, never deleted.
5. Record kernel, architecture, filesystem, mount options, schedule, seed, result, and quarantine state for replay.

Gate definition: at least **10,000 completed mutation attempts per supported filesystem** (ext4, XFS, Btrfs; 30,000 total) in release qualification, with every race class represented and zero outside-root mutation. PR smoke runs at least 1,000 ext4 attempts. Nightly/release runs the full root-capable mount suite. A zero-escape run is necessary evidence, not a proof that races are impossible.

### Disposable VM and mount harness

- QEMU/KVM snapshot-backed VMs; never the developer host.
- Separate loopback images for ext4, XFS, and Btrfs plus unsupported FUSE/overlay/network/removable fixtures that must reject mutation.
- Private mount namespaces with root-only harness control for bind, remount, lazy-unmount, read-only, and mount-ID-reuse scenarios.
- Three actors: unprivileged LDC caller, different hostile UID, and root harness; same-UID attacker is a separate process under the caller UID.
- Real packaged helper, polkit policy, sudo path, filesystem binaries, and production protocol; no test-only helper authority.
- Snapshot/reset after each destructive case; sentinel disk mounted separately and read-only where the scenario permits.
- Exercise denial/no-agent/wrong-password/caller-marker conflicts, malformed requests, helper kill, manager lock contention, and reboot/crash recovery.
- Run focused safety qualification on the minimum PR distro set and all supported distro/architecture VMs before release.

## Phase boundaries

1. **Contracts and threat model:** freeze `BytePath`, root authority, precondition masks, CBOR profile, action/result enums, same-UID boundary, and corrections in this report.
2. **Unprivileged filesystem core:** TDD `openat2`/`statx` leases, parent/basename API, mount checks, staging, FD-recursive deletion, and unit/fuzz tests. No provider mutation yet.
3. **Trash and quarantine:** implement spec-compliant same-mount Trash, durability/recovery, quarantine retention, and crash tests.
4. **Reject-only helper skeleton:** package launcher/policy, caller derivation, strict protocol, self/env/FD sanitation, and negative privilege tests before any action executor exists.
5. **One privileged action family at a time:** add validator, fixed executor, reprobe, result/reconciliation, VM tests, and review together. Package transactions come after bounded filesystem/system actions.
6. **Adversarial qualification:** full race, fuzz, interruption, unsupported-mount, polkit/sudo, and 10,000-per-filesystem gates.
7. **Provider integration:** only after safety APIs are stable; providers emit data and cannot bypass root/action registries.

Each phase must be independently fail-closed and reviewable. Do not defer negative tests until after provider breadth.

## Corrections to the approved design report

| Existing claim | Problem | Minimum correction |
|---|---|---|
| Compare the "complete" `statx` snapshot | Optional fields may be absent; mutable fields cause false drift | Compare a named action-specific required mask and reject missing required bits |
| Mount ID is part of target identity | 5.15 mount IDs may be reused | Treat as drift evidence; combine mountinfo/device/inode/fs type and hold the root FD throughout apply |
| Perform a collision-safe rename after safe reopen | `renameat2` performs its own pathname lookup and has no resolve flags | Resolve/open the parent safely, then rename exactly one basename to an open same-mount staging FD |
| Authorization prompt lists action count/categories | Stock `pkexec` authorizes before stdin request validation | LDC displays dynamic details; polkit prompt is static |
| Validate the plan digest | Digest is recomputable by a compromised main | Use it only for binding/audit; independently validate every field/action |
| Restore a staged mismatch when possible | Restoration can overwrite or race | Only `RENAME_NOREPLACE`; otherwise retain quarantine and return a handle |
| Trash metadata is written durably with the move | Trash metadata and file live in separate directories; no atomic pair | Define ordered fsync/recovery semantics and never claim pair atomicity |
| Results end in success/skipped/drifted/denied/unsupported/failed | Helper/manager interruption can leave unknown effects | Add `interrupted`, `indeterminate`, and `reconciliation_required` |

These corrections narrow claims; they do not change plan-first, unprivileged-main, fail-closed, or no-arbitrary-command decisions.

## Primary sources

- Linux man-pages: [`openat2(2)`](https://man7.org/linux/man-pages/man2/openat2.2.html), [`statx(2)`](https://man7.org/linux/man-pages/man2/statx.2.html), [`rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html), [`unlinkat(2)`](https://man7.org/linux/man-pages/man2/unlinkat.2.html), [`path_resolution(7)`](https://man7.org/linux/man-pages/man7/path_resolution.7.html), [`proc_pid_mountinfo(5)`](https://man7.org/linux/man-pages/man5/proc_pid_mountinfo.5.html), [`fsync(2)`](https://man7.org/linux/man-pages/man2/fsync.2.html), [`execve(2)`](https://man7.org/linux/man-pages/man2/execve.2.html).
- Freedesktop.org: [Trash specification](https://specifications.freedesktop.org/trash/latest/), [`pkexec(1)`](https://polkit.pages.freedesktop.org/polkit/pkexec.1.html), [polkit reference](https://polkit.pages.freedesktop.org/polkit/polkit.8.html).
- Sudo project: [`sudo(8)`](https://www.sudo.ws/docs/man/sudo.man/) and [`sudoers(5)`](https://www.sudo.ws/docs/man/sudoers.man/).
- IETF: [RFC 8949, Concise Binary Object Representation](https://www.rfc-editor.org/rfc/rfc8949.html).

## Actionable next steps

1. Amend the later phase files—not the approved product decisions—to name the interfaces, CBOR budgets, state machine, and four critical corrections above.
2. Make the reject-only helper and point-of-use race tests prerequisites for adding any privileged action.
3. Require a security review at each new action enum; adding an enum expands root authority.
4. Run the disposable mount harness before claiming any filesystem is mutation-supported.

## Unresolved questions

- Exact per-action `statx` required masks must be selected with each provider/action contract.
- Exact per-mount quarantine locations require a write/ownership survey on every supported distro layout; absence must disable irreversible deletion for that root.
- The safe fingerprint mutation matrix remains a separate distro-VM research item.
