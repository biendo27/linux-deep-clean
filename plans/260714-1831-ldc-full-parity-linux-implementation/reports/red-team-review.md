---
type: red-team-review
date: 2026-07-14
status: complete
tier: full
---

# LDC plan red-team review

## Method

The controller applied all four Full-tier lenses: Security Adversary/Fact Checker, Failure Mode Analyst/Flow Tracer, Assumption Destroyer/Scope Auditor, and Scope & Complexity Critic/Contract Verifier. Parallel hostile reviewers were attempted, but the review agents exhausted their usage allowance before producing valid evidence; their unsupported output was discarded. The findings below come from a fresh line-evidenced read of `plan.md`, all eleven phase files, the approved design, and all three research reports. Evidence line numbers identify the pre-correction draft reviewed on 2026-07-14.

Planned source paths are intentionally absent in this greenfield repository and were classified as planned, not failed. Only contradictions, missing execution ownership, or infeasible release sequencing were accepted.

## Findings and adjudication

### Finding 1: Destructive VM authority has two incompatible sentinels — Critical

**Lens:** Security Adversary / Fact Checker
**Location:** Phases 1 and 5–11, VM guards
**Flaw:** Phase 1 freezes a per-run token-bound sentinel under `/run`, while later phases use a persistent `/etc` sentinel and omit the token from commands.
**Failure scenario:** A stale image marker or copied guest satisfies a weaker later-phase guard, allowing root/package/filesystem tests outside the intended disposable run.
**Evidence:** `phase-01-start.md:147` requires `/run/linux-deep-clean/disposable-guest` containing the matching run token; `phase-05-provider-discovery-and-read-only-inventory.md:195`, `phase-07-user-owned-mutation-and-installer-cleanup.md:185`, `phase-08-privilege-protocol-and-reject-only-helper.md:208`, `phase-09-privileged-package-and-system-mutation.md:47`, `phase-10-optimization-fingerprint-and-remaining-parity.md:56`, and `phase-11-packaging-release-and-vm-qualification.md:60` instead require `/etc/linux-deep-clean-disposable-guest`.
**Disposition:** Accept.
**Fix:** Use the Phase 1 `/run` path everywhere, require `LDCLEAN_VMTEST_TOKEN`, exact root ownership/type/mode/content, snapshot identity, and harness-created targets.

### Finding 2: Read-only providers cross the filesystem mutation boundary — High

**Lens:** Security Adversary / Contract Verifier
**Location:** Phases 3 and 5, provider scanner architecture
**Flaw:** Phase 3 forbids providers importing `mounts`/`linuxfs`, but Phase 5 says scanners receive a Phase 3 `RootLease` and reuse its descriptor scanner.
**Failure scenario:** Concrete provider packages become coupled to mutation-capable types, expanding their import authority and bypassing the promised leaf adapter boundary.
**Evidence:** `phase-03-linux-filesystem-safety-and-trash.md:78` forbids provider imports of mounts/linuxfs/trash/quarantine; `phase-05-provider-discovery-and-read-only-inventory.md:138` gives scanners a Phase 3 `RootLease`, and its implementation step 7/9 routes provider scanning through that implied dependency.
**Disposition:** Accept.
**Fix:** Add a read-only `providerapi.ReadTree` port and a separately audited `internal/discoveryfs` adapter that composes Phase 3 resolution without exposing leases or mutation methods to providers.

### Finding 3: Preview and helper disagree on transaction-graph ownership — High

**Lens:** Assumption Destroyer / Scope Auditor
**Location:** Phases 2, 5, and 9, manager transaction contract
**Flaw:** Phase 5 invents `PreviewGraph`; Phase 9 invents `managerexec.TransactionGraph` and its dependency map lets providers/application consume managerexec graph types, despite provider import prohibitions.
**Failure scenario:** Preview and helper normalize into different structures or providers import the privileged executor package, so graph equality is either impossible or circular.
**Evidence:** `phase-05-provider-discovery-and-read-only-inventory.md:62,137,199` defines/uses `PreviewGraph`; `phase-09-privileged-package-and-system-mutation.md:72,158,211` places `TransactionGraph` in the executor flow and states `providers/application -> ... managerexec graph types`; `phase-05-provider-discovery-and-read-only-inventory.md:133` forbids providers importing manager executors.
**Disposition:** Accept.
**Fix:** Freeze one dependency-free `domain.TransactionGraph` in Phase 2; providers produce it, the plan seals it, and managerexec only normalizes fresh output and compares that same type.

### Finding 4: Analyze mutation is claimed but never implemented — High

**Lens:** Failure Mode Analyst / Flow Tracer
**Location:** Phases 7 and 10, semantic parity
**Flaw:** Phase 7 explicitly defers `analyze` mutation, while Phase 10's ledger says Phase 7 already provides it and assigns no implementation work.
**Failure scenario:** The parity ledger passes on documentation while selected ordinary files in `analyze` remain preview-only.
**Evidence:** `phase-07-user-owned-mutation-and-installer-cleanup.md:16` defers analyze mutation; `phase-10-optimization-fingerprint-and-remaining-parity.md:92` attributes Trash-only analyze mutation to Phase 7; the approved design requires it at `plans/reports/260714-1754-ldc-linux-native-design-report.md:135,394`.
**Disposition:** Accept.
**Fix:** Add Phase 10 command-specific analyze admission and tests using the already qualified `trash_path` executor; ordinary selected files only, never directory trees or unsupported mounts.

### Finding 5: Completion installation has an enum but no executor — High

**Lens:** Contract Verifier
**Location:** Phases 2, 6, and 10
**Flaw:** `install_completion` is defined and the CLI promises user/system install, but no phase owns the unprivileged durable publication or typed helper action.
**Failure scenario:** Completion generation works, yet full-parity installation silently remains preview-only or is implemented later with arbitrary system paths.
**Evidence:** `phase-02-canonical-domain-and-plan-protocol.md:57` defines `install_completion`; `phase-06-cli-structured-output-and-tui.md:32` promises generation and separately previewed apply; the approved contract at `plans/reports/260714-1754-ldc-linux-native-design-report.md:141` requires user-owned install by default and typed helper action for system scope.
**Disposition:** Accept.
**Fix:** Add sealed user/system completion destinations, source/content binding, durable no-overwrite publication, rollback, helper validator, negative path/content tests, and VM qualification in Phase 10.

### Finding 6: State repair is planned but cannot reach apply — Medium

**Lens:** Failure Mode Analyst / Flow Tracer
**Location:** Phases 4–10, history recovery
**Flaw:** Phase 4 creates `repair_state` plans but no later executor registers that action.
**Failure scenario:** A corrupt history tail can be diagnosed forever but the advertised separate explicit repair plan is permanently unsupported.
**Evidence:** `phase-04-policy-engine-state-and-recovery.md:30,64` defines the preserving repair action and builder; `phase-07-user-owned-mutation-and-installer-cleanup.md:201` closes its executor set to three actions; Phase 10 has no repair file, test, or implementation step.
**Disposition:** Accept.
**Fix:** Add an unprivileged state-repair executor in Phase 10 that preserves original bytes, publishes a verified replacement through Phase 3 durable APIs, and remains separately selected/applied.

### Finding 7: Uninstall residues have discovery and ledger claims but no application flow — High

**Lens:** Failure Mode Analyst / Flow Tracer
**Location:** Phases 5, 7, 9, and 10, uninstall lifecycle
**Flaw:** Phase 5 discovers separately evidenced residues and Phase 10 claims parity, but Phase 7 excludes `uninstall` from user-owned mutation and Phase 9 only implements manager actions.
**Failure scenario:** The manager package is removed, while independently selectable attributable residue actions never execute or are incorrectly folded into the privileged manager transaction.
**Evidence:** `phase-05-provider-discovery-and-read-only-inventory.md:32` separates residue proposals; `phase-07-user-owned-mutation-and-installer-cleanup.md:14,201` enables only clean/purge/installer; `phase-10-optimization-fingerprint-and-remaining-parity.md:90` claims uninstall plus residues; approved design `plans/reports/260714-1754-ldc-linux-native-design-report.md:133,329` requires manager first and separate residue actions.
**Disposition:** Accept.
**Fix:** Add Phase 10 uninstall orchestration with exact action dependencies and existing userfs executors; residue selection stays independent and excludes application data.

### Finding 8: User-scoped Flatpak apply has no unprivileged executor — High

**Lens:** Contract Verifier
**Location:** Phase 9, Flatpak family
**Flaw:** The provider matrix says system scope uses the helper and user scope remains unprivileged, but all Phase 9 execution files are helper-side managerexec work and no main-process ActionExecutor is assigned.
**Failure scenario:** User Flatpaks either remain preview-only or are unnecessarily elevated through the helper, violating least privilege.
**Evidence:** `phase-09-privileged-package-and-system-mutation.md:41,81` requires user scope unprivileged; its related files and implementation steps contain no user-manager executor/composition path.
**Disposition:** Accept.
**Fix:** Add a closed unprivileged Flatpak ActionExecutor that reuses the typed spec/graph/reconciliation contracts without importing providers or exposing generic exec.

### Finding 9: Release signing order can mutate already-qualified artifacts — High

**Lens:** Failure Mode Analyst / Flow Tracer
**Location:** Phase 11, reproducibility and publication
**Flaw:** The plan qualifies/fixes digests and then signs, while also requiring package signature verification. An embedded package signature (notably RPM) changes the package bytes after qualification.
**Failure scenario:** Users receive signed bytes different from the VM-qualified candidate, or the release cannot meet its byte-for-byte rebuild claim because signatures are intentionally nondeterministic.
**Evidence:** `phase-11-packaging-release-and-vm-qualification.md:43-47` requires package signatures and signing after qualification; lines 91-100 describe qualification before signed publication; lines 276-279 require reproducible published artifacts and signatures.
**Disposition:** Accept.
**Fix:** Compare unsigned build payloads reproducibly, apply any byte-mutating format signature before freezing the release candidate, bind all later detached signatures/metadata to those final digests, and run every package/content/VM/repository gate on those exact final bytes.

### Finding 10: BytePath symbol is inconsistent — Medium

**Lens:** Fact Checker
**Location:** Phases 2 and 5
**Flaw:** Phase 5 refers to `domain.BytePath`, but Phase 2 defines `pathbytes.BytePath` embedded in domain filesystem targets.
**Failure scenario:** Implementation invents a duplicate path type or puts raw-path codecs into the wrong dependency layer.
**Evidence:** `phase-02-canonical-domain-and-plan-protocol.md:53,66-67` defines `pathbytes.BytePath` and import direction; `phase-05-provider-discovery-and-read-only-inventory.md:23` names `domain.BytePath`.
**Disposition:** Accept.
**Fix:** Use `pathbytes.BytePath` only inside typed domain targets and keep providers on domain target contracts.

## Resolution sweep

| Finding | Resolution evidence | Result |
|---:|---|---|
| 1 | Master plan and Phases 1, 3, 5–11 use the same token-bound `/run/linux-deep-clean/disposable-guest` contract. | Applied |
| 2 | Phase 5 adds `providerapi.ReadTree`; only `internal/discoveryfs` composes Phase 3 read resolution. | Applied |
| 3 | Phase 2 owns `domain.TransactionGraph`; Phases 5 and 9 consume it without defining another graph type. | Applied |
| 4 | Phase 10 owns explicit-file analyze admission over Phase 7 `trash_path`, with negative and integration tests. | Applied |
| 5 | Phase 10 owns bound user completion publication and the fixed system-helper completion slice. | Applied |
| 6 | Phase 10 owns explicit preserving state-repair apply; Phase 4 handoff names that boundary. | Applied |
| 7 | Phase 10 composes exact manager removal with independently selected attributed residues and excludes app data. | Applied |
| 8 | Phase 9 adds the closed unprivileged user-scope Flatpak executor; system scope alone reaches the helper. | Applied |
| 9 | Phase 11 signs byte-mutating formats before final freeze and binds all qualification to exact frozen bytes. | Applied |
| 10 | Phase 5 consistently uses `pathbytes.BytePath` inside typed domain targets. | Applied |

## Verification summary

- Tier: Full (11 phases; all four roles applied)
- Claims sampled: 184
- Verified as consistent after correction: 179
- Failed after correction: 0 (10 draft findings resolved above)
- Unverified implementation-time gates: 5 (permanent module path, TOML decoder, safe quarantine layout, signing custody, reference hardware)
- Rejected abstract/style findings: 7

The five unverified items are explicit entry gates and do not require a product decision before implementation begins. A mechanical search found no active use of the rejected sentinel, duplicate transaction-graph type, or `domain.BytePath`; the master plan and every affected phase were re-read after correction.

Status: DONE
Summary: Ten material cross-phase defects were accepted and corrected; no approved user decision was reversed.
Concerns/Blockers: Five named implementation entry gates remain intentionally unresolved and fail closed at their owning phases.
