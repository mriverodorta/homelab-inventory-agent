package services

import (
	"context"
	"errors"
	"fmt"
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

func (collector *Systemd) Collect(ctx context.Context) ([]protocol.Service, error) {
	requestContext, cancel := context.WithTimeout(ctx, collector.timeout)
	defer cancel()
	output, err := collector.runner.Run(
		requestContext,
		"systemctl", "show", "--type=service", "--all", "--no-pager",
		"--property=Id,Description,ActiveState,SubState,UnitFileState,MemoryCurrent,MemoryPeak,CPUUsageNSec,NRestarts,TasksCurrent,TasksMax,Result,ActiveEnterTimestamp,InactiveEnterTimestamp",
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
			Enabled: enabledState(unit["UnitFileState"]), MemoryCurrent: parseOptionalUint(unit["MemoryCurrent"]),
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
