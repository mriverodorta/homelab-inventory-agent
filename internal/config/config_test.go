package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStrictConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	body := `{"endpoint":"http://inventory.local/","host":{"type":"nas","id":7},"stateDirectory":"/var/lib/hli-agent"}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if value.Endpoint != "http://inventory.local" || value.Host.ID != 7 {
		t.Fatalf("unexpected config: %#v", value)
	}

	if err := os.WriteFile(file, []byte(`{"endpoint":"http://inventory.local","host":{"type":"server","id":1},"stateDirectory":"/tmp","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("unknown configuration field was accepted")
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
