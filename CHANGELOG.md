# Changelog

## Unreleased

### Added

- Alpine Linux 3.22 and capability-based OpenRC support for AMD64 and ARM64 hosts.
- OpenRC service telemetry with active, enabled, description, and package classification fields when locally available.
- Manager-aware installation, uninstallation, rollback, and native updates for systemd and OpenRC.
- Explicit direct Docker or Podman socket access through verified supplementary-group membership without changing socket ownership or permissions.

### Changed

- Linux hardware inventory now resolves fixed trusted executable locations and remains useful when optional `dmidecode`, `lspci`, or `smartctl` tools are absent.
- Linux telemetry now reports the operating system, distribution, and init system independently.
