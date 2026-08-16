package runtime

import (
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func compactFixture(at time.Time) protocol.Heartbeat {
	return protocol.Heartbeat{
		CollectedAt:  at,
		Capabilities: map[string]protocol.Capability{"host.cpu": {State: protocol.Available}},
		Metrics: protocol.Metrics{
			CPU:         map[string]any{"percent": 12.0, "idlePercent": 88.0, "model": "static", "cores": []any{1, 2}},
			Memory:      map[string]any{"usedBytes": uint64(25), "totalBytes": uint64(100)},
			System:      map[string]any{"operatingSystem": "Linux"},
			DiskIO:      []map[string]any{{"name": "sda", "readBytes": 2}},
			Network:     []map[string]any{{"name": "eth0", "receiveBytes": 3}},
			Filesystems: []map[string]any{{"mountPoint": "/", "usedBytes": uint64(40)}},
		},
		Services:   []protocol.Service{{Name: "docker", ActiveState: "active"}},
		Containers: []protocol.Container{{Runtime: "docker", RuntimeID: "abc", Name: "web", State: "running"}},
	}
}

func TestCompactHeartbeatSendsFullStateThenOnlyChanges(t *testing.T) {
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	first, state, err := buildCompactHeartbeat(compactFixture(at), emptyTelemetrySyncState())
	if err != nil {
		t.Fatal(err)
	}
	if first.State == nil || first.State.Services == nil || !first.State.Services.Full || first.State.Containers == nil {
		t.Fatalf("first compact heartbeat was not a full reconciliation: %#v", first.State)
	}
	if first.Capabilities == nil || first.CapabilitiesHash == "" {
		t.Fatalf("first heartbeat must carry capabilities and their digest")
	}
	if first.Metrics.DiskIO != nil || first.Metrics.Network != nil || first.Metrics.System != nil || first.Metrics.CPU["cores"] != nil || first.Metrics.CPU["model"] != nil {
		t.Fatalf("compact metrics retained discarded data: %#v", first.Metrics)
	}

	second, _, err := buildCompactHeartbeat(compactFixture(at.Add(time.Minute)), state)
	if err != nil {
		t.Fatal(err)
	}
	if second.Capabilities != nil || second.State.Services != nil || second.State.Containers != nil || second.State.System != nil {
		t.Fatalf("unchanged state was retransmitted: %#v", second)
	}
}

func TestCompactHeartbeatReconcilesEverySixHours(t *testing.T) {
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	_, state, err := buildCompactHeartbeat(compactFixture(at), emptyTelemetrySyncState())
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := buildCompactHeartbeat(compactFixture(at.Add(6*time.Hour)), state)
	if err != nil {
		t.Fatal(err)
	}
	if next.State.Services == nil || !next.State.Services.Full || next.State.Services.Revision != 2 {
		t.Fatalf("six-hour service reconciliation missing: %#v", next.State.Services)
	}
}

func TestCompactHeartbeatPreservesServiceManagerIdentity(t *testing.T) {
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	heartbeat := compactFixture(at)
	heartbeat.Services = []protocol.Service{{Manager: "rcd", Name: "dnsmasq", ActiveState: "active"}}

	first, state, err := buildCompactHeartbeat(heartbeat, emptyTelemetrySyncState())
	if err != nil {
		t.Fatal(err)
	}
	if first.State.Services == nil || len(first.State.Services.Changed) != 1 || first.State.Services.Changed[0].Manager != "rcd" {
		t.Fatalf("rc.d service manager was not preserved: %#v", first.State.Services)
	}
	if _, exists := state.Families["services"].Hashes["rcd\x00dnsmasq"]; !exists {
		t.Fatalf("rc.d service identity was not used: %#v", state.Families["services"].Hashes)
	}
}
