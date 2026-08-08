package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	commandrunner "github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const maxSystemdOutput = 1024 * 1024

type Systemd struct {
	runner      commandrunner.Runner
	timeout     time.Duration
	now         func() time.Time
	previousAt  time.Time
	previousCPU map[string]uint64
}

func NewSystemd() *Systemd {
	return &Systemd{runner: commandrunner.Exec{MaxOutput: maxSystemdOutput}, timeout: 5 * time.Second, now: time.Now, previousCPU: map[string]uint64{}}
}

func parseSystemdProperties(body []byte) []map[string]string {
	var result []map[string]string
	current := map[string]string{}
	flush := func() {
		if len(current) > 0 {
			result = append(result, current)
			current = map[string]string{}
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if found && key != "" {
			current[key] = value
		}
	}
	flush()
	return result
}

func parseOptionalUint(value string) *uint64 {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func enabledState(value string) *bool {
	switch value {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias":
		value := true
		return &value
	case "disabled", "masked", "masked-runtime":
		value := false
		return &value
	default:
		return nil
	}
}

func localSystemdUnit(path string) bool {
	clean := filepath.Clean(path)
	return clean == "/etc/systemd/system" || strings.HasPrefix(clean, "/etc/systemd/system/") ||
		clean == "/usr/local/lib/systemd/system" || strings.HasPrefix(clean, "/usr/local/lib/systemd/system/") ||
		clean == "/opt" || strings.HasPrefix(clean, "/opt/")
}

func packagedSystemdUnit(path string) bool {
	clean := filepath.Clean(path)
	return clean == "/lib/systemd/system" || strings.HasPrefix(clean, "/lib/systemd/system/") ||
		clean == "/usr/lib/systemd/system" || strings.HasPrefix(clean, "/usr/lib/systemd/system/")
}

func debianPackageName(value string) string {
	value = strings.TrimSpace(value)
	if separator := strings.LastIndex(value, ":"); separator > 0 {
		value = value[:separator]
	}
	return value
}

func classifySystemdUnits(ctx context.Context, runner commandrunner.Runner, units []map[string]string) map[string]string {
	classifications := make(map[string]string, len(units))
	packagedPaths := make([]string, 0, len(units))
	for _, unit := range units {
		name := strings.TrimSuffix(strings.TrimSpace(unit["Id"]), ".service")
		path := filepath.Clean(strings.TrimSpace(unit["FragmentPath"]))
		switch {
		case name == "":
			continue
		case localSystemdUnit(path):
			classifications[name] = "user-installed"
		case packagedSystemdUnit(path):
			classifications[name] = "unknown"
			packagedPaths = append(packagedPaths, path)
		default:
			classifications[name] = "unknown"
		}
	}
	if len(packagedPaths) == 0 {
		return classifications
	}
	manualOutput, manualErr := runner.Run(ctx, "/usr/bin/apt-mark", "showmanual")
	if manualErr != nil {
		return classifications
	}
	manualPackages := map[string]struct{}{}
	for _, line := range strings.Split(string(manualOutput), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			manualPackages[debianPackageName(name)] = struct{}{}
		}
	}
	arguments := append([]string{"--search"}, packagedPaths...)
	ownerOutput, ownerErr := runner.Run(ctx, "/usr/bin/dpkg-query", arguments...)
	if ownerErr != nil {
		return classifications
	}
	pathOwners := map[string][]string{}
	for _, line := range strings.Split(string(ownerOutput), "\n") {
		owners, path, found := strings.Cut(line, ": ")
		if !found || strings.TrimSpace(path) == "" {
			continue
		}
		for _, owner := range strings.Split(owners, ",") {
			if name := debianPackageName(owner); name != "" {
				pathOwners[filepath.Clean(strings.TrimSpace(path))] = append(pathOwners[filepath.Clean(strings.TrimSpace(path))], name)
			}
		}
	}
	for _, unit := range units {
		name := strings.TrimSuffix(strings.TrimSpace(unit["Id"]), ".service")
		path := filepath.Clean(strings.TrimSpace(unit["FragmentPath"]))
		if !packagedSystemdUnit(path) {
			continue
		}
		owners := pathOwners[path]
		if len(owners) == 0 {
			continue
		}
		classifications[name] = "system"
		for _, owner := range owners {
			if _, manual := manualPackages[owner]; manual {
				classifications[name] = "user-installed"
				break
			}
		}
	}
	return classifications
}

func (collector *Systemd) Collect(ctx context.Context) ([]protocol.Service, error) {
	requestContext, cancel := context.WithTimeout(ctx, collector.timeout)
	defer cancel()
	output, err := collector.runner.Run(
		requestContext,
		"systemctl", "show", "--type=service", "--all", "--no-pager",
		"--property=Id,Description,ActiveState,SubState,UnitFileState,FragmentPath,MemoryCurrent,MemoryPeak,CPUUsageNSec,NRestarts,TasksCurrent,TasksMax,Result,ActiveEnterTimestamp,InactiveEnterTimestamp",
	)
	if err != nil {
		return nil, fmt.Errorf("inspect systemd services: %w", err)
	}
	units := parseSystemdProperties(output)
	if len(units) > 512 {
		return nil, errors.New("systemd service count exceeds the protocol limit")
	}
	now := collector.now()
	elapsed := now.Sub(collector.previousAt)
	classifications := classifySystemdUnits(requestContext, collector.runner, units)
	services := make([]protocol.Service, 0, len(units))
	for _, unit := range units {
		name := strings.TrimSuffix(strings.TrimSpace(unit["Id"]), ".service")
		if name == "" || len(name) > 256 {
			continue
		}
		description := unit["Description"]
		if len(description) > 2048 {
			description = description[:2048]
		}
		active := unit["ActiveState"]
		if strings.TrimSpace(active) == "" {
			continue
		}
		if len(active) > 64 {
			active = active[:64]
		}
		sub := unit["SubState"]
		if len(sub) > 64 {
			sub = sub[:64]
		}
		service := protocol.Service{
			Name: name, Description: description, ActiveState: active, SubState: sub,
			Classification: classifications[name],
			Enabled:        enabledState(unit["UnitFileState"]), MemoryCurrent: parseOptionalUint(unit["MemoryCurrent"]),
			MemoryPeak: parseOptionalUint(unit["MemoryPeak"]), RestartCount: parseOptionalUint(unit["NRestarts"]),
			TaskCount: parseOptionalUint(unit["TasksCurrent"]), TaskLimit: parseOptionalUint(unit["TasksMax"]),
		}
		if result := strings.TrimSpace(unit["Result"]); result != "" {
			service.LastResult = &result
		}
		if entered := strings.TrimSpace(unit["ActiveEnterTimestamp"]); entered != "" {
			service.ActiveEnteredAt = &entered
		}
		if entered := strings.TrimSpace(unit["InactiveEnterTimestamp"]); entered != "" {
			service.InactiveEnteredAt = &entered
		}
		if cpu := parseOptionalUint(unit["CPUUsageNSec"]); cpu != nil {
			if previous, exists := collector.previousCPU[name]; exists && *cpu >= previous && elapsed > 0 {
				percent := float64(*cpu-previous) * 100 / float64(elapsed.Nanoseconds())
				service.CPUPercent = &percent
			}
			collector.previousCPU[name] = *cpu
		}
		services = append(services, service)
	}
	collector.previousAt = now
	return services, nil
}
