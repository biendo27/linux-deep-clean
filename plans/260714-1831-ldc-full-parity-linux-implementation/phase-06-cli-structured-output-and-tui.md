---
phase: 6
title: "CLI, Structured Output, and TUI"
status: pending
priority: P1
effort: "14d"
dependencies: [5]
---

# Phase 6: CLI, Structured Output, and TUI

## Overview

Freeze the complete `ldclean` command and automation contract, implement human/JSON/NDJSON/no-TTY presenters, and only then add the Bubble Tea v2/Lip Gloss v2 TUI over the same application events. This phase changes presentation only: no command gains a private discovery path or mutation authority.

Commands whose backend arrives in a later phase must still exist, return an honest capability-unavailable result, and exercise the same output/exit mapping. Never add an `ldc` executable or alias.

## Requirements

### Exact public command surface

| Invocation | Alias | Phase 6 behavior |
|---|---|---|
| `ldclean` | none | With interactive stdin/stdout: capability-aware menu. Otherwise: help to stdout, no prompt/mutation, exit 0. |
| `ldclean clean` | none | Inventory/preview only until Phase 7/9 executors are registered. Trash usage is informational and Trash is never emptied. |
| `ldclean uninstall` | none | Exact installed-object and separately evidenced residue preview; later manager action remains unavailable. |
| `ldclean optimize` | `ldclean optimise` | Same command identity/output schema; conservative proposals only, later behavior unavailable until Phase 10. |
| `ldclean analyze` | `ldclean analyse` | Explicit local-root disk inventory/explorer; selection preview only in this phase. |
| `ldclean status` | none | Single snapshot or live TUI; non-TTY stdout defaults to one JSON snapshot unless `--human`. |
| `ldclean history` | none | Query Phase 4 history; a corrupt tail is reported and may expose a separately selectable repair proposal through the same plan/apply flow, never an implicit repair or new subcommand. |
| `ldclean purge` | none | Evidenced project-artifact preview; no basename-only candidates. |
| `ldclean installer` | none | Evidenced installer preview under approved roots. |
| `ldclean fingerprint` | none | Capability/status or actionable unsupported guidance; no PAM change in this phase. |
| `ldclean completion <bash\|zsh\|fish>` | none | Deterministic completion text to stdout. Installation is a separately previewed apply action, not an implicit write. |
| `ldclean update` | none | Installation-channel status/preview; no self-replacement and no network under dry-run. |
| `ldclean remove` | none | Native-package or archive guidance preview; user state remains preserved by default. |
| `ldclean --whitelist` | none | Compatibility-facing route to protection-list inspection/management; all user-facing prose says “protection list.” It can only narrow candidates/actions. |
| `ldclean --help` / command `--help` | none | Stable offline help, exit 0. |
| `ldclean --version` | none | Stable build/version/schema line, offline, exit 0. |

Do not add `ldc`, hidden aliases, positional shell text, arbitrary paths for commands that require configured/typed roots, or unapproved maintenance commands.

### Options and interaction rules

- Global output/diagnostic flags: `--json`, `--ndjson`, `--human`, `--debug`, and `--no-color`. Output modes are mutually exclusive; an invalid combination exits 2 without invoking an application use case.
- Mutating-command flags: `--apply`, `--yes`, and `--dry-run` use the Phase 4 confirmation contract. `--yes` suppresses only the second confirmation and never implies `--apply`. `--dry-run` overrides `--apply` and guarantees no writes, authorization, or network.
- Structured output disables prompts, spinners, cursor control, color, progress bars, and decorative text. Valid documents/events go only to stdout; diagnostics go only to stderr.
- Non-interactive apply requires `--apply` and a fully resolvable typed selection. Missing/ambiguous selection exits 2 before authorization or mutation.
- TUI confirmation shows immutable plan digest, selected actions, provider guarantees, risk/reversibility, privileged subset, estimated apparent/allocated effects, and network disclosure. The model returns a typed confirmation response; it cannot edit an action.
- Capability absence stays visible with an exact reason. Do not hide menu items or fabricate success.
- Human path rendering is escaped and unambiguous. JSON uses the Phase 2 display/base64 representation; presenter code never decodes display text back into authority.
- Every JSON document and NDJSON event includes schema version, command, run ID, and stable type. Size fields preserve apparent/allocated/estimated/verified distinctions; unavailable metrics are `null` plus reason.
- NDJSON begins with a run/header event and ends with exactly one terminal event, including cancellation or partial failure. Broken-pipe handling cancels producers and does not write diagnostics to stdout.
- TUI supports arrows and Vim-style keys where meaningful, clear focus, help, cancellation, terminal resize, narrow terminals, no-color mode, and restoration of terminal state after success, error, panic boundary, or signal.

### Exit-status contract

| Code | Mapping |
|---:|---|
| 0 | All requested actions terminal-successful or explicitly skipped; informational commands and preview complete successfully. |
| 1 | General discovery, presentation, execution, output, or internal error not covered below. |
| 2 | Invalid arguments, conflicting flags, invalid structured request, or unresolved non-interactive selection. |
| 3 | Immutable plan expired because state/config/capability/preconditions drifted; regenerate. |
| 4 | Partial result: at least one action failed/indeterminate after another completed; action results remain authoritative. |
| 5 | Authorization denied/unavailable, main binary invoked with effective UID 0, or required privilege channel unavailable. |
| 6 | Platform/provider/capability unsupported for the requested operation. |

The application result determines the code through one total mapping function. Cobra/parser errors must use code 2, not Cobra's default ad hoc code. Alias spelling does not alter command identity in JSON/history.

### Performance and quality

- `--help` and `--version` p95 are at most 150 ms on the documented reference machine, offline and without config/provider scans.
- TUI first paint p95 is at most 300 ms; slow capability/provider work begins after an immediately renderable model exists.
- Cancellation reaches application quiescence within one second, excluding an already-running non-interruptible manager transaction in later phases.
- At least 85% statement coverage for JSON/NDJSON/human/exit-map packages and 80% for behavior-bearing TUI packages. Existing 90% domain/engine and 85% provider gates remain green.

## Architecture

```text
cmd/ldclean
  -> Cobra syntax + option validation
  -> application.UseCases (Phase 4)
  -> application.Event / domain result stream
  -> one presenter selected before use-case invocation
       human | JSON document | NDJSON stream | Bubble Tea TUI
  -> one exit-summary mapper
```

Implementation order is a dependency boundary, not a preference:

1. Freeze command/alias/flag/exit grammar in black-box tests.
2. Implement JSON and NDJSON schemas/goldens.
3. Implement deterministic no-TTY and plain-human behavior.
4. Implement completion/help/version fast paths.
5. Build the TUI exclusively from the same application/domain types.

Presenter selection happens before provider work. `--help` and `--version` bypass configuration, capability, provider, state migration, and TUI initialization. No-TTY decisions use injected terminal facts for deterministic tests.

## Related Code Files

### Create

- `internal/presenters/cli/commands.go` — exact command/alias registration and application use-case routing.
- `internal/presenters/cli/options.go`, `internal/presenters/cli/exit.go`, `internal/presenters/cli/terminal.go` — output selection, total exit map, injected TTY facts.
- `internal/presenters/cli/commands_test.go`, `internal/presenters/cli/exit_test.go`.
- `internal/presenters/json/document.go`, `internal/presenters/json/document_test.go` — one-document renderer.
- `internal/presenters/ndjson/stream.go`, `internal/presenters/ndjson/stream_test.go` — streaming event renderer and terminal-event guarantee.
- `internal/presenters/human/preview.go`, `internal/presenters/human/status.go`, `internal/presenters/human/path.go`, `internal/presenters/human/human_test.go`.
- `internal/presenters/tui/program.go`, `internal/presenters/tui/menu.go`, `internal/presenters/tui/selector.go`, `internal/presenters/tui/analyze.go`, `internal/presenters/tui/status.go`, `internal/presenters/tui/progress.go`, `internal/presenters/tui/confirm.go`, `internal/presenters/tui/styles.go`.
- `internal/presenters/tui/program_test.go`, `internal/presenters/tui/navigation_test.go`, `internal/presenters/tui/render_test.go`.
- `tests/contract/command_surface_test.go`, `tests/contract/structured_output_v1_test.go`, `tests/contract/no_tty_test.go`, `tests/contract/completion_test.go`.
- `tests/contract/golden/help/root.txt`, `tests/contract/golden/help/commands.txt`, `tests/contract/golden/version.txt`.
- `tests/contract/golden/json/{inventory,status,plan,result,error}.json`.
- `tests/contract/golden/ndjson/{inventory,status,plan,result,cancelled}.ndjson`.
- `tests/integration/cli/pty_test.go`, `tests/integration/cli/broken_pipe_test.go`.
- `tests/performance/presenter_benchmark_test.go` — help/version and first-paint p95 harness.

### Modify

- `cmd/ldclean/main.go` — call the injected root constructor and return its stable exit code; retain the effective-UID-0 refusal.
- `internal/presenters/cli/root.go`, `internal/presenters/cli/root_test.go` — extend the Phase 1 bootstrap root into the frozen full command tree and option-validation contract.
- `internal/application/events.go` — add only presenter-neutral event variants proven necessary by JSON/NDJSON/TUI contract tests.
- `internal/application/confirmation.go` — carry plan digest, disclosure, and typed response without terminal/UI types.

### Delete

- None.

## Interfaces and Dependency Boundaries

```go
type UseCases interface {
    Inventory(context.Context, application.InventoryRequest, application.EventSink) application.Summary
    CreatePlan(context.Context, application.PlanRequest, application.EventSink) application.Summary
    ApplyPlan(context.Context, application.ApplyRequest, application.Confirmation, application.EventSink) application.Summary
    QueryHistory(context.Context, application.HistoryRequest, application.EventSink) application.Summary
    Reconcile(context.Context, application.ReconcileRequest, application.EventSink) application.Summary
}

type Presenter interface {
    Present(context.Context, <-chan application.Event) error
}

func ExitCode(application.Summary) int
```

- Every package under `internal/presenters/**` may import only the Go standard library, its assigned UI dependency (Cobra or Bubble Tea/Lip Gloss), `internal/application`, and dependency-free `internal/domain`.
- Architecture tests reject imports from presenters to providers, capability, engine, linuxfs, managerexec, privilege, metrics adapters, or state implementations.
- Application/domain packages never import Cobra, Bubble Tea, Lip Gloss, ANSI helpers, JSON presenter types, or terminal detection.
- The TUI sends typed application requests and confirmation values. It does not call provider methods, recalculate totals, reclassify risk, write state, or execute actions.
- JSON and NDJSON encoders share domain DTO mapping but not framing. JSON is one valid document; NDJSON is individually valid versioned objects, one per line.
- Completion generation is driven by the same Cobra tree and is deterministic. Completion installation is merely a typed proposed action until a later executor exists.

## TDD Test Strategy

### RED — automation contract before UI

1. Add `TestExactCommandSurface` to assert the table above, only `optimise`/`analyse` aliases, executable name `ldclean`, and absence of `ldc`.
2. Add table-driven `TestExitCode_TotalMapping` covering every terminal action state, partial/indeterminate sets, drift, denial, unsupported, invalid arguments, and cancellation.
3. Add golden tests for root/command help, version, completion, one JSON document, and NDJSON ordering. Validate JSON with Phase 2 schemas and decode every NDJSON line independently.
4. Add no-TTY cases for all four stdin/stdout combinations:
   - no command without interactive stdin+stdout prints help and exits 0;
   - `status` with non-TTY stdout emits one JSON snapshot unless `--human`;
   - structured output never prompts;
   - non-interactive apply without `--apply` and resolved selection exits 2 before use-case apply.
5. Add `TestDryRunSuppressesWriteAuthAndNetworkPorts`, `TestYesNeverImpliesApply`, and conflicting output-flag tests.
6. Add `TestPresenterImportsOnlyApplicationAndDomain` using `go list -deps`.
7. Add PTY model tests for first paint, resize, narrow width, focus, arrows/Vim keys, cancellation, no color, unavailable capability, confirmation disclosure, and terminal restoration.
8. Add latency benchmarks before TUI initialization is implemented.

### GREEN — structured and plain paths first

1. Build the Cobra tree with injected streams, terminal facts, build info, use cases, and clock. Turn off Cobra's uncontrolled usage/error printing.
2. Implement one total error/result-to-exit mapper and route every command/alias through it.
3. Implement schema-versioned JSON and NDJSON renderers, stderr diagnostics, broken-pipe cancellation, then plain human output.
4. Implement no-TTY selection, root/no-command behavior, `status` auto-JSON, completion, and help/version fast paths.
5. Only after all black-box contracts pass, build Bubble Tea models over recorded application event sequences. Use Lip Gloss only for rendering.

### REFACTOR — protect presenter neutrality

1. Extract shared DTO/path/size formatting only when human and structured tests demonstrate identical semantics; never share terminal decoration with structured output.
2. Reduce first-paint work until the 300 ms p95 gate passes; keep provider discovery asynchronous and cancellable.
3. Consolidate TUI submodels without moving policy or action editing into the presenter.
4. Re-run golden, PTY, import, race, coverage, and performance gates after every renderer refactor.

### Test matrix

| Axis | Cases |
|---|---|
| Output | human, JSON, NDJSON, TUI, auto-JSON status |
| Terminal | both TTY, stdin only, stdout only, neither, resize, narrow, `TERM=dumb`, no color |
| Completion | Bash, Zsh, Fish; stdout purity; deterministic bytes |
| Result | success, skipped, failed, drifted, partial, denied, unsupported, interrupted, indeterminate/reconciliation required |
| Paths/metrics | UTF-8, invalid UTF-8/base64, control bytes, huge sizes, unavailable/null metrics |
| Control flow | cancellation, SIGINT, broken pipe, slow producer, capability absent, corrupt history tail |

### Commands and gates

```bash
go test ./internal/presenters/... ./tests/contract/...
go test -race ./internal/presenters/... ./internal/application/...
go test -tags=integration ./tests/integration/cli/...
go test -run '^$' -bench 'Benchmark(Help|Version|TUIFirstPaint)' -count=20 ./tests/performance/...
go test -coverprofile=coverage.out ./internal/presenters/...
go tool cover -func=coverage.out
```

The default suite is hermetic, offline, unprivileged, and uses in-memory application event streams. Any VM terminal/package smoke test requires `-tags=vmtest`, `LDCLEAN_VMTEST=1`, `LDCLEAN_VMTEST_TOKEN`, and a root-owned non-symlink, non-group/world-writable `/run/linux-deep-clean/disposable-guest` whose content exactly matches the token.

## Implementation Steps

1. Freeze command names, aliases, flags, no-TTY truth table, structured schemas, and total exit mapping in contract tests.
2. Implement a dependency-injected Cobra root that never initializes providers for help/version/completion generation.
3. Implement JSON document and NDJSON event presenters with stdout/stderr separation, path byte encoding, null metrics, and terminal events.
4. Implement plain-human preview/status/history and deterministic no-TTY routing.
5. Generate Bash/Zsh/Fish completions from the frozen tree; keep installation as a plan-only capability.
6. Add architecture import tests before adding Bubble Tea/Lip Gloss dependencies.
7. Implement an immediately renderable capability-aware menu, then selector, analyze, status, progress, and confirmation submodels over application events.
8. Add cancellation/signal/broken-pipe handling and terminal restoration. Confirm already-completed application events remain recordable.
9. Tune startup/first paint and run golden, PTY, race, coverage, and performance gates.

## Success Criteria

- [ ] The executable and every document say `ldclean`; no `ldc` binary, alias, completion, or help entry exists.
- [ ] Every approved command is registered; only `optimise` and `analyse` are command aliases.
- [ ] Exit codes 0–6 are total, stable, and contract-tested against action-level results.
- [ ] JSON is one versioned document; NDJSON is a valid ordered event stream with exactly one terminal event.
- [ ] Structured stdout contains no prompt, ANSI, spinner, log, or diagnostic bytes.
- [ ] No-command/no-TTY, status auto-JSON, conflicting flags, unresolved selection, `--yes`, and dry-run semantics pass black-box tests.
- [ ] Help/version p95 is at most 150 ms and TUI first paint p95 is at most 300 ms.
- [ ] TUI keyboard, resize, cancellation, no-color, unavailable-capability, and terminal-restoration tests pass.
- [ ] Presenters import only application/domain plus their UI/standard-library dependencies.
- [ ] Structured/human/exit packages meet 85% coverage; TUI behavior meets 80%; earlier gates remain green.
- [ ] Phase 6 introduces no mutation, authorization prompt, or network request.

## Risk Assessment and Rollback

| Risk | Mitigation | Rollback |
|---|---|---|
| TUI becomes authoritative | Import allowlist; event-sequence tests; typed confirmation only | Disable TUI entry and retain CLI/structured paths |
| Structured output is polluted | Dedicated stdout writer, stderr diagnostics, golden byte tests | Revert renderer; schemas/goldens remain contract authority |
| Cobra defaults destabilize usage/exit codes | Disable automatic printing; one root constructor and total exit map | Pin/revert Cobra adapter without changing application contracts |
| Slow startup/first paint | Help/version fast path; lazy capability work; repeated p95 benchmarks | Ship CLI/structured presentation while TUI remains unavailable |
| Alias or schema drift breaks automation | Golden command-tree/schema tests and explicit versioning | Restore prior alias/schema; add a new schema version only when intentionally accepted |

Rollback removes or disables a presenter only. It must not alter provider facts, immutable plans, history, or schemas already advertised without a versioned compatibility decision.

## Phase Exit/Handoff

Phase 7 receives a frozen confirmation/apply interaction, output schemas, no-TTY rules, and exit-code mapping. It registers user-owned executors through Phase 4; it must not add presenter-specific apply behavior. Phase 8 may reuse the privileged-subset confirmation view, but the dynamic count/categories remain in unprivileged LDC output because stock `pkexec` provides only a static trusted prompt.
