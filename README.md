# Homelab Inventory Agent

Homelab Inventory Agent is the independent, open-source host telemetry agent for [Homelab Inventory](https://github.com/mriverodorta/homelab-inventory).

The project is under active development. It is not yet published as an installable agent release.

## Design boundaries

- Outbound-only communication to one Homelab Inventory compute host.
- Ed25519 request authentication with replay protection.
- An unprivileged background service on Linux and FreeBSD.
- One-minute historical telemetry by default, without a one-second realtime stream.
- Explicit opt-in for container and SMART collection.
- No remote command execution, shell plugins, process command lines, environment variables, container secrets, or host mount paths.
- A separate, explicit `sudo homelab-inventory-agent inventory` workflow for reviewed one-time hardware discovery.

The capability baseline was informed by an implementation review of Beszel v0.18.7, but this is an independent implementation. No Beszel source code or protocol is copied into this repository.

## Current Linux telemetry

The Linux collector reads procfs, sysfs, `statfs`, and a bounded `systemctl show` projection. It reports CPU totals and per-core deltas, CPU state breakdown, load, memory and swap, ZFS ARC, root and configured filesystems, disk I/O, aggregate and per-interface network traffic, temperature sensors, batteries, systemd services, and safe DRM GPU metrics. GPU readings are sampled in memory at the contract cadence and averaged into the normal one-minute heartbeat; no high-frequency series is transmitted or persisted.

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

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

The canonical wire contract lives in [`protocol/v1`](protocol/v1). Breaking protocol changes require a new protocol-major directory.

The current protocol bundle defines activation, one-minute heartbeats, bounded host metrics, capability states, services, opt-in container summaries, and storage-health observations. The application verifies the exact gzip body, endpoint, timestamp, and monotonic sequence with the enrolled Ed25519 public key before committing telemetry.

Application persistence is intentionally split: device enrollment and latest compatibility status remain in relational JSON stores during the database transition, while historical samples and latest telemetry projections live in an independent WAL-mode SQLite database. No heartbeat is acknowledged until telemetry persistence succeeds.

## Linux packaging

Release artifacts are built reproducibly for Linux AMD64, Linux ARM64, and FreeBSD AMD64:

```bash
scripts/build-release.sh 0.1.0 dist
```

The Linux installer creates a dedicated `homelab-inventory-agent` system user, verifies the binary and systemd unit against the release checksum manifest, activates the agent once, and starts the hardened service. The background process has no Linux capabilities and writes only to `/var/lib/homelab-inventory-agent`.

Upgrades preserve both `/etc/homelab-inventory-agent/config.json` and `/var/lib/homelab-inventory-agent/identity.json`. They replace only the verified binary and service unit, and restore the prior files if activation of the new installation fails.

```bash
sudo ./install.sh --endpoint https://inventory.example.com --version 0.1.0 --upgrade
```

Uninstalling preserves configuration and identity so a later reinstall remains the same enrolled device. Use the destructive purge option only when intentionally retiring the identity.

```bash
sudo ./uninstall.sh
sudo ./uninstall.sh --purge
```

The release workflow runs the race detector, vet, static analysis, vulnerability analysis, shell validation, reproducibility checks, CodeQL, SBOM generation, keyless signature generation, and build provenance attestation before publishing artifacts.

## FreeBSD and OPNsense

FreeBSD AMD64 uses the same signed, outbound-only protocol and one-minute heartbeat cadence. The collector reads bounded output from fixed `sysctl`, `df`, `netstat`, `iostat`, `pciconf`, `geom`, and rc.d status commands. It does not call `configctl`, inspect `/conf/config.xml`, or collect firewall, VPN, routing, gateway, CARP, or NAT state. OPNsense is treated only as a generic FreeBSD host.

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
