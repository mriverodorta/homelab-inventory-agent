package containers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.41/containers/json", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("all") != "0" {
			http.Error(response, "invalid all value", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(response).Encode([]map[string]any{{
			"Id": "abcdef0123456789", "Names": []string{"/media"}, "Image": "example/media:1",
			"ImageID": "sha256:image", "State": "running", "Status": "Up 2 hours (healthy)",
			"Ports": []map[string]any{{"IP": "127.0.0.1", "PrivatePort": 8096, "PublicPort": 8096, "Type": "tcp"}},
			"Env":   []string{"TOKEN=secret"}, "Labels": map[string]string{"secret": "value"},
			"Command": []string{"server", "--token", "secret"}, "Mounts": []string{"/private"},
		}})
	})
	mux.HandleFunc("/v1.41/containers/abcdef0123456789/stats", func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{
			"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": 200, "percpu_usage": []int{100, 100}}, "system_cpu_usage": 1000, "online_cpus": 2},
			"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": 100}, "system_cpu_usage": 500},
			"memory_stats": map[string]any{"usage": 2048, "stats": map[string]any{"inactive_file": 512}},
			"networks":     map[string]any{"eth0": map[string]any{"rx_bytes": 2000, "tx_bytes": 1000}},
			"blkio_stats":  map[string]any{"io_service_bytes_recursive": []map[string]any{{"op": "Read", "value": 3000}, {"op": "Write", "value": 4000}}},
			"environment":  []string{"SHOULD_NOT=DECODE"},
		})
	})
	return mux
}

func TestProxyCollectorAllowsOnlySanitizedContainerFields(t *testing.T) {
	server := httptest.NewServer(runtimeHandler())
	defer server.Close()
	collector, err := New(Options{Mode: "proxy", Runtime: "docker", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	collector.now = func() time.Time { return time.Unix(100, 0) }
	containers, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].Name != "media" || containers[0].Health == nil || *containers[0].Health != "healthy" {
		t.Fatalf("unexpected containers: %#v", containers)
	}
	if containers[0].MemoryBytes == nil || *containers[0].MemoryBytes != 1536 || containers[0].CPUPercent == nil || *containers[0].CPUPercent != 40 {
		t.Fatalf("unexpected metrics: %#v", containers[0])
	}
	body, err := json.Marshal(containers)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{"token=secret", "should_not", "labels", "command", "mounts", "/private"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("payload exposed forbidden value %q: %s", forbidden, body)
		}
	}
}

func TestSocketCollectorUsesUnixSocketWithoutRuntimePrivileges(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "hli-container-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: runtimeHandler(), ReadHeaderTimeout: time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	collector, err := New(Options{Mode: "socket", Runtime: "podman", Endpoint: socket})
	if err != nil {
		t.Fatal(err)
	}
	containers, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].Runtime != "podman" {
		t.Fatalf("unexpected socket result: %#v", containers)
	}
	if info, err := os.Stat(socket); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket was not used: %v", err)
	}
}

func TestCollectorCalculatesRatesOnlyAfterAPreviousSample(t *testing.T) {
	server := httptest.NewServer(runtimeHandler())
	defer server.Close()
	collector, err := New(Options{Mode: "proxy", Runtime: "docker", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	collector.now = func() time.Time { return now }
	first, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first[0].NetworkRxBytesPerSecond != nil {
		t.Fatal("first sample reported an invented rate")
	}
	now = now.Add(time.Minute)
	second, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second[0].NetworkRxBytesPerSecond == nil || *second[0].NetworkRxBytesPerSecond != 0 {
		t.Fatalf("unexpected second-sample rate: %#v", second[0].NetworkRxBytesPerSecond)
	}
}
