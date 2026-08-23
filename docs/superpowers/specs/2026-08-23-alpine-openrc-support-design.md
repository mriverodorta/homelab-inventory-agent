# Alpine Linux And OpenRC Agent Support Design

**Date:** 2026-08-23

**Status:** Approved in conversation; awaiting written-spec review

**Target releases:** Homelab Inventory Agent 0.3.4 and Homelab Inventory 0.15.2

## Objective

Make Alpine Linux 3.22 and Linux systems using OpenRC first-class supported
Homelab Inventory Agent hosts without changing protocol major version 1. The
agent must continue operating when optional hardware tools are absent, must run
as an unprivileged service, and must preserve all existing systemd, FreeBSD,
OPNsense, identity, update, and telemetry behavior.

## Verified Starting Point

The production application currently serves Agent 0.3.3. A read-only diagnostic
on Alpine Linux 3.22.1 AMD64 established the following facts:

- The checksum-verified, `CGO_ENABLED=0` Linux binary runs successfully on musl.
- Core procfs, mount, thermal, hwmon, power-supply, and DRM inputs are available.
- OpenRC is active and systemd is unavailable.
- The current installer cannot create its account because Alpine lacks
  `groupadd` and `useradd` by default.
- The current installer, updater, uninstaller, and service collector require
  `systemctl`.
- Alpine provides `lsblk` at `/bin/lsblk` and `ip` at `/sbin/ip`, while the
  scanner currently uses fixed Ubuntu-oriented paths.
- `dmidecode`, `lspci`, and `smartctl` were not installed.
- A Docker socket exists, but direct access was not enabled for an unprivileged
  agent account.

These results mean that the Linux telemetry implementation is portable, but the
host integration layer is not yet portable.

## Scope

### Included

- Capability-based OpenRC detection on Linux.
- Alpine-specific account and package-ownership behavior.
- One Linux installer with systemd and OpenRC adapters.
- One native updater and uninstaller with systemd and OpenRC adapters.
- A signed, checksummed OpenRC service asset.
- A Linux OpenRC service collector.
- Trusted multi-path command resolution.
- Independent, optional hardware inventory sources.
- Explicit direct-container-socket group access when selected by the user.
- A one-time standard-path Agent 0.3.3 to 0.3.4 Alpine upgrade bridge.
- Agent and application contract, packaging, test, documentation, and release
  updates.

### Excluded

- Automatic `apk add` or any other package installation.
- Arbitrary custom service supervisors or custom installation paths.
- Root background execution.
- Approximate OpenRC per-service CPU, memory, task, restart, or timestamp data.
- Shell plugins, remote commands, process command lines, environment variables,
  or broader host inspection.
- A protocol-major change or application database migration.

## Platform Model

Operating system, distribution, and init manager are separate facts.

- Operating system selects Linux or FreeBSD collectors and release binaries.
- Distribution selects Alpine account creation and APK ownership behavior.
- Init manager selects systemd, OpenRC, or FreeBSD rc.d lifecycle behavior.

The Linux init detector selects systemd only when systemd is operational. It
selects OpenRC only when the required OpenRC command set and runtime evidence are
present. Merely finding an executable is insufficient. Ambiguous, inactive, or
unsupported managers fail before the installer mutates the host.

Alpine is detected through normalized `/etc/os-release` data and
`/etc/alpine-release`; APK-specific behavior additionally requires a trusted APK
executable. Other Linux distributions running OpenRC use the generic OpenRC
lifecycle. Distribution-specific classification degrades to `unknown` when no
supported package database exists.

## Component Boundaries

### Init Manager Adapter

The installer, updater, and uninstaller consume one manager abstraction that
provides:

- Service asset name and destination path.
- Stop, enable, disable, start, restart, and health commands.
- Runtime-directory preparation.
- Sustained-health verification.
- Rollback and daemon-reload behavior where applicable.

The existing systemd behavior becomes one implementation without changing its
paths or hardening. OpenRC becomes a second implementation. FreeBSD remains on
its existing dedicated packaging path.

### Distribution Account Adapter

The account adapter provides idempotent group creation, account creation, and
supplementary-group membership.

- Alpine uses `addgroup` and `adduser` with non-login, no-home, system-account
  semantics.
- Conventional supported Linux retains `groupadd`, `useradd`, and the current
  account layout.
- Existing matching accounts and groups are accepted.
- Conflicting ownership, unexpected home directories, interactive shells, or
  unsafe group mappings fail before installation changes files.
- `/sbin/nologin` is preferred; a reviewed non-interactive fallback is allowed
  when the distribution does not provide it.

### Trusted Executable Resolver

Hardware tools are resolved only from reviewed absolute path candidates. The
agent never executes an arbitrary program selected from a root-controlled
`PATH` during privileged inventory collection.

Initial candidates include:

| Tool | Trusted candidates |
| --- | --- |
| `dmidecode` | `/usr/sbin/dmidecode`, `/sbin/dmidecode`, `/usr/local/sbin/dmidecode` |
| `lsblk` | `/usr/bin/lsblk`, `/bin/lsblk` |
| `lspci` | `/usr/bin/lspci`, `/bin/lspci`, `/usr/sbin/lspci`, `/sbin/lspci` |
| `ip` | `/usr/sbin/ip`, `/sbin/ip`, `/usr/bin/ip`, `/bin/ip` |
| `smartctl` | `/usr/sbin/smartctl`, `/usr/bin/smartctl`, `/usr/local/sbin/smartctl` |

Resolution verifies a regular executable file and uses fixed arguments, output
limits, timeouts, and strict parsers. A present command whose required output
mode is unsupported is treated as unavailable for that source.

## OpenRC Service

The release adds `homelab-inventory-agent.openrc` to the manifest, checksum
file, SBOM, provenance, and signed release assets. The installer places it at
`/etc/init.d/homelab-inventory-agent` with a non-writable executable mode.

The service:

- Runs `/usr/local/sbin/homelab-inventory-agent` with the canonical config path.
- Runs as the dedicated `homelab-inventory-agent` user and group.
- Uses OpenRC supervision and bounded restart behavior.
- Depends on an operational network without blocking boot indefinitely.
- Creates `/run/homelab-inventory-agent` with agent ownership and mode `0700`.
- Preserves umask `0077`.
- Emits logs through the host logging facility rather than an unbounded private
  log file.
- Exposes health through `rc-service homelab-inventory-agent status`.
- Never grants root, Linux capabilities, or access beyond configured state,
  runtime, telemetry, and explicitly selected container resources.

OpenRC cannot reproduce every systemd namespace restriction. Documentation
must state that distinction. The security boundary remains the unprivileged
account, strict file ownership, fixed executable paths, bounded command output,
no shell hooks, no remote execution, and no additional writable application
paths.

## Installation And Upgrade Transactions

The existing transactional order remains mandatory:

1. Validate arguments, platform, distribution, init manager, account state, and
   optional direct-socket access requirements.
2. Download the binary, checksum file, and matching manager service asset.
3. Verify every downloaded asset and execute the candidate binary's version
   check before stopping the current service.
4. Preserve the previous binary, service asset, configuration, identity, queue,
   contract cache, hardware state, and service state needed for rollback.
5. Install the binary and matching service definition atomically.
6. Start or restart the selected manager.
7. Require repeated healthy status checks.
8. Commit only after sustained health succeeds.
9. Restore the previous binary and service definition and restart the previous
   service if any post-installation step fails.

The OpenRC fresh installer creates the standard directories and performs the
existing one-time unprivileged enrollment. It never installs packages.

A manually installed Alpine Agent 0.3.3 can transition only when its
configuration and identity use the standard Linux paths. The generated 0.3.4
`install.sh --upgrade` command preserves those files and installs the canonical
OpenRC service transactionally. Agent 0.3.3's native updater is not advertised
as an Alpine bridge because it assumes systemd. Arbitrary custom supervisors,
paths, users, or state layouts remain unsupported.

After the bridge, `sudo homelab-inventory-agent update`, `update --check`, and
exact-version updates use the OpenRC adapter and preserve all durable state.

Uninstall stops, disables, and removes the selected manager's service and binary
while preserving configuration and identity. `--purge` remains the only action
that destroys state and device identity.

## Direct Container Socket Access

Container telemetry remains disabled by default. Proxy mode changes no group
memberships.

When the user explicitly selects direct socket mode, the installer:

1. Requires one of the existing allowlisted Docker or Podman socket paths.
2. Verifies that the path is a Unix socket.
3. Reads its numeric owning group and permissions without changing them.
4. Resolves a safe named group for that GID.
5. Adds only the dedicated agent account to that supplementary group through
   the distribution account adapter.
6. Verifies the account can open the socket before committing installation.
7. Rolls back installation when access cannot be established.

The installer never changes socket ownership or mode, never runs the daemon as
root, and never joins a group when container mode is disabled or proxy-based.

## Telemetry And Inventory

Core Linux heartbeat telemetry is unchanged. Alpine already provides the
required procfs, mount, memory, load, uptime, thermal, hwmon, power-supply, and
DRM inputs. Optional absent inputs such as ZFS ARC remain omitted.

Hardware inventory becomes a composition of independent sources:

- DMI/SMBIOS through optional `dmidecode`.
- Storage topology through optional compatible `lsblk`.
- PCI devices through optional `lspci`.
- Network interfaces through optional JSON-capable `ip`.
- Storage health through optional `smartctl`, still requiring both application
  policy and explicit local device configuration.

One missing or incompatible source does not prevent other sources from
contributing. The explicit inventory command fails only when no supported
source returns any usable component. Serial and private-data sanitization
remains unchanged.

Capabilities identify each available, unavailable, permission-blocked,
disabled, or unsupported source. Installing an optional tool later is detected
by the existing capability reconciliation mechanism without reinstalling the
agent. The installer never calls `apk add`.

## OpenRC Service Collection

OpenRC service telemetry is a dedicated Linux collector; it does not masquerade
as FreeBSD rc.d.

The collector reports only values it can establish accurately:

- Stable service name.
- Bounded description when safely available.
- Active state and substate.
- Enabled state.
- `user-installed`, `system`, or `unknown` classification.
- Manager `openrc`.

CPU, memory, task, restart, result, and transition timestamp fields are omitted
because OpenRC does not provide equivalent authoritative accounting. They are
not emitted as zero.

The collector uses trusted OpenRC executables, fixed arguments, strict service
name validation, deduplication, bounded timeouts/output, and the existing
512-service ceiling. It preserves the ten-minute default discovery cadence and
the one-minute cadence for notification-selected services.

On Alpine, service scripts are mapped through APK ownership. Non-package-owned
scripts and scripts owned by explicitly installed packages are
`user-installed`; base or dependency-owned services are `system`; unresolved
cases are `unknown`. Other OpenRC distributions retain accurate lifecycle state
while classification may remain `unknown`.

## Protocol And Application Changes

Protocol v1 adds `openrc` to the service-manager enum. This is an additive value,
not a protocol-major change. Agent service records always send it explicitly.

Homelab Inventory 0.15.2 must:

- Accept `openrc` in its protocol-v1 service validator.
- Preserve `openrc` as part of the service entity key.
- Render and filter OpenRC service records without assuming systemd-only
  resource fields.
- Embed and serve the exact Agent 0.3.4 release manifest and assets.
- Generate the same installation command; `install.sh` performs manager
  detection.
- Report Agent 0.3.4 as current and provide the correct native update guidance.

The telemetry database already stores service manager values as text. No
application schema migration is required.

## Failure Behavior

- Unsupported or ambiguous init managers fail before mutation.
- Optional tool absence degrades only its own capability.
- Unsafe executable paths are ignored rather than executed.
- Incompatible command output is unavailable, not partially trusted.
- Unsafe OpenRC names, excessive service counts, oversized output, and command
  timeouts fail the service collection family without stopping heartbeats.
- Direct socket setup fails closed without changing socket permissions.
- Candidate asset or version mismatch fails before service interruption.
- Failed service startup restores the previous binary and service definition.
- Rollback failure is surfaced explicitly and never reported as successful.

## Verification Strategy

### Unit And Rooted-Filesystem Tests

- Active systemd, active OpenRC, ambiguous, inactive, and unsupported manager
  detection.
- Alpine and conventional account command selection.
- Existing safe accounts and unsafe conflicts.
- Trusted executable candidates and path rejection.
- Every combination of missing and available inventory sources.
- BusyBox or incompatible `ip` output degradation.
- OpenRC parsing, classification, limits, deduplication, timeouts, and unsafe
  output rejection.
- Direct-socket group resolution, membership, access proof, and rollback.
- Fresh install, upgrade, uninstall, purge, and injected failure rollback for
  each manager.
- Reproducible manifests containing both service assets.

### Container Integration

- Alpine 3.22 AMD64 and ARM64 release binaries.
- OpenRC fresh installation and enrollment.
- Service restart and boot persistence.
- Minimal Alpine with all optional hardware packages absent.
- Enriched Alpine with optional tools present.
- Proxy and explicitly selected direct-socket modes.
- Standard-path Agent 0.3.3 installer bridge.
- Native Agent 0.3.4 check, update, exact update, and rollback.
- Uninstall preservation and purge destruction.
- Existing Ubuntu/systemd integration remains unchanged.

### Real Alpine Host Proof

The tested Alpine 3.22 server must prove:

- Fresh installation through its Homelab Inventory command.
- An unprivileged OpenRC daemon and correct file ownership/modes.
- Enrollment and signed one-minute heartbeats.
- CPU, memory, load, uptime, filesystem, thermal, and available sensor data.
- Accurate OpenRC service state without fabricated resource counters.
- Precise unavailable capabilities for absent optional tools.
- Partial and enriched one-time inventory as tools become available.
- Restart identity and queue preservation.
- Update checks and native update behavior.
- Proxy/direct container behavior when explicitly selected.
- Unlink dormancy without retry loops.

### Existing Platform Regression

- Go formatting, race tests, vet, staticcheck, and govulncheck.
- ShellCheck for all packaging scripts.
- Ubuntu/systemd installation transaction and rollback.
- FreeBSD and OPNsense rc.d behavior.
- Reproducible AMD64, ARM64, and FreeBSD release artifacts.
- SBOM, keyless checksum signature, and provenance attestation.

## Release Sequence

1. Implement and validate Agent 0.3.4 without changing the wire protocol major.
2. Publish the signed `v0.3.4` agent release with all reproducible assets.
3. Pin the exact source revision and release bytes in Homelab Inventory.
4. Add application acceptance for `manager: "openrc"` and its regression tests.
5. Run all Homelab Inventory lint, test, build, migration, and dual-architecture
   zero-vulnerability container gates.
6. Validate the candidate on the real Alpine host.
7. Deploy Homelab Inventory 0.15.2 when explicitly requested.

Agent and application changelogs and release notes must describe Alpine/OpenRC
support, optional hardware tools, direct-socket behavior, and the one-time 0.3.3
upgrade bridge. Docker Hub documentation must remain aligned when the app patch
is deployed.

## Acceptance Criteria

- Agent 0.3.4 installs, enrolls, runs, restarts, updates, and uninstalls through
  OpenRC on Alpine 3.22 AMD64 and ARM64.
- The daemon is never root.
- Core telemetry works without optional packages.
- Missing `dmidecode`, `lspci`, `smartctl`, `lsblk`, or compatible `ip` never
  stops heartbeats or unrelated inventory sources.
- Tools installed later are discovered without reinstalling the agent.
- OpenRC services report accurate state and classification without fabricated
  resource counters.
- Direct socket access is granted only after explicit selection and without
  changing socket ownership or permissions.
- Systemd, FreeBSD, and OPNsense behavior remains unchanged.
- Agent 0.3.3 standard-path Alpine state transitions safely through the
  generated 0.3.4 installer bridge.
- Homelab Inventory 0.15.2 accepts and persists `openrc` service records and
  serves the exact Agent 0.3.4 release.
- No application database migration or protocol-major bump is introduced.
- All release security and reproducibility gates pass.
