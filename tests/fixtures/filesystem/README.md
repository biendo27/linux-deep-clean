# Filesystem test fixtures

Phase 3 filesystem tests create every fixture dynamically beneath test-owned
temporary directories. They never contain a checked-in host path, mounted
filesystem image, user file, or destructive fixture.

The integration harness uses two separate temporary roots: one reserved for a
future production API attempt and one containing an outside sentinel. It
records a deterministic replay seed and schedule label, captures the
sentinel's identity, mode, size, and bytes, and proves that its comparison
rejects a deliberately poisoned in-memory observation. The initial schedule
is intentionally a no-op; it establishes assertion plumbing only and is not
evidence of Trash, quarantine, mount, race, or crash-recovery behavior.

VM qualification will replace the no-op only with production APIs in a
verified disposable guest. Its fixtures must record kernel, architecture,
filesystem, mount options, actor schedule, seed, result, and retained
recovery state. The root-owned VM guard remains the only entry point for that
lane. Before a concurrent actor is introduced, the sentinel must be sampled
through a held descriptor rather than an `lstat`-then-read sequence, and its
snapshot must include the action-appropriate ownership, link-count, and time
facts in addition to identity, mode, size, and contents.
