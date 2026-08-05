//go:build freebsd

package platform

import "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/baseline"

func New(_ func(string, string) string, _, _ []string) baseline.Collector {
	return baseline.Collector{}
}
