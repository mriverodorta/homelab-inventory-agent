//go:build linux

package platform

import (
	"testing"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func TestNewLeavesDisabledContainerCollectorUnset(t *testing.T) {
	collector := New(func(_, value string) string { return value }, Options{})
	capability := collector.Capabilities()["containers"]
	if capability.State != protocol.Disabled {
		t.Fatalf("disabled container telemetry reported unexpected capability: %#v", capability)
	}
}
