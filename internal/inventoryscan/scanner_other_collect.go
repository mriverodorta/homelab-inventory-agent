//go:build !linux && !freebsd

package inventoryscan

import (
	"context"
	"errors"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func (unsupportedScanner) Collect(context.Context, protocol.HostRef) (protocol.HardwareSnapshot, error) {
	return protocol.HardwareSnapshot{}, errors.New("privileged hardware inventory is unsupported on this platform")
}
