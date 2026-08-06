package freebsd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type fixtureRunner struct {
	responses map[string][]byte
	calls     []string
}

func (runner *fixtureRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, arguments...), " ")
	runner.calls = append(runner.calls, key)
	response, found := runner.responses[key]
	if !found {
		return nil, fmt.Errorf("unexpected command %s", key)
	}
	return response, nil
}

func freeBSDContract() protocol.Contract {
	return protocol.Contract{
		Collection: protocol.CollectionPolicy{ServiceIntervalSeconds: 600, StorageHealthIntervalSeconds: 3600},
	}
}

func TestCollectorProducesBoundedFreeBSDTelemetryWithoutPrivateInterfaces(t *testing.T) {
	coreCommand := strings.Join(append([]string{"/sbin/sysctl"}, coreSysctlKeys...), " ")
	sensorCommand := strings.Join(append([]string{"/sbin/sysctl"}, sensorSysctlKeys(4)...), " ")
	runner := &fixtureRunner{responses: map[string][]byte{
		coreCommand: []byte(strings.Join([]string{
			"kern.osrelease: 14.3-RELEASE-p1",
			"hw.machine_arch: amd64",
			"hw.model: Intel(R) Core(TM) i7-7700T CPU @ 2.90GHz",
			"hw.ncpu: 4",
			"hw.physmem: 17179869184",
			"kern.boottime: { sec = 1699996400, usec = 0 } Tue Nov 14 21:13:20 2023",
			"vm.loadavg: { 0.12 0.34 0.56 }",
			"kern.cp_time: 100 10 40 5 845",
			"kern.cp_times: 25 2 10 1 212 25 3 10 1 211 25 2 10 2 211 25 3 10 1 211",
			"vm.stats.vm.v_page_size: 4096",
			"vm.stats.vm.v_free_count: 100000",
			"vm.stats.vm.v_inactive_count: 200000",
			"vm.stats.vm.v_cache_count: 50000",
			"kstat.zfs.misc.arcstats.size: 536870912",
		}, "\n")),
		"/usr/sbin/swapinfo -k":   []byte("Device 1K-blocks Used Avail Capacity\n/dev/nda0p3 2097152 524288 1572864 25%\n"),
		"/bin/df -kP / /data":     []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/ufs/root 1048576 262144 786432 25% /\n/dev/ufs/data 2097152 1048576 1048576 50% /data\n"),
		"/usr/bin/netstat -ibdnW": []byte("Name Mtu Network Address Ipkts Ierrs Idrop Ibytes Opkts Oerrs Obytes Coll\nigc0 1500 <Link#1> aa:bb:cc:dd:ee:ff 100 1 2 10000 80 3 8000 0\nlo0 16384 <Link#2> 00:00:00:00:00:00 50 0 0 5000 50 0 5000 0\n"),
		"/usr/sbin/iostat -Ix":    []byte("device r/i w/i kr/i kw/i wait actv wsvc_t asvc_t %w %b\nnvme0 2.00 3.00 128.00 256.00 0 0.1 0 0.5 0 4.5\n"),
		"/usr/sbin/pciconf -lv":   []byte("vgapci0@pci0:0:2:0: class=0x030000 rev=0x04 hdr=0x00 vendor=0x8086 device=0x5912 subvendor=0x1028 subdevice=0x07a1\n    vendor     = 'Intel Corporation'\n    device     = 'HD Graphics 630'\n    class      = display\n    subclass   = VGA\n"),
		"/sbin/geom disk list":    []byte("Geom name: ada0\nProviders:\n1. Name: ada0\n   Mediasize: 500107862016 (466G)\n   Sectorsize: 512\n   descr: Samsung SSD\n   lunid: 5002538d00000000\n   ident: PRIVATE-SERIAL\n   rotationrate: 0\n"),
		sensorCommand:             []byte("dev.cpu.0.temperature: 48.0C\ndev.cpu.1.temperature: 49.0C\nhw.acpi.thermal.tz0.temperature: 45.1C\n"),
		"/sbin/sysctl hw.acpi.battery.life hw.acpi.battery.state": []byte("hw.acpi.battery.life: 88\nhw.acpi.battery.state: 1\n"),
	}}
	services := &serviceFixture{services: []protocol.Service{{Name: "dnsmasq", ActiveState: "active"}}}
	collector := New(Options{Runner: runner, Services: services, Filesystems: []string{"/data"}})
	collector.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	collector.hostname = func() (string, error) { return "skygate", nil }

	heartbeat, err := collector.Collect(context.Background(), freeBSDContract())
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Hostname != "skygate" || len(heartbeat.Metrics.LoadAverage) != 3 {
		t.Fatalf("host metrics missing: %#v", heartbeat)
	}
	if heartbeat.Capabilities["host.processes"].State != protocol.PermissionBlocked {
		t.Fatalf("restricted process visibility overstated: %#v", heartbeat.Capabilities)
	}
	if heartbeat.Capabilities["host.gpu"].State != protocol.Unavailable || heartbeat.Capabilities["containers"].State != protocol.Disabled {
		t.Fatalf("unsupported capabilities overstated: %#v", heartbeat.Capabilities)
	}
	if got := heartbeat.Metrics.System["operatingSystem"]; got != "FreeBSD" {
		t.Fatalf("system identity missing: %#v", heartbeat.Metrics.System)
	}
	if fmt.Sprint(heartbeat.Metrics.System["storageDevices"]) == "" || strings.Contains(fmt.Sprint(heartbeat.Metrics.System), "PRIVATE-SERIAL") || strings.Contains(fmt.Sprint(heartbeat.Metrics.System), "5002538d00000000") {
		t.Fatalf("static hardware snapshot is missing or private: %#v", heartbeat.Metrics.System)
	}
	if got := heartbeat.Metrics.Memory["totalBytes"]; got != uint64(17179869184) {
		t.Fatalf("memory parse mismatch: %#v", heartbeat.Metrics.Memory)
	}
	if len(heartbeat.Metrics.Filesystems) != 2 || len(heartbeat.Metrics.Network) != 3 || len(heartbeat.Metrics.DiskIO) != 1 || len(heartbeat.Metrics.Sensors) != 3 || len(heartbeat.Metrics.Batteries) != 1 {
		t.Fatalf("metric collections missing: %#v", heartbeat.Metrics)
	}
	if len(heartbeat.Services) != 1 || heartbeat.Services[0].Name != "dnsmasq" {
		t.Fatalf("services missing: %#v", heartbeat.Services)
	}
	for _, call := range runner.calls {
		for _, forbidden := range []string{"configctl", "pluginctl", "/conf/config.xml", "pfctl", "sockstat", "procstat"} {
			if strings.Contains(call, forbidden) {
				t.Fatalf("collector invoked forbidden interface %q", call)
			}
		}
	}
}

func TestCollectorCalculatesCPUAndNetworkRatesOnSubsequentSamples(t *testing.T) {
	coreCommand := strings.Join(append([]string{"/sbin/sysctl"}, coreSysctlKeys...), " ")
	sensorCommand := strings.Join(append([]string{"/sbin/sysctl"}, sensorSysctlKeys(1)...), " ")
	runner := &sequenceRunner{responses: map[string][][]byte{
		coreCommand: {
			[]byte("kern.osrelease: 14.3-RELEASE\nhw.machine_arch: amd64\nhw.model: CPU\nhw.ncpu: 1\nhw.physmem: 4096000\nkern.boottime: { sec = 1699990000, usec = 0 }\nvm.loadavg: { 0.1 0.2 0.3 }\nkern.cp_time: 100 0 100 0 800\nkern.cp_times: 100 0 100 0 800\nvm.stats.vm.v_page_size: 4096\nvm.stats.vm.v_free_count: 100\nvm.stats.vm.v_inactive_count: 100\nvm.stats.vm.v_cache_count: 0\n"),
			[]byte("kern.osrelease: 14.3-RELEASE\nhw.machine_arch: amd64\nhw.model: CPU\nhw.ncpu: 1\nhw.physmem: 4096000\nkern.boottime: { sec = 1699990000, usec = 0 }\nvm.loadavg: { 0.1 0.2 0.3 }\nkern.cp_time: 120 0 110 0 870\nkern.cp_times: 120 0 110 0 870\nvm.stats.vm.v_page_size: 4096\nvm.stats.vm.v_free_count: 100\nvm.stats.vm.v_inactive_count: 100\nvm.stats.vm.v_cache_count: 0\n"),
		},
		"/usr/bin/netstat -ibdnW": {
			[]byte("Name Mtu Network Address Ipkts Ierrs Idrop Ibytes Opkts Oerrs Obytes Coll\nem0 1500 <Link#1> x 1 0 0 1000 1 0 500 0\n"),
			[]byte("Name Mtu Network Address Ipkts Ierrs Idrop Ibytes Opkts Oerrs Obytes Coll\nem0 1500 <Link#1> x 2 0 0 7000 2 0 3500 0\n"),
		},
		"/usr/sbin/swapinfo -k": {[]byte("Device 1K-blocks Used Avail Capacity\n"), []byte("Device 1K-blocks Used Avail Capacity\n")},
		"/bin/df -kP /":         {[]byte("Filesystem 1024-blocks Used Available Capacity Mounted on\nx 100 20 80 20% /\n"), []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\nx 100 20 80 20% /\n")},
		"/usr/sbin/iostat -Ix":  {[]byte("device r/i w/i kr/i kw/i wait actv wsvc_t asvc_t %w %b\n"), []byte("device r/i w/i kr/i kw/i wait actv wsvc_t asvc_t %w %b\n")},
		"/usr/sbin/pciconf -lv": {nil},
		"/sbin/geom disk list":  {nil},
		sensorCommand:           {nil, nil},
		"/sbin/sysctl hw.acpi.battery.life hw.acpi.battery.state": {nil, nil},
	}}
	collector := New(Options{Runner: runner, Services: &serviceFixture{}})
	now := time.Unix(1700000000, 0).UTC()
	collector.now = func() time.Time { return now }
	collector.hostname = func() (string, error) { return "host", nil }
	if _, err := collector.Collect(context.Background(), freeBSDContract()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	heartbeat, err := collector.Collect(context.Background(), freeBSDContract())
	if err != nil {
		t.Fatal(err)
	}
	if got := heartbeat.Metrics.CPU["percent"]; got != float64(30) {
		t.Fatalf("CPU delta mismatch: %#v", heartbeat.Metrics.CPU)
	}
	if got := heartbeat.Metrics.Network[1]["receiveBytesPerSecond"]; got != float64(100) {
		t.Fatalf("network delta mismatch: %#v", heartbeat.Metrics.Network)
	}
}

type sequenceRunner struct {
	responses map[string][][]byte
	indexes   map[string]int
}

func (runner *sequenceRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	if runner.indexes == nil {
		runner.indexes = map[string]int{}
	}
	key := strings.Join(append([]string{name}, arguments...), " ")
	index := runner.indexes[key]
	values, found := runner.responses[key]
	if !found || index >= len(values) {
		return nil, errors.New("unexpected command: " + key)
	}
	runner.indexes[key]++
	return values[index], nil
}

type serviceFixture struct {
	services []protocol.Service
	err      error
}

func (fixture *serviceFixture) Collect(context.Context) ([]protocol.Service, error) {
	return append([]protocol.Service(nil), fixture.services...), fixture.err
}
