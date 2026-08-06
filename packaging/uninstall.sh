#!/bin/sh
set -eu

purge=0
[ "${1:-}" != "--purge" ] || purge=1
[ "$(id -u)" -eq 0 ] || { echo "Run the uninstaller with sudo." >&2; exit 77; }

systemctl disable --now homelab-inventory-agent.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/homelab-inventory-agent.service
rm -f /usr/local/sbin/homelab-inventory-agent
systemctl daemon-reload

if [ "$purge" -eq 1 ]; then
  rm -rf /etc/homelab-inventory-agent /var/lib/homelab-inventory-agent
  userdel homelab-inventory-agent >/dev/null 2>&1 || true
  groupdel homelab-inventory-agent >/dev/null 2>&1 || true
  echo "Agent and local identity were removed."
else
  echo "Agent removed; configuration and identity were preserved. Use --purge to delete them."
fi
