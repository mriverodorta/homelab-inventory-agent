//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type fakeCommandRunner struct {
	name      string
	arguments []string
	body      []byte
	err       error
}

type fakeServicesCollector struct {
	calls int
}

func (collector *fakeServicesCollector) Collect(_ context.Context) ([]protocol.Service, error) {
	collector.calls++
	return []protocol.Service{{Name: "example", ActiveState: "active"}}, nil
}

func (runner *fakeCommandRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.name = name
	runner.arguments = append([]string(nil), arguments...)
	return runner.body, runner.err
}

func fixture(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeBaseFixtures(t *testing.T, root string, cpuUser uint64) {
	t.Helper()
	fixture(t, root, "proc/uptime", "1234.50 100.00\n")
	fixture(t, root, "proc/loadavg", "0.10 0.20 0.30 1/100 22\n")
	fixture(t, root, "proc/stat", "cpu  "+uint(cpuUser)+" 0 50 850 0 0 0 0 0 0\ncpu0 "+uint(cpuUser)+" 0 50 850 0 0 0 0 0 0\n")
	fixture(t, root, "proc/meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\nBuffers: 10 kB\nCached: 100 kB\nSwapTotal: 200 kB\nSwapFree: 150 kB\n")
	fixture(t, root, "proc/net/dev", "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\neth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n")
	fixture(t, root, "proc/diskstats", "8 0 sda 10 0 20 5 30 0 40 6 0 11 0 0 0 0 0 0\n")
	fixture(t, root, "proc/cpuinfo", "processor : 0\nphysical id : 0\ncore id : 0\nmodel name : Example CPU\n\n")
	fixture(t, root, "proc/sys/kernel/osrelease", "6.12.0\n")
	fixture(t, root, "etc/os-release", "ID=example\nVERSION_ID=1\nPRETTY_NAME=\"Example Linux\"\n")
	fixture(t, root, "sys/class/hwmon/hwmon0/temp1_input", "42000\n")
	fixture(t, root, "sys/class/power_supply/BAT0/type", "Battery\n")
	fixture(t, root, "sys/class/power_supply/BAT0/capacity", "80\n")
	fixture(t, root, "sys/class/power_supply/BAT0/status", "Discharging\n")
}

func uint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func TestCollectorReadsProcAndSysfsWithoutInventingInitialRates(t *testing.T) {
	root := t.TempDir()
	writeBaseFixtures(t, root, 100)
	collector := New(root, nil)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	collector.now = func() time.Time { return now }
	first, err := collector.Collect(context.Background(), protocol.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Metrics.UptimeSeconds == nil || *first.Metrics.UptimeSeconds != 1234.5 {
		t.Fatalf("uptime not collected: %#v", first.Metrics.UptimeSeconds)
	}
	if _, exists := first.Metrics.CPU["percent"]; exists {
		t.Fatal("collector invented initial CPU percentage")
	}
	if first.Metrics.Memory["usedBytes"] != uint64(600*1024) {
		t.Fatalf("memory mismatch: %#v", first.Metrics.Memory)
	}
	if first.Metrics.System["distribution"] != "example" || first.Metrics.CPU["model"] != "Example CPU" {
		t.Fatalf("static host profile mismatch: system=%#v cpu=%#v", first.Metrics.System, first.Metrics.CPU)
	}
	if len(first.Metrics.Sensors) != 1 || first.Metrics.Sensors[0]["temperatureC"] != float64(42) {
		t.Fatalf("sensor mismatch: %#v", first.Metrics.Sensors)
	}
	if len(first.Metrics.Batteries) != 1 {
		t.Fatalf("battery mismatch: %#v", first.Metrics.Batteries)
	}

	now = now.Add(time.Minute)
	writeBaseFixtures(t, root, 200)
	second, err := collector.Collect(context.Background(), protocol.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	percent, ok := second.Metrics.CPU["percent"].(float64)
	if !ok || percent <= 0 || percent > 100 {
		t.Fatalf("CPU delta mismatch: %#v", second.Metrics.CPU)
	}
	if _, ok := second.Metrics.Network[0]["receiveBytesPerSecond"]; !ok {
		t.Fatalf("network rate missing: %#v", second.Metrics.Network)
	}
	if _, ok := second.Metrics.CPU["userPercent"]; !ok {
		t.Fatalf("CPU breakdown missing: %#v", second.Metrics.CPU)
	}
}

func TestParseZFSARC(t *testing.T) {
	value, err := parseZFSARC([]byte("13 1 0x01 7 216 123456789\nname type data\nsize 4 987654321\n"))
	if err != nil || value != 987654321 {
		t.Fatalf("ZFS ARC mismatch: %d %v", value, err)
	}
}

func TestCounterResetOmitsInvalidRates(t *testing.T) {
	previous := cpuCounters{Name: "cpu", User: 100, Idle: 900}
	current := cpuCounters{Name: "cpu", User: 10, Idle: 90}
	if _, valid := percentage(current, previous); valid {
		t.Fatal("counter reset produced a percentage")
	}
	if _, valid := rate(10, 100, time.Minute); valid {
		t.Fatal("counter reset produced a rate")
	}
}

func TestServiceCollectionUsesContractCadenceAndCachedHeartbeatState(t *testing.T) {
	root := t.TempDir()
	writeBaseFixtures(t, root, 100)
	collector := New(root, nil)
	services := &fakeServicesCollector{}
	collector.services = services
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	collector.now = func() time.Time { return now }
	contract := protocol.Contract{Collection: protocol.CollectionPolicy{ServiceIntervalSeconds: 600}}
	first, err := collector.Collect(context.Background(), contract)
	if err != nil || services.calls != 1 || len(first.Services) != 1 {
		t.Fatalf("initial service collection mismatch: calls=%d services=%#v err=%v", services.calls, first.Services, err)
	}
	now = now.Add(time.Minute)
	second, err := collector.Collect(context.Background(), contract)
	if err != nil || services.calls != 1 || len(second.Services) != 1 {
		t.Fatalf("cached service collection mismatch: calls=%d services=%#v err=%v", services.calls, second.Services, err)
	}
	now = now.Add(9 * time.Minute)
	if _, err := collector.Collect(context.Background(), contract); err != nil || services.calls != 2 {
		t.Fatalf("scheduled service refresh mismatch: calls=%d err=%v", services.calls, err)
	}
}

func TestStorageHealthUsesOpaqueIDsAndReadOnlySysfs(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "sys/block/mmcblk0/device/pre_eol_info", "0x02\n")
	fixture(t, root, "sys/block/mmcblk0/device/name", "OEM eMMC\n")
	fixture(t, root, "sys/block/mmcblk0/device/fwrev", "1.0\n")
	fixture(t, root, "sys/block/mmcblk0/device/life_time", "0x01 0x02\n")
	fixture(t, root, "sys/block/mmcblk0/size", "1000\n")
	fixture(t, root, "sys/block/md0/md/array_state", "clean\n")
	fixture(t, root, "sys/block/md0/md/level", "raid1\n")
	fixture(t, root, "sys/block/md0/md/raid_disks", "2\n")
	fixture(t, root, "sys/block/md0/md/degraded", "0\n")
	fixture(t, root, "sys/block/md0/md/sync_action", "idle\n")
	fixture(t, root, "sys/block/md0/md/sync_completed", "none\n")
	fixture(t, root, "sys/block/md0/md/mismatch_cnt", "0\n")
	collector := New(root, func(namespace, value string) string { return namespace + "-opaque-" + value })
	health, err := collector.collectStorageHealth(context.Background(), time.Now().UTC(), false)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]protocol.StorageHealth{}
	for _, record := range health {
		byKind[record.Kind] = record
	}
	if len(health) != 2 || byKind["emmc"].DeviceID != "storage-opaque-mmcblk0" || byKind["emmc"].State != "warning" {
		t.Fatalf("eMMC health mismatch: %#v", health)
	}
	if byKind["mdraid"].State != "healthy" {
		t.Fatalf("mdraid health mismatch: %#v", byKind["mdraid"])
	}
}

func TestGPUDiscoveryUsesReadOnlyDRMSysfs(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "sys/class/drm/card0/device/vendor", "0x8086\n")
	fixture(t, root, "sys/class/drm/card0/device/device", "0x9bc5\n")
	fixture(t, root, "sys/class/drm/card0/device/gpu_busy_percent", "25\n")
	fixture(t, root, "sys/class/drm/card0/device/mem_info_vram_used", "1024\n")
	fixture(t, root, "sys/class/drm/card0/device/mem_info_vram_total", "4096\n")
	fixture(t, root, "sys/class/drm/card0/device/hwmon/hwmon0/temp1_input", "51000\n")
	collector := New(root, func(namespace, value string) string { return namespace + "-opaque-" + value })
	gpus, err := collector.collectGPUs()
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 || gpus[0]["vendor"] != "Intel" || gpus[0]["temperatureC"] != float64(51) || gpus[0]["utilizationPercent"] != float64(25) {
		t.Fatalf("GPU mismatch: %#v", gpus)
	}
}

func TestGPUAggregationProducesOneAveragedHeartbeatSample(t *testing.T) {
	aggregation := newGPUAggregation()
	aggregation.add([]map[string]any{{"id": "gpu-1", "vendor": "Intel", "temperatureC": 40.0, "utilizationPercent": 20.0}}, nil)
	aggregation.add([]map[string]any{{"id": "gpu-1", "vendor": "Intel", "temperatureC": 50.0, "utilizationPercent": 40.0}}, nil)
	result, err := aggregation.drain()
	if err != nil || len(result) != 1 || result[0]["temperatureC"] != 45.0 || result[0]["utilizationPercent"] != 30.0 {
		t.Fatalf("GPU aggregation mismatch: %#v %v", result, err)
	}
	if second, err := aggregation.drain(); err != nil || len(second) != 0 {
		t.Fatalf("GPU high-frequency samples leaked into another heartbeat: %#v %v", second, err)
	}
}

func TestSMARTCollectorUsesStandbySafeFixedArgumentsAndSanitizesIdentity(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "sys/block/.keep", "")
	runner := &fakeCommandRunner{body: []byte(`{
		"model_name":"Example NVMe","serial_number":"PRIVATE-SERIAL","firmware_version":"1.2",
		"device":{"type":"nvme","protocol":"NVMe"},"user_capacity":{"bytes":1000000},
		"smart_status":{"passed":true},"nvme_smart_health_information_log":{"percentage_used":4,"unsafe_shutdowns":2,"media_errors":0}
	}`)}
	collector := New(root, func(namespace, value string) string { return namespace + "-opaque" }, Options{
		SMARTDevices: []string{"/dev/nvme0"}, SMARTRunner: runner, SMARTCTLPath: "/usr/sbin/smartctl",
	})
	health, err := collector.collectStorageHealth(context.Background(), time.Now().UTC(), true)
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "/usr/sbin/smartctl" || len(runner.arguments) != 5 || runner.arguments[0] != "-n" || runner.arguments[1] != "standby,0" || runner.arguments[4] != "/dev/nvme0" {
		t.Fatalf("unsafe smartctl invocation: %s %#v", runner.name, runner.arguments)
	}
	if len(health) != 1 || health[0].State != "healthy" || health[0].DeviceID != "storage-opaque" {
		t.Fatalf("SMART health mismatch: %#v", health)
	}
	body, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "PRIVATE-SERIAL") || strings.Contains(string(body), "serial_number") {
		t.Fatalf("SMART payload leaked a raw hardware identifier: %s", body)
	}
}

func TestSMARTCollectorReportsStandbyWithoutWakingDisk(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "sys/block/.keep", "")
	runner := &fakeCommandRunner{body: []byte(`{"power_mode":"standby"}`), err: errors.New("smartctl exit 2")}
	collector := New(root, nil, Options{SMARTDevices: []string{"/dev/sda"}, SMARTRunner: runner, SMARTCTLPath: "/usr/sbin/smartctl"})
	health, err := collector.collectStorageHealth(context.Background(), time.Now().UTC(), true)
	if err != nil || len(health) != 1 || health[0].Metrics["standby"] != true {
		t.Fatalf("standby disk was not represented safely: %#v %v", health, err)
	}
}
