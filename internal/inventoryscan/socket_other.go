//go:build !linux && !freebsd

package inventoryscan

import (
	"errors"
	"net"
)

const DefaultSocketPath = "/var/run/homelab-inventory-agent/inventory.sock"

func authorizeRootPeer(*net.UnixConn) error {
	return errors.New("privileged inventory snapshots are unsupported on this platform")
}
