package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/buffer"
	"github.com/mriverodorta/homelab-inventory-agent/internal/config"
	"github.com/mriverodorta/homelab-inventory-agent/internal/identity"
	"github.com/mriverodorta/homelab-inventory-agent/internal/transport"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Collector interface {
	Collect(context.Context, protocol.Contract) (protocol.Heartbeat, error)
}

type backgroundCollector interface {
	Start(context.Context, protocol.Contract)
}

var ErrRegistrationRevoked = errors.New("agent registration was revoked; the agent is dormant until it is enrolled again")

type Agent struct {
	config         config.Config
	version        string
	capabilities   map[string]protocol.Capability
	identity       *identity.Identity
	queue          *buffer.Queue
	client         *transport.Client
	collector      Collector
	contractPath   string
	monitoringPath string
	monitoringMu   sync.RWMutex
	monitoring     protocol.MonitoringConfig
	telemetryPath  string
	telemetryMu    sync.Mutex
	telemetry      telemetrySyncState
	dormantPath    string
	onError        func(error)
}

type Options struct {
	Config       config.Config
	Version      string
	Capabilities map[string]protocol.Capability
	Identity     *identity.Identity
	Queue        *buffer.Queue
	Client       *transport.Client
	Collector    Collector
	OnError      func(error)
}

func New(options Options) (*Agent, error) {
	if err := options.Config.Validate(); err != nil {
		return nil, err
	}
	if options.Version == "" || options.Identity == nil || options.Queue == nil || options.Client == nil || options.Collector == nil {
		return nil, errors.New("agent runtime dependencies are incomplete")
	}
	onError := options.OnError
	if onError == nil {
		onError = func(error) {}
	}
	agent := &Agent{
		config: options.Config, version: options.Version, capabilities: options.Capabilities,
		identity: options.Identity, queue: options.Queue, client: options.Client,
		collector:      options.Collector,
		contractPath:   filepath.Join(options.Config.StateDirectory, "contract.json"),
		monitoringPath: filepath.Join(options.Config.StateDirectory, "monitoring-config.json"),
		telemetryPath:  filepath.Join(options.Config.StateDirectory, "telemetry-sync.json"),
		dormantPath:    filepath.Join(options.Config.StateDirectory, "dormant.json"), onError: onError,
	}
	monitoring, err := loadMonitoringConfig(agent.monitoringPath)
	if err != nil {
		return nil, fmt.Errorf("load monitoring config: %w", err)
	}
	agent.monitoring = monitoring
	telemetry, err := loadTelemetrySyncState(agent.telemetryPath)
	if err != nil {
		return nil, fmt.Errorf("load telemetry sync state: %w", err)
	}
	agent.telemetry = telemetry
	return agent, nil
}

func (a *Agent) effectiveContract(contract protocol.Contract) protocol.Contract {
	a.monitoringMu.RLock()
	defer a.monitoringMu.RUnlock()
	if a.monitoring.Revision > 0 && a.monitoring.Enabled {
		contract.Collection.ServiceIntervalSeconds = a.monitoring.ServiceIntervalSeconds
	}
	return contract
}

func (a *Agent) monitoringRevision() uint64 {
	a.monitoringMu.RLock()
	defer a.monitoringMu.RUnlock()
	return a.monitoring.Revision
}

func (a *Agent) applyMonitoringConfig(config protocol.MonitoringConfig) error {
	if err := protocol.ValidateMonitoringConfig(config); err != nil {
		return err
	}
	a.monitoringMu.Lock()
	defer a.monitoringMu.Unlock()
	if config.Revision <= a.monitoring.Revision {
		return nil
	}
	if err := writeMonitoringConfig(a.monitoringPath, config); err != nil {
		return err
	}
	a.monitoring = config
	return nil
}

func (a *Agent) dormant() bool {
	info, err := os.Stat(a.dormantPath)
	return err == nil && info.Mode().IsRegular()
}

func (a *Agent) markDormant() error {
	body := []byte(fmt.Sprintf("{\"state\":\"revoked\",\"recordedAt\":%q}\n", time.Now().UTC().Format(time.RFC3339Nano)))
	temporary := a.dormantPath + ".tmp"
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, a.dormantPath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (a *Agent) waitDormant(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (a *Agent) Activate(ctx context.Context, enrollmentToken string) error {
	if a.identity.DeviceID() != 0 {
		return nil
	}
	publicKey, err := a.identity.PublicKeyBase64()
	if err != nil {
		return err
	}
	result, err := a.client.Activate(ctx, a.config.Host, enrollmentToken, protocol.Activation{
		ProtocolMajor: protocol.CurrentMajor,
		AgentVersion:  a.version,
		PublicKey:     publicKey,
		Capabilities:  a.capabilities,
	})
	if err != nil {
		return err
	}
	return a.identity.Activate(result.DeviceID)
}

func (a *Agent) Contract(ctx context.Context) (protocol.Contract, error) {
	cached, err := loadContractCache(a.contractPath)
	if errors.Is(err, errContractCacheSchemaIncompatible) {
		cached = cachedContract{}
	} else if err != nil {
		return protocol.Contract{}, err
	}
	contract, etag, notModified, fetchErr := a.client.FetchContract(ctx, cached.ETag)
	if fetchErr != nil {
		if ctx.Err() != nil {
			return protocol.Contract{}, ctx.Err()
		}
		var httpError *transport.HTTPError
		if cached.Contract.ProtocolMajor != 0 && !errors.As(fetchErr, &httpError) {
			return cached.Contract, a.applyContractLimits(cached.Contract)
		}
		return protocol.Contract{}, fetchErr
	}
	if notModified {
		if cached.Contract.ProtocolMajor == 0 {
			return protocol.Contract{}, errors.New("server returned not-modified without a cached contract")
		}
		return cached.Contract, a.applyContractLimits(cached.Contract)
	}
	if err := writeContractCache(a.contractPath, cachedContract{ETag: etag, Contract: contract}); err != nil {
		return protocol.Contract{}, err
	}
	if err := a.applyContractLimits(contract); err != nil {
		return protocol.Contract{}, err
	}
	return contract, nil
}

func (a *Agent) applyContractLimits(contract protocol.Contract) error {
	_, err := a.queue.SetLimits(buffer.Limits{
		Samples: contract.Limits.OfflineSamples,
		Bytes:   int64(contract.Limits.OfflineBytes),
	})
	return err
}

func compressHeartbeat(heartbeat protocol.Heartbeat) ([]byte, int, error) {
	body, err := json.Marshal(heartbeat)
	if err != nil {
		return nil, 0, err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, 0, err
	}
	if _, err := writer.Write(body); err != nil {
		_ = writer.Close()
		return nil, 0, err
	}
	if err := writer.Close(); err != nil {
		return nil, 0, err
	}
	return compressed.Bytes(), len(body), nil
}

func reportedDropped(body []byte) uint64 {
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return 0
	}
	defer reader.Close()
	var value struct {
		Dropped uint64 `json:"droppedSamples"`
	}
	if json.NewDecoder(io.LimitReader(reader, 1024*1024)).Decode(&value) != nil {
		return 0
	}
	return value.Dropped
}

func (a *Agent) Collect(ctx context.Context, contract protocol.Contract) error {
	heartbeat, err := a.collector.Collect(ctx, a.effectiveContract(contract))
	if err != nil {
		return err
	}
	sequence, err := a.identity.ReserveSequence()
	if err != nil {
		return err
	}
	dropped, err := a.queue.Dropped()
	if err != nil {
		return err
	}
	heartbeat.ProtocolMajor = protocol.CurrentMajor
	heartbeat.Sequence = sequence
	heartbeat.AgentVersion = a.version
	heartbeat.Host = a.config.Host
	heartbeat.DroppedSamples = dropped
	supportsMonitoring, err := protocol.SupportsMonitoringPolicy(contract.SchemaBundleDigest)
	if err != nil {
		return err
	}
	if supportsMonitoring {
		heartbeat.MonitoringRevision = a.monitoringRevision()
	}
	if heartbeat.CollectedAt.IsZero() {
		heartbeat.CollectedAt = time.Now().UTC()
	}
	if heartbeat.Capabilities == nil {
		heartbeat.Capabilities = map[string]protocol.Capability{}
	}
	for name, capability := range a.capabilities {
		heartbeat.Capabilities[name] = capability
	}
	heartbeat.Capabilities["notifications.monitoring-policy"] = protocol.Capability{
		State:  protocol.Available,
		Detail: "revisioned heartbeat-response policy",
	}
	supportsCompact, err := protocol.SupportsCompactTelemetry(contract.SchemaBundleDigest)
	if err != nil {
		return err
	}
	nextTelemetry := a.telemetry
	if supportsCompact {
		a.telemetryMu.Lock()
		compact, next, compactErr := buildCompactHeartbeat(heartbeat, a.telemetry)
		a.telemetryMu.Unlock()
		if compactErr != nil {
			return compactErr
		}
		heartbeat, nextTelemetry = compact, next
	}
	if err := protocol.ValidateHeartbeat(heartbeat); err != nil {
		return err
	}
	body, decompressedBytes, err := compressHeartbeat(heartbeat)
	if err != nil {
		return err
	}
	if decompressedBytes > contract.Limits.DecompressedBytes {
		return fmt.Errorf("heartbeat exceeds contract decompressed limit of %d bytes", contract.Limits.DecompressedBytes)
	}
	if len(body) > contract.Limits.CompressedBytes {
		return fmt.Errorf("compressed heartbeat exceeds contract limit of %d bytes", contract.Limits.CompressedBytes)
	}
	if _, err = a.queue.Add(buffer.Entry{Sequence: sequence, Body: body}); err != nil {
		return err
	}
	if supportsCompact {
		a.telemetryMu.Lock()
		defer a.telemetryMu.Unlock()
		if err := writeTelemetrySyncState(a.telemetryPath, nextTelemetry); err != nil {
			return err
		}
		a.telemetry = nextTelemetry
	}
	return nil
}

func (a *Agent) Flush(ctx context.Context) error {
	entries, err := a.queue.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		response, err := a.client.SendHeartbeat(ctx, a.config.Host, a.identity.DeviceID(), a.identity.PrivateKey(), entry.Sequence, entry.Body)
		if err != nil {
			var endpointError *transport.HTTPError
			if errors.As(err, &endpointError) && endpointError.StatusCode == 410 && endpointError.Code == "agent-registration-revoked" {
				if persistErr := a.markDormant(); persistErr != nil {
					return fmt.Errorf("persist revoked agent state: %w", persistErr)
				}
				return ErrRegistrationRevoked
			}
			if !errors.As(err, &endpointError) || endpointError.StatusCode != 409 || endpointError.Code != "replayed-agent-request" {
				return err
			}
		}
		if response.MonitoringConfig != nil {
			if err := a.applyMonitoringConfig(*response.MonitoringConfig); err != nil {
				return fmt.Errorf("persist monitoring config: %w", err)
			}
		}
		if response.Telemetry != nil && (response.Telemetry.RequestCapabilities || len(response.Telemetry.Reconcile) > 0) {
			a.telemetryMu.Lock()
			if response.Telemetry.RequestCapabilities {
				a.telemetry.CapabilitiesHash = ""
			}
			for _, family := range response.Telemetry.Reconcile {
				state := a.telemetry.Families[family]
				state.LastFullAt = time.Time{}
				a.telemetry.Families[family] = state
			}
			if err := writeTelemetrySyncState(a.telemetryPath, a.telemetry); err != nil {
				a.telemetryMu.Unlock()
				return err
			}
			a.telemetryMu.Unlock()
		}
		if err := a.queue.Remove(entry.Sequence); err != nil {
			return err
		}
		if dropped := reportedDropped(entry.Body); dropped > 0 {
			if err := a.queue.AcknowledgeDropped(dropped); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Agent) SubmitHardwareSnapshot(ctx context.Context, snapshot protocol.HardwareSnapshot) error {
	if a.dormant() {
		return ErrRegistrationRevoked
	}
	if snapshot.Host != a.config.Host {
		return errors.New("hardware snapshot does not match the configured host")
	}
	if err := protocol.ValidateHardwareSnapshot(snapshot); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	defer func() {
		for index := range body {
			body[index] = 0
		}
	}()
	if len(body) > 2<<20 {
		return errors.New("hardware snapshot exceeds the transmission limit")
	}
	sequence, err := a.identity.ReserveSequence()
	if err != nil {
		return err
	}
	return a.client.SendHardwareSnapshot(ctx, a.config.Host, a.identity.DeviceID(), a.identity.PrivateKey(), sequence, body)
}

func (a *Agent) RunOnce(ctx context.Context) error {
	if a.dormant() {
		return ErrRegistrationRevoked
	}
	contract, err := a.Contract(ctx)
	if err != nil {
		return err
	}
	if err := a.Collect(ctx, contract); err != nil {
		return err
	}
	return a.Flush(ctx)
}

func (a *Agent) Run(ctx context.Context) error {
	if a.dormant() {
		return a.waitDormant(ctx)
	}
	contract, err := a.Contract(ctx)
	if err != nil {
		return err
	}
	if collector, ok := a.collector.(backgroundCollector); ok {
		collector.Start(ctx, contract)
	}
	if err := a.Collect(ctx, contract); err != nil {
		a.onError(err)
	} else if err := a.Flush(ctx); err != nil {
		if errors.Is(err, ErrRegistrationRevoked) {
			return a.waitDormant(ctx)
		}
		a.onError(err)
	}
	ticker := time.NewTicker(time.Duration(contract.Collection.HostIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.Collect(ctx, contract); err != nil {
				a.onError(err)
				continue
			}
			if err := a.Flush(ctx); err != nil {
				if errors.Is(err, ErrRegistrationRevoked) {
					return a.waitDormant(ctx)
				}
				a.onError(err)
			}
		}
	}
}
