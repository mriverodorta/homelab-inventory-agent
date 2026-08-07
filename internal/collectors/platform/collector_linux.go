//go:build linux

package platform

import "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/linux"

func New(opaqueID func(string, string) string, options Options) *linux.Collector {
	settings := linux.Options{
		Filesystems:  options.Filesystems,
		SMARTDevices: options.SMARTDevices,
	}
	if options.Containers != nil {
		settings.Containers = options.Containers
	}
	return linux.New("/", opaqueID, settings)
}
