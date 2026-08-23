//go:build linux

package inventoryscan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLSBLKPreservesDiskIdentity(t *testing.T) {
	components := parseLSBLK([]byte(`{"blockdevices":[{"name":"nvme0n1","path":"/dev/nvme0n1","size":512110190592,"type":"disk","model":"Example SSD","serial":"PRIVATE-SSD","wwn":"PRIVATE-WWN","tran":"nvme","rota":false,"pttype":"gpt","children":[{"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":511000000000,"type":"part","fstype":"ext4","mountpoints":["/"]}]}]}`))
	if len(components) != 1 || components[0].Locator != "/dev/nvme0n1" || components[0].Values["serial"] != "PRIVATE-SSD" {
		t.Fatalf("lsblk storage identity mismatch: %#v", components)
	}
	children, ok := components[0].Values["children"].([]map[string]any)
	if !ok || len(children) != 1 || children[0]["fstype"] != "ext4" {
		t.Fatalf("lsblk topology was not retained: %#v", components[0].Values)
	}
}

func TestFirstTrustedExecutableUsesApprovedOrderOnly(t *testing.T) {
	existing := map[string]bool{"/bin/lsblk": true, "/tmp/untrusted/lsblk": true}
	selected := firstTrustedExecutable([]string{"/usr/bin/lsblk", "/bin/lsblk"}, func(path string) bool { return existing[path] })
	if selected != "/bin/lsblk" {
		t.Fatalf("alternate trusted path was not selected: %q", selected)
	}
	if selected := firstTrustedExecutable([]string{"/usr/bin/lsblk", "/bin/lsblk"}, func(string) bool { return false }); selected != "" {
		t.Fatalf("missing executable produced a path: %q", selected)
	}
}

type recordingLinuxRunner struct {
	commands []string
}

func (runner *recordingLinuxRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.commands = append(runner.commands, name)
	switch filepath.Base(name) {
	case "lsblk":
		return []byte(`{"blockdevices":[{"name":"vda","path":"/dev/vda","size":1024,"type":"disk"}]}`), nil
	case "ip":
		return []byte(`[{"ifname":"eth0","address":"00:00:00:00:00:00","link_type":"ether","mtu":1500}]`), nil
	default:
		return nil, errors.New("optional tool unavailable")
	}
}

func TestLinuxInventoryRemainsUsefulWithoutOptionalDMIOrPCI(t *testing.T) {
	runner := &recordingLinuxRunner{}
	components, err := scanLinuxWithTools(context.Background(), runner, linuxTools{lsblk: "/bin/lsblk", ip: "/sbin/ip"})
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 2 || components[0].Kind != "storage" || components[1].Kind != "network-interface" {
		t.Fatalf("partial inventory mismatch: %#v", components)
	}
	if len(runner.commands) != 2 || runner.commands[0] != "/bin/lsblk" || runner.commands[1] != "/sbin/ip" {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestExecutableFileRequiresARegularExecutable(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "tool")
	if err := os.WriteFile(regular, []byte("tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !executableFile(regular) {
		t.Fatal("regular executable was rejected")
	}
	nonExecutable := filepath.Join(root, "data")
	if err := os.WriteFile(nonExecutable, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if executableFile(nonExecutable) {
		t.Fatal("non-executable file was accepted")
	}
}
