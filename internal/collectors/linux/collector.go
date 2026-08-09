//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	servicecollector "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/services"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type servicesCollector interface {
	Collect(context.Context) ([]protocol.Service, error)
}

type containersCollector interface {
	Collect(context.Context) ([]protocol.Container, error)
	Detail() string
}

type Collector struct {
	mu                  sync.Mutex
	root                string
	now                 func() time.Time
	previousAt          time.Time
	previousCPU         map[string]cpuCounters
	previousNetwork     map[string]networkCounters
	previousDisk        map[string]diskCounters
	services            servicesCollector
	lastServicesAt      time.Time
	cachedServices      []protocol.Service
	serviceCapability   protocol.Capability
	opaqueID            func(string, string) string
	lastStorageAt       time.Time
	cachedStorage       []protocol.StorageHealth
	storageCapability   protocol.Capability
	containers          containersCollector
	containerCapability protocol.Capability
	gpu                 gpuAggregation
	filesystems         []string
	smartDevices        []string
	smartRunner         commandRunner
	smartctlPath        string
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type Options struct {
	Filesystems  []string
	SMARTDevices []string
	SMARTRunner  commandRunner
	SMARTCTLPath string
	Containers   containersCollector
}

func New(root string, opaqueID func(string, string) string, options ...Options) *Collector {
	if root == "" {
		root = "/"
	}
	settings := Options{}
	if len(options) > 0 {
		settings = options[0]
	}
	if settings.SMARTRunner == nil {
		settings.SMARTRunner = newSMARTCommandRunner()
	}
	if settings.SMARTCTLPath == "" {
		settings.SMARTCTLPath = findSMARTCTL()
	}
	return &Collector{
		root: root, now: time.Now,
		previousCPU: map[string]cpuCounters{}, previousNetwork: map[string]networkCounters{}, previousDisk: map[string]diskCounters{},
		services: servicecollector.NewSystemd(), opaqueID: opaqueID,
		gpu:          newGPUAggregation(),
		filesystems:  append([]string(nil), settings.Filesystems...),
		containers:   settings.Containers,
		smartDevices: append([]string(nil), settings.SMARTDevices...), smartRunner: settings.SMARTRunner, smartctlPath: settings.SMARTCTLPath,
	}
}

func (collector *Collector) Capabilities() map[string]protocol.Capability {
	capabilities := map[string]protocol.Capability{
		"host.uptime":      {State: protocol.Available, Detail: "procfs"},
		"host.system":      {State: protocol.Available, Detail: "procfs and os-release"},
		"host.load":        {State: protocol.Available, Detail: "procfs"},
		"host.cpu":         {State: protocol.Available, Detail: "procfs"},
		"host.memory":      {State: protocol.Available, Detail: "procfs"},
		"host.filesystems": {State: protocol.Available, Detail: "statfs"},
		"host.disk-io":     {State: protocol.Available, Detail: "procfs"},
		"host.network":     {State: protocol.Available, Detail: "procfs"},
		"host.sensors":     {State: protocol.Available, Detail: "sysfs when exposed"},
		"host.batteries":   {State: protocol.Available, Detail: "sysfs when exposed"},
		"host.services":    {State: protocol.Disabled, Detail: "service discovery is not enabled"},
		"host.gpu":         {State: protocol.Unavailable, Detail: "no safe GPU collector detected"},
		"storage.health":   {State: protocol.Disabled, Detail: "storage health is opt-in or unavailable"},
		"containers":       {State: protocol.Disabled, Detail: "container discovery is opt-in"},
	}
	if collector.containers != nil {
		capabilities["containers"] = available(collector.containers.Detail())
	}
	return capabilities
}

func available(detail string) protocol.Capability {
	return protocol.Capability{State: protocol.Available, Detail: detail}
}

func unavailable(err error) protocol.Capability {
	detail := err.Error()
	if len(detail) > 256 {
		detail = detail[:256]
	}
	return protocol.Capability{State: protocol.Unavailable, Detail: detail}
}

func (collector *Collector) Collect(ctx context.Context, contract protocol.Contract) (protocol.Heartbeat, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	now := collector.now().UTC()
	elapsed := now.Sub(collector.previousAt)
	capabilities := collector.Capabilities()
	metrics := protocol.Metrics{}
	collector.collectSystem(&metrics, capabilities)

	if body, err := readFile(collector.root, "proc/uptime"); err == nil {
		if uptime, parseErr := parseUptime(body); parseErr == nil {
			metrics.UptimeSeconds = &uptime
			capabilities["host.uptime"] = available("procfs")
		} else {
			capabilities["host.uptime"] = unavailable(parseErr)
		}
	} else {
		capabilities["host.uptime"] = unavailable(err)
	}
	if body, err := readFile(collector.root, "proc/loadavg"); err == nil {
		if load, parseErr := parseLoadAverage(body); parseErr == nil {
			metrics.LoadAverage = load
			capabilities["host.load"] = available("procfs")
		} else {
			capabilities["host.load"] = unavailable(parseErr)
		}
	} else {
		capabilities["host.load"] = unavailable(err)
	}
	collector.collectCPU(&metrics, capabilities)
	collector.collectMemory(&metrics, capabilities)
	collector.collectNetwork(&metrics, capabilities, elapsed)
	collector.collectDiskIO(&metrics, capabilities, elapsed)
	collector.collectFilesystems(&metrics, capabilities)
	collector.collectSensors(&metrics, capabilities)
	collector.collectBatteries(&metrics, capabilities)
	if gpus, err := collector.collectAggregatedGPUs(); err != nil {
		capabilities["host.gpu"] = unavailable(err)
	} else {
		metrics.GPUs = gpus
		capabilities["host.gpu"] = available("read-only DRM sysfs")
	}
	serviceInterval := contract.Collection.ServiceIntervalSeconds
	if serviceInterval <= 0 {
		serviceInterval = 600
	}
	if collector.lastServicesAt.IsZero() || now.Sub(collector.lastServicesAt) >= time.Duration(serviceInterval)*time.Second {
		collector.lastServicesAt = now
		services, err := collector.services.Collect(ctx)
		if err != nil {
			collector.serviceCapability = unavailable(err)
		} else {
			collector.cachedServices = services
			collector.serviceCapability = available("systemd")
		}
	}
	if collector.serviceCapability.State != "" {
		capabilities["host.services"] = collector.serviceCapability
	}
	storageInterval := contract.Collection.StorageHealthIntervalSeconds
	if storageInterval <= 0 {
		storageInterval = 3600
	}
	if collector.lastStorageAt.IsZero() || now.Sub(collector.lastStorageAt) >= time.Duration(storageInterval)*time.Second {
		collector.lastStorageAt = now
		smartEnabled := contract.Privacy.SMARTEnabled && len(collector.smartDevices) > 0
		storage, err := collector.collectStorageHealth(ctx, now, smartEnabled)
		if err != nil {
			collector.storageCapability = unavailable(err)
		} else if len(storage) == 0 && len(collector.smartDevices) > 0 && !contract.Privacy.SMARTEnabled {
			collector.cachedStorage = nil
			collector.storageCapability = protocol.Capability{State: protocol.Disabled, Detail: "SMART disabled by server contract"}
		} else if len(storage) == 0 {
			collector.cachedStorage = nil
			collector.storageCapability = protocol.Capability{State: protocol.Unavailable, Detail: "no supported storage-health device found"}
		} else {
			collector.cachedStorage = storage
			collector.storageCapability = available("read-only sysfs")
		}
	}
	if collector.storageCapability.State != "" {
		capabilities["storage.health"] = collector.storageCapability
	}
	containers := []protocol.Container(nil)
	if collector.containers == nil {
		capabilities["containers"] = protocol.Capability{State: protocol.Disabled, Detail: "container discovery is not configured"}
	} else if !contract.Privacy.ContainersEnabled {
		capabilities["containers"] = protocol.Capability{State: protocol.Disabled, Detail: "container discovery disabled by server contract"}
	} else if collected, err := collector.containers.Collect(ctx); err != nil {
		state := protocol.Unavailable
		if strings.Contains(strings.ToLower(err.Error()), "permission denied") || strings.Contains(strings.ToLower(err.Error()), "access denied") {
			state = protocol.PermissionBlocked
		}
		collector.containerCapability = protocol.Capability{State: state, Detail: err.Error()[:min(len(err.Error()), 256)]}
		capabilities["containers"] = collector.containerCapability
	} else {
		containers = collected
		collector.containerCapability = available(collector.containers.Detail())
		capabilities["containers"] = collector.containerCapability
	}

	collector.previousAt = now
	hostname, _ := os.Hostname()
	return protocol.Heartbeat{
		CollectedAt: now, Hostname: hostname, Capabilities: capabilities, Metrics: metrics,
		Services:      append([]protocol.Service(nil), collector.cachedServices[:min(len(collector.cachedServices), 512)]...),
		Containers:    append([]protocol.Container(nil), containers[:min(len(containers), 128)]...),
		StorageHealth: append([]protocol.StorageHealth(nil), collector.cachedStorage[:min(len(collector.cachedStorage), 64)]...),
	}, nil
}

func (collector *Collector) collectCPU(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, err := readFile(collector.root, "proc/stat")
	if err != nil {
		capabilities["host.cpu"] = unavailable(err)
		return
	}
	counters, err := parseCPUCounters(body)
	if err != nil {
		capabilities["host.cpu"] = unavailable(err)
		return
	}
	cpu := metrics.CPU
	if cpu == nil {
		cpu = map[string]any{}
	}
	cores := make([]map[string]any, 0, len(counters)-1)
	for _, current := range counters {
		previous, exists := collector.previousCPU[current.Name]
		percent, valid := percentage(current, previous)
		if current.Name == "cpu" {
			if exists && valid {
				cpu["percent"] = percent
				if breakdown, breakdownValid := cpuBreakdown(current, previous); breakdownValid {
					for key, value := range breakdown {
						cpu[key] = value
					}
				}
			}
		} else {
			core := map[string]any{"name": current.Name}
			if exists && valid {
				core["percent"] = percent
			}
			cores = append(cores, core)
		}
		collector.previousCPU[current.Name] = current
	}
	cpu["logicalCores"] = len(cores)
	cpu["cores"] = cores
	metrics.CPU = cpu
	capabilities["host.cpu"] = available("procfs")
}

func (collector *Collector) collectMemory(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, err := readFile(collector.root, "proc/meminfo")
	if err != nil {
		capabilities["host.memory"] = unavailable(err)
		return
	}
	memory, err := parseMeminfo(body)
	if err != nil {
		capabilities["host.memory"] = unavailable(err)
		return
	}
	metrics.Memory = memory
	if body, arcErr := readFile(collector.root, "proc/spl/kstat/zfs/arcstats"); arcErr == nil {
		if arcBytes, parseErr := parseZFSARC(body); parseErr == nil {
			metrics.Memory["zfsArcBytes"] = arcBytes
		}
	}
	capabilities["host.memory"] = available("procfs")
}

func (collector *Collector) collectNetwork(metrics *protocol.Metrics, capabilities map[string]protocol.Capability, elapsed time.Duration) {
	body, err := readFile(collector.root, "proc/net/dev")
	if err != nil {
		capabilities["host.network"] = unavailable(err)
		return
	}
	counters, err := parseNetworkCounters(body)
	if err != nil {
		capabilities["host.network"] = unavailable(err)
		return
	}
	result := make([]map[string]any, 0, min(len(counters)+1, 128))
	aggregate := map[string]any{"name": "all", "aggregate": true}
	var aggregateReceive, aggregateTransmit uint64
	var aggregateReceiveRate, aggregateTransmitRate float64
	var aggregateHasRate bool
	for _, current := range counters {
		if len(result) == 127 {
			break
		}
		entry := map[string]any{
			"name": current.Name, "receiveBytes": current.ReceiveBytes, "transmitBytes": current.TransmitBytes,
			"receivePackets": current.ReceivePackets, "transmitPackets": current.TransmitPackets,
			"receiveErrors": current.ReceiveErrors, "transmitErrors": current.TransmitErrors,
			"receiveDrops": current.ReceiveDrops, "transmitDrops": current.TransmitDrops,
		}
		if previous, exists := collector.previousNetwork[current.Name]; exists {
			if value, valid := rate(current.ReceiveBytes, previous.ReceiveBytes, elapsed); valid {
				entry["receiveBytesPerSecond"] = value
				if current.Name != "lo" {
					aggregateReceiveRate += value
					aggregateHasRate = true
				}
			}
			if value, valid := rate(current.TransmitBytes, previous.TransmitBytes, elapsed); valid {
				entry["transmitBytesPerSecond"] = value
				if current.Name != "lo" {
					aggregateTransmitRate += value
					aggregateHasRate = true
				}
			}
		}
		if current.Name != "lo" {
			aggregateReceive += current.ReceiveBytes
			aggregateTransmit += current.TransmitBytes
		}
		collector.previousNetwork[current.Name] = current
		result = append(result, entry)
	}
	aggregate["receiveBytes"] = aggregateReceive
	aggregate["transmitBytes"] = aggregateTransmit
	if aggregateHasRate {
		aggregate["receiveBytesPerSecond"] = aggregateReceiveRate
		aggregate["transmitBytesPerSecond"] = aggregateTransmitRate
	}
	result = append([]map[string]any{aggregate}, result...)
	metrics.Network = result
	capabilities["host.network"] = available("procfs")
}

func (collector *Collector) collectDiskIO(metrics *protocol.Metrics, capabilities map[string]protocol.Capability, elapsed time.Duration) {
	body, err := readFile(collector.root, "proc/diskstats")
	if err != nil {
		capabilities["host.disk-io"] = unavailable(err)
		return
	}
	counters, err := parseDiskCounters(body)
	if err != nil {
		capabilities["host.disk-io"] = unavailable(err)
		return
	}
	result := make([]map[string]any, 0, len(counters))
	for _, current := range counters {
		if len(result) == 128 {
			break
		}
		entry := map[string]any{
			"name": current.Name, "reads": current.Reads, "writes": current.Writes,
			"readBytes": current.SectorsRead * 512, "writeBytes": current.SectorsWritten * 512,
			"readMilliseconds": current.ReadMilliseconds, "writeMilliseconds": current.WriteMilliseconds,
			"ioInProgress": current.IOInProgress, "ioMilliseconds": current.IOMilliseconds,
		}
		if previous, exists := collector.previousDisk[current.Name]; exists {
			if value, valid := rate(current.SectorsRead*512, previous.SectorsRead*512, elapsed); valid {
				entry["readBytesPerSecond"] = value
			}
			if value, valid := rate(current.SectorsWritten*512, previous.SectorsWritten*512, elapsed); valid {
				entry["writeBytesPerSecond"] = value
			}
			if current.validDelta(previous) {
				readDelta := current.Reads - previous.Reads
				writeDelta := current.Writes - previous.Writes
				entry["readOperationsPerSecond"] = float64(readDelta) / elapsed.Seconds()
				entry["writeOperationsPerSecond"] = float64(writeDelta) / elapsed.Seconds()
				if readDelta > 0 {
					entry["readAwaitMilliseconds"] = float64(current.ReadMilliseconds-previous.ReadMilliseconds) / float64(readDelta)
				}
				if writeDelta > 0 {
					entry["writeAwaitMilliseconds"] = float64(current.WriteMilliseconds-previous.WriteMilliseconds) / float64(writeDelta)
				}
				elapsedMilliseconds := float64(elapsed.Milliseconds())
				if elapsedMilliseconds > 0 {
					utilization := float64(current.IOMilliseconds-previous.IOMilliseconds) * 100 / elapsedMilliseconds
					entry["utilizationPercent"] = min(utilization, 100)
				}
				entry["weightedIOMilliseconds"] = current.WeightedIOMilliseconds - previous.WeightedIOMilliseconds
			}
		}
		collector.previousDisk[current.Name] = current
		result = append(result, entry)
	}
	metrics.DiskIO = result
	capabilities["host.disk-io"] = available("procfs")
}

func (collector *Collector) collectFilesystems(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	if collector.root != "/" {
		capabilities["host.filesystems"] = protocol.Capability{State: protocol.Disabled, Detail: "statfs disabled for fixture root"}
		return
	}
	result, err := collectMountedFilesystems("/proc/self/mountinfo")
	metrics.Filesystems = result
	if err != nil {
		capabilities["host.filesystems"] = unavailable(err)
		return
	}
	capabilities["host.filesystems"] = available("mountinfo and statfs")
}

func (collector *Collector) collectSensors(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	base := fmt.Sprintf("%s/sys/class/hwmon", strings.TrimRight(collector.root, "/"))
	entries, err := os.ReadDir(base)
	if err != nil {
		capabilities["host.sensors"] = unavailable(err)
		return
	}
	var sensors []map[string]any
	for _, directory := range entries {
		if len(sensors) == 256 {
			break
		}
		chipName := strings.TrimSpace(string(readOptional(base + "/" + directory.Name() + "/name")))
		if chipName == "" {
			chipName = directory.Name()
		}
		files, _ := os.ReadDir(base + "/" + directory.Name())
		for _, file := range files {
			if len(sensors) == 256 {
				break
			}
			if !strings.HasPrefix(file.Name(), "temp") || !strings.HasSuffix(file.Name(), "_input") {
				continue
			}
			body, readErr := os.ReadFile(base + "/" + directory.Name() + "/" + file.Name())
			if readErr != nil {
				continue
			}
			var milli float64
			if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(body)), "%f", &milli); scanErr == nil {
				baseName := strings.TrimSuffix(file.Name(), "_input")
				label := strings.TrimSpace(string(readOptional(base + "/" + directory.Name() + "/" + baseName + "_label")))
				if label == "" {
					label = baseName
				}
				sensors = append(sensors, map[string]any{
					"id": collector.opaque("sensor", chipName+":"+label), "source": chipName,
					"name": label, "temperatureC": milli / 1000,
				})
			}
		}
	}
	sort.Slice(sensors, func(i, j int) bool { return fmt.Sprint(sensors[i]["name"]) < fmt.Sprint(sensors[j]["name"]) })
	metrics.Sensors = sensors
	capabilities["host.sensors"] = available("sysfs")
}

func (collector *Collector) collectBatteries(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	base := fmt.Sprintf("%s/sys/class/power_supply", strings.TrimRight(collector.root, "/"))
	entries, err := os.ReadDir(base)
	if err != nil {
		capabilities["host.batteries"] = unavailable(err)
		return
	}
	var batteries []map[string]any
	for _, directory := range entries {
		if len(batteries) == 16 {
			break
		}
		typeBody, _ := os.ReadFile(base + "/" + directory.Name() + "/type")
		if strings.TrimSpace(string(typeBody)) != "Battery" {
			continue
		}
		entry := map[string]any{"id": collector.opaque("battery", directory.Name()), "name": directory.Name()}
		for file, key := range map[string]string{"capacity": "percent", "energy_now": "energyNowMicrowattHours", "energy_full": "energyFullMicrowattHours"} {
			if body, readErr := os.ReadFile(base + "/" + directory.Name() + "/" + file); readErr == nil {
				if parsed, parseErr := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 64); parseErr == nil {
					entry[key] = parsed
				}
			}
		}
		status := strings.ToLower(strings.TrimSpace(string(readOptional(base + "/" + directory.Name() + "/status"))))
		switch status {
		case "charging", "discharging", "full", "empty", "not charging":
			if status == "not charging" {
				status = "idle"
			}
		case "":
			status = "unknown"
		default:
			status = "unknown"
		}
		entry["status"] = status
		batteries = append(batteries, entry)
	}
	metrics.Batteries = batteries
	capabilities["host.batteries"] = available("sysfs")
}

func readOptional(path string) []byte {
	body, _ := os.ReadFile(path)
	return body
}
