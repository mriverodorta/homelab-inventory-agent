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
  echo "usage: install.sh [--endpoint URL --host-type TYPE --host-id ID --enrollment-code CODE] [--version VERSION] [--containers-mode disabled|proxy|socket] [--containers-runtime docker|podman] [--containers-endpoint URL_OR_SOCKET] [--upgrade]" >&2
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

if [ -n "$INSTALL_ROOT" ] && [ -n "${HLI_TEST_OS:-}" ] && [ -n "${HLI_TEST_ARCH:-}" ]; then
  os=$HLI_TEST_OS
  arch=$HLI_TEST_ARCH
else
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
fi
case "$os" in linux) ;; *) echo "This installer supports Linux only." >&2; exit 69 ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Unsupported architecture: $arch" >&2; exit 69 ;; esac

detect_service_manager() {
  if [ -n "${HLI_TEST_INIT:-}" ]; then
    case "$HLI_TEST_INIT" in systemd|openrc) printf '%s\n' "$HLI_TEST_INIT"; return ;; *) echo "Unsupported test init manager." >&2; exit 69 ;; esac
  fi
  if [ -n "$INSTALL_ROOT" ]; then
    printf '%s\n' systemd
    return
  fi
  systemd_detected=0
  openrc_detected=0
  if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then systemd_detected=1; fi
  if [ -d /run/openrc ] && command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then openrc_detected=1; fi
  if [ "$systemd_detected" -eq 1 ] && [ "$openrc_detected" -eq 0 ]; then printf '%s\n' systemd; return; fi
  if [ "$openrc_detected" -eq 1 ] && [ "$systemd_detected" -eq 0 ]; then printf '%s\n' openrc; return; fi
  if [ "$systemd_detected" -eq 1 ] && [ "$openrc_detected" -eq 1 ]; then
    echo "Multiple supported Linux service managers were detected." >&2
  else
    echo "No supported Linux service manager was detected." >&2
  fi
  exit 69
}

service_manager=$(detect_service_manager)

binary_path="$INSTALL_ROOT/usr/local/sbin/homelab-inventory-agent"
config_directory="$INSTALL_ROOT/etc/homelab-inventory-agent"
config_path="$config_directory/config.json"
state_directory="$INSTALL_ROOT/var/lib/homelab-inventory-agent"
identity_path="$state_directory/identity.json"
case "$service_manager" in
  systemd)
    service_path="$INSTALL_ROOT/etc/systemd/system/homelab-inventory-agent.service"
    service_asset=homelab-inventory-agent.service
    service_mode=0644
    ;;
  openrc)
    service_path="$INSTALL_ROOT/etc/init.d/homelab-inventory-agent"
    service_asset=homelab-inventory-agent.openrc
    service_mode=0555
    ;;
esac
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
socket_group=
socket_group_added=0
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
    if [ "$socket_group_added" -eq 1 ]; then
      remove_service_group_membership "$socket_group" >/dev/null 2>&1 || true
    fi
    if [ -z "$INSTALL_ROOT" ]; then
      service_reload >/dev/null 2>&1 || true
      service_start >/dev/null 2>&1 || true
    fi
  fi
  rm -f "$state_directory/.enrollment-code"
  rm -rf "$temporary"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

service_stop() {
  [ -z "$INSTALL_ROOT" ] || return 0
  case "$service_manager" in
    systemd) systemctl stop homelab-inventory-agent.service >/dev/null 2>&1 || true ;;
    openrc) rc-service homelab-inventory-agent stop >/dev/null 2>&1 || true ;;
  esac
}

service_reload() {
  [ -z "$INSTALL_ROOT" ] || return 0
  [ "$service_manager" != systemd ] || systemctl daemon-reload
}

service_enable() {
  [ -z "$INSTALL_ROOT" ] || return 0
  case "$service_manager" in
    systemd) systemctl enable homelab-inventory-agent.service >/dev/null ;;
    openrc) rc-update add homelab-inventory-agent default >/dev/null ;;
  esac
}

service_start() {
  [ -z "$INSTALL_ROOT" ] || return 0
  case "$service_manager" in
    systemd) systemctl restart homelab-inventory-agent.service ;;
    openrc) rc-service homelab-inventory-agent start ;;
  esac
}

service_is_sustained_healthy() {
  check=1
  while [ "$check" -le 3 ]; do
    case "$service_manager" in
      systemd) systemctl is-active --quiet homelab-inventory-agent.service || return 1 ;;
      openrc) rc-service homelab-inventory-agent status >/dev/null 2>&1 || return 1 ;;
    esac
    [ "$check" -eq 3 ] || sleep 1
    check=$((check + 1))
  done
}

if [ -n "$ASSET_DIRECTORY" ]; then
  cp "$ASSET_DIRECTORY/$filename" "$temporary/$filename"
  cp "$ASSET_DIRECTORY/checksums.txt" "$temporary/checksums.txt"
  cp "$ASSET_DIRECTORY/$service_asset" "$temporary/service"
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
  curl -fsSL --proto "$protocols" --proto-redir '=https' --tlsv1.2 "$asset_base/$service_asset" -o "$temporary/service"
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
verify_checksum "$service_asset" "$temporary/service"
chmod 0755 "$temporary/$filename"
if [ -z "$INSTALL_ROOT" ] || [ "${HLI_TEST_SKIP_BINARY_EXEC:-0}" != "1" ]; then
  [ "$("$temporary/$filename" -version)" = "$version" ] || { echo "Agent version verification failed." >&2; exit 65; }
fi

for pair in "$binary_path:binary" "$config_path:config" "$service_path:service"; do
  target=${pair%:*}
  name=${pair#*:}
  if [ -f "$target" ]; then cp "$target" "$temporary/$name.previous"; else : > "$temporary/$name.created"; fi
done

group_exists() {
  if command -v getent >/dev/null 2>&1; then getent group "$1" >/dev/null 2>&1; else grep -q "^$1:" /etc/group; fi
}

ensure_service_account() {
  if ! group_exists "$SERVICE_USER"; then
    if command -v groupadd >/dev/null 2>&1; then groupadd --system "$SERVICE_USER"
    elif command -v addgroup >/dev/null 2>&1; then addgroup -S "$SERVICE_USER"
    else echo "No supported system-group command is available." >&2; exit 69
    fi
  fi
  if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then useradd --system --gid "$SERVICE_USER" --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    elif command -v adduser >/dev/null 2>&1; then adduser -S -D -H -h /nonexistent -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
    else echo "No supported system-user command is available." >&2; exit 69
    fi
  fi
  user_id=$(id -u "$SERVICE_USER")
  case "$user_id" in ''|*[!0-9]*|0) echo "The agent account must exist and be unprivileged." >&2; exit 77 ;; esac
}

service_has_group() {
  target_gid=$1
  for member_gid in $(id -G "$SERVICE_USER"); do
    [ "$member_gid" != "$target_gid" ] || return 0
  done
  return 1
}

group_name_for_gid() {
  target_gid=$1
  if command -v getent >/dev/null 2>&1; then
    getent group "$target_gid" 2>/dev/null | awk -F: -v gid="$target_gid" '$3 == gid { print $1; exit }'
  else
    awk -F: -v gid="$target_gid" '$3 == gid { print $1; exit }' /etc/group
  fi
}

add_service_group_membership() {
  group_name=$1
  if command -v usermod >/dev/null 2>&1; then usermod -a -G "$group_name" "$SERVICE_USER"
  elif command -v addgroup >/dev/null 2>&1; then addgroup "$SERVICE_USER" "$group_name"
  else echo "No supported supplementary-group command is available." >&2; return 69
  fi
}

remove_service_group_membership() {
  group_name=$1
  if command -v gpasswd >/dev/null 2>&1; then gpasswd -d "$SERVICE_USER" "$group_name"
  elif command -v delgroup >/dev/null 2>&1; then delgroup "$SERVICE_USER" "$group_name"
  elif command -v deluser >/dev/null 2>&1; then deluser "$SERVICE_USER" "$group_name"
  else return 69
  fi
}

verify_socket_access() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u "$SERVICE_USER" -- /bin/sh -c 'test -r "$1" && test -w "$1"' sh "$containers_endpoint"
  else
    su -s /bin/sh -c "test -r '$containers_endpoint' && test -w '$containers_endpoint'" "$SERVICE_USER"
  fi
}

configure_direct_socket_access() {
  [ "$containers_mode" = socket ] || return 0
  [ -S "$containers_endpoint" ] || { echo "The configured container endpoint is not a Unix socket." >&2; return 69; }
  socket_gid=$(stat -c '%g' "$containers_endpoint" 2>/dev/null) || { echo "Unable to read the container socket group." >&2; return 69; }
  case "$socket_gid" in ''|*[!0-9]*) echo "The container socket group is invalid." >&2; return 69 ;; esac
  socket_group=$(group_name_for_gid "$socket_gid")
  case "$socket_group" in ''|*[!A-Za-z0-9_.-]*) echo "The container socket group is not a safe local group." >&2; return 69 ;; esac
  if ! service_has_group "$socket_gid"; then
    add_service_group_membership "$socket_group"
    socket_group_added=1
  fi
  service_has_group "$socket_gid" || { echo "The agent account was not added to the container socket group." >&2; return 77; }
  verify_socket_access || { echo "The agent account cannot access the configured container socket." >&2; return 77; }
}

run_enrollment_as_service_user() {
  if command -v runuser >/dev/null 2>&1; then
    runuser -u "$SERVICE_USER" -- "$binary_path" -config "$config_path" -enrollment-code-file "$enrollment_file" -once
  elif command -v su >/dev/null 2>&1; then
    su -s /bin/sh -c "$binary_path -config $config_path -enrollment-code-file $enrollment_file -once" "$SERVICE_USER"
  else
    echo "No supported user-switching command is available." >&2
    return 69
  fi
}

if [ -z "$INSTALL_ROOT" ]; then
  ensure_service_account
  configure_direct_socket_access
  service_stop
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
install -m "$service_mode" "$temporary/service" "$service_path"

if [ "$upgrade" -eq 0 ]; then
  umask 077
  cat > "$temporary/config.json" <<EOF
{"endpoint":"$endpoint","host":{"type":"$host_type","id":$host_id},"stateDirectory":"/var/lib/homelab-inventory-agent","containers":{"mode":"$containers_mode","runtime":"$containers_runtime","endpoint":"$containers_endpoint"}}
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
    run_enrollment_as_service_user
    rm -f "$enrollment_file"
  fi
  service_reload
  service_enable
  service_start
  service_is_sustained_healthy || {
    echo "The updated agent service did not remain healthy; restoring the previous installation." >&2
    exit 70
  }
fi

rollback=0
echo "Homelab Inventory Agent $version installed successfully."
