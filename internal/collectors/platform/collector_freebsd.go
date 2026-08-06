//go:build freebsd

package platform

import freebsdcollector "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/freebsd"

func New(_ func(string, string) string, options Options) *freebsdcollector.Collector {
	return freebsdcollector.New(freebsdcollector.Options{Filesystems: options.Filesystems})
}
