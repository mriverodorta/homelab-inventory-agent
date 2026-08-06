package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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

type Agent struct {
	config       config.Config
	version      string
	capabilities map[string]protocol.Capability
	identity     *identity.Identity
	queue        *buffer.Queue
	client       *transport.Client
	collector    Collector
	contractPath string
	onError      func(error)
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
	return &Agent{
		config: options.Config, version: options.Version, capabilities: options.Capabilities,
		identity: options.Identity, queue: options.Queue, client: options.Client,
		collector: options.Collector, contractPath: filepath.Join(options.Config.StateDirectory, "contract.json"), onError: onError,
	}, nil
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
	if err != nil {
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
	heartbeat, err := a.collector.Collect(ctx, contract)
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
	if heartbeat.CollectedAt.IsZero() {
		heartbeat.CollectedAt = time.Now().UTC()
	}
	if heartbeat.Capabilities == nil {
		heartbeat.Capabilities = a.capabilities
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
	_, err = a.queue.Add(buffer.Entry{Sequence: sequence, Body: body})
	return err
}

func (a *Agent) Flush(ctx context.Context) error {
	entries, err := a.queue.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := a.client.SendHeartbeat(ctx, a.config.Host, a.identity.DeviceID(), a.identity.PrivateKey(), entry.Sequence, entry.Body); err != nil {
			var endpointError *transport.HTTPError
			if !errors.As(err, &endpointError) || endpointError.StatusCode != 409 || endpointError.Code != "replayed-agent-request" {
				return err
			}
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
				a.onError(err)
			}
		}
	}
}
