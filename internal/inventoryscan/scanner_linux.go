//go:build linux

package inventoryscan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func NewScanner() Scanner {
	return fixedScanner{runner: command.Exec{MaxOutput: 1 << 20}, scan: scanLinux}
}

func scanLinux(ctx context.Context, runner command.Runner) ([]protocol.HardwareComponent, error) {
	dmi, err := runBounded(ctx, runner, 10*time.Second, "/usr/sbin/dmidecode", "--type", "0,1,2,3,4,16,17,39")
	if err != nil {
		return nil, err
	}
	components := parseDMI(dmi)
	lsblk, err := runBounded(ctx, runner, 10*time.Second, "/usr/bin/lsblk", "--json", "--bytes", "--output", "NAME,PATH,SIZE,TYPE,MODEL,VENDOR,SERIAL,WWN,TRAN,ROTA")
	if err == nil {
		components = append(components, parseLSBLK(lsblk)...)
	}
	lspci, err := runBounded(ctx, runner, 10*time.Second, "/usr/bin/lspci", "-Dmmnn")
	if err == nil {
		components = append(components, parseLineRecords("pci-device", lspci, 256)...)
	}
	links, err := runBounded(ctx, runner, 10*time.Second, "/usr/sbin/ip", "-details", "-json", "link", "show")
	if err == nil {
		components = append(components, parseLinuxLinks(links)...)
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
	var result []protocol.HardwareComponent
	var visit func([]map[string]any)
	visit = func(devices []map[string]any) {
		for _, device := range devices {
			if len(result) >= 128 {
				return
			}
			children, _ := device["children"].([]any)
			delete(device, "children")
			kind := strings.ToLower(fmt.Sprint(device["type"]))
			if kind == "disk" || kind == "rom" {
				locator := boundedString(fmt.Sprint(device["path"]))
				if locator == "" {
					locator = boundedString(fmt.Sprint(device["name"]))
				}
				values := map[string]any{}
				for key, value := range device {
					if value != nil && fmt.Sprint(value) != "" {
						values[fieldKey(key)] = value
					}
				}
				result = append(result, protocol.HardwareComponent{Kind: "storage", Locator: locator, Values: values})
			}
			if len(children) > 0 {
				nested := make([]map[string]any, 0, len(children))
				for _, child := range children {
					if record, ok := child.(map[string]any); ok {
						nested = append(nested, record)
					}
				}
				visit(nested)
			}
		}
	}
	visit(payload.Devices)
	return result
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
