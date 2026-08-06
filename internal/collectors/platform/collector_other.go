//go:build !linux && !freebsd

package platform

import "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/baseline"

func New(_ func(string, string) string, _ Options) baseline.Collector {
	return baseline.Collector{}
}
