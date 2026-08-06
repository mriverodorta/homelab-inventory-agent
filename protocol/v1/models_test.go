package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func validContract() Contract {
	return Contract{
		ProtocolMajor:      CurrentMajor,
		Revision:           1,
		IssuedAt:           time.Now().UTC(),
		SchemaBundleDigest: strings.Repeat("a", 64),
		Collection: CollectionPolicy{
			HostIntervalSeconds:          60,
			ServiceIntervalSeconds:       600,
			StorageHealthIntervalSeconds: 3600,
			GPUSampleIntervalSeconds:     4,
		},
		Limits: PayloadLimits{
			CompressedBytes:   262144,
			DecompressedBytes: 1048576,
			OfflineSamples:    60,
			OfflineBytes:      10485760,
		},
	}
}

func TestHeartbeatRejectsServerIncompatibleBounds(t *testing.T) {
	heartbeat := validHeartbeat()
	heartbeat.Capabilities["host.cpu"] = Capability{State: Available, Detail: strings.Repeat("x", 257)}
	if err := ValidateHeartbeat(heartbeat); err == nil {
		t.Fatal("oversized capability detail was accepted")
	}
	heartbeat = validHeartbeat()
	heartbeat.Metrics.CPU = map[string]any{"percent": math.NaN()}
	if err := ValidateHeartbeat(heartbeat); err == nil {
		t.Fatal("nonfinite metric was accepted")
	}
	heartbeat = validHeartbeat()
	heartbeat.Metrics.Network = make([]map[string]any, 129)
	if err := ValidateHeartbeat(heartbeat); err == nil {
		t.Fatal("oversized network collection was accepted")
	}
}

func validHeartbeat() Heartbeat {
	return Heartbeat{
		ProtocolMajor: CurrentMajor,
		Sequence:      1,
		AgentVersion:  "0.1.0-dev",
		CollectedAt:   time.Now().UTC(),
		Host:          HostRef{Type: HostServer, ID: 1},
		Capabilities:  map[string]Capability{"host.cpu": {State: Available}},
		Metrics:       Metrics{LoadAverage: []float64{0.1, 0.2, 0.3}},
	}
}

func TestValidateContract(t *testing.T) {
	contract := validContract()
	if err := ValidateContract(contract); err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
	contract.ProtocolMajor = 2
	if err := ValidateContract(contract); err == nil || !strings.Contains(err.Error(), "unsupported protocol major") {
		t.Fatalf("unsupported protocol was not rejected: %v", err)
	}
	contract = validContract()
	contract.Privacy.RawHardwareIdentifiers = true
	if err := ValidateContract(contract); err == nil {
		t.Fatal("raw hardware identifiers were accepted")
	}
	contract = validContract()
	contract.Limits.OfflineSamples = 0
	if err := ValidateContract(contract); err == nil {
		t.Fatal("invalid offline buffer limit was accepted")
	}
}

func TestValidateHeartbeat(t *testing.T) {
	heartbeat := validHeartbeat()
	if err := ValidateHeartbeat(heartbeat); err != nil {
		t.Fatalf("valid heartbeat rejected: %v", err)
	}
	heartbeat.Host.Type = "desktop"
	if err := ValidateHeartbeat(heartbeat); err == nil {
		t.Fatal("unsupported host type was accepted")
	}
	heartbeat = validHeartbeat()
	heartbeat.ProtocolMajor = 99
	if err := ValidateHeartbeat(heartbeat); err == nil {
		t.Fatal("unsupported protocol was accepted")
	}
}

func TestContainerJSONCannotRepresentForbiddenFields(t *testing.T) {
	payload, err := json.Marshal(Container{Runtime: "docker", RuntimeID: "abc", Name: "web", Image: "example/web:1", State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range ForbiddenContainerFields() {
		if strings.Contains(strings.ToLower(string(payload)), `"`+strings.ToLower(field)+`"`) {
			t.Fatalf("container payload exposed forbidden field %s", field)
		}
	}
}

func TestValidateHardwareSnapshot(t *testing.T) {
	snapshot := HardwareSnapshot{
		ProtocolMajor: CurrentMajor,
		Host:          HostRef{Type: HostServer, ID: 1},
		CollectedAt:   time.Now().UTC(),
		Components: []HardwareComponent{{
			Kind: "memory", Locator: "DIMM_A1",
			Values: map[string]any{"manufacturer": "Example", "serialNumber": "private", "sizeBytes": uint64(8 << 30)},
		}},
	}
	if err := ValidateHardwareSnapshot(snapshot); err != nil {
		t.Fatalf("valid hardware snapshot rejected: %v", err)
	}
	snapshot.Components[0].Values["bad"] = math.NaN()
	if err := ValidateHardwareSnapshot(snapshot); err == nil {
		t.Fatal("nonfinite hardware value was accepted")
	}
	delete(snapshot.Components[0].Values, "bad")
	snapshot.Components[0].Kind = "../../command"
	if err := ValidateHardwareSnapshot(snapshot); err == nil {
		t.Fatal("unsafe hardware kind was accepted")
	}
}
