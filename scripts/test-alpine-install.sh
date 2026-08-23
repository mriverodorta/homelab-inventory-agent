#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
VERSION=${1:-0.0.0-alpine-test}
ARCH=${2:-amd64}
case "$ARCH" in
  amd64) ALPINE_IMAGE='alpine:3.22@sha256:7c8cb692ae09657cbc4a3f3cbd0e8d5a2690ba38386aaaf252dbb060bf5eb2e6' ;;
  arm64) ALPINE_IMAGE='alpine:3.22@sha256:2c9d26f410d032d5b1525aa8a873e238b05b90c4ae8618743d4311f0cc827e37' ;;
  *) echo "usage: $0 [version] [amd64|arm64]" >&2; exit 64 ;;
esac
temporary=$(mktemp -d "${TMPDIR:-/tmp}/homelab-inventory-agent-alpine.XXXXXX")

cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT HUP INT TERM

command -v docker >/dev/null 2>&1 || { echo "Docker is required." >&2; exit 69; }
command -v go >/dev/null 2>&1 || { echo "Go is required." >&2; exit 69; }
docker info >/dev/null 2>&1 || { echo "Docker is unavailable." >&2; exit 69; }

assets="$temporary/assets"
mkdir -p "$assets"
"$ROOT/scripts/build-release.sh" "$VERSION" "$assets"
(
  cd "$ROOT"
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags='-s -w' -o "$temporary/test-server" ./internal/packaging/testfixture
)

docker run --rm --platform "linux/$ARCH" \
  --env HLI_TEST_VERSION="$VERSION" \
  --volume "$assets:/assets:ro" \
  --volume "$temporary/test-server:/usr/local/bin/hli-agent-test-server:ro" \
  "$ALPINE_IMAGE" \
  sh -ceu '
    mkdir -p /run/openrc /test-bin /var/run
    cat > /test-bin/rc-service <<"RC_SERVICE"
#!/bin/sh
printf "%s\n" "$*" >>/tmp/hli-rc-service.log
if [ "${3:-}" = "status" ] && [ -f /tmp/hli-openrc-fail-health ]; then exit 1; fi
exit 0
RC_SERVICE
    cat > /test-bin/rc-update <<"RC_UPDATE"
#!/bin/sh
printf "%s\n" "$*" >>/tmp/hli-rc-update.log
exit 0
RC_UPDATE
    chmod 0755 /test-bin/rc-service /test-bin/rc-update
    PATH=/test-bin:/usr/sbin:/usr/bin:/sbin:/bin
    export PATH

    addgroup -S -g 1555 docker-test
    /usr/local/bin/hli-agent-test-server -runtime-socket /var/run/docker.sock >/tmp/hli-test-server.log 2>&1 &
    fixture_pid=$!
    trap "kill $fixture_pid 2>/dev/null || true" EXIT HUP INT TERM
    check=0
    while [ ! -S /var/run/docker.sock ]; do
      check=$((check + 1))
      [ "$check" -lt 50 ] || { cat /tmp/hli-test-server.log >&2; exit 1; }
      sleep 0.1
    done
    chgrp docker-test /var/run/docker.sock
    chmod 0660 /var/run/docker.sock
    socket_before=$(stat -c "%u:%g:%a" /var/run/docker.sock)

    install_agent() {
      mode=$1
      endpoint=$2
      set -- --endpoint http://127.0.0.1:8080 --version "$HLI_TEST_VERSION" --host-type server --host-id 7 --enrollment-code disposable-test-code --containers-mode "$mode" --containers-runtime docker
      [ -z "$endpoint" ] || set -- "$@" --containers-endpoint "$endpoint"
      HLI_ASSET_DIR=/assets sh /assets/install.sh "$@"
    }

    install_agent disabled ""
    test -s /tmp/hli-heartbeat
    test "$(id -u homelab-inventory-agent)" -gt 0
    test -x /etc/init.d/homelab-inventory-agent
    grep -q "homelab-inventory-agent default" /tmp/hli-rc-update.log
    grep -q "homelab-inventory-agent start" /tmp/hli-rc-service.log
    if id -G homelab-inventory-agent | tr " " "\n" | grep -qx 1555; then
      echo "Disabled mode changed supplementary groups." >&2
      exit 1
    fi
    first_identity=$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")
    first_config=$(sha256sum /etc/homelab-inventory-agent/config.json | awk "{print \$1}")

    HLI_ASSET_DIR=/assets sh /assets/install.sh --endpoint http://127.0.0.1:8080 --version "$HLI_TEST_VERSION" --upgrade
    test "$first_identity" = "$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")"
    test "$first_config" = "$(sha256sum /etc/homelab-inventory-agent/config.json | awk "{print \$1}")"

    touch /tmp/hli-fail-activation
    if install_agent socket /var/run/docker.sock; then
      echo "Injected direct-socket activation failure was accepted." >&2
      exit 1
    fi
    rm -f /tmp/hli-fail-activation
    if id -G homelab-inventory-agent | tr " " "\n" | grep -qx 1555; then
      echo "Failed installation retained newly-added socket access." >&2
      exit 1
    fi
    test "$socket_before" = "$(stat -c "%u:%g:%a" /var/run/docker.sock)"

    rm -f /tmp/hli-runtime /tmp/hli-heartbeat
    install_agent socket /var/run/docker.sock
    id -G homelab-inventory-agent | tr " " "\n" | grep -qx 1555
    test -s /tmp/hli-runtime
    test -s /tmp/hli-heartbeat
    test "$socket_before" = "$(stat -c "%u:%g:%a" /var/run/docker.sock)"

    HLI_INSTALL_ROOT= HLI_TEST_INIT=openrc sh /assets/uninstall.sh
    test ! -e /usr/local/sbin/homelab-inventory-agent
    test ! -e /etc/init.d/homelab-inventory-agent
    test -e /etc/homelab-inventory-agent/config.json
    test -e /var/lib/homelab-inventory-agent/identity.json
    HLI_TEST_INIT=openrc sh /assets/uninstall.sh --purge
    test ! -e /etc/homelab-inventory-agent
    test ! -e /var/lib/homelab-inventory-agent

    echo "Alpine OpenRC installer transaction smoke test passed."
  '
