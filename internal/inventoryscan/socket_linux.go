//go:build linux

package inventoryscan

import (
	"errors"
	"net"
	"syscall"
)

const DefaultSocketPath = "/run/homelab-inventory-agent/inventory.sock"

func authorizeRootPeer(connection *net.UnixConn) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var credential *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if credential == nil || credential.Uid != 0 {
		return errors.New("inventory snapshots are accepted only from a local root process")
	}
	return nil
}
