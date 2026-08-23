#!/bin/sh
set -eu

SERVICE_USER=homelab-inventory-agent
INSTALL_ROOT=${HLI_INSTALL_ROOT:-}
purge=0
[ "${1:-}" != "--purge" ] || purge=1

if [ -z "$INSTALL_ROOT" ] && [ "$(id -u)" -ne 0 ]; then
  echo "Run the uninstaller with sudo." >&2
  exit 77
fi

detect_service_manager() {
  if [ -n "${HLI_TEST_INIT:-}" ]; then printf '%s\n' "$HLI_TEST_INIT"; return; fi
  systemd_service="$INSTALL_ROOT/etc/systemd/system/homelab-inventory-agent.service"
  openrc_service="$INSTALL_ROOT/etc/init.d/homelab-inventory-agent"
  if [ -f "$systemd_service" ] && [ ! -f "$openrc_service" ]; then printf '%s\n' systemd; return; fi
  if [ -f "$openrc_service" ] && [ ! -f "$systemd_service" ]; then printf '%s\n' openrc; return; fi
  if [ -f "$systemd_service" ] && [ -f "$openrc_service" ]; then
    echo "Multiple agent service managers were found; refusing an ambiguous uninstall." >&2
    exit 69
  fi
  if [ -z "$INSTALL_ROOT" ] && [ -d /run/openrc ]; then printf '%s\n' openrc; else printf '%s\n' systemd; fi
}

service_manager=$(detect_service_manager)
case "$service_manager" in
  systemd)
    if [ -z "$INSTALL_ROOT" ]; then systemctl disable --now homelab-inventory-agent.service >/dev/null 2>&1 || true; fi
    rm -f "$INSTALL_ROOT/etc/systemd/system/homelab-inventory-agent.service"
    if [ -z "$INSTALL_ROOT" ]; then systemctl daemon-reload; fi
    ;;
  openrc)
    if [ -z "$INSTALL_ROOT" ]; then
      rc-service homelab-inventory-agent stop >/dev/null 2>&1 || true
      rc-update del homelab-inventory-agent default >/dev/null 2>&1 || true
    fi
    rm -f "$INSTALL_ROOT/etc/init.d/homelab-inventory-agent"
    ;;
  *) echo "Unsupported agent service manager." >&2; exit 69 ;;
esac

rm -f "$INSTALL_ROOT/usr/local/sbin/homelab-inventory-agent"

if [ "$purge" -eq 1 ]; then
  rm -rf "$INSTALL_ROOT/etc/homelab-inventory-agent" "$INSTALL_ROOT/var/lib/homelab-inventory-agent" "$INSTALL_ROOT/run/homelab-inventory-agent"
  if [ -z "$INSTALL_ROOT" ]; then
    if command -v userdel >/dev/null 2>&1; then userdel "$SERVICE_USER" >/dev/null 2>&1 || true
    elif command -v deluser >/dev/null 2>&1; then deluser "$SERVICE_USER" >/dev/null 2>&1 || true
    fi
    if command -v groupdel >/dev/null 2>&1; then groupdel "$SERVICE_USER" >/dev/null 2>&1 || true
    elif command -v delgroup >/dev/null 2>&1; then delgroup "$SERVICE_USER" >/dev/null 2>&1 || true
    fi
  fi
  echo "Agent and local identity were removed."
else
  echo "Agent removed; configuration and identity were preserved. Use --purge to delete them."
fi
