package protocol

import (
	"errors"
	"fmt"
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

type Container struct {
	Runtime                 string   `json:"runtime"`
	RuntimeID               string   `json:"runtimeId"`
	Name                    string   `json:"name"`
	Image                   string   `json:"image"`
	ImageDigest             *string  `json:"imageDigest,omitempty"`
	State                   string   `json:"state"`
	Health                  *string  `json:"health,omitempty"`
	PublishedPorts          []string `json:"publishedPorts,omitempty"`
	CPUPercent              *float64 `json:"cpuPercent,omitempty"`
	MemoryBytes             *uint64  `json:"memoryBytes,omitempty"`
	NetworkRxBytesPerSecond *float64 `json:"networkRxBytesPerSecond,omitempty"`
	NetworkTxBytesPerSecond *float64 `json:"networkTxBytesPerSecond,omitempty"`
	DiskReadBytesPerSecond  *float64 `json:"diskReadBytesPerSecond,omitempty"`
	DiskWriteBytesPerSecond *float64 `json:"diskWriteBytesPerSecond,omitempty"`
}

type StorageHealth struct {
	DeviceID    string         `json:"deviceId"`
	Kind        string         `json:"kind"`
	State       string         `json:"state"`
	CollectedAt time.Time      `json:"collectedAt"`
	Metrics     map[string]any `json:"metrics,omitempty"`
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
	for _, container := range heartbeat.Containers {
		if strings.TrimSpace(container.RuntimeID) == "" || strings.TrimSpace(container.Name) == "" || strings.TrimSpace(container.Image) == "" {
			return errors.New("container identity fields are required")
		}
	}
	return nil
}

func ForbiddenContainerFields() []string {
	return append([]string(nil), forbiddenContainerFields...)
}
