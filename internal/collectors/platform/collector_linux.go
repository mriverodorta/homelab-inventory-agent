//go:build linux

package platform

import "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/linux"

func New(opaqueID func(string, string) string, filesystems, smartDevices []string) *linux.Collector {
	return linux.New("/", opaqueID, linux.Options{Filesystems: filesystems, SMARTDevices: smartDevices})
}
