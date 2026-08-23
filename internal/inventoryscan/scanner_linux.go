//go:build linux

package inventoryscan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func NewScanner() Scanner {
	return fixedScanner{runner: command.Exec{MaxOutput: 1 << 20}, scan: scanLinux}
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func firstTrustedExecutable(candidates []string, exists func(string) bool) string {
	for _, candidate := range candidates {
		if candidate != "" && strings.HasPrefix(candidate, "/") && exists(candidate) {
			return candidate
		}
	}
	return ""
}

func scanLinux(ctx context.Context, runner command.Runner) ([]protocol.HardwareComponent, error) {
	return scanLinuxWithTools(ctx, runner, linuxTools{
		dmidecode: firstTrustedExecutable([]string{"/usr/sbin/dmidecode", "/sbin/dmidecode", "/usr/bin/dmidecode"}, executableFile),
		lsblk:     firstTrustedExecutable([]string{"/usr/bin/lsblk", "/bin/lsblk"}, executableFile),
		lspci:     firstTrustedExecutable([]string{"/usr/bin/lspci", "/usr/sbin/lspci", "/sbin/lspci"}, executableFile),
		ip:        firstTrustedExecutable([]string{"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"}, executableFile),
	})
}

type linuxTools struct {
	dmidecode string
	lsblk     string
	lspci     string
	ip        string
}

func scanLinuxWithTools(ctx context.Context, runner command.Runner, tools linuxTools) ([]protocol.HardwareComponent, error) {
	components := []protocol.HardwareComponent{}
	if tools.dmidecode != "" {
		if dmi, err := runBounded(ctx, runner, 10*time.Second, tools.dmidecode, "--type", "0,1,2,3,4,16,17,39"); err == nil {
			components = append(components, parseDMI(dmi)...)
		}
	}
	if tools.lsblk != "" {
		lsblk, err := runBounded(ctx, runner, 10*time.Second, tools.lsblk, "--json", "--bytes", "--output", strings.Join([]string{
			"NAME", "KNAME", "PATH", "SIZE", "TYPE", "MODEL", "VENDOR", "SERIAL", "WWN", "TRAN", "ROTA",
			"PTTYPE", "PTUUID", "FSTYPE", "FSVER", "LABEL", "UUID", "MOUNTPOINTS", "PKNAME", "MAJ:MIN",
			"RM", "RO", "DISC-GRAN", "DISC-MAX", "DISC-ZERO", "PARTTYPE", "PARTTYPENAME", "PARTUUID",
			"PARTLABEL", "START",
		}, ","))
		if err == nil {
			components = append(components, parseLSBLK(lsblk)...)
		}
	}
	if tools.lspci != "" {
		if lspci, err := runBounded(ctx, runner, 10*time.Second, tools.lspci, "-Dmmnn"); err == nil {
			components = append(components, parseLineRecords("pci-device", lspci, 256)...)
		}
	}
	if tools.ip != "" {
		if links, err := runBounded(ctx, runner, 10*time.Second, tools.ip, "-details", "-json", "link", "show"); err == nil {
			components = append(components, parseLinuxLinks(links)...)
		}
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("no supported hardware was detected")
	}
	return components, nil
}

func parseLSBLK(body []byte) []protocol.HardwareComponent {
	var payload struct {
		Devices []map[string]any `json:"blockdevices"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	result := make([]protocol.HardwareComponent, 0, len(payload.Devices))
	for _, device := range payload.Devices {
		if len(result) >= 128 {
			break
		}
		kind := strings.ToLower(fmt.Sprint(device["type"]))
		if kind != "disk" && kind != "rom" {
			continue
		}
		locator := boundedString(fmt.Sprint(device["path"]))
		if locator == "" {
			locator = boundedString(fmt.Sprint(device["name"]))
		}
		result = append(result, protocol.HardwareComponent{
			Kind: "storage", Locator: locator, Values: normalizeLSBLKNode(device, 0),
		})
	}
	return result
}

func normalizeLSBLKNode(device map[string]any, depth int) map[string]any {
	values := map[string]any{}
	for key, value := range device {
		if value == nil || fmt.Sprint(value) == "" {
			continue
		}
		if key == "children" {
			if depth >= 4 {
				continue
			}
			children, ok := value.([]any)
			if !ok {
				continue
			}
			normalized := make([]map[string]any, 0, min(len(children), 64))
			for _, child := range children {
				record, valid := child.(map[string]any)
				if !valid || len(normalized) >= 64 {
					continue
				}
				normalized = append(normalized, normalizeLSBLKNode(record, depth+1))
			}
			if len(normalized) > 0 {
				values["children"] = normalized
			}
			continue
		}
		values[fieldKey(key)] = value
	}
	return values
}

func parseLinuxLinks(body []byte) []protocol.HardwareComponent {
	var records []map[string]any
	if json.Unmarshal(body, &records) != nil {
		return nil
	}
	result := make([]protocol.HardwareComponent, 0, len(records))
	for _, record := range records {
		name := boundedString(fmt.Sprint(record["ifname"]))
		if name == "" || name == "lo" || len(result) >= 128 {
			continue
		}
		values := map[string]any{"name": name}
		for _, key := range []string{"address", "link_type", "mtu"} {
			if value, present := record[key]; present && fmt.Sprint(value) != "" {
				values[fieldKey(key)] = value
			}
		}
		result = append(result, protocol.HardwareComponent{Kind: "network-interface", Locator: name, Values: values})
	}
	return result
}
