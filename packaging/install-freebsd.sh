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
containers_mode=disabled
containers_runtime=docker
containers_endpoint=

usage() {
  echo "usage: install-freebsd.sh [--endpoint URL --host-type TYPE --host-id ID --enrollment-code CODE] [--version VERSION] [--containers-mode disabled|proxy|socket] [--containers-runtime docker|podman] [--containers-endpoint URL_OR_SOCKET] [--upgrade]" >&2
  exit 64
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --endpoint) [ "$#" -ge 2 ] || usage; endpoint=$2; shift 2 ;;
    --version) [ "$#" -ge 2 ] || usage; version=$2; shift 2 ;;
    --host-type) [ "$#" -ge 2 ] || usage; host_type=$2; shift 2 ;;
    --host-id) [ "$#" -ge 2 ] || usage; host_id=$2; shift 2 ;;
    --enrollment-code) [ "$#" -ge 2 ] || usage; enrollment_code=$2; shift 2 ;;
    --containers-mode) [ "$#" -ge 2 ] || usage; containers_mode=$2; shift 2 ;;
    --containers-runtime) [ "$#" -ge 2 ] || usage; containers_runtime=$2; shift 2 ;;
    --containers-endpoint) [ "$#" -ge 2 ] || usage; containers_endpoint=$2; shift 2 ;;
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
case "$version" in ''|'__HLI_AGENT_VERSION__'|.*|*..*|*[!0-9A-Za-z.-]*) echo "A valid --version is required." >&2; exit 64 ;; esac
case "$containers_mode" in disabled|proxy|socket) ;; *) echo "Invalid --containers-mode." >&2; exit 64 ;; esac
case "$containers_runtime" in docker|podman) ;; *) echo "Invalid --containers-runtime." >&2; exit 64 ;; esac
case "$containers_endpoint" in *[\"\'\\\`\$\;\|\&\<\>\(\)\{\}]*|*' '*) echo "Container endpoint contains unsupported characters." >&2; exit 64 ;; esac
if [ "$containers_mode" = disabled ]; then containers_endpoint=; elif [ -z "$containers_endpoint" ]; then echo "--containers-endpoint is required when container collection is enabled." >&2; exit 64; fi

if [ -n "$INSTALL_ROOT" ] && [ -n "${HLI_TEST_OS:-}" ]; then
  os=$HLI_TEST_OS
  arch=${HLI_TEST_ARCH:-amd64}
else
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
fi
[ "$os" = freebsd ] || { echo "This installer supports FreeBSD only." >&2; exit 69; }
case "$arch" in x86_64|amd64) arch=amd64 ;; *) echo "Unsupported FreeBSD architecture: $arch" >&2; exit 69 ;; esac

opnsense=0
if [ "${HLI_TEST_OPNSENSE:-0}" = 1 ] || [ -d "$INSTALL_ROOT/usr/local/opnsense" ]; then
  opnsense=1
fi
if [ "$opnsense" -eq 1 ]; then
  runtime_state_directory=/conf/homelab-inventory-agent
else
  runtime_state_directory=/var/db/homelab-inventory-agent
fi

binary_path="$INSTALL_ROOT/usr/local/sbin/homelab-inventory-agent"
config_directory="$INSTALL_ROOT/usr/local/etc/homelab-inventory-agent"
config_path="$config_directory/config.json"
state_directory="$INSTALL_ROOT$runtime_state_directory"
identity_path="$state_directory/identity.json"
service_path="$INSTALL_ROOT/usr/local/etc/rc.d/homelab_inventory_agent"
filename=homelab-inventory-agent-freebsd-amd64

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
        rm -f "$target"
        cp -p "$temporary/$entry.previous" "$target"
      elif [ -f "$temporary/$entry.created" ]; then
        rm -f "$target"
      fi
    done
    if [ -f "$temporary/state.transaction" ]; then
      rm -rf "$state_directory"
      if [ -d "$temporary/state.previous" ]; then
        install -d -m 0755 "$(dirname "$state_directory")"
        cp -Rp "$temporary/state.previous" "$state_directory"
      fi
    fi
    if [ -z "$INSTALL_ROOT" ]; then
      service homelab_inventory_agent restart >/dev/null 2>&1 || true
    fi
  fi
  rm -f "$state_directory/.enrollment-code"
  rm -rf "$temporary"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

service_is_sustained_healthy() {
  check=1
  while [ "$check" -le 3 ]; do
    service homelab_inventory_agent onestatus >/dev/null || return 1
    [ "$check" -eq 3 ] || sleep 1
    check=$((check + 1))
  done
}

if [ -n "$ASSET_DIRECTORY" ]; then
  cp "$ASSET_DIRECTORY/$filename" "$temporary/$filename"
  cp "$ASSET_DIRECTORY/checksums.txt" "$temporary/checksums.txt"
  cp "$ASSET_DIRECTORY/homelab_inventory_agent" "$temporary/service"
else
  command -v fetch >/dev/null 2>&1 || { echo "fetch is required." >&2; exit 69; }
  asset_base="${endpoint%/}/api/agent/releases/$version"
  fetch -q -o "$temporary/$filename" "$asset_base/$filename"
  fetch -q -o "$temporary/checksums.txt" "$asset_base/checksums.txt"
  fetch -q -o "$temporary/service" "$asset_base/homelab_inventory_agent"
fi

verify_checksum() {
  file=$1
  path=$2
  expected=$(awk -v file="$file" '$NF == file || $NF == "*" file { print $1 }' "$temporary/checksums.txt")
  [ -n "$expected" ] || { echo "Checksum for $file is missing." >&2; exit 65; }
  actual=$(sha256 -q "$path")
  [ "$actual" = "$expected" ] || { echo "Checksum verification failed for $file." >&2; exit 65; }
}

if [ -n "$INSTALL_ROOT" ]; then
  actual=$(shasum -a 256 "$temporary/$filename" | awk '{print $1}')
  expected=$(awk -v file="$filename" '$NF == file || $NF == "*" file { print $1 }' "$temporary/checksums.txt")
  [ "$actual" = "$expected" ] || { echo "Checksum verification failed for $filename." >&2; exit 65; }
  actual=$(shasum -a 256 "$temporary/service" | awk '{print $1}')
  expected=$(awk '$NF == "homelab_inventory_agent" || $NF == "*homelab_inventory_agent" { print $1 }' "$temporary/checksums.txt")
  [ "$actual" = "$expected" ] || { echo "Checksum verification failed for homelab_inventory_agent." >&2; exit 65; }
else
  verify_checksum "$filename" "$temporary/$filename"
  verify_checksum homelab_inventory_agent "$temporary/service"
fi
chmod 0755 "$temporary/$filename" "$temporary/service"
if [ -z "$INSTALL_ROOT" ] || [ "${HLI_TEST_SKIP_BINARY_EXEC:-0}" != 1 ]; then
  [ "$("$temporary/$filename" -version)" = "$version" ] || { echo "Agent version verification failed." >&2; exit 65; }
fi

for pair in "$binary_path:binary" "$config_path:config" "$service_path:service"; do
  target=${pair%:*}
  name=${pair#*:}
  if [ -f "$target" ]; then cp "$target" "$temporary/$name.previous"; else : > "$temporary/$name.created"; fi
done

if [ -z "$INSTALL_ROOT" ]; then
  pw groupshow "$SERVICE_USER" >/dev/null 2>&1 || pw groupadd "$SERVICE_USER"
  pw usershow "$SERVICE_USER" >/dev/null 2>&1 || pw useradd "$SERVICE_USER" -g "$SERVICE_USER" -d /nonexistent -s /usr/sbin/nologin -c "Homelab Inventory Agent"
  service homelab_inventory_agent stop >/dev/null 2>&1 || true
fi

if [ -d "$state_directory" ]; then
  cp -Rp "$state_directory" "$temporary/state.previous"
fi
: > "$temporary/state.transaction"
if [ "$upgrade" -eq 0 ]; then
  rm -rf "$state_directory"
fi

install -d -m 0755 "$(dirname "$binary_path")" "$(dirname "$service_path")"
install -d -m 0750 "$config_directory"
install -d -m 0700 "$state_directory"
install -m 0755 "$temporary/$filename" "$binary_path"
install -m 0555 "$temporary/service" "$service_path"

if [ "$upgrade" -eq 0 ]; then
  umask 077
  cat > "$temporary/config.json" <<EOF
{"endpoint":"$endpoint","host":{"type":"$host_type","id":$host_id},"stateDirectory":"$runtime_state_directory","containers":{"mode":"$containers_mode","runtime":"$containers_runtime","endpoint":"$containers_endpoint"}}
EOF
  install -m 0640 "$temporary/config.json" "$config_path"
fi

if [ -n "$INSTALL_ROOT" ] && [ "${HLI_TEST_FAIL_AFTER_INSTALL:-0}" = 1 ]; then
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
    su -m "$SERVICE_USER" -c "$binary_path -config $config_path -enrollment-code-file $runtime_state_directory/.enrollment-code -once"
    rm -f "$enrollment_file"
  fi
  sysrc homelab_inventory_agent_enable=YES >/dev/null
  service homelab_inventory_agent restart
  service_is_sustained_healthy || {
    service homelab_inventory_agent onestatus >&2 || true
    echo "The updated agent service did not remain healthy; restoring the previous installation." >&2
    exit 70
  }
fi

rollback=0
echo "Homelab Inventory Agent $version installed successfully."
