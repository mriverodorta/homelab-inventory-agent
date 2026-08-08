#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
UBUNTU_IMAGE='ubuntu:24.04@sha256:561618e2c15bf2397621dd04f96926663a3b5616c189cf7e38db7e82f5c538ea'
VERSION=${1:-0.0.0-ubuntu-test}
temporary=$(mktemp -d "${TMPDIR:-/tmp}/homelab-inventory-agent-ubuntu.XXXXXX")

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
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$temporary/test-server" ./internal/packaging/testfixture
)

docker run --rm --platform linux/amd64 \
  --env HLI_TEST_VERSION="$VERSION" \
  --volume "$assets:/assets:ro" \
  --volume "$temporary/test-server:/usr/local/bin/hli-agent-test-server:ro" \
  "$UBUNTU_IMAGE" \
  sh -ceu '
    mkdir -p /test-bin
    cat > /test-bin/systemctl <<"SYSTEMCTL"
#!/bin/sh
if [ "${1:-}" = "is-active" ] && [ -f /tmp/hli-systemctl-fail-after-first ]; then
  count=0
  [ ! -f /tmp/hli-systemctl-health-count ] || count=$(cat /tmp/hli-systemctl-health-count)
  count=$((count + 1))
  printf "%s\n" "$count" >/tmp/hli-systemctl-health-count
  [ "$count" -lt 2 ] || exit 1
fi
exit 0
SYSTEMCTL
    chmod 0755 /test-bin/systemctl
    PATH=/test-bin:/usr/sbin:/usr/bin:/sbin:/bin
    export PATH

    /usr/local/bin/hli-agent-test-server >/tmp/hli-test-server.log 2>&1 &
    fixture_pid=$!
    trap "kill $fixture_pid 2>/dev/null || true" EXIT HUP INT TERM
    sleep 1

    install_agent() {
      mode=$1
      runtime_endpoint=$2
      if [ "$mode" = disabled ]; then
        HLI_ASSET_DIR=/assets sh /assets/install.sh \
          --endpoint http://127.0.0.1:8080 \
          --version "$HLI_TEST_VERSION" \
          --host-type server \
          --host-id 7 \
          --enrollment-code disposable-test-code \
          --containers-mode disabled \
          --containers-runtime docker
      else
        HLI_ASSET_DIR=/assets sh /assets/install.sh \
          --endpoint http://127.0.0.1:8080 \
          --version "$HLI_TEST_VERSION" \
          --host-type server \
          --host-id 7 \
          --enrollment-code disposable-test-code \
          --containers-mode "$mode" \
          --containers-runtime docker \
          --containers-endpoint "$runtime_endpoint"
      fi
    }

    install_agent disabled ""
    test -s /tmp/hli-heartbeat
    first_identity=$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")
    first_config=$(sha256sum /etc/homelab-inventory-agent/config.json | awk "{print \$1}")

    touch /tmp/hli-systemctl-fail-after-first
    rm -f /tmp/hli-systemctl-health-count
    if HLI_ASSET_DIR=/assets sh /assets/install.sh \
      --endpoint http://127.0.0.1:8080 \
      --version "$HLI_TEST_VERSION" \
      --upgrade; then
      echo "Crash-looping service passed the sustained health gate." >&2
      exit 1
    fi
    rm -f /tmp/hli-systemctl-fail-after-first /tmp/hli-systemctl-health-count
    test "$first_identity" = "$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")"
    test "$first_config" = "$(sha256sum /etc/homelab-inventory-agent/config.json | awk "{print \$1}")"

    touch /tmp/hli-fail-activation
    if install_agent proxy http://127.0.0.1:2375; then
      echo "Injected activation failure was accepted." >&2
      exit 1
    fi
    test "$first_identity" = "$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")"
    test "$first_config" = "$(sha256sum /etc/homelab-inventory-agent/config.json | awk "{print \$1}")"

    rm -f /tmp/hli-fail-activation /tmp/hli-heartbeat /tmp/hli-runtime
    install_agent proxy http://127.0.0.1:2375
    second_identity=$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")
    test "$first_identity" != "$second_identity"
    test -s /tmp/hli-heartbeat || { cat /tmp/hli-test-server.log >&2; echo "Replacement heartbeat was not delivered." >&2; exit 1; }
    test -s /tmp/hli-runtime || { cat /tmp/hli-test-server.log >&2; cat /etc/homelab-inventory-agent/config.json >&2; echo "Container runtime was not queried." >&2; exit 1; }

    HLI_ASSET_DIR=/assets sh /assets/install.sh \
      --endpoint http://127.0.0.1:8080 \
      --version "$HLI_TEST_VERSION" \
      --upgrade
    test "$second_identity" = "$(sha256sum /var/lib/homelab-inventory-agent/identity.json | awk "{print \$1}")"
    test "$(stat -c %a /var/lib/homelab-inventory-agent/identity.json)" = 600

    sed -E -i "s/(\"schemaBundleDigest\":\")[a-f0-9]{64}(\")/\1$(printf 0%.0s $(seq 1 64))\2/" \
      /var/lib/homelab-inventory-agent/contract.json
    grep -q "$(printf 0%.0s $(seq 1 64))" /var/lib/homelab-inventory-agent/contract.json
    runuser -u homelab-inventory-agent -- \
      /usr/local/sbin/homelab-inventory-agent \
      -config /etc/homelab-inventory-agent/config.json \
      -once
    if grep -q "$(printf 0%.0s $(seq 1 64))" /var/lib/homelab-inventory-agent/contract.json; then
      echo "Incompatible contract cache was not refreshed." >&2
      exit 1
    fi

    echo "Ubuntu installer transaction smoke test passed."
  '
