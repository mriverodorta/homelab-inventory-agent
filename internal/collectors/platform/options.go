package platform

import containercollector "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/containers"

type Options struct {
	Filesystems  []string
	SMARTDevices []string
	Containers   *containercollector.Collector
}
