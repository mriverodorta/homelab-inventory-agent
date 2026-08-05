//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	commandrunner "github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const maxSMARTOutput = 1024 * 1024

func newSMARTCommandRunner() commandRunner {
	return commandrunner.Exec{MaxOutput: maxSMARTOutput}
}

func findSMARTCTL() string {
	for _, candidate := range []string{"/usr/sbin/smartctl", "/usr/bin/smartctl", "/usr/local/sbin/smartctl"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func readTrimmed(filePath string) string {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func numeric(value string) any {
	if parsed, err := strconv.ParseUint(value, 0, 64); err == nil {
		return parsed
	}
	return value
}

func (collector *Collector) opaque(namespace, value string) string {
	if collector.opaqueID != nil {
		return collector.opaqueID(namespace, value)
	}
	return fmt.Sprintf("fixture-%016x", len(namespace)+len(value))
}

func (collector *Collector) collectStorageHealth(ctx context.Context, now time.Time, smartEnabled bool) ([]protocol.StorageHealth, error) {
	blockRoot := filepath.Join(collector.root, "sys", "block")
	entries, err := os.ReadDir(blockRoot)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.StorageHealth, 0)
	for _, entry := range entries {
		if len(result) == 64 {
			break
		}
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "mmcblk") && !strings.Contains(name, "p"):
			deviceRoot := filepath.Join(blockRoot, name, "device")
			preEOL := readTrimmed(filepath.Join(deviceRoot, "pre_eol_info"))
			state := "unknown"
			switch preEOL {
			case "0x01", "1":
				state = "healthy"
			case "0x02", "2":
				state = "warning"
			case "0x03", "3":
				state = "failed"
			}
			metrics := map[string]any{
				"model":    readTrimmed(filepath.Join(deviceRoot, "name")),
				"firmware": readTrimmed(filepath.Join(deviceRoot, "fwrev")),
				"preEol":   numeric(preEOL),
				"lifeTime": readTrimmed(filepath.Join(deviceRoot, "life_time")),
			}
			if sectors, parseErr := strconv.ParseUint(readTrimmed(filepath.Join(blockRoot, name, "size")), 10, 64); parseErr == nil {
				metrics["capacityBytes"] = sectors * 512
			}
			result = append(result, protocol.StorageHealth{
				DeviceID: collector.opaque("storage", name), Kind: "emmc", State: state, CollectedAt: now, Metrics: metrics,
			})
		case strings.HasPrefix(name, "md"):
			mdRoot := filepath.Join(blockRoot, name, "md")
			arrayState := readTrimmed(filepath.Join(mdRoot, "array_state"))
			degraded := readTrimmed(filepath.Join(mdRoot, "degraded"))
			state := "healthy"
			if degraded != "" && degraded != "0" {
				state = "warning"
			}
			if arrayState == "inactive" || arrayState == "clear" || arrayState == "broken" {
				state = "failed"
			}
			result = append(result, protocol.StorageHealth{
				DeviceID: collector.opaque("storage", name), Kind: "mdraid", State: state, CollectedAt: now,
				Metrics: map[string]any{
					"level": readTrimmed(filepath.Join(mdRoot, "level")), "arrayState": arrayState,
					"raidDisks":     numeric(readTrimmed(filepath.Join(mdRoot, "raid_disks"))),
					"degradedDisks": numeric(degraded), "syncAction": readTrimmed(filepath.Join(mdRoot, "sync_action")),
					"syncCompleted": readTrimmed(filepath.Join(mdRoot, "sync_completed")),
					"mismatchCount": numeric(readTrimmed(filepath.Join(mdRoot, "mismatch_cnt"))),
				},
			})
		}
	}
	if smartEnabled {
		if collector.smartctlPath == "" {
			return nil, errors.New("smartctl is not installed at a supported path")
		}
		for _, device := range collector.smartDevices {
			if len(result) == 64 {
				break
			}
			record, collectErr := collector.collectSMART(ctx, now, device)
			if collectErr != nil {
				return nil, collectErr
			}
			result = append(result, record)
		}
	}
	return result, nil
}

type smartPayload struct {
	ModelName       string `json:"model_name"`
	ModelFamily     string `json:"model_family"`
	FirmwareVersion string `json:"firmware_version"`
	Device          struct {
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	Capacity struct {
		Bytes uint64 `json:"bytes"`
	} `json:"user_capacity"`
	Temperature struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	SMARTStatus struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	PowerOnTime struct {
		Hours *uint64 `json:"hours"`
	} `json:"power_on_time"`
	PowerCycleCount *uint64 `json:"power_cycle_count"`
	PowerMode       string  `json:"power_mode"`
	NVMe            struct {
		CriticalWarning    uint64   `json:"critical_warning"`
		AvailableSpare     *float64 `json:"available_spare"`
		SpareThreshold     *float64 `json:"available_spare_threshold"`
		PercentageUsed     *float64 `json:"percentage_used"`
		UnsafeShutdowns    *uint64  `json:"unsafe_shutdowns"`
		MediaErrors        *uint64  `json:"media_errors"`
		ErrorLogEntries    *uint64  `json:"num_err_log_entries"`
		PowerCycles        *uint64  `json:"power_cycles"`
		PowerOnHours       *uint64  `json:"power_on_hours"`
		TemperatureCelsius *float64 `json:"temperature"`
	} `json:"nvme_smart_health_information_log"`
}

func (collector *Collector) collectSMART(parent context.Context, now time.Time, device string) (protocol.StorageHealth, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	body, runErr := collector.smartRunner.Run(ctx, collector.smartctlPath, "-n", "standby,0", "-a", "-j", device)
	if len(body) == 0 {
		if runErr != nil {
			return protocol.StorageHealth{}, fmt.Errorf("inspect SMART device: %w", runErr)
		}
		return protocol.StorageHealth{}, errors.New("smartctl returned an empty payload")
	}
	var payload smartPayload
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		// smartctl adds version-specific fields. Decode only the allowlisted projection.
		if fallbackErr := json.Unmarshal(body, &payload); fallbackErr != nil {
			return protocol.StorageHealth{}, fmt.Errorf("decode smartctl payload: %w", fallbackErr)
		}
	}
	metrics := map[string]any{}
	for key, value := range map[string]string{
		"model": payload.ModelName, "modelFamily": payload.ModelFamily, "firmware": payload.FirmwareVersion,
		"deviceType": payload.Device.Type, "protocol": payload.Device.Protocol,
	} {
		if value != "" {
			metrics[key] = value
		}
	}
	if payload.Capacity.Bytes > 0 {
		metrics["capacityBytes"] = payload.Capacity.Bytes
	}
	if payload.Temperature.Current != nil {
		metrics["temperatureC"] = *payload.Temperature.Current
	} else if payload.NVMe.TemperatureCelsius != nil {
		metrics["temperatureC"] = *payload.NVMe.TemperatureCelsius
	}
	for key, value := range map[string]*uint64{
		"powerOnHours": payload.PowerOnTime.Hours, "powerCycleCount": payload.PowerCycleCount,
		"unsafeShutdowns": payload.NVMe.UnsafeShutdowns, "mediaErrors": payload.NVMe.MediaErrors,
		"errorLogEntries": payload.NVMe.ErrorLogEntries,
	} {
		if value != nil {
			metrics[key] = *value
		}
	}
	for key, value := range map[string]*float64{
		"availableSparePercent": payload.NVMe.AvailableSpare, "spareThresholdPercent": payload.NVMe.SpareThreshold,
		"percentageUsed": payload.NVMe.PercentageUsed,
	} {
		if value != nil {
			metrics[key] = *value
		}
	}
	state := "unknown"
	if payload.SMARTStatus.Passed != nil {
		if *payload.SMARTStatus.Passed {
			state = "healthy"
		} else {
			state = "failed"
		}
	}
	if payload.NVMe.CriticalWarning > 0 && state != "failed" {
		state = "warning"
		metrics["criticalWarning"] = payload.NVMe.CriticalWarning
	}
	if strings.EqualFold(payload.PowerMode, "standby") || strings.EqualFold(payload.PowerMode, "sleep") {
		metrics = map[string]any{"standby": true}
		state = "unknown"
	}
	return protocol.StorageHealth{
		DeviceID: collector.opaque("storage", device), Kind: "smart", State: state, CollectedAt: now, Metrics: metrics,
	}, nil
}

func (current diskCounters) validDelta(previous diskCounters) bool {
	return current.Reads >= previous.Reads && current.SectorsRead >= previous.SectorsRead &&
		current.ReadMilliseconds >= previous.ReadMilliseconds && current.Writes >= previous.Writes &&
		current.SectorsWritten >= previous.SectorsWritten && current.WriteMilliseconds >= previous.WriteMilliseconds &&
		current.IOMilliseconds >= previous.IOMilliseconds && current.WeightedIOMilliseconds >= previous.WeightedIOMilliseconds
}
