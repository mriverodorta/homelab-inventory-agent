package runtime

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/buffer"
	"github.com/mriverodorta/homelab-inventory-agent/internal/config"
	"github.com/mriverodorta/homelab-inventory-agent/internal/identity"
	"github.com/mriverodorta/homelab-inventory-agent/internal/transport"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type staticCollector struct{}

func (staticCollector) Collect(_ context.Context, _ protocol.Contract) (protocol.Heartbeat, error) {
	return protocol.Heartbeat{
		CollectedAt:  time.Now().UTC(),
		Capabilities: map[string]protocol.Capability{"host.cpu": {State: protocol.Available}},
		Metrics:      protocol.Metrics{LoadAverage: []float64{0.1, 0.2, 0.3}},
	}, nil
}

func runtimeContract(t *testing.T) protocol.Contract {
	t.Helper()
	digest, err := protocol.BundleDigest()
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Contract{
		ProtocolMajor: 1, Revision: 1, IssuedAt: time.Now().UTC(), SchemaBundleDigest: digest,
		Collection: protocol.CollectionPolicy{HostIntervalSeconds: 60, ServiceIntervalSeconds: 600, StorageHealthIntervalSeconds: 3600, GPUSampleIntervalSeconds: 4},
		Limits:     protocol.PayloadLimits{CompressedBytes: 262144, DecompressedBytes: 1048576, OfflineSamples: 60, OfflineBytes: 10485760},
	}
}

type runtimeServer struct {
	server      *httptest.Server
	mu          sync.Mutex
	fail        bool
	sequences   []uint64
	droppedSeen []uint64
}

func newRuntimeServer(t *testing.T, offlineSamples ...int) *runtimeServer {
	t.Helper()
	state := &runtimeServer{}
	contract := runtimeContract(t)
	if len(offlineSamples) > 0 {
		contract.Limits.OfflineSamples = offlineSamples[0]
	}
	state.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/agent/contracts/current":
			if request.Header.Get("If-None-Match") == `"v1"` {
				response.WriteHeader(http.StatusNotModified)
				return
			}
			response.Header().Set("ETag", `"v1"`)
			_ = json.NewEncoder(response).Encode(contract)
		case "/api/agent/hosts/server/1/activate":
			_ = json.NewEncoder(response).Encode(transport.ActivationResponse{DeviceID: 4, ProtocolMajor: 1})
		case "/api/agent/hosts/server/1/heartbeats":
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.fail {
				http.Error(response, "offline", http.StatusServiceUnavailable)
				return
			}
			reader, err := gzip.NewReader(request.Body)
			if err != nil {
				t.Error(err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			var heartbeat protocol.Heartbeat
			if err := json.NewDecoder(reader).Decode(&heartbeat); err != nil {
				t.Error(err)
			}
			_ = reader.Close()
			state.sequences = append(state.sequences, heartbeat.Sequence)
			state.droppedSeen = append(state.droppedSeen, heartbeat.DroppedSamples)
			_, _ = response.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(response, request)
		}
	}))
	return state
}

func createRuntime(t *testing.T, endpoint, directory string, limits buffer.Limits) (*Agent, *identity.Identity, *buffer.Queue) {
	t.Helper()
	configuration := config.Config{Endpoint: endpoint, Host: protocol.HostRef{Type: protocol.HostServer, ID: 1}, StateDirectory: directory}
	deviceIdentity, err := identity.LoadOrCreate(filepath.Join(directory, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	queue, err := buffer.Open(filepath.Join(directory, "queue"), limits)
	if err != nil {
		t.Fatal(err)
	}
	client, err := transport.New(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := New(Options{
		Config: configuration, Version: "0.1.0-dev",
		Capabilities: map[string]protocol.Capability{"host.cpu": {State: protocol.Available}},
		Identity:     deviceIdentity, Queue: queue, Client: client, Collector: staticCollector{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return agent, deviceIdentity, queue
}

func TestAgentActivatesCachesContractAndPersistsIdentity(t *testing.T) {
	server := newRuntimeServer(t)
	defer server.server.Close()
	directory := t.TempDir()
	agent, deviceIdentity, _ := createRuntime(t, server.server.URL, directory, buffer.Limits{Samples: 60, Bytes: 10 << 20})
	if err := agent.Activate(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	if deviceIdentity.DeviceID() != 4 {
		t.Fatalf("device was not activated: %d", deviceIdentity.DeviceID())
	}
	if _, err := agent.Contract(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.server.Close()
	if contract, err := agent.Contract(context.Background()); err != nil || contract.Revision != 1 {
		t.Fatalf("cached contract unavailable during outage: %#v %v", contract, err)
	}
	restarted, err := identity.LoadOrCreate(filepath.Join(directory, "identity.json"))
	if err != nil || restarted.DeviceID() != 4 {
		t.Fatalf("activated identity did not survive restart: %v", err)
	}
}

func TestAgentBuffersOutageAndFlushesInSequence(t *testing.T) {
	server := newRuntimeServer(t)
	defer server.server.Close()
	agent, _, queue := createRuntime(t, server.server.URL, t.TempDir(), buffer.Limits{Samples: 60, Bytes: 10 << 20})
	if err := agent.Activate(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	if err := agent.RunOnce(context.Background()); err == nil {
		t.Fatal("outage did not surface to the one-shot caller")
	}
	entries, _ := queue.Entries()
	if len(entries) != 1 || entries[0].Sequence != 1 {
		t.Fatalf("heartbeat was not buffered: %#v", entries)
	}

	server.mu.Lock()
	server.fail = false
	server.mu.Unlock()
	if err := agent.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.sequences) != 2 || server.sequences[0] != 1 || server.sequences[1] != 2 {
		t.Fatalf("buffer flushed out of order: %#v", server.sequences)
	}
}

func TestAgentReportsOnlyAcknowledgedDroppedSamples(t *testing.T) {
	server := newRuntimeServer(t, 1)
	defer server.server.Close()
	agent, _, queue := createRuntime(t, server.server.URL, t.TempDir(), buffer.Limits{Samples: 1, Bytes: 10 << 20})
	if err := agent.Activate(context.Background(), "token"); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.fail = true
	server.mu.Unlock()
	_ = agent.RunOnce(context.Background())
	_ = agent.RunOnce(context.Background())

	server.mu.Lock()
	server.fail = false
	server.mu.Unlock()
	if err := agent.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	if len(server.droppedSeen) != 1 || server.droppedSeen[0] != 1 {
		t.Fatalf("drop report mismatch: %#v", server.droppedSeen)
	}
	server.mu.Unlock()
	if dropped, _ := queue.Dropped(); dropped != 1 {
		t.Fatalf("newer unreported drop was cleared: %d", dropped)
	}
}
