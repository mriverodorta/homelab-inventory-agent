package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityPersistsKeyDeviceAndSequence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "identity.json")
	first, err := LoadOrCreate(file)
	if err != nil {
		t.Fatal(err)
	}
	key := first.PrivateKey()
	if err := first.Activate(9); err != nil {
		t.Fatal(err)
	}
	if sequence, err := first.ReserveSequence(); err != nil || sequence != 1 {
		t.Fatalf("reserve sequence: %d %v", sequence, err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity mode: %v %v", info, err)
	}

	second, err := LoadOrCreate(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, second.PrivateKey()) || second.DeviceID() != 9 {
		t.Fatal("identity changed after restart")
	}
	if sequence, err := second.ReserveSequence(); err != nil || sequence != 2 {
		t.Fatalf("restart sequence: %d %v", sequence, err)
	}
}

func TestIdentityRejectsWeakPermissionsAndRebinding(t *testing.T) {
	file := filepath.Join(t.TempDir(), "identity.json")
	identity, err := LoadOrCreate(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Activate(1); err != nil {
		t.Fatal(err)
	}
	if err := identity.Activate(2); err == nil {
		t.Fatal("identity rebound to another device")
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(file); err == nil {
		t.Fatal("weak identity permissions were accepted")
	}
}
