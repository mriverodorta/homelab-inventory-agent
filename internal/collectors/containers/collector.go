package containers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const (
	maximumContainers = 128
	maximumBodyBytes  = 8 << 20
)

type Options struct {
	Mode     string
	Runtime  string
	Endpoint string
	Client   *http.Client
}

type counters struct {
	at        time.Time
	networkRx uint64
	networkTx uint64
	diskRead  uint64
	diskWrite uint64
}

type Collector struct {
	mode     string
	runtime  string
	baseURL  string
	client   *http.Client
	mu       sync.Mutex
	previous map[string]counters
	now      func() time.Time
}

type listContainer struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	ImageID string   `json:"ImageID"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort uint16 `json:"PrivatePort"`
		PublicPort  uint16 `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

type statsPayload struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint64 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Stats struct {
			Cache    uint64 `json:"cache"`
			Inactive uint64 `json:"inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		Bytes []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

func New(options Options) (*Collector, error) {
	if options.Mode != "proxy" && options.Mode != "socket" {
		return nil, errors.New("container collector requires proxy or socket mode")
	}
	if options.Runtime != "docker" && options.Runtime != "podman" {
		return nil, errors.New("container collector runtime is unsupported")
	}
	client := options.Client
	baseURL := strings.TrimRight(options.Endpoint, "/")
	if client == nil {
		transport := &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
		if options.Mode == "socket" {
			socket := options.Endpoint
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
			}
			baseURL = "http://container-runtime"
		}
		client = &http.Client{Transport: transport, Timeout: 12 * time.Second}
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return nil, errors.New("container collector endpoint is invalid")
	}
	return &Collector{mode: options.Mode, runtime: options.Runtime, baseURL: baseURL, client: client, previous: map[string]counters{}, now: time.Now}, nil
}

func (collector *Collector) Detail() string {
	if collector.mode == "proxy" {
		return "read-only " + collector.runtime + " socket proxy"
	}
	return "local " + collector.runtime + " socket"
}

func (collector *Collector) getJSON(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, collector.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := collector.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("container runtime access denied with HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("container runtime returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumBodyBytes+1))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode container runtime response: %w", err)
	}
	return nil
}

func (collector *Collector) Collect(ctx context.Context) ([]protocol.Container, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	var listed []listContainer
	if err := collector.getJSON(ctx, "/v1.41/containers/json?all=0", &listed); err != nil {
		return nil, err
	}
	if len(listed) > maximumContainers {
		listed = listed[:maximumContainers]
	}
	sort.Slice(listed, func(first, second int) bool { return listed[first].ID < listed[second].ID })
	now := collector.now().UTC()
	result := make([]protocol.Container, 0, len(listed))
	currentIDs := make(map[string]struct{}, len(listed))
	for _, listedContainer := range listed {
		if strings.TrimSpace(listedContainer.ID) == "" || strings.TrimSpace(listedContainer.Image) == "" {
			continue
		}
		currentIDs[listedContainer.ID] = struct{}{}
		name := strings.TrimPrefix(first(listedContainer.Names), "/")
		if name == "" {
			name = shortID(listedContainer.ID)
		}
		container := protocol.Container{
			Runtime: collector.runtime, RuntimeID: listedContainer.ID, Name: name,
			Image: listedContainer.Image, State: listedContainer.State,
			PublishedPorts: publishedPorts(listedContainer),
		}
		if listedContainer.ImageID != "" {
			digest := listedContainer.ImageID
			container.ImageDigest = &digest
		}
		if health := healthFromStatus(listedContainer.Status); health != "" {
			container.Health = &health
		}
		var stats statsPayload
		if err := collector.getJSON(ctx, "/v1.41/containers/"+url.PathEscape(listedContainer.ID)+"/stats?stream=false&one-shot=true", &stats); err == nil {
			applyStats(&container, stats, collector.previous[listedContainer.ID], now)
			collector.previous[listedContainer.ID] = statsCounters(stats, now)
		}
		result = append(result, container)
	}
	for id := range collector.previous {
		if _, exists := currentIDs[id]; !exists {
			delete(collector.previous, id)
		}
	}
	return result, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func shortID(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func healthFromStatus(status string) string {
	lower := strings.ToLower(status)
	for _, health := range []string{"healthy", "unhealthy", "starting"} {
		if strings.Contains(lower, "("+health+")") {
			return health
		}
	}
	return ""
}

func publishedPorts(container listContainer) []string {
	ports := make([]string, 0, len(container.Ports))
	for _, port := range container.Ports {
		if port.PublicPort == 0 {
			continue
		}
		value := fmt.Sprintf("%d:%d/%s", port.PublicPort, port.PrivatePort, strings.ToLower(port.Type))
		if port.IP != "" {
			value = port.IP + ":" + value
		}
		ports = append(ports, value)
	}
	sort.Strings(ports)
	return ports
}

func statsCounters(stats statsPayload, at time.Time) counters {
	value := counters{at: at}
	for _, network := range stats.Networks {
		value.networkRx += network.RxBytes
		value.networkTx += network.TxBytes
	}
	for _, entry := range stats.BlkioStats.Bytes {
		switch strings.ToLower(entry.Op) {
		case "read":
			value.diskRead += entry.Value
		case "write":
			value.diskWrite += entry.Value
		}
	}
	return value
}

func applyStats(container *protocol.Container, stats statsPayload, previous counters, now time.Time) {
	cpuDelta := stats.CPUStats.CPUUsage.TotalUsage - min(stats.CPUStats.CPUUsage.TotalUsage, stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := stats.CPUStats.SystemCPUUsage - min(stats.CPUStats.SystemCPUUsage, stats.PreCPUStats.SystemCPUUsage)
	cores := stats.CPUStats.OnlineCPUs
	if cores == 0 {
		cores = uint64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if systemDelta > 0 && cores > 0 {
		value := float64(cpuDelta) / float64(systemDelta) * float64(cores) * 100
		container.CPUPercent = &value
	}
	memory := stats.MemoryStats.Usage
	cache := max(stats.MemoryStats.Stats.Cache, stats.MemoryStats.Stats.Inactive)
	if memory >= cache {
		memory -= cache
	}
	container.MemoryBytes = &memory
	current := statsCounters(stats, now)
	elapsed := now.Sub(previous.at).Seconds()
	if !previous.at.IsZero() && elapsed > 0 {
		container.NetworkRxBytesPerSecond = rate(current.networkRx, previous.networkRx, elapsed)
		container.NetworkTxBytesPerSecond = rate(current.networkTx, previous.networkTx, elapsed)
		container.DiskReadBytesPerSecond = rate(current.diskRead, previous.diskRead, elapsed)
		container.DiskWriteBytesPerSecond = rate(current.diskWrite, previous.diskWrite, elapsed)
	}
}

func rate(current, previous uint64, elapsed float64) *float64 {
	if current < previous {
		return nil
	}
	value := float64(current-previous) / elapsed
	return &value
}
