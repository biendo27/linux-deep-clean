# Phase 3 Filesystem Safety Foundation Report

Date: 2026-07-17

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
- Added an engine/helper-owned root-plus-layout-kind registry contract. It
  requalifies the held root and fixed layout descriptor against complete mount
  evidence before issuing an opaque layout lease; a second mount is rejected.
  Only `linuxfs` can duplicate that lease, and each private operation
  requalifies the retained root/layout before acquiring a fresh descriptor.
  The matching private-directory lease supports bounded no-replace durable
  publication with preflight/file/directory syncs and a post-sync identity and
  byte recheck. A collision or any post-create ambiguity is retained for
  reconciliation rather than overwritten or reported as durable success.
- Added a capability-gated descriptor-relative, two-pass staged-tree removal
  core that rejects symlinks and special entries before generic cleanup and
  turns any uncertain post-effect result into reconciliation rather than a
  false success. The current staging authority deliberately grants no
  irreversible-removal capability because `unlinkat` cannot atomically bind a
  just-classified name against a same-UID replacement race.
- Added a bounded `.trashinfo` metadata profile with raw-byte percent
  encoding, local wall-clock deletion dates, and fail-closed parsing. Parsed
  metadata creates no apply-time pathname authority.
- Added an engine/helper-selected Trash descriptor-bundle foundation. The
  ownership-transfer constructor normalizes opener-owned descriptors away from
  standard streams; `TrashLease` requalifies root, role, ownership, mode, and
  mount evidence before only `linuxfs` can receive a fresh legacy
  `files`/`info` pair or, for an attested topology, the complete fixed-role
  descriptor set. Capability-specific raw-descriptor handoffs are rejected by
  the architecture boundary even through an interface receiver. Its legacy
  form remains metadata-only.
- Added an opt-in topology-qualified Trash boundary. A lease with an attested
  home-data or filesystem-top anchor lends a full, requalified descriptor set
  only to `linuxfs`; `trash.ValidateTrashLayout` proves literal `Trash`,
  `.Trash-$uid`, or sticky `.Trash/$uid` relationships plus literal `files`
  and `info` children with required `openat2` resolution. It rejects unrelated
  descriptors, symlinked children, wrong placement anchors, and sticky-bit
  drift, and repeats the proof before and after metadata publication. A
  post-create proof failure retains the metadata record and reports an
  interrupted/drifted result. It still performs no path discovery or
  user-content mutation.
- Added descriptor-rooted durable publication of one `.trashinfo` record with
  no-follow/no-replace creation, file and directory syncs, and post-sync
  identity/content verification. The accepted `ldc-` lowercase-hex token
  profile is rejection defense only; generation, reservation, durable intent,
  content move, restore, metadata removal, and orphan reconciliation remain
  unimplemented.
- Added `trash.WriteTrashInfoDurable`, which maps a lease-attested lexical
  source-relative metadata path, reselects the topology-qualified Trash
  layout, serializes bounded `.trashinfo` bytes, and invokes the durable
  publication primitive. It deliberately does not resolve or validate the
  source, reserve a token, move content, issue a recovery handle, or clean up
  a possibly published record.
- Added `quarantine.OpenPerMountQuarantine`, an open-only facade over a
  requalified `LayoutPrivateQuarantine` lease. The returned store exposes only
  the trusted root identity and idempotent close; it cannot disclose a path or
  descriptor or retain, restore, scan, delete, or reconcile content.

## Validation evidence

The default lane uses test-owned temporary roots only. At this checkpoint, the
following local checks passed:

- `GOTOOLCHAIN=local go test ./... -count=1`
- `GOTOOLCHAIN=local go test -race ./... -count=1`
- `GOTOOLCHAIN=local go vet ./...`
- `GOTOOLCHAIN=local go mod verify` and `make build`
- `GOCACHE=/tmp/ldc-go-build make coverage` passed and enforces >=90%
  statement coverage for the Phase 3 validation/state-machine packages:
  `internal/linuxfs` 90.1%, `internal/mounts` 90.0%, `internal/trash` 94.7%,
  and `internal/quarantine` 94.3%. The explicit cache location is required by
  this sandbox; the Makefile itself remains hermetic/offline.

The targeted integration command
`GOTOOLCHAIN=local go test -tags=integration ./tests/integration -run 'LinuxFS|TrashCrash' -count=1`
completed with `[no tests to run]`; it is not evidence for the missing Phase 3
integration or crash-recovery behavior.

`govulncheck` could not be run in this environment: the tool was not cached
and the sandbox denied its required request to `proxy.golang.org`. It remains
an external validation step, not a passing result.

An independent adversarial review found a same-UID name-replacement race in
the initial recursive-removal design. The final implementation gates all
production removal before the first unlink; a regression test proves that an
ordinary staged object remains retained and reports `unsupported`.

## Hard gates still open

- The layout-authority contract and descriptor-rooted FDO topology validator
  exist, but no engine/helper composition has registered a real source-root
  mapping for a private same-mount quarantine directory or a usable Trash
  topology with the required metadata basis for a source root that may be a
  subdirectory. The metadata and open-only facades do not alter that fact:
  content Trash moves, quarantine retention, restore, and reconciliation remain
  unsupported rather than accepting an arbitrary path or falling back to
  permanent deletion. That authority must also prove an exclusive staged
  namespace before irreversible recursive removal can be enabled.
- No disposable supported VM has run the ext4/XFS/Btrfs mount and adversarial
  race campaigns. Default-lane tests cannot certify a production root or
  satisfy the required 1,000-attempt PR smoke gate.
- Durable Trash metadata pairing, recovery reconciliation, replacement state
  publication, and quarantine retention require the missing registered layout
  authority plus durable intent records and remain unimplemented.

## Handoff

Do not start Phase 4 while this phase remains gated. The next Phase 3 work
must register and prove engine/helper-owned layouts for the supported root
classes, then implement and qualify the documented Trash and quarantine
ordering without giving providers, presenters, plans, or callers absolute-path
authority.
