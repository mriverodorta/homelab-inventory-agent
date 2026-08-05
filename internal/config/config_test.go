package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStrictConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	body := `{"endpoint":"http://inventory.local/","host":{"type":"nas","id":7},"stateDirectory":"/var/lib/hli-agent","filesystems":["/mnt/media"],"storageHealth":{"smartDevices":["/dev/nvme0"]}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if value.Endpoint != "http://inventory.local" || value.Host.ID != 7 || len(value.Filesystems) != 1 || len(value.StorageHealth.SMARTDevices) != 1 {
		t.Fatalf("unexpected config: %#v", value)
	}

	if err := os.WriteFile(file, []byte(`{"endpoint":"http://inventory.local","host":{"type":"server","id":1},"stateDirectory":"/tmp","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("unknown configuration field was accepted")
	}
}

func TestRejectsUnsafeSMARTDeviceAllowlist(t *testing.T) {
	value := Config{Endpoint: "https://inventory.local", StateDirectory: "/tmp"}
	value.Host.Type = "server"
	value.Host.ID = 1
	for _, device := range []string{"/dev/../etc/passwd", "sda", "/tmp/sda", "/dev/sda/../sdb"} {
		value.StorageHealth.SMARTDevices = []string{device}
		if err := value.Validate(); err == nil {
			t.Fatalf("unsafe SMART device %q was accepted", device)
		}
	}
}

func TestRejectsUnsafeEndpoint(t *testing.T) {
	config := Config{Endpoint: "https://user:pass@example.com/path", StateDirectory: "/tmp"}
	config.Host.Type = "server"
	config.Host.ID = 1
	if err := config.Validate(); err == nil {
		t.Fatal("unsafe endpoint was accepted")
	}
}
