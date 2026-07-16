# Phase 3 Filesystem Safety Foundation Report

Date: 2026-07-16

## Outcome

Phase 3 is in progress. Its descriptor-rooted safety foundation is implemented
and intentionally does not enable any user filesystem mutation. The remaining
Trash, quarantine, and VM gates are hard stops, not behavior deferred behind a
weaker fallback.

## Delivered foundation

- Added audited `golang.org/x/sys/unix` Linux bindings and architecture gates
  that confine raw filesystem syscalls to `internal/linuxfs` and mount
  qualification to `internal/mounts`.
- Added trusted-root leases that accept only engine/helper registry authority,
  retain qualified descriptors, compare complete mount records, and require
  fixed-local-device plus bind-free provenance attestations. A mount ID alone
  never authorizes a root; unstable mount/namespace reads and ambiguous
  full-root topology fail closed.
- Added raw-byte, held-descriptor parent resolution with the required
  `openat2` constraints, bounded retry, one-basename mutation boundaries,
  `statx` action masks, and target precondition comparison.
- Added opaque private staging qualification, no-replace staging and restore,
  post-move identity checks, required directory syncs, and typed retained or
  indeterminate state transitions.
- Added a capability-gated descriptor-relative, two-pass staged-tree removal
  core that rejects symlinks and special entries before generic cleanup and
  turns any uncertain post-effect result into reconciliation rather than a
  false success. The current staging authority deliberately grants no
  irreversible-removal capability because `unlinkat` cannot atomically bind a
  just-classified name against a same-UID replacement race.

## Validation evidence

The default lane uses test-owned temporary roots only. Before publication, the
following local checks passed:

- `GOTOOLCHAIN=local go test ./... -count=1`
- `GOTOOLCHAIN=local go test -race ./... -count=1`
- `GOTOOLCHAIN=local go vet ./...`
- `GOTOOLCHAIN=local go mod verify`, `make build`, and
  `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`
- `internal/linuxfs` statement coverage: 90.2%; `internal/mounts` statement
  coverage: 91.7%

An independent adversarial review found a same-UID name-replacement race in
the initial recursive-removal design. The final implementation gates all
production removal before the first unlink; a regression test proves that an
ordinary staged object remains retained and reports `unsupported`.

## Hard gates still open

- No engine/helper-owned per-mount layout authority has yet proven a private
  same-mount quarantine directory or a compliant Trash layout. The staging
  lease constructor is therefore internal and Trash/quarantine operations are
  unsupported rather than accepting an arbitrary path or falling back to
  permanent deletion. That authority must also prove an exclusive staged
  namespace before irreversible recursive removal can be enabled.
- No disposable supported VM has run the ext4/XFS/Btrfs mount and adversarial
  race campaigns. Default-lane tests cannot certify a production root or
  satisfy the required 1,000-attempt PR smoke gate.
- Durable Trash metadata pairing, recovery reconciliation, and quarantine
  retention require the missing layout authority and remain unimplemented.

## Handoff

Do not start Phase 4 while this phase remains gated. The next Phase 3 work
must introduce an engine/helper-owned layout authority with held private
directory descriptors, then implement and qualify the documented Trash and
quarantine ordering without giving providers, presenters, plans, or callers
absolute-path authority.
