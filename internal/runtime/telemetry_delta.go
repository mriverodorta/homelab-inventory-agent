package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const telemetrySyncVersion = 1
const fullReconciliationInterval = 6 * time.Hour

type familySyncState struct {
	Revision   uint64            `json:"revision"`
	Hashes     map[string]string `json:"hashes"`
	LastFullAt time.Time         `json:"lastFullAt,omitempty"`
}

type telemetrySyncState struct {
	Version          int                        `json:"version"`
	CapabilitiesHash string                     `json:"capabilitiesHash,omitempty"`
	Families         map[string]familySyncState `json:"families"`
}

func emptyTelemetrySyncState() telemetrySyncState {
	return telemetrySyncState{Version: telemetrySyncVersion, Families: map[string]familySyncState{}}
}

func loadTelemetrySyncState(filePath string) (telemetrySyncState, error) {
	body, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return emptyTelemetrySyncState(), nil
	}
	if err != nil {
		return telemetrySyncState{}, err
	}
	var state telemetrySyncState
	if json.Unmarshal(body, &state) != nil || state.Version != telemetrySyncVersion || state.Families == nil {
		return emptyTelemetrySyncState(), nil
	}
	return state, nil
}

func writeTelemetrySyncState(filePath string, state telemetrySyncState) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := filePath + ".tmp"
	if err := os.WriteFile(temporary, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, filePath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Chmod(filePath, 0o600)
}

func valueHash(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func capabilityHash(capabilities map[string]protocol.Capability) (string, error) {
	return valueHash(capabilities)
}

type keyedValue[T any] struct {
	key   string
	value T
}

func deltaFamily[T any](previous familySyncState, values []keyedValue[T], now time.Time) (*protocol.StateFamily[T], familySyncState, error) {
	nextHashes := make(map[string]string, len(values))
	changed := make([]T, 0)
	for _, entry := range values {
		digest, err := valueHash(entry.value)
		if err != nil {
			return nil, previous, err
		}
		nextHashes[entry.key] = digest
		if previous.Hashes[entry.key] != digest {
			changed = append(changed, entry.value)
		}
	}
	removed := make([]string, 0)
	for key := range previous.Hashes {
		if _, exists := nextHashes[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	full := previous.Revision == 0 || previous.LastFullAt.IsZero() || now.Sub(previous.LastFullAt) >= fullReconciliationInterval
	next := familySyncState{Revision: previous.Revision, Hashes: nextHashes, LastFullAt: previous.LastFullAt}
	if !full && len(changed) == 0 && len(removed) == 0 {
		return nil, next, nil
	}
	next.Revision++
	if full {
		next.LastFullAt = now
		changed = make([]T, 0, len(values))
		for _, entry := range values {
			changed = append(changed, entry.value)
		}
	}
	return &protocol.StateFamily[T]{Revision: next.Revision, Full: full, Changed: changed, Removed: removed}, next, nil
}

func mapKey(value map[string]any, candidates ...string) string {
	for _, candidate := range candidates {
		if text, ok := value[candidate].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func keyedMaps(values []map[string]any, candidates ...string) []keyedValue[map[string]any] {
	result := make([]keyedValue[map[string]any], 0, len(values))
	for _, value := range values {
		if key := mapKey(value, candidates...); key != "" {
			result = append(result, keyedValue[map[string]any]{key, value})
		}
	}
	return result
}

func compactCPU(input map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"percent", "idlePercent", "ioWaitPercent", "stealPercent", "systemPercent", "userPercent"} {
		if value, exists := input[key]; exists {
			result[key] = value
		}
	}
	return result
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func compactSensors(input []map[string]any) []map[string]any {
	cpuPattern := regexp.MustCompile(`(?i)(cpu|package|coretemp|k10temp|core\s)`)
	nvmePattern := regexp.MustCompile(`(?i)(nvme|composite)`)
	cpu := []float64{}
	nvme := map[string]map[string]any{}
	for _, sensor := range input {
		temperature, ok := number(sensor["temperatureC"])
		if !ok {
			temperature, ok = number(sensor["temperatureCelsius"])
		}
		if !ok {
			continue
		}
		identity := strings.Join([]string{mapKey(sensor, "key"), mapKey(sensor, "id"), mapKey(sensor, "name"), mapKey(sensor, "source")}, " ")
		if cpuPattern.MatchString(identity) {
			cpu = append(cpu, temperature)
		}
		if nvmePattern.MatchString(identity) {
			key := mapKey(sensor, "source", "id", "name", "key")
			if key != "" {
				if !strings.HasPrefix(strings.ToLower(key), "nvme:") {
					key = "nvme:" + key
				}
				nvme[key] = map[string]any{"key": key, "kind": "nvme", "temperatureC": temperature}
			}
		}
	}
	result := make([]map[string]any, 0, len(nvme)+1)
	if len(cpu) > 0 {
		total := 0.0
		for _, value := range cpu {
			total += value
		}
		result = append(result, map[string]any{"key": "cpu:average", "kind": "cpu-average", "temperatureC": total / float64(len(cpu))})
	}
	keys := make([]string, 0, len(nvme))
	for key := range nvme {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, nvme[key])
	}
	return result
}

func buildCompactHeartbeat(input protocol.Heartbeat, previous telemetrySyncState) (protocol.Heartbeat, telemetrySyncState, error) {
	now := input.CollectedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	next := previous
	if next.Families == nil {
		next = emptyTelemetrySyncState()
	}

	capabilitiesHash, err := capabilityHash(input.Capabilities)
	if err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	input.CapabilitiesHash = capabilitiesHash
	if previous.CapabilitiesHash == capabilitiesHash {
		input.Capabilities = nil
	} else {
		next.CapabilitiesHash = capabilitiesHash
	}

	services := make([]keyedValue[protocol.Service], 0, len(input.Services))
	for _, value := range input.Services {
		manager := strings.TrimSpace(value.Manager)
		if manager == "" {
			manager = "systemd"
			value.Manager = manager
		}
		services = append(services, keyedValue[protocol.Service]{manager + "\x00" + value.Name, value})
	}
	containers := make([]keyedValue[protocol.Container], 0, len(input.Containers))
	for _, value := range input.Containers {
		containers = append(containers, keyedValue[protocol.Container]{value.Runtime + "\x00" + value.RuntimeID, value})
	}
	storage := make([]keyedValue[protocol.StorageHealth], 0, len(input.StorageHealth))
	for _, value := range input.StorageHealth {
		storage = append(storage, keyedValue[protocol.StorageHealth]{value.DeviceID, value})
	}
	system := []keyedValue[map[string]any]{}
	if len(input.Metrics.System) > 0 {
		system = append(system, keyedValue[map[string]any]{"system", input.Metrics.System})
	}

	state := &protocol.HeartbeatState{}
	if state.Services, next.Families["services"], err = deltaFamily(previous.Families["services"], services, now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.Containers, next.Families["containers"], err = deltaFamily(previous.Families["containers"], containers, now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.Filesystems, next.Families["filesystems"], err = deltaFamily(previous.Families["filesystems"], keyedMaps(input.Metrics.Filesystems, "mountPoint", "mount", "source"), now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.GPUs, next.Families["gpus"], err = deltaFamily(previous.Families["gpus"], keyedMaps(input.Metrics.GPUs, "id", "pciAddress", "uuid", "name"), now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.Sensors, next.Families["sensors"], err = deltaFamily(previous.Families["sensors"], keyedMaps(compactSensors(input.Metrics.Sensors), "key"), now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.System, next.Families["system"], err = deltaFamily(previous.Families["system"], system, now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}
	if state.StorageHealth, next.Families["storageHealth"], err = deltaFamily(previous.Families["storageHealth"], storage, now); err != nil {
		return protocol.Heartbeat{}, previous, err
	}

	input.State = state
	input.Services = nil
	input.Containers = nil
	input.StorageHealth = nil
	input.Metrics.CPU = compactCPU(input.Metrics.CPU)
	input.Metrics.System = nil
	input.Metrics.Filesystems = nil
	input.Metrics.DiskIO = nil
	input.Metrics.Network = nil
	input.Metrics.Sensors = nil
	input.Metrics.Batteries = nil
	input.Metrics.GPUs = nil
	return input, next, nil
}
