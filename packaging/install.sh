#!/bin/sh
set -eu

DEFAULT_ENDPOINT='__HLI_ENDPOINT__'
DEFAULT_VERSION='__HLI_AGENT_VERSION__'
SERVICE_USER=homelab-inventory-agent
INSTALL_ROOT=${HLI_INSTALL_ROOT:-}
ASSET_DIRECTORY=${HLI_ASSET_DIR:-}

endpoint=$DEFAULT_ENDPOINT
version=$DEFAULT_VERSION
host_type=
host_id=
enrollment_code=
upgrade=0

usage() {
  echo "usage: install.sh [--endpoint URL --host-type TYPE --host-id ID --enrollment-code CODE] [--version VERSION] [--upgrade]" >&2
  exit 64
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --endpoint) [ "$#" -ge 2 ] || usage; endpoint=$2; shift 2 ;;
    --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
    --host-type) [ "$#" -ge 2 ] || usage; host_type=$2; shift 2 ;;
    --host-id) [ "$#" -ge 2 ] || usage; host_id=$2; shift 2 ;;
    --enrollment-code) [ "$#" -ge 2 ] || usage; enrollment_code=$2; shift 2 ;;
    --upgrade) upgrade=1; shift ;;
    *) usage ;;
  esac
done

case "$endpoint" in
  http://*|https://*) ;;
  *) echo "A valid --endpoint is required." >&2; exit 64 ;;
esac
case "$endpoint" in *[\"\'\\\`\$\;\|\&\<\>\(\)\{\}]*|*' '*) echo "Endpoint contains unsupported characters." >&2; exit 64 ;; esac
authority=${endpoint#*://}
case "$authority" in ''|*/*|*\?*|*#*|*@*) echo "Endpoint must contain only a scheme and host." >&2; exit 64 ;; esac
case "$version" in ''|'__HLI_AGENT_VERSION__'|*[!0-9A-Za-z.-]*) echo "A valid --version is required." >&2; exit 64 ;; esac

if [ -n "$INSTALL_ROOT" ] && [ -n "${HLI_TEST_OS:-}" ] && [ -n "${HLI_TEST_ARCH:-}" ]; then
  os=$HLI_TEST_OS
  arch=$HLI_TEST_ARCH
else
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
fi
case "$os" in linux) ;; *) echo "This installer supports Linux only." >&2; exit 69 ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Unsupported architecture: $arch" >&2; exit 69 ;; esac

binary_path="$INSTALL_ROOT/usr/local/sbin/homelab-inventory-agent"
config_directory="$INSTALL_ROOT/etc/homelab-inventory-agent"
config_path="$config_directory/config.json"
state_directory="$INSTALL_ROOT/var/lib/homelab-inventory-agent"
identity_path="$state_directory/identity.json"
service_path="$INSTALL_ROOT/etc/systemd/system/homelab-inventory-agent.service"
filename="homelab-inventory-agent-linux-$arch"

if [ -z "$INSTALL_ROOT" ] && [ "$(id -u)" -ne 0 ]; then
  echo "Run the installer with sudo." >&2
  exit 77
fi

if [ "$upgrade" -eq 0 ]; then
  case "$host_type" in server|nas|pcBuild) ;; *) echo "A valid --host-type is required." >&2; exit 64 ;; esac
  case "$host_id" in ''|*[!0-9]*|0|0*) echo "A positive numeric --host-id is required." >&2; exit 64 ;; esac
  [ "$host_id" -le 9007199254740991 ] || { echo "--host-id exceeds the safe integer range." >&2; exit 64; }
  [ -n "$enrollment_code" ] || { echo "A short-lived --enrollment-code is required." >&2; exit 64; }
elif [ ! -f "$config_path" ] || [ ! -f "$identity_path" ]; then
  echo "Upgrade requires an existing configuration and identity." >&2
  exit 66
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/homelab-inventory-agent.XXXXXX")
rollback=1
cleanup() {
  status=$?
  if [ "$rollback" -eq 1 ]; then
    for entry in binary config service; do
      eval target=\$${entry}_path
      if [ -f "$temporary/$entry.previous" ]; then
        cp "$temporary/$entry.previous" "$target"
      elif [ -f "$temporary/$entry.created" ]; then
        rm -f "$target"
      fi
    done
    if [ -z "$INSTALL_ROOT" ] && command -v systemctl >/dev/null 2>&1; then
      systemctl daemon-reload >/dev/null 2>&1 || true
      systemctl restart homelab-inventory-agent.service >/dev/null 2>&1 || true
    fi
  fi
  rm -f "$state_directory/.enrollment-code"
  rm -rf "$temporary"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

if [ -n "$ASSET_DIRECTORY" ]; then
  cp "$ASSET_DIRECTORY/$filename" "$temporary/$filename"
  cp "$ASSET_DIRECTORY/checksums.txt" "$temporary/checksums.txt"
  cp "$ASSET_DIRECTORY/homelab-inventory-agent.service" "$temporary/service"
else
  command -v curl >/dev/null 2>&1 || { echo "curl is required." >&2; exit 69; }
  asset_base="${endpoint%/}/api/agent/releases/$version"
  if [ "${endpoint#https://}" != "$endpoint" ]; then
    protocols='=https'
  else
    protocols='=http,https'
  fi
  curl -fsSL --proto "$protocols" --proto-redir '=https' --tlsv1.2 "$asset_base/$filename" -o "$temporary/$filename"
  curl -fsSL --proto "$protocols" --proto-redir '=https' --tlsv1.2 "$asset_base/checksums.txt" -o "$temporary/checksums.txt"
  curl -fsSL --proto "$protocols" --proto-redir '=https' --tlsv1.2 "$asset_base/homelab-inventory-agent.service" -o "$temporary/service"
fi

verify_checksum() {
  file=$1
  path=$2
  expected=$(awk -v file="$file" '$NF == file || $NF == "*" file { print $1 }' "$temporary/checksums.txt")
  [ -n "$expected" ] || { echo "Checksum for $file is missing." >&2; exit 65; }
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$path" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "$path" | awk '{print $1}')
  fi
  [ "$actual" = "$expected" ] || { echo "Checksum verification failed for $file." >&2; exit 65; }
}

verify_checksum "$filename" "$temporary/$filename"
verify_checksum homelab-inventory-agent.service "$temporary/service"
chmod 0755 "$temporary/$filename"
if [ -z "$INSTALL_ROOT" ] || [ "${HLI_TEST_SKIP_BINARY_EXEC:-0}" != "1" ]; then
  [ "$("$temporary/$filename" -version)" = "$version" ] || { echo "Agent version verification failed." >&2; exit 65; }
fi

for pair in "$binary_path:binary" "$config_path:config" "$service_path:service"; do
  target=${pair%:*}
  name=${pair#*:}
  if [ -f "$target" ]; then cp "$target" "$temporary/$name.previous"; else : > "$temporary/$name.created"; fi
done

if [ -z "$INSTALL_ROOT" ]; then
  getent group "$SERVICE_USER" >/dev/null 2>&1 || groupadd --system "$SERVICE_USER"
  id "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --gid "$SERVICE_USER" --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  systemctl stop homelab-inventory-agent.service >/dev/null 2>&1 || true
fi

install -d -m 0755 "$(dirname "$binary_path")" "$(dirname "$service_path")"
install -d -m 0750 "$config_directory"
install -d -m 0700 "$state_directory"
install -m 0755 "$temporary/$filename" "$binary_path"
install -m 0644 "$temporary/service" "$service_path"

if [ "$upgrade" -eq 0 ]; then
  umask 077
  cat > "$temporary/config.json" <<EOF
{"endpoint":"$endpoint","host":{"type":"$host_type","id":$host_id},"stateDirectory":"/var/lib/homelab-inventory-agent"}
EOF
  install -m 0640 "$temporary/config.json" "$config_path"
fi

if [ -n "$INSTALL_ROOT" ] && [ "${HLI_TEST_FAIL_AFTER_INSTALL:-0}" = "1" ]; then
  echo "Injected packaging test failure." >&2
  exit 70
fi

if [ -z "$INSTALL_ROOT" ]; then
  chown -R "$SERVICE_USER:$SERVICE_USER" "$state_directory"
  chown root:"$SERVICE_USER" "$config_directory" "$config_path"
  if [ "$upgrade" -eq 0 ]; then
    enrollment_file="$state_directory/.enrollment-code"
    printf '%s\n' "$enrollment_code" > "$enrollment_file"
    chown "$SERVICE_USER:$SERVICE_USER" "$enrollment_file"
    chmod 0600 "$enrollment_file"
    runuser -u "$SERVICE_USER" -- "$binary_path" -config "$config_path" -enrollment-code-file "$enrollment_file" -once
    rm -f "$enrollment_file"
  fi
  systemctl daemon-reload
  systemctl enable homelab-inventory-agent.service >/dev/null
  systemctl restart homelab-inventory-agent.service
  systemctl is-active --quiet homelab-inventory-agent.service
fi

rollback=0
echo "Homelab Inventory Agent $version installed successfully."
