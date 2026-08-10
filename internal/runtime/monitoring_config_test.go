package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mriverodorta/homelab-inventory-agent/internal/buffer"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func TestMonitoringConfigPersistsAndRejectsStaleRevisions(t *testing.T) {
	directory := t.TempDir()
	agent, _, _ := createRuntime(t, "http://127.0.0.1:1", directory, buffer.Limits{Samples: 60, Bytes: 10 << 20})
	current := protocol.MonitoringConfig{
		Revision:               3,
		Enabled:                true,
		ServiceIntervalSeconds: 60,
		SelectedServices:       []string{"docker.service"},
		SelectedContainers:     []string{"docker\x00compose\x00app"},
	}
	if err := agent.applyMonitoringConfig(current); err != nil {
		t.Fatal(err)
	}
	if err := agent.applyMonitoringConfig(protocol.MonitoringConfig{
		Revision:               2,
		Enabled:                false,
		ServiceIntervalSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(directory, "monitoring-config.json")
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("monitoring config mode = %o, want 600", info.Mode().Perm())
	}
	persisted, err := loadMonitoringConfig(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Revision != 3 || persisted.ServiceIntervalSeconds != 60 || len(persisted.SelectedServices) != 1 {
		t.Fatalf("stale config replaced current config: %#v", persisted)
	}

	restarted, _, _ := createRuntime(t, "http://127.0.0.1:1", directory, buffer.Limits{Samples: 60, Bytes: 10 << 20})
	contract := restarted.effectiveContract(runtimeContract(t))
	if contract.Collection.ServiceIntervalSeconds != 60 {
		t.Fatalf("persisted monitoring interval was not restored: %d", contract.Collection.ServiceIntervalSeconds)
	}
	if restarted.monitoringRevision() != 3 {
		t.Fatalf("persisted monitoring revision was not restored: %d", restarted.monitoringRevision())
	}
}

func TestHeartbeatAcknowledgesAppliedMonitoringRevision(t *testing.T) {
	directory := t.TempDir()
	agent, deviceIdentity, queue := createRuntime(t, "http://127.0.0.1:1", directory, buffer.Limits{Samples: 60, Bytes: 10 << 20})
	if err := deviceIdentity.Activate(4); err != nil {
		t.Fatal(err)
	}
	if err := agent.applyMonitoringConfig(protocol.MonitoringConfig{
		Revision: 7, Enabled: true, ServiceIntervalSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.Collect(context.Background(), runtimeContract(t)); err != nil {
		t.Fatal(err)
	}
	entries, err := queue.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("queued heartbeats = %d, err = %v", len(entries), err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(entries[0].Body))
	if err != nil {
		t.Fatal(err)
	}
	var heartbeat protocol.Heartbeat
	if err := json.NewDecoder(reader).Decode(&heartbeat); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if heartbeat.MonitoringRevision != 7 {
		t.Fatalf("monitoring revision = %d, want 7", heartbeat.MonitoringRevision)
	}
	if heartbeat.Capabilities["notifications.monitoring-policy"].State != protocol.Available {
		t.Fatal("monitoring-policy capability was not reported")
	}
}

func TestHeartbeatOmitsMonitoringAcknowledgementForLegacyApplicationContract(t *testing.T) {
	directory := t.TempDir()
	agent, deviceIdentity, queue := createRuntime(t, "http://127.0.0.1:1", directory, buffer.Limits{Samples: 60, Bytes: 10 << 20})
	if err := deviceIdentity.Activate(4); err != nil {
		t.Fatal(err)
	}
	if err := agent.applyMonitoringConfig(protocol.MonitoringConfig{
		Revision: 7, Enabled: true, ServiceIntervalSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	contract := runtimeContract(t)
	contract.SchemaBundleDigest = protocol.LegacyBundleDigest
	if err := agent.Collect(context.Background(), contract); err != nil {
		t.Fatal(err)
	}
	entries, err := queue.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatalf("queued heartbeats = %d, err = %v", len(entries), err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(entries[0].Body))
	if err != nil {
		t.Fatal(err)
	}
	var heartbeat map[string]any
	if err := json.NewDecoder(reader).Decode(&heartbeat); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if _, present := heartbeat["monitoringRevision"]; present {
		t.Fatal("legacy application contract received an unsupported monitoringRevision field")
	}
}

func TestAgentFailsClosedWhenPersistedMonitoringConfigIsInvalid(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "monitoring-config.json"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMonitoringConfig(filepath.Join(directory, "monitoring-config.json")); err == nil {
		t.Fatal("invalid monitoring config was accepted")
	}
}
