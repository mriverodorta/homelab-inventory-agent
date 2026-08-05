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

## Development

```bash
go test ./...
go vet ./...
```

The canonical wire contract lives in [`protocol/v1`](protocol/v1). Breaking protocol changes require a new protocol-major directory.

The current protocol bundle defines activation, one-minute heartbeats, bounded host metrics, capability states, services, opt-in container summaries, and storage-health observations. The application verifies the exact gzip body, endpoint, timestamp, and monotonic sequence with the enrolled Ed25519 public key before committing telemetry.

Application persistence is intentionally split: device enrollment and latest compatibility status remain in relational JSON stores during the database transition, while historical samples and latest telemetry projections live in an independent WAL-mode SQLite database. No heartbeat is acknowledged until telemetry persistence succeeds.
