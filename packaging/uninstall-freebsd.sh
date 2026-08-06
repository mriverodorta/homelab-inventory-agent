#!/bin/sh
set -eu

purge=0
[ "${1:-}" != "--purge" ] || purge=1
[ "$(id -u)" -eq 0 ] || { echo "Run the uninstaller with sudo." >&2; exit 77; }

service homelab_inventory_agent stop >/dev/null 2>&1 || true
sysrc -x homelab_inventory_agent_enable >/dev/null 2>&1 || true
rm -f /usr/local/etc/rc.d/homelab_inventory_agent
rm -f /usr/local/sbin/homelab-inventory-agent

if [ "$purge" -eq 1 ]; then
  rm -rf /usr/local/etc/homelab-inventory-agent /var/db/homelab-inventory-agent /conf/homelab-inventory-agent
  pw userdel homelab-inventory-agent >/dev/null 2>&1 || true
  pw groupdel homelab-inventory-agent >/dev/null 2>&1 || true
  echo "Agent and local identity were removed."
else
  echo "Agent removed; configuration and identity were preserved. Use --purge to delete them."
fi
