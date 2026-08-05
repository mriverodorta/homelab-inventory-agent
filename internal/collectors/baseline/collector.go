package baseline

import (
	"context"
	"os"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Collector struct{}

func (Collector) Capabilities() map[string]protocol.Capability {
	return map[string]protocol.Capability{
		"host.cpu":      {State: protocol.Unavailable, Detail: "collector not installed in this development build"},
		"host.memory":   {State: protocol.Unavailable, Detail: "collector not installed in this development build"},
		"host.storage":  {State: protocol.Unavailable, Detail: "collector not installed in this development build"},
		"host.network":  {State: protocol.Unavailable, Detail: "collector not installed in this development build"},
		"host.services": {State: protocol.Disabled, Detail: "service discovery is not enabled"},
		"containers":    {State: protocol.Disabled, Detail: "container discovery is opt-in"},
	}
}

func (collector Collector) Collect(_ context.Context, _ protocol.Contract) (protocol.Heartbeat, error) {
	hostname, _ := os.Hostname()
	return protocol.Heartbeat{
		CollectedAt:  time.Now().UTC(),
		Hostname:     hostname,
		Capabilities: collector.Capabilities(),
		Metrics:      protocol.Metrics{},
	}, nil
}
