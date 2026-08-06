#!/bin/sh
set -eu

VERSION=${1:-}
OUTPUT_DIRECTORY=${2:-dist}

case "$VERSION" in
  ''|*[!0-9A-Za-z.-]*)
    echo "usage: $0 <version> [output-directory]" >&2
    exit 64
    ;;
esac

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT_DIRECTORY=$(mkdir -p "$OUTPUT_DIRECTORY" && CDPATH='' cd -- "$OUTPUT_DIRECTORY" && pwd)
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0}
export SOURCE_DATE_EPOCH CGO_ENABLED=0
export LC_ALL=C

build() {
  os=$1
  arch=$2
  output="$OUTPUT_DIRECTORY/homelab-inventory-agent-$os-$arch"
  (
    cd "$ROOT"
    GOOS=$os GOARCH=$arch go build \
      -trimpath \
      -buildvcs=false \
      -ldflags "-s -w -buildid= -X main.version=$VERSION" \
      -o "$output" \
      ./cmd/agent
  )
  chmod 0755 "$output"
}

rm -f \
  "$OUTPUT_DIRECTORY"/homelab-inventory-agent-linux-amd64 \
  "$OUTPUT_DIRECTORY"/homelab-inventory-agent-linux-arm64 \
  "$OUTPUT_DIRECTORY"/homelab-inventory-agent-freebsd-amd64 \
  "$OUTPUT_DIRECTORY"/homelab-inventory-agent.service \
  "$OUTPUT_DIRECTORY"/homelab_inventory_agent \
  "$OUTPUT_DIRECTORY"/install.sh \
  "$OUTPUT_DIRECTORY"/install-freebsd.sh \
  "$OUTPUT_DIRECTORY"/uninstall.sh \
  "$OUTPUT_DIRECTORY"/uninstall-freebsd.sh \
  "$OUTPUT_DIRECTORY"/version.txt \
  "$OUTPUT_DIRECTORY"/checksums.txt
build linux amd64
build linux arm64
build freebsd amd64
cp "$ROOT/packaging/homelab-inventory-agent.service" "$OUTPUT_DIRECTORY/homelab-inventory-agent.service"
cp "$ROOT/packaging/homelab_inventory_agent" "$OUTPUT_DIRECTORY/homelab_inventory_agent"
cp "$ROOT/packaging/install.sh" "$OUTPUT_DIRECTORY/install.sh"
cp "$ROOT/packaging/install-freebsd.sh" "$OUTPUT_DIRECTORY/install-freebsd.sh"
cp "$ROOT/packaging/uninstall.sh" "$OUTPUT_DIRECTORY/uninstall.sh"
cp "$ROOT/packaging/uninstall-freebsd.sh" "$OUTPUT_DIRECTORY/uninstall-freebsd.sh"
chmod 0644 "$OUTPUT_DIRECTORY/homelab-inventory-agent.service"
chmod 0555 "$OUTPUT_DIRECTORY/homelab_inventory_agent"
chmod 0755 "$OUTPUT_DIRECTORY/install.sh" "$OUTPUT_DIRECTORY/install-freebsd.sh" "$OUTPUT_DIRECTORY/uninstall.sh" "$OUTPUT_DIRECTORY/uninstall-freebsd.sh"

printf '%s\n' "$VERSION" > "$OUTPUT_DIRECTORY/version.txt"

(
  cd "$OUTPUT_DIRECTORY"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum homelab-inventory-agent-* homelab-inventory-agent.service homelab_inventory_agent install.sh install-freebsd.sh uninstall.sh uninstall-freebsd.sh version.txt > checksums.txt
  else
    shasum -a 256 homelab-inventory-agent-* homelab-inventory-agent.service homelab_inventory_agent install.sh install-freebsd.sh uninstall.sh uninstall-freebsd.sh version.txt > checksums.txt
  fi
)
