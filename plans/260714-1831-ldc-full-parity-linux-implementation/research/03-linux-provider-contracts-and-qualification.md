---
type: research
date: 2026-07-14
subject: Linux provider contracts, mutation feasibility, and VM qualification
status: complete
---

# Linux provider contracts and qualification research

## Provider matrix

| Provider | Discovery and preview | Apply boundary and feasibility | Required fixtures and destructive tests |
|---|---|---|---|
| APT/dpkg | Use `dpkg-query -W --showformat` for exact package, architecture, version, status, and Essential flag. Use `dpkg-query -S/-L` only for registered ownership. Normalize root-context `apt-get --simulate remove <name>:<arch>` under `LC_ALL=C`. Estimate cache from a bounded scan. | Use only fixed `apt-get -y remove`; never purge, autoremove, fix-broken, or dangerous allow flags. `apt-get clean` alone owns archive-cache deletion. Simulation disables locking, so re-simulate immediately before apply and reject drift; a residual race remains. | Per-release output goldens, multiarch, held/essential/broken packages, dependency change, lock contention, interrupted dpkg, cache drift. |
| DNF5/RPM | Query exact NEVRA, file lists, and ownership from RPM; use DNF5 repoquery for installed reason/origin. Preview exact-NEVRA removal with `--no-autoremove`. | Prefer `dnf5 remove --store=<private-dir>` followed by DNF5 replay. Replay binds the operation graph and rejects installed-version drift. Use `dnf5 clean packages` only for package cache. DNF4 is unsupported. | Stored transaction JSON, protected packages, running kernel, dependency leaves, repository disappearance, RPM lock, replay drift/interruption. |
| Pacman/paccache | Query exact installed names/versions and ownership with Pacman. Use `-R --print --print-format` for removal preview. Use `paccache --dryrun --keep 3` for cache candidates. Normalize any non-format output locale. | Use only fixed `pacman -R --noconfirm <exact-name>`, without recursive dependency removal. Repeat exact `paccache --remove --keep 3` for cache apply. There is no atomic transaction token; re-preview and fail on drift. Never touch `db.lck`. | Fully upgraded Arch, dependent-package refusal, IgnorePkg, lock contention, versioned cache names, missing paccache, preview/apply drift. |
| Flatpak | Probe supported columns, then use `flatpak list --columns=...` and `flatpak info` to capture exact ref, commit, origin, installation, and scope. Maintain versioned tab-output fixtures; there is no stable JSON contract. | Use `flatpak uninstall --user|--system --noninteractive <exact-ref>`. Never use `--unused`, `--delete-data`, or wildcard refs. There is no real dry-run or atomic preview binding. | User/system duplicate refs, runtime dependency, active app, disappearing ref, OSTree lock. |
| Snap | Prefer snapd's versioned REST API to human table output. Capture exact name, snap ID, revision, channel, and status. There is no transaction preview. | Submit an exact remove action and follow the asynchronous change to a terminal state. Do not use `--purge`; disclose snapd's automatic data snapshot behavior. No broad prune. | Classic/strict snaps, active services, snapshot success/failure, concurrent change, daemon unavailable, interrupted removal. |
| journald | Use `journalctl --disk-usage` under C locale and version-gate its parser. It reports localized prose and combines active and archived use; preview is a bound, not an exact deletion list. | Use only `journalctl --rotate` with `--vacuum-size`, `--vacuum-time`, or `--vacuum-files`. Never delete journal files or edit retention configuration. Exact preview parity is impossible during concurrent writes. | Volatile/persistent journals, user/system scope, rotation, concurrent logging, permissions, thresholds, and post-use verification. |
| fprintd/PAM | Probe fprintd D-Bus, device, caller enrollment, PAM module ownership, and platform manager capability. | Debian/Ubuntu: exact `pam-auth-update --enable/--disable <profile>`, never `--force`. Fedora: `authselect check/test`, then enable/disable the fingerprint feature with backup; refuse unmanaged profiles. Arch: guidance only. Preserve password fallback. | No device/enrollment, busy device, local PAM edits, missing profile, authselect check failure, backup/restore, real-hardware sudo lane. |
| XDG/Trash | Resolve XDG roots using specification fallbacks. Select cache candidates only within cache roots or by exact manifests. Trash usage remains read-only. | Use same-filesystem collision-safe Trash moves with valid `.trashinfo`; validate `.Trash/$uid` or `.Trash-$uid`, symlinks, sticky bit, and ownership. Refuse cross-device/unsupported mounts and never empty Trash. | Home/top-directory Trash, collisions, non-UTF-8 paths, mount boundaries, crash between metadata and rename, restoration. |
| Projects/installers | Require configured-root containment, nearest ecosystem marker, artifact-specific rules, and no symlink or mount crossing. Installer roots are XDG Downloads plus explicit roots; require extension plus magic/package metadata. | Trash by default. Do not select deployment outputs. Do not treat archives as installers without stronger evidence. | Marker collision, nested workspaces, custom build paths, malicious archives, MIME/extension mismatch, Downloads fallback. |

## Metrics contract

Read documented procfs and sysfs counters, calculate deltas, bind process measurements to PID plus start time, and emit unavailable measurements as `null` rather than fabricated zeroes. Fixtures must cover reset/wrap, missing fields, permission loss, hotplug, absent thermal/power sensors, and disk-sector-size differences. Add gopsutil only if direct adapters cannot meet the cross-distribution contract.

## Mutation policy conclusions

Full semantic parity does not mean pretending every manager has an atomic preview token. DNF5 replay offers the strongest binding. APT and Pacman require an immediate re-simulation and typed graph comparison, while Flatpak, Snap, and journald can provide only exact-target or bounded-effect confirmation. The product must disclose those provider-specific guarantees, re-probe capabilities at apply, reject observable drift, and classify interrupted or unverifiable completion as `indeterminate` with reconciliation required.

Every external operation maps a typed enum to a compiled absolute executable and a fixed argument template. It receives a new minimal environment, bounded output and duration, no shell, no `PATH` lookup, no caller argv, and no authority derived from parsed display text.

## VM and release lanes

Pull-request qualification should cover Ubuntu 24.04 x86_64, Debian 13 x86_64, Fedora 44 x86_64, and current Arch x86_64. Nightly and release qualification covers the approved 15 guests:

- Ubuntu 22.04, 24.04, and 26.04 on x86_64 and aarch64;
- Debian 12 and 13 on x86_64 and aarch64;
- Fedora 43 and 44 on x86_64 and aarch64;
- current Arch on x86_64.

Each destructive lane starts from a snapshot, proves the disposable-guest sentinel, uses only locally built `linux-deep-clean-test-*` packages, and covers locks, interruption, package lifecycle, Trash, journal, and capability behavior. Native packages own `/usr/bin/ldclean`, the helper, static polkit policy, man pages, and completions. Removal preserves XDG state. Test install, exact-package update, remove, and reinstall for `.deb`, `.rpm`, and Arch artifacts. The rootless archive never installs the helper or privileged integration.

## Primary sources

- [APT `apt-get(8)`](https://manpages.debian.org/unstable/apt/apt-get.8.en.html)
- [`dpkg-query(1)`](https://manpages.debian.org/unstable/dpkg/dpkg-query.1.en.html)
- [DNF5 replay](https://dnf5.readthedocs.io/en/stable/commands/replay.8.html)
- [`pacman(8)`](https://man.archlinux.org/man/pacman.8)
- [`paccache(8)`](https://man.archlinux.org/man/paccache.8)
- [Flatpak command reference](https://docs.flatpak.org/en/latest/flatpak-command-reference.html)
- [Snap removal behavior](https://snapcraft.io/docs/explanation/security/decommissioning/)
- [`journalctl`](https://www.freedesktop.org/software/systemd/man/255/journalctl.html)
- [fprintd](https://fprint.freedesktop.org/fprintd-dev/)
- [`pam-auth-update(8)`](https://manpages.debian.org/trixie/libpam-runtime/pam-auth-update.8.en.html)
- [FreeDesktop Trash specification](https://specifications.freedesktop.org/trash/latest/)
- [Linux procfs documentation](https://docs.kernel.org/filesystems/proc.html)

## Planning consequence

Provider capability and guarantee descriptions are part of the public plan schema and structured output. Mutation for a provider remains disabled until its fixtures, drift rules, negative tests, and supported-VM action family pass. Full parity is a release qualification claim, not permission to weaken a provider's native safety limitations.
