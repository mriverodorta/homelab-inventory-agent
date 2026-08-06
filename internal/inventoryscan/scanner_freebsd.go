//go:build freebsd

package inventoryscan

import (
	"context"
	"errors"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func NewScanner() Scanner {
	return fixedScanner{runner: command.Exec{MaxOutput: 1 << 20}, scan: scanFreeBSD}
}

func scanFreeBSD(ctx context.Context, runner command.Runner) ([]protocol.HardwareComponent, error) {
	var components []protocol.HardwareComponent
	dmi, err := runBounded(ctx, runner, 10*time.Second, "/usr/local/sbin/dmidecode", "--type", "0,1,2,3,4,16,17,39")
	if err == nil {
		components = append(components, parseDMI(dmi)...)
	} else {
		components = append(components, scanFreeBSDKenv(ctx, runner)...)
	}
	for _, specification := range []struct {
		path string
		args []string
		kind string
		max  int
	}{
		{path: "/usr/sbin/pciconf", args: []string{"-lv"}, kind: "pci-device", max: 256},
		{path: "/sbin/geom", args: []string{"disk", "list"}, kind: "storage", max: 128},
		{path: "/sbin/ifconfig", args: []string{"-l"}, kind: "network-interface", max: 128},
		{path: "/sbin/sysctl", args: []string{"-n", "hw.model"}, kind: "cpu", max: 16},
	} {
		body, commandErr := runBounded(ctx, runner, 10*time.Second, specification.path, specification.args...)
		if commandErr == nil {
			components = append(components, parseLineRecords(specification.kind, body, specification.max)...)
		}
	}
	if len(components) == 0 {
		return nil, errors.New("no supported hardware was detected; install dmidecode or verify read-only system tools")
	}
	return components, nil
}

func scanFreeBSDKenv(ctx context.Context, runner command.Runner) []protocol.HardwareComponent {
	keys := []string{
		"smbios.bios.vendor", "smbios.bios.version", "smbios.bios.reldate",
		"smbios.system.maker", "smbios.system.product", "smbios.system.version", "smbios.system.serial", "smbios.system.uuid",
		"smbios.planar.maker", "smbios.planar.product", "smbios.planar.version", "smbios.planar.serial",
		"smbios.chassis.maker", "smbios.chassis.type", "smbios.chassis.version", "smbios.chassis.serial",
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		body, err := runBounded(ctx, runner, 2*time.Second, "/usr/bin/kenv", "-q", key)
		if err == nil {
			values[key] = string(body)
		}
	}
	return parseKenvSMBIOS(values)
}
