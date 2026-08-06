//go:build freebsd

package inventoryscan

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

const DefaultSocketPath = "/var/run/homelab-inventory-agent/inventory.sock"

func authorizeRootPeer(connection *net.UnixConn) error {
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Xucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
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
