package protocol

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const CurrentMajor = 1

type HostType string

const (
	HostServer  HostType = "server"
	HostNAS     HostType = "nas"
	HostPCBuild HostType = "pcBuild"
)

type HostRef struct {
	Type HostType `json:"type"`
	ID   uint64   `json:"id"`
}

type Availability string

const (
	Available         Availability = "available"
	Unavailable       Availability = "unavailable"
	PermissionBlocked Availability = "permission-blocked"
	Disabled          Availability = "disabled"
)

type Capability struct {
	State  Availability `json:"state"`
	Detail string       `json:"detail,omitempty"`
}

type CollectionPolicy struct {
	HostIntervalSeconds          int `json:"hostIntervalSeconds"`
	ServiceIntervalSeconds       int `json:"serviceIntervalSeconds"`
	StorageHealthIntervalSeconds int `json:"storageHealthIntervalSeconds"`
	GPUSampleIntervalSeconds     int `json:"gpuSampleIntervalSeconds"`
}

type PayloadLimits struct {
	CompressedBytes   int `json:"compressedBytes"`
	DecompressedBytes int `json:"decompressedBytes"`
	OfflineSamples    int `json:"offlineSamples"`
	OfflineBytes      int `json:"offlineBytes"`
}

type PrivacyPolicy struct {
	ContainersEnabled      bool `json:"containersEnabled"`
	SMARTEnabled           bool `json:"smartEnabled"`
	RawHardwareIdentifiers bool `json:"rawHardwareIdentifiersEnabled"`
}

type Contract struct {
	ProtocolMajor      int              `json:"protocolMajor"`
	Revision           uint64           `json:"revision"`
	IssuedAt           time.Time        `json:"issuedAt"`
	SchemaBundleDigest string           `json:"schemaBundleDigest"`
	Collection         CollectionPolicy `json:"collection"`
	Limits             PayloadLimits    `json:"limits"`
	Privacy            PrivacyPolicy    `json:"privacy"`
}

type Activation struct {
	ProtocolMajor int                   `json:"protocolMajor"`
	AgentVersion  string                `json:"agentVersion"`
	PublicKey     string                `json:"publicKey"`
	Capabilities  map[string]Capability `json:"capabilities"`
}

type Metrics struct {
	UptimeSeconds *float64         `json:"uptimeSeconds,omitempty"`
	LoadAverage   []float64        `json:"loadAverage,omitempty"`
	System        map[string]any   `json:"system,omitempty"`
	CPU           map[string]any   `json:"cpu,omitempty"`
	Memory        map[string]any   `json:"memory,omitempty"`
	Filesystems   []map[string]any `json:"filesystems,omitempty"`
	DiskIO        []map[string]any `json:"diskIo,omitempty"`
	Network       []map[string]any `json:"network,omitempty"`
	Sensors       []map[string]any `json:"sensors,omitempty"`
	Batteries     []map[string]any `json:"batteries,omitempty"`
	GPUs          []map[string]any `json:"gpus,omitempty"`
}

type Service struct {
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	ActiveState       string   `json:"activeState"`
	Classification    string   `json:"classification,omitempty"`
	SubState          string   `json:"subState,omitempty"`
	Enabled           *bool    `json:"enabled,omitempty"`
	MemoryCurrent     *uint64  `json:"memoryCurrentBytes,omitempty"`
	MemoryPeak        *uint64  `json:"memoryPeakBytes,omitempty"`
	CPUPercent        *float64 `json:"cpuPercent,omitempty"`
	RestartCount      *uint64  `json:"restartCount,omitempty"`
	TaskCount         *uint64  `json:"taskCount,omitempty"`
	TaskLimit         *uint64  `json:"taskLimit,omitempty"`
	LastResult        *string  `json:"lastResult,omitempty"`
	ActiveEnteredAt   *string  `json:"activeEnteredAt,omitempty"`
	InactiveEnteredAt *string  `json:"inactiveEnteredAt,omitempty"`
}

type ContainerPort struct {
	HostPort      uint16 `json:"hostPort"`
	ContainerPort uint16 `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

type Container struct {
	Runtime                 string          `json:"runtime"`
	RuntimeID               string          `json:"runtimeId"`
	Name                    string          `json:"name"`
	Image                   string          `json:"image"`
	ImageDigest             *string         `json:"imageDigest,omitempty"`
	State                   string          `json:"state"`
	Status                  string          `json:"status,omitempty"`
	Uptime                  string          `json:"uptime,omitempty"`
	Health                  *string         `json:"health,omitempty"`
	ComposeService          string          `json:"composeService,omitempty"`
	NetworkMode             string          `json:"networkMode,omitempty"`
	NetworkNames            []string        `json:"networkNames,omitempty"`
	Ports                   []ContainerPort `json:"ports,omitempty"`
	PublishedPorts          []string        `json:"publishedPorts,omitempty"`
	CPUPercent              *float64        `json:"cpuPercent,omitempty"`
	MemoryBytes             *uint64         `json:"memoryBytes,omitempty"`
	NetworkRxBytesPerSecond *float64        `json:"networkRxBytesPerSecond,omitempty"`
	NetworkTxBytesPerSecond *float64        `json:"networkTxBytesPerSecond,omitempty"`
	DiskReadBytesPerSecond  *float64        `json:"diskReadBytesPerSecond,omitempty"`
	DiskWriteBytesPerSecond *float64        `json:"diskWriteBytesPerSecond,omitempty"`
}

type StorageHealth struct {
	DeviceID    string         `json:"deviceId"`
	Kind        string         `json:"kind"`
	State       string         `json:"state"`
	CollectedAt time.Time      `json:"collectedAt"`
	Metrics     map[string]any `json:"metrics,omitempty"`
}

type HardwareComponent struct {
	Kind    string         `json:"kind"`
	Locator string         `json:"locator"`
	Values  map[string]any `json:"values"`
}

type HardwareSnapshot struct {
	ProtocolMajor int                 `json:"protocolMajor"`
	Host          HostRef             `json:"host"`
	CollectedAt   time.Time           `json:"collectedAt"`
	Components    []HardwareComponent `json:"components"`
}

type Heartbeat struct {
	ProtocolMajor  int                   `json:"protocolMajor"`
	Sequence       uint64                `json:"sequence"`
	AgentVersion   string                `json:"agentVersion"`
	CollectedAt    time.Time             `json:"collectedAt"`
	Host           HostRef               `json:"host"`
	Hostname       string                `json:"hostname,omitempty"`
	DroppedSamples uint64                `json:"droppedSamples,omitempty"`
	Capabilities   map[string]Capability `json:"capabilities"`
	Metrics        Metrics               `json:"metrics"`
	Services       []Service             `json:"services,omitempty"`
	Containers     []Container           `json:"containers,omitempty"`
	StorageHealth  []StorageHealth       `json:"storageHealth,omitempty"`
}

var forbiddenContainerFields = []string{
	"env", "environment", "labels", "annotations", "command", "commands", "args",
	"arguments", "mounts", "hostMounts", "logs", "credentials", "secrets",
}

var capabilityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var hardwareComponentKindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validAvailability(value Availability) bool {
	return value == Available || value == Unavailable || value == PermissionBlocked || value == Disabled
}

func validateBoundedValue(value any, depth int) error {
	if depth > 6 {
		return errors.New("metric value is nested too deeply")
	}
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string:
		if len(typed) > 2048 {
			return errors.New("metric string exceeds 2048 characters")
		}
		return nil
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return errors.New("metric contains a nonfinite number")
		}
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return errors.New("metric contains a nonfinite number")
		}
		return nil
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Map:
		if reflected.Len() > 128 {
			return errors.New("metric object exceeds 128 fields")
		}
		iterator := reflected.MapRange()
		for iterator.Next() {
			if iterator.Key().Kind() != reflect.String {
				return errors.New("metric object keys must be strings")
			}
			key := iterator.Key().String()
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return errors.New("metric object contains an unsafe key")
			}
			if err := validateBoundedValue(iterator.Value().Interface(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		if reflected.Len() > 1024 {
			return errors.New("metric array exceeds 1024 values")
		}
		for index := 0; index < reflected.Len(); index++ {
			if err := validateBoundedValue(reflected.Index(index).Interface(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Pointer:
		if reflected.IsNil() {
			return nil
		}
		return validateBoundedValue(reflected.Elem().Interface(), depth+1)
	case reflect.Struct:
		typeInfo := reflected.Type()
		for index := 0; index < reflected.NumField(); index++ {
			field := typeInfo.Field(index)
			if !field.IsExported() {
				continue
			}
			if err := validateBoundedValue(reflected.Field(index).Interface(), depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("metric contains unsupported value type %T", value)
	}
}

func ValidateHostRef(host HostRef) error {
	if host.Type != HostServer && host.Type != HostNAS && host.Type != HostPCBuild {
		return fmt.Errorf("unsupported host type %q", host.Type)
	}
	if host.ID == 0 || host.ID > 1<<53-1 {
		return errors.New("host id must be a positive safe integer")
	}
	return nil
}

func ValidateContract(contract Contract) error {
	if contract.ProtocolMajor != CurrentMajor {
		return fmt.Errorf("unsupported protocol major %d", contract.ProtocolMajor)
	}
	if contract.Revision == 0 || contract.IssuedAt.IsZero() {
		return errors.New("contract revision and issuedAt are required")
	}
	if len(contract.SchemaBundleDigest) != 64 {
		return errors.New("contract schema bundle digest is required")
	}
	if contract.Collection.HostIntervalSeconds < 30 || contract.Collection.HostIntervalSeconds > 300 {
		return errors.New("host interval must be between 30 and 300 seconds")
	}
	if contract.Collection.ServiceIntervalSeconds < 300 || contract.Collection.ServiceIntervalSeconds > 3600 {
		return errors.New("service interval must be between 300 and 3600 seconds")
	}
	if contract.Collection.StorageHealthIntervalSeconds < 1800 || contract.Collection.StorageHealthIntervalSeconds > 86400 {
		return errors.New("storage-health interval must be between 1800 and 86400 seconds")
	}
	if contract.Collection.GPUSampleIntervalSeconds < 3 || contract.Collection.GPUSampleIntervalSeconds > 60 {
		return errors.New("GPU sample interval must be between 3 and 60 seconds")
	}
	if contract.Limits.CompressedBytes < 1024 || contract.Limits.CompressedBytes > 1048576 {
		return errors.New("compressed payload limit must be between 1024 and 1048576 bytes")
	}
	if contract.Limits.DecompressedBytes < 4096 || contract.Limits.DecompressedBytes > 4194304 {
		return errors.New("decompressed payload limit must be between 4096 and 4194304 bytes")
	}
	if contract.Limits.OfflineSamples < 1 || contract.Limits.OfflineSamples > 1440 {
		return errors.New("offline sample limit must be between 1 and 1440")
	}
	if contract.Limits.OfflineBytes < 1048576 || contract.Limits.OfflineBytes > 52428800 {
		return errors.New("offline byte limit must be between 1048576 and 52428800")
	}
	if contract.Privacy.RawHardwareIdentifiers {
		return errors.New("normal telemetry cannot enable raw hardware identifiers")
	}
	return nil
}

func ValidateHeartbeat(heartbeat Heartbeat) error {
	if heartbeat.ProtocolMajor != CurrentMajor {
		return fmt.Errorf("unsupported protocol major %d", heartbeat.ProtocolMajor)
	}
	if heartbeat.Sequence == 0 || heartbeat.CollectedAt.IsZero() {
		return errors.New("heartbeat sequence and collectedAt are required")
	}
	if err := ValidateHostRef(heartbeat.Host); err != nil {
		return err
	}
	if strings.TrimSpace(heartbeat.AgentVersion) == "" || len(heartbeat.AgentVersion) > 64 {
		return errors.New("agent version must contain 1 to 64 characters")
	}
	if len(heartbeat.Hostname) > 255 || len(heartbeat.Services) > 512 || len(heartbeat.Containers) > 256 || len(heartbeat.StorageHealth) > 64 {
		return errors.New("heartbeat collection limit exceeded")
	}
	if len(heartbeat.Capabilities) > 64 {
		return errors.New("heartbeat capability limit exceeded")
	}
	for name, capability := range heartbeat.Capabilities {
		if !capabilityNamePattern.MatchString(name) || !validAvailability(capability.State) || len(capability.Detail) > 256 {
			return fmt.Errorf("capability %q is invalid", name)
		}
	}
	if len(heartbeat.Metrics.LoadAverage) != 0 && len(heartbeat.Metrics.LoadAverage) != 3 {
		return errors.New("load average must contain exactly three values")
	}
	for _, value := range heartbeat.Metrics.LoadAverage {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("load average is invalid")
		}
	}
	metricCollections := []struct {
		name  string
		count int
		limit int
	}{
		{name: "filesystems", count: len(heartbeat.Metrics.Filesystems), limit: 64},
		{name: "disk I/O", count: len(heartbeat.Metrics.DiskIO), limit: 128},
		{name: "network", count: len(heartbeat.Metrics.Network), limit: 128},
		{name: "sensors", count: len(heartbeat.Metrics.Sensors), limit: 256},
		{name: "batteries", count: len(heartbeat.Metrics.Batteries), limit: 16},
		{name: "GPUs", count: len(heartbeat.Metrics.GPUs), limit: 16},
	}
	for _, collection := range metricCollections {
		if collection.count > collection.limit {
			return fmt.Errorf("heartbeat %s collection limit exceeded", collection.name)
		}
	}
	if err := validateBoundedValue(heartbeat.Metrics, 0); err != nil {
		return err
	}
	for _, service := range heartbeat.Services {
		if strings.TrimSpace(service.Name) == "" || len(service.Name) > 256 || len(service.Description) > 2048 || strings.TrimSpace(service.ActiveState) == "" || len(service.ActiveState) > 64 || len(service.SubState) > 64 {
			return errors.New("service identity fields are invalid")
		}
		if service.Classification != "" && service.Classification != "user-installed" && service.Classification != "system" && service.Classification != "unknown" {
			return errors.New("service classification is invalid")
		}
		if (service.LastResult != nil && len(*service.LastResult) > 256) ||
			(service.ActiveEnteredAt != nil && len(*service.ActiveEnteredAt) > 128) ||
			(service.InactiveEnteredAt != nil && len(*service.InactiveEnteredAt) > 128) ||
			(service.CPUPercent != nil && (math.IsNaN(*service.CPUPercent) || math.IsInf(*service.CPUPercent, 0) || *service.CPUPercent < 0)) {
			return errors.New("service telemetry fields are invalid")
		}
	}
	for _, container := range heartbeat.Containers {
		if (container.Runtime != "docker" && container.Runtime != "podman") || strings.TrimSpace(container.RuntimeID) == "" || len(container.RuntimeID) > 128 || strings.TrimSpace(container.Name) == "" || len(container.Name) > 256 || strings.TrimSpace(container.Image) == "" || len(container.Image) > 512 || len(container.State) > 64 || len(container.Status) > 256 || len(container.Uptime) > 128 || len(container.ComposeService) > 256 || len(container.NetworkNames) > 32 || len(container.Ports) > 128 || len(container.PublishedPorts) > 128 {
			return errors.New("container identity fields are required")
		}
		if container.NetworkMode != "" && container.NetworkMode != "host" && container.NetworkMode != "bridge" && container.NetworkMode != "none" && container.NetworkMode != "custom" {
			return errors.New("container network mode is invalid")
		}
		for _, name := range container.NetworkNames {
			if strings.TrimSpace(name) == "" || len(name) > 128 {
				return errors.New("container network name is invalid")
			}
		}
		for _, port := range container.Ports {
			if port.HostPort == 0 || port.ContainerPort == 0 || (port.Protocol != "tcp" && port.Protocol != "udp" && port.Protocol != "sctp") {
				return errors.New("container port mapping is invalid")
			}
		}
		for _, port := range container.PublishedPorts {
			if len(port) > 128 {
				return errors.New("container published port is invalid")
			}
		}
		for _, metric := range []*float64{container.CPUPercent, container.NetworkRxBytesPerSecond, container.NetworkTxBytesPerSecond, container.DiskReadBytesPerSecond, container.DiskWriteBytesPerSecond} {
			if metric != nil && (math.IsNaN(*metric) || math.IsInf(*metric, 0) || *metric < 0) {
				return errors.New("container metric is invalid")
			}
		}
	}
	for _, storage := range heartbeat.StorageHealth {
		if len(storage.DeviceID) < 16 || len(storage.DeviceID) > 128 || (storage.Kind != "smart" && storage.Kind != "emmc" && storage.Kind != "mdraid") || (storage.State != "healthy" && storage.State != "warning" && storage.State != "failed" && storage.State != "unknown") || storage.CollectedAt.IsZero() {
			return errors.New("storage-health record is invalid")
		}
		if err := validateBoundedValue(storage.Metrics, 0); err != nil {
			return err
		}
	}
	return nil
}

func ValidateHardwareSnapshot(snapshot HardwareSnapshot) error {
	if snapshot.ProtocolMajor != CurrentMajor {
		return fmt.Errorf("unsupported protocol major %d", snapshot.ProtocolMajor)
	}
	if err := ValidateHostRef(snapshot.Host); err != nil {
		return err
	}
	if snapshot.CollectedAt.IsZero() {
		return errors.New("hardware snapshot collectedAt is required")
	}
	if len(snapshot.Components) == 0 || len(snapshot.Components) > 1024 {
		return errors.New("hardware snapshot must contain 1 to 1024 components")
	}
	for index, component := range snapshot.Components {
		if !hardwareComponentKindPattern.MatchString(component.Kind) {
			return fmt.Errorf("hardware component %d kind is invalid", index)
		}
		if strings.TrimSpace(component.Locator) == "" || len(component.Locator) > 256 {
			return fmt.Errorf("hardware component %d locator is invalid", index)
		}
		if len(component.Values) == 0 || len(component.Values) > 128 {
			return fmt.Errorf("hardware component %d values are invalid", index)
		}
		if err := validateBoundedValue(component.Values, 0); err != nil {
			return fmt.Errorf("hardware component %d: %w", index, err)
		}
	}
	return nil
}

func ForbiddenContainerFields() []string {
	return append([]string(nil), forbiddenContainerFields...)
}
