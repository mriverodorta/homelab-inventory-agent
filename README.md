# Homelab Inventory Agent

Homelab Inventory Agent is the independent, open-source host telemetry agent for [Homelab Inventory](https://github.com/mriverodorta/homelab-inventory).

The project is under active development. Homelab Inventory builds and embeds a pinned release of this source so enrolled hosts install directly from their own trusted application instance.

## Design boundaries

- Outbound-only communication to one Homelab Inventory compute host.
- Ed25519 request authentication with replay protection.
- An unprivileged background service on Linux and FreeBSD.
- Exactly 30 one-minute CPU, memory, and heartbeat slots, without a one-second realtime stream.
- Explicit opt-in for container and SMART collection.
- No remote command execution, shell plugins, process command lines, environment variables, container secrets, or host mount paths.
- A separate, explicit `sudo homelab-inventory-agent inventory` workflow for reviewed one-time hardware discovery.
- A separate, explicit root-only updater that never runs from the background daemon.

The capability baseline was informed by an implementation review of Beszel v0.18.7, but this is an independent implementation. No Beszel source code or protocol is copied into this repository.

## Current Linux telemetry

The Linux collector reads procfs, sysfs, mountinfo, `statfs`, and a bounded `systemctl show` projection. Normal heartbeats report aggregate CPU state, load, memory and swap, ZFS ARC, filtered local filesystem usage, a CPU temperature average, one sensor per NVMe device, batteries, systemd services, and safe DRM GPU metrics. Per-core CPU, network, and disk-I/O telemetry are not collected for normal delivery. Service records identify locally installed or manually installed units separately from operating-system units when the host exposes enough package ownership data. GPU readings are sampled in memory at the contract cadence and averaged into the normal one-minute heartbeat; no high-frequency series is transmitted or persisted.

eMMC and mdraid health use read-only sysfs. SMART is collected only when the application contract enables it and the local configuration explicitly allowlists normalized `/dev/...` paths. The fixed `smartctl -n standby,0 -a -j` invocation has a timeout and output limit and does not wake standby disks. Serial numbers and WWNs are discarded; device references are installation-specific HMAC identifiers.

An example development configuration is:

```json
{
  "endpoint": "https://inventory.example.com",
  "host": { "type": "server", "id": 1 },
  "stateDirectory": "/var/lib/homelab-inventory-agent",
  "filesystems": ["/mnt/media"],
  "storageHealth": {
    "smartDevices": ["/dev/nvme0"]
  }
}
```

Listing a SMART device does not override the application contract. Both controls must allow SMART before the command runs.

## Optional container telemetry

Container collection is disabled by default. A host administrator can opt in while generating an installation command and choose either:

- a credential-free Docker-compatible API proxy bound to loopback; or
- advanced direct access to an allowlisted local Docker or Podman socket.

The direct socket option grants the agent the runtime API access exposed by that socket and should be used only after reviewing that trust boundary. The collector sends only container runtime, ID, name, image/digest, state, health, uptime, the allowlisted Compose service value, published port mappings, network mode and names, and aggregate CPU, memory, network, and disk rates. It never sends arbitrary labels, environment variables, commands, arguments, mounts, secrets, IP or MAC addresses, or raw inspect responses.

For Docker-compatible HTTP proxies, the agent reads the runtime's `/version` response and uses an API version within the daemon-advertised supported range. It does not assume a fixed Docker API version, and it renegotiates once if the daemon's supported range changes while the agent is running.

```json
{
  "containers": {
    "mode": "proxy",
    "runtime": "docker",
    "endpoint": "http://127.0.0.1:2375"
  }
}
```

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

The canonical wire contract lives in [`protocol/v1`](protocol/v1). Breaking protocol changes require a new protocol-major directory.

The current protocol bundle defines activation, one-minute heartbeats, bounded host metrics, capability states, services, opt-in container summaries, and storage-health observations. The application verifies the exact gzip body, endpoint, timestamp, and monotonic sequence with the enrolled Ed25519 public key before committing telemetry.

Heartbeat acknowledgements can include a revisioned monitoring policy for notification-selected services and containers. The agent validates and atomically persists only newer revisions in `monitoring-config.json` with mode `0600`, then reports the applied revision on later outbound heartbeats so the application can distinguish pending from active policy. Selected services use a one-minute collection interval; when none are selected, service discovery retains its normal ten-minute cadence. The policy contains stable resource keys only, never notification destinations, credentials, or remote commands, and survives agent restarts without changing device identity or queued telemetry.

Application persistence is intentionally split: enrollment belongs to the core relational database while exactly 30 CPU/memory slots, current component state, and meaningful transitions live in an independent WAL-mode SQLite database. The agent hashes its capabilities and state families, sends only changes between six-hour full reconciliations, and atomically persists acknowledged revisions in `telemetry-sync.json` with mode `0600`. The application can request capabilities or a family reconciliation only in the response to an agent-initiated heartbeat. No heartbeat is acknowledged until telemetry persistence succeeds.

## Linux packaging

Release artifacts are built reproducibly for Linux AMD64, Linux ARM64, and FreeBSD AMD64:

```bash
scripts/build-release.sh 0.1.0 dist
```

Each release bundle includes a machine-readable `manifest.json`, SHA-256 checksums, the canonical protocol-v1 schemas, installers, service definitions, and all supported binaries. The manifest records the exact source revision. Homelab Inventory validates every embedded byte against this manifest before exposing a release route.

The Linux installer creates a dedicated `homelab-inventory-agent` system user, verifies the binary and systemd unit against the release checksum manifest, activates the agent once, and starts the hardened service. The background process has no Linux capabilities and writes only to `/var/lib/homelab-inventory-agent`.

The first upgrade from an agent release that predates native updates uses the generated installer command once. That transaction preserves both `/etc/homelab-inventory-agent/config.json` and `/var/lib/homelab-inventory-agent/identity.json`:

```bash
sudo ./install.sh --endpoint https://inventory.example.com --version 0.1.0 --upgrade
```

After that transition, the installed agent discovers releases from its configured Homelab Inventory origin:

```bash
sudo homelab-inventory-agent update --check
sudo homelab-inventory-agent update
sudo homelab-inventory-agent update --version 0.1.5
```

The normal command installs the newest compatible embedded release. `--version` requests an exact version only when that application instance serves it. The updater verifies the release manifest, protocol, platform, byte size, and SHA-256 digest, refuses cross-origin redirects, stops the service only after downloads validate, and atomically restores the prior binary and service definition if restart health checks fail. Configuration, Ed25519 identity, offline queue, contract cache, hardware state, and container settings are not rewritten.

Uninstalling preserves configuration and identity so a later reinstall remains the same enrolled device. Use the destructive purge option only when intentionally retiring the identity.

```bash
sudo ./uninstall.sh
sudo ./uninstall.sh --purge
```

The release workflow runs the race detector, vet, static analysis, vulnerability analysis, shell validation, reproducibility checks, CodeQL, SBOM generation, keyless signature generation, and build provenance attestation before publishing artifacts. Agent upgrades remain manual: Homelab Inventory reports an available version and provides a verified command, but the background service never replaces itself.

When an administrator unlinks a host in Homelab Inventory, the revoked agent records a dormant state and stops delivery instead of retrying indefinitely. Re-enrollment is an explicit administrator action.

## FreeBSD and OPNsense

FreeBSD AMD64 uses the same signed, outbound-only protocol and one-minute heartbeat cadence. The normal collector reads bounded output from fixed `sysctl`, `df`, and rc.d status commands; network and disk-I/O history are disabled. Reviewed hardware inventory can use sanitized `pciconf` and `geom` data separately. It does not call `configctl`, inspect `/conf/config.xml`, or collect firewall, VPN, routing, gateway, CARP, or NAT state. OPNsense is treated only as a generic FreeBSD host.

The normal service remains unprivileged. When FreeBSD hardening hides other users' processes, the agent reports process and per-service resource visibility as permission-blocked and omits those values instead of reporting misleading zeros. PCI and disk descriptions are sanitized before transmission; GEOM identifiers and LUN identifiers are discarded.

The FreeBSD installer places files at:

```text
Binary:        /usr/local/sbin/homelab-inventory-agent
Configuration: /usr/local/etc/homelab-inventory-agent/config.json
Service:       /usr/local/etc/rc.d/homelab_inventory_agent
FreeBSD state: /var/db/homelab-inventory-agent
OPNsense state:/conf/homelab-inventory-agent
```

OPNsense detection is based on its installation directory and never reads its configuration. Identity remains under `/conf` across operating-system upgrades. Uninstall preserves configuration and identity by default; `--purge` is intentionally destructive.

## Reviewed hardware inventory

The normal service never runs as root. When a user wants complete hardware identity for local matching, they can run one explicit scan:

```bash
sudo homelab-inventory-agent inventory
```

The subcommand invokes only fixed, read-only operating-system tools with strict time and output bounds. It prints a component-count summary without displaying raw serials, asks `Send this hardware snapshot? [y/N]`, and defaults to cancel. It neither loads the agent identity nor opens a network connection.

After confirmation, the transient root process sends the validated snapshot through the local `inventory.sock` Unix socket and clears its in-memory model. The unprivileged daemon accepts only a local UID-0 peer, validates the snapshot a second time, derives installation-specific opaque fingerprints with its private identity, signs the request, and sends it to the configured Homelab Inventory host. The reviewed snapshot preserves raw physical storage identity, partition tables, partitions, filesystems, and LVM/RAID child topology; interpretation remains a server-side responsibility. Linux uses `/run/homelab-inventory-agent/inventory.sock`; FreeBSD uses `/var/run/homelab-inventory-agent/inventory.sock`.

The application retains only the latest snapshot for that host. Raw serials, WWNs, and DMI identifiers are private local evidence for matching and user-reviewed field suggestions; they are never eligible for automatic registry contribution.
