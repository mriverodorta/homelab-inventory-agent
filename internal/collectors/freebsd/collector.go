package freebsd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	servicecollector "github.com/mriverodorta/homelab-inventory-agent/internal/collectors/services"
	commandrunner "github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

var coreSysctlKeys = []string{
	"kern.osrelease", "hw.machine_arch", "hw.model", "hw.ncpu", "hw.physmem", "kern.boottime", "vm.loadavg",
	"kern.cp_time", "kern.cp_times", "vm.stats.vm.v_page_size", "vm.stats.vm.v_free_count",
	"vm.stats.vm.v_page_count", "vm.stats.vm.v_active_count", "vm.stats.vm.v_inactive_count",
	"vm.stats.vm.v_cache_count", "vm.stats.vm.v_laundry_count", "vm.stats.vm.v_wire_count",
	"kstat.zfs.misc.arcstats.size",
}

type serviceCollector interface {
	Collect(context.Context) ([]protocol.Service, error)
}

type Options struct {
	Runner      commandrunner.Runner
	Services    serviceCollector
	Filesystems []string
}

type Collector struct {
	mu              sync.Mutex
	runner          commandrunner.Runner
	services        serviceCollector
	filesystems     []string
	now             func() time.Time
	hostname        func() (string, error)
	previousAt      time.Time
	previousCPU     cpuCounters
	previousCores   []cpuCounters
	previousNetwork map[string]networkCounters
	lastServicesAt  time.Time
	cachedServices  []protocol.Service
	serviceState    protocol.Capability
	staticCollected bool
	staticSystem    map[string]any
}

func New(options Options) *Collector {
	if options.Runner == nil {
		options.Runner = commandrunner.Exec{MaxOutput: 1024 * 1024}
	}
	if options.Services == nil {
		options.Services = servicecollector.NewRCd()
	}
	return &Collector{
		runner: options.Runner, services: options.Services, filesystems: append([]string(nil), options.Filesystems...),
		now: time.Now, hostname: os.Hostname, previousNetwork: map[string]networkCounters{},
	}
}

func (*Collector) Capabilities() map[string]protocol.Capability {
	return map[string]protocol.Capability{
		"host.uptime":            available("FreeBSD sysctl"),
		"host.system":            available("FreeBSD sysctl"),
		"host.load":              available("FreeBSD sysctl"),
		"host.cpu":               available("FreeBSD sysctl"),
		"host.memory":            available("FreeBSD sysctl and swapinfo"),
		"host.filesystems":       available("FreeBSD df"),
		"host.disk-io":           {State: protocol.Disabled, Detail: "continuous disk I/O collection is disabled"},
		"host.network":           {State: protocol.Disabled, Detail: "continuous network collection is disabled"},
		"host.pci":               {State: protocol.Unavailable, Detail: "PCI inventory not collected yet"},
		"host.storage-inventory": {State: protocol.Unavailable, Detail: "disk inventory not collected yet"},
		"host.sensors":           {State: protocol.Unavailable, Detail: "no readable FreeBSD sensor exposed"},
		"host.batteries":         {State: protocol.Unavailable, Detail: "no readable ACPI battery exposed"},
		"host.services":          {State: protocol.Unavailable, Detail: "rc.d services not collected yet"},
		"host.processes":         {State: protocol.PermissionBlocked, Detail: "host policy hides other users' processes; resource values are omitted"},
		"host.gpu":               {State: protocol.Unavailable, Detail: "no safe FreeBSD GPU collector is enabled"},
		"storage.health":         {State: protocol.Disabled, Detail: "continuous FreeBSD storage health is not enabled"},
		"containers":             {State: protocol.Disabled, Detail: "container discovery is opt-in"},
	}
}

func available(detail string) protocol.Capability {
	return protocol.Capability{State: protocol.Available, Detail: detail}
}

func unavailable(detail string) protocol.Capability {
	if len(detail) > 256 {
		detail = detail[:256]
	}
	return protocol.Capability{State: protocol.Unavailable, Detail: detail}
}

func sensorSysctlKeys(cpus int) []string {
	if cpus < 0 {
		cpus = 0
	}
	if cpus > 128 {
		cpus = 128
	}
	keys := make([]string, 0, cpus+8)
	for index := 0; index < cpus; index++ {
		keys = append(keys, fmt.Sprintf("dev.cpu.%d.temperature", index))
	}
	for index := 0; index < 8; index++ {
		keys = append(keys, fmt.Sprintf("hw.acpi.thermal.tz%d.temperature", index))
	}
	return keys
}

func (collector *Collector) Collect(ctx context.Context, contract protocol.Contract) (protocol.Heartbeat, error) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	now := collector.now().UTC()
	elapsed := now.Sub(collector.previousAt)
	capabilities := collector.Capabilities()
	metrics := protocol.Metrics{}
	coreBody, coreErr := collector.run(ctx, "/sbin/sysctl", coreSysctlKeys...)
	values := parseSysctl(coreBody)
	if len(values) == 0 {
		if coreErr == nil {
			coreErr = errors.New("sysctl returned no recognized values")
		}
		return protocol.Heartbeat{}, fmt.Errorf("collect FreeBSD sysctl metrics: %w", coreErr)
	}
	collector.collectSystem(values, now, &metrics, capabilities)
	collector.collectStaticHardware(ctx, &metrics, capabilities)
	collector.collectCPU(values, &metrics, capabilities)
	collector.collectMemory(ctx, values, &metrics, capabilities)
	collector.collectFilesystems(ctx, &metrics, capabilities)
	_ = elapsed
	collector.collectSensors(ctx, values, &metrics, capabilities)
	collector.collectBattery(ctx, &metrics, capabilities)
	collector.collectServices(ctx, contract, now, capabilities)
	collector.previousAt = now
	hostname, _ := collector.hostname()

	return protocol.Heartbeat{
		CollectedAt: now, Hostname: hostname, Capabilities: capabilities, Metrics: metrics,
		Services: append([]protocol.Service(nil), collector.cachedServices...),
	}, nil
}

func (collector *Collector) run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return collector.runner.Run(commandContext, name, arguments...)
}

func (collector *Collector) collectSystem(values map[string]string, now time.Time, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	metrics.System = map[string]any{
		"operatingSystem": "FreeBSD", "kernel": values["kern.osrelease"], "architecture": values["hw.machine_arch"],
		"cpuModel": values["hw.model"],
	}
	if cores, ok := parseUint(values["hw.ncpu"]); ok {
		metrics.System["logicalCores"] = cores
	}
	if boot, err := parseBootTime(values["kern.boottime"]); err == nil && !boot.After(now) {
		uptime := now.Sub(boot).Seconds()
		metrics.UptimeSeconds = &uptime
	} else {
		capabilities["host.uptime"] = unavailable("FreeBSD boot time is unavailable")
	}
	if load, err := parseLoad(values["vm.loadavg"]); err == nil {
		metrics.LoadAverage = load
	} else {
		capabilities["host.load"] = unavailable(err.Error())
	}
}

func (collector *Collector) collectCPU(values map[string]string, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	current, err := parseCounter(values["kern.cp_time"])
	if err != nil {
		capabilities["host.cpu"] = unavailable(err.Error())
		return
	}
	cores, coreErr := parsePerCPU(values["kern.cp_times"])
	cpu := map[string]any{"logicalCores": len(cores)}
	if value, valid := current.percentage(collector.previousCPU); valid {
		cpu["percent"] = value
		total := float64(current.total() - collector.previousCPU.total())
		cpu["userPercent"] = float64(current.user+current.nice-collector.previousCPU.user-collector.previousCPU.nice) * 100 / total
		cpu["systemPercent"] = float64(current.system+current.interrupt-collector.previousCPU.system-collector.previousCPU.interrupt) * 100 / total
		cpu["idlePercent"] = float64(current.idle-collector.previousCPU.idle) * 100 / total
	}
	coreMetrics := make([]map[string]any, 0, len(cores))
	for index, core := range cores {
		entry := map[string]any{"name": fmt.Sprintf("cpu%d", index)}
		if index < len(collector.previousCores) {
			if value, valid := core.percentage(collector.previousCores[index]); valid {
				entry["percent"] = value
			}
		}
		coreMetrics = append(coreMetrics, entry)
	}
	cpu["cores"] = coreMetrics
	metrics.CPU = cpu
	collector.previousCPU = current
	collector.previousCores = append([]cpuCounters(nil), cores...)
	if coreErr != nil {
		capabilities["host.cpu"] = unavailable(coreErr.Error())
	}
}

func (collector *Collector) collectMemory(ctx context.Context, values map[string]string, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	total, totalOK := parseUint(values["hw.physmem"])
	pageSize, pageOK := parseUint(values["vm.stats.vm.v_page_size"])
	pageCount, pageCountOK := parseUint(values["vm.stats.vm.v_page_count"])
	freePages, freeOK := parseUint(values["vm.stats.vm.v_free_count"])
	inactivePages, inactiveOK := parseUint(values["vm.stats.vm.v_inactive_count"])
	cachePages, _ := parseUint(values["vm.stats.vm.v_cache_count"])
	laundryPages, _ := parseUint(values["vm.stats.vm.v_laundry_count"])
	if !totalOK || !pageOK || !pageCountOK || !freeOK || !inactiveOK || pageSize == 0 || pageCount == 0 {
		capabilities["host.memory"] = unavailable("FreeBSD memory counters are incomplete")
		return
	}
	reclaimablePages := freePages + inactivePages + cachePages + laundryPages
	if reclaimablePages > pageCount {
		capabilities["host.memory"] = unavailable("FreeBSD memory counters are inconsistent")
		return
	}
	usedBeforeARC := float64(pageCount-reclaimablePages) * float64(total) / float64(pageCount)
	arc, hasARC := parseUint(values["kstat.zfs.misc.arcstats.size"])
	used := math.Max(0, usedBeforeARC-float64(arc))
	used = math.Min(used, float64(total))
	usedBytes := uint64(math.Round(used))
	memory := map[string]any{
		"totalBytes": total, "availableBytes": total - usedBytes, "usedBytes": usedBytes,
		"usedPercent":   float64(usedBytes) * 100 / float64(total),
		"pageSizeBytes": pageSize, "pageCount": pageCount, "inactivePages": inactivePages,
		"cachePages": cachePages, "laundryPages": laundryPages, "freePages": freePages,
	}
	for source, target := range map[string]string{
		"vm.stats.vm.v_active_count": "activePages", "vm.stats.vm.v_wire_count": "wiredPages",
	} {
		if value, exists := parseUint(values[source]); exists {
			memory[target] = value
		}
	}
	if hasARC {
		memory["zfsArcBytes"] = arc
	}
	if body, err := collector.run(ctx, "/usr/sbin/swapinfo", "-k"); err == nil {
		for key, value := range parseSwap(body) {
			memory[key] = value
		}
	}
	metrics.Memory = memory
}

func (collector *Collector) collectFilesystems(ctx context.Context, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, err := collector.run(ctx, "/bin/df", "-kP")
	if err != nil {
		capabilities["host.filesystems"] = unavailable(err.Error())
		return
	}
	mountBody, mountErr := collector.run(ctx, "/sbin/mount", "-p")
	metrics.Filesystems = mergeFreeBSDMounts(parseDF(body), parseMountP(mountBody))
	if len(metrics.Filesystems) == 0 {
		capabilities["host.filesystems"] = unavailable("FreeBSD df returned no filesystems")
	} else if mountErr != nil {
		capabilities["host.filesystems"] = available("df; mount metadata unavailable")
	} else {
		capabilities["host.filesystems"] = available("df and mount")
	}
}

func (collector *Collector) collectNetwork(ctx context.Context, elapsed time.Duration, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, err := collector.run(ctx, "/usr/bin/netstat", "-ibdnW")
	if err != nil {
		capabilities["host.network"] = unavailable(err.Error())
		return
	}
	counters := parseNetwork(body)
	result := make([]map[string]any, 0, len(counters)+1)
	aggregate := map[string]any{"name": "all", "aggregate": true}
	var receiveBytes, transmitBytes uint64
	var receiveRate, transmitRate float64
	var hasRate bool
	for _, current := range counters {
		if len(result) == 127 {
			break
		}
		entry := map[string]any{
			"name": current.name, "receiveBytes": current.receiveBytes, "transmitBytes": current.transmitBytes,
			"receivePackets": current.receivePackets, "transmitPackets": current.transmitPackets,
			"receiveErrors": current.receiveErrors, "transmitErrors": current.transmitErrors, "receiveDrops": current.receiveDrops,
		}
		if previous, found := collector.previousNetwork[current.name]; found && elapsed > 0 && current.receiveBytes >= previous.receiveBytes && current.transmitBytes >= previous.transmitBytes {
			entry["receiveBytesPerSecond"] = float64(current.receiveBytes-previous.receiveBytes) / elapsed.Seconds()
			entry["transmitBytesPerSecond"] = float64(current.transmitBytes-previous.transmitBytes) / elapsed.Seconds()
			if current.name != "lo0" {
				receiveRate += entry["receiveBytesPerSecond"].(float64)
				transmitRate += entry["transmitBytesPerSecond"].(float64)
				hasRate = true
			}
		}
		if current.name != "lo0" {
			receiveBytes += current.receiveBytes
			transmitBytes += current.transmitBytes
		}
		collector.previousNetwork[current.name] = current
		result = append(result, entry)
	}
	aggregate["receiveBytes"], aggregate["transmitBytes"] = receiveBytes, transmitBytes
	if hasRate {
		aggregate["receiveBytesPerSecond"], aggregate["transmitBytesPerSecond"] = receiveRate, transmitRate
	}
	metrics.Network = append([]map[string]any{aggregate}, result...)
	if len(counters) == 0 {
		capabilities["host.network"] = unavailable("FreeBSD netstat returned no link counters")
	}
}

func (collector *Collector) collectDisk(ctx context.Context, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, err := collector.run(ctx, "/usr/sbin/iostat", "-Ix")
	if err != nil {
		capabilities["host.disk-io"] = unavailable(err.Error())
		return
	}
	metrics.DiskIO = parseIostat(body)
	if len(metrics.DiskIO) == 0 {
		capabilities["host.disk-io"] = unavailable("FreeBSD iostat returned no device metrics")
	}
}

func (collector *Collector) collectSensors(ctx context.Context, values map[string]string, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	logicalCores, _ := strconv.Atoi(values["hw.ncpu"])
	keys := sensorSysctlKeys(logicalCores)
	body, _ := collector.run(ctx, "/sbin/sysctl", keys...)
	sensors := parseSysctl(body)
	names := make([]string, 0, len(sensors))
	for name := range sensors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if value, err := parseTemperature(sensors[name]); err == nil && len(metrics.Sensors) < 256 {
			metrics.Sensors = append(metrics.Sensors, map[string]any{"key": name, "temperatureCelsius": value})
		}
	}
	if len(metrics.Sensors) > 0 {
		capabilities["host.sensors"] = available("read-only FreeBSD sysctl")
	}
}

func (collector *Collector) collectBattery(ctx context.Context, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	body, _ := collector.run(ctx, "/sbin/sysctl", "hw.acpi.battery.life", "hw.acpi.battery.state")
	values := parseSysctl(body)
	percentage, percentageOK := parseUint(values["hw.acpi.battery.life"])
	stateValue, stateOK := parseUint(values["hw.acpi.battery.state"])
	if !percentageOK || !stateOK || percentage > 100 {
		return
	}
	state := "unknown"
	switch {
	case stateValue&2 != 0:
		state = "charging"
	case stateValue&1 != 0:
		state = "discharging"
	case percentage == 100:
		state = "full"
	}
	metrics.Batteries = []map[string]any{{"name": "acpi-battery", "percentage": percentage, "state": state}}
	capabilities["host.batteries"] = available("read-only FreeBSD ACPI sysctl")
}

func (collector *Collector) collectStaticHardware(ctx context.Context, metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	if !collector.staticCollected {
		collector.staticCollected = true
		collector.staticSystem = map[string]any{}
		if body, err := collector.run(ctx, "/usr/sbin/pciconf", "-lv"); err == nil {
			if devices := parsePCI(body); len(devices) > 0 {
				collector.staticSystem["pciDevices"] = devices
			}
		}
		if body, err := collector.run(ctx, "/sbin/geom", "disk", "list"); err == nil {
			if devices := parseGEOMDisks(body); len(devices) > 0 {
				collector.staticSystem["storageDevices"] = devices
			}
		}
	}
	for key, value := range collector.staticSystem {
		metrics.System[key] = value
	}
	if _, found := collector.staticSystem["pciDevices"]; found {
		capabilities["host.pci"] = available("sanitized FreeBSD pciconf inventory")
	}
	if _, found := collector.staticSystem["storageDevices"]; found {
		capabilities["host.storage-inventory"] = available("sanitized FreeBSD GEOM inventory; identifiers omitted")
	}
}

func (collector *Collector) collectServices(ctx context.Context, contract protocol.Contract, now time.Time, capabilities map[string]protocol.Capability) {
	interval := contract.Collection.ServiceIntervalSeconds
	if interval <= 0 {
		interval = 600
	}
	if collector.lastServicesAt.IsZero() || now.Sub(collector.lastServicesAt) >= time.Duration(interval)*time.Second {
		collector.lastServicesAt = now
		services, err := collector.services.Collect(ctx)
		if err != nil {
			collector.serviceState = unavailable(err.Error())
		} else {
			for index := range services {
				services[index].Manager = "rcd"
			}
			collector.cachedServices = services[:min(len(services), 512)]
			collector.serviceState = available("FreeBSD rc.d; process resource fields may be unavailable")
		}
	}
	if collector.serviceState.State != "" {
		capabilities["host.services"] = collector.serviceState
	}
}
