//go:build linux

package platform

import "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/linux"

func New(opaqueID func(string, string) string, options Options) *linux.Collector {
	return linux.New("/", opaqueID, linux.Options{
		Filesystems:  options.Filesystems,
		SMARTDevices: options.SMARTDevices,
		Containers:   options.Containers,
	})
}
