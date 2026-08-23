# Alpine Linux And OpenRC Agent Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Release Homelab Inventory Agent 0.3.4 with first-class Alpine/OpenRC support and integrate it into Homelab Inventory 0.15.2 without a protocol-major or database-schema change.

**Architecture:** Keep one static Linux binary and one installer while separating operating system, distribution, and init-manager decisions. Add focused OpenRC lifecycle and service-collection implementations, use trusted executable candidates for partial hardware inventory, then embed the exact reproducible Agent 0.3.4 bundle into the application after agent verification.

**Tech Stack:** Go 1.26, POSIX shell, OpenRC, systemd, Alpine APK, Bun, SQLite, Docker, GitHub release artifacts.

## Global Constraints

- Do not install optional host packages automatically.
- The background agent must never run as root.
- Missing optional inventory tools must not stop enrollment or heartbeats.
- Preserve Agent protocol major 1 and existing systemd, FreeBSD, and OPNsense behavior.
- Preserve configuration, identity, queue, contract cache, hardware state, and service state across update and rollback.
- Direct container socket access requires explicit selection and must never chmod or chown the socket.
- Do not bump the Homelab Inventory application version until deployment is explicitly requested.
- Clean all task-scoped build output, containers, images, caches, and temporary files after verification.

---

### Task 1: Add The OpenRC Protocol And Collector Boundary

**Files:**
- Modify: `protocol/v1/service.schema.json`
- Modify: `internal/collectors/linux/collector.go`
- Create: `internal/collectors/services/openrc.go`
- Create: `internal/collectors/services/openrc_test.go`
- Modify: `internal/runtime/telemetry_delta_test.go`

**Interfaces:**
- Produces: `services.NewForLinux(root string) (Collector, manager, detail)` and protocol manager `openrc`.
- Consumes: existing `servicesCollector` and `protocol.Service` boundaries.

- [ ] Write failing tests for OpenRC manager selection, bounded service discovery, enabled/active states, Alpine package classification, unsafe names, duplicates, timeouts, and omitted resource counters.
- [ ] Add `openrc` to the protocol-v1 service manager enum and delta identity coverage.
- [ ] Implement active-manager detection that chooses operational systemd or OpenRC and rejects ambiguity.
- [ ] Implement the dedicated OpenRC collector with fixed trusted commands, output limits, timeouts, and the 512-service ceiling.
- [ ] Update the Linux collector to use the selected collector and emit its explicit manager/detail instead of hardcoding systemd.
- [ ] Run `go test ./internal/collectors/services ./internal/collectors/linux ./internal/runtime ./protocol/v1` and commit the independently passing collector boundary.

### Task 2: Make Hardware Inventory Partial And Path-Portable

**Files:**
- Create: `internal/inventoryscan/executables_linux.go`
- Create: `internal/inventoryscan/executables_linux_test.go`
- Modify: `internal/inventoryscan/scanner_linux.go`
- Modify: `internal/inventoryscan/scanner_linux_test.go`
- Modify: `internal/collectors/linux/storage.go`

**Interfaces:**
- Produces: trusted optional executable resolution and independently composed Linux hardware sources.
- Consumes: existing bounded command runner and hardware component parsers.

- [ ] Write failing tests for each trusted candidate path, non-regular/untrusted rejection, optional absence, incompatible JSON `ip`, and every partial-source combination.
- [ ] Implement a trusted absolute-path resolver for `dmidecode`, `lsblk`, `lspci`, `ip`, and `smartctl`.
- [ ] Change the scanner so DMI, storage, PCI, and network sources execute independently and only total absence fails the explicit inventory command.
- [ ] Keep fixed arguments, parser limits, serial sanitization, and private-data handling unchanged.
- [ ] Reuse the trusted SMART candidates without enabling SMART automatically.
- [ ] Run inventory and Linux collector tests and commit the partial-inventory implementation.

### Task 3: Add OpenRC Packaging And Alpine Account Management

**Files:**
- Create: `packaging/homelab-inventory-agent.openrc`
- Modify: `packaging/install.sh`
- Modify: `packaging/uninstall.sh`
- Modify: `internal/packaging/packaging_test.go`
- Create: `scripts/test-alpine-install.sh`

**Interfaces:**
- Produces: one manager-aware Linux installer/uninstaller and canonical OpenRC service asset.
- Consumes: the same release binary, configuration, state, and enrollment paths as systemd Linux.

- [ ] Write failing rooted-filesystem tests for OpenRC detection, Alpine account commands, runtime-directory ownership, service installation, enrollment transaction, uninstall preservation, purge, ambiguous manager rejection, and injected rollback.
- [ ] Add the OpenRC service with unprivileged execution, `0077` umask, `0700` runtime directory, host logging, network dependency, supervision, bounded restart, and health status.
- [ ] Refactor `install.sh` into manager-specific shell functions while retaining one downloaded installer.
- [ ] Add Alpine `addgroup`/`adduser` behavior and safe existing-account validation without invoking APK.
- [ ] Add the standard-path 0.3.3 Alpine `--upgrade` bridge while rejecting arbitrary custom layouts.
- [ ] Refactor `uninstall.sh` to stop/remove the detected service manager and preserve state unless `--purge` is explicit.
- [ ] Add Alpine container installation tests without weakening existing Ubuntu transaction tests.
- [ ] Run packaging tests and ShellCheck, then commit the manager-aware installer.

### Task 4: Add Explicit Direct-Socket Group Access

**Files:**
- Modify: `packaging/install.sh`
- Modify: `internal/packaging/packaging_test.go`
- Modify: `scripts/test-alpine-install.sh`
- Modify: `scripts/test-ubuntu-install.sh`

**Interfaces:**
- Produces: fail-closed supplementary group enrollment for explicit socket mode.
- Consumes: the distribution account adapter from Task 3 and existing socket allowlist from configuration validation.

- [ ] Write failing tests for disabled/proxy no-op, safe Docker and Podman sockets, missing sockets, non-sockets, unknown GIDs, inaccessible sockets, existing membership, new membership, and rollback removal.
- [ ] Resolve the socket's numeric group without changing its ownership or permissions.
- [ ] Add the agent account only through the selected distribution adapter and verify access as that account before commit.
- [ ] Track whether membership was newly added so rollback never removes pre-existing access.
- [ ] Exercise proxy and direct modes on Alpine and Ubuntu test fixtures.
- [ ] Run packaging and shell tests and commit the explicit socket-access transaction.

### Task 5: Make Native Updates Init-Manager Aware

**Files:**
- Modify: `internal/agentupdate/platform.go`
- Modify: `internal/agentupdate/update.go`
- Modify: `internal/agentupdate/update_test.go`

**Interfaces:**
- Produces: systemd, OpenRC, and FreeBSD update platform projections with exact service assets and lifecycle commands.
- Consumes: existing release manifest verification and atomic binary/service replacement.

- [ ] Write failing tests for OpenRC detection, asset choice, command sequence, sustained health, exact-version update, restart, delayed failure, and rollback.
- [ ] Split Linux platform selection into active init-manager detection while preserving architecture checks.
- [ ] Map OpenRC to `/etc/init.d/homelab-inventory-agent`, its release asset, and `rc-service` lifecycle/health commands.
- [ ] Preserve validation-before-stop, file modes, atomic replacement, state preservation, and rollback semantics.
- [ ] Verify systemd and FreeBSD command sequences remain byte-for-byte behaviorally unchanged.
- [ ] Run all updater tests and commit the native OpenRC update path.

### Task 6: Extend Reproducible Agent Releases And CI

**Files:**
- Modify: `scripts/build-release.sh`
- Modify: `cmd/releasemanifest/main.go` only if asset validation requires it
- Modify: `internal/packaging/packaging_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `README.md`
- Create: `CHANGELOG.md` if the agent repository still lacks one

**Interfaces:**
- Produces: complete reproducible Agent 0.3.4 bundle including the OpenRC asset.
- Consumes: all passing agent implementation tasks.

- [ ] Add the OpenRC service to cleanup, copy, mode, checksum, manifest, SBOM, signature, provenance, and reproducibility checks.
- [ ] Add Alpine AMD64/ARM64 build and installation verification to CI and release workflows.
- [ ] Document capability-based OpenRC support, optional tools, direct socket access, and the one-time 0.3.3 bridge.
- [ ] Run formatting, race tests, vet, staticcheck, govulncheck, ShellCheck, Ubuntu integration, Alpine integration, reproducible builds, and manifest verification.
- [ ] Commit the verified agent implementation without tagging until the exact release commit is known.

### Task 7: Integrate Agent 0.3.4 Into Homelab Inventory

**Files:**
- Modify in application: `server/agents/protocol-v1.mjs`
- Modify in application: `server/agents/protocol-v1.test.mjs`
- Modify in application: `server/agent-release-pin.json`
- Modify in application: `Dockerfile`
- Update application submodule: `vendor/homelab-inventory-agent`
- Regenerate: `server/agent-release/**`
- Modify application: `CHANGELOG.md`
- Modify application structured unreleased release-note draft
- Modify application: `README.md` and `DOCKERHUB.md` where platform support is listed

**Interfaces:**
- Produces: an unreleased Homelab Inventory patch candidate that accepts `openrc` and serves exact Agent 0.3.4 bytes.
- Consumes: the final committed Agent 0.3.4 source revision and reproducible bundle.

- [ ] Add failing server parser tests for accepted `openrc`, rejected unknown managers, and omitted OpenRC resource counters.
- [ ] Add `openrc` to the application parser without changing SQLite schema or service identity behavior.
- [ ] Tag and publish signed Agent `v0.3.4` only after all agent gates pass, then verify release asset checksums and source revision.
- [ ] Pin the exact agent revision/version, update the Docker build arguments, and regenerate the embedded verified bundle.
- [ ] Update public documentation, changelog, and unreleased structured notes without bumping the app version.
- [ ] Run the app's agent pin, release service, protocol, telemetry, lint, test, build, migration, and container security gates.
- [ ] Commit the coordinated application integration.

### Task 8: Prove The Candidate On Alpine And Prepare Deployment

**Files:**
- Use: generated Agent 0.3.4 installation command and release assets
- Record: both repositories' release evidence and verification output

**Interfaces:**
- Produces: deployment-ready Agent 0.3.4 and Homelab Inventory 0.15.2 evidence.
- Consumes: Tasks 1-7 and the real Alpine 3.22 host.

- [ ] Verify the Alpine host has no prior test residue and capture only non-secret baseline service/file ownership state.
- [ ] Install Agent 0.3.4 through the generated OpenRC command and verify the daemon UID is non-root.
- [ ] Verify enrollment, one-minute heartbeats, core metrics, OpenRC services, capability degradation, restart identity, queue state, update check, and unlink behavior.
- [ ] Verify optional tools can appear later without reinstalling and only enrich their matching inventory families.
- [ ] Verify explicit proxy/direct container modes according to the selected test path without exposing secrets or changing socket permissions.
- [ ] Remove test-only host state if the installation is not intended to remain enrolled.
- [ ] Re-run both repositories' complete validation and clean local build/test artifacts.
- [ ] Report exact commits, release artifacts, tests, remaining limitations, disk cleanup, and that the app version remains unreleased until deployment is requested.
