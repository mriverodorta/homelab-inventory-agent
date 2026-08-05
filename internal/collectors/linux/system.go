//go:build linux

package linux

import (
	"bufio"
	"errors"
	"runtime"
	"strconv"
	"strings"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func parseOSRelease(body []byte) map[string]string {
	result := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found || key == "" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch key {
		case "ID", "VERSION_ID", "PRETTY_NAME":
			result[key] = value
		}
	}
	return result
}

func parseCPUInfo(body []byte) map[string]any {
	result := map[string]any{}
	logicalThreads := 0
	physicalCores := map[string]struct{}{}
	processor := map[string]string{}
	flush := func() {
		if len(processor) == 0 {
			return
		}
		logicalThreads++
		physicalID, physicalFound := processor["physical id"]
		coreID, coreFound := processor["core id"]
		if physicalFound && coreFound {
			physicalCores[physicalID+":"+coreID] = struct{}{}
		}
		for _, key := range []string{"model name", "Processor", "Hardware"} {
			if result["model"] == nil && strings.TrimSpace(processor[key]) != "" {
				result["model"] = strings.TrimSpace(processor[key])
			}
		}
		processor = map[string]string{}
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			processor[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	flush()
	if logicalThreads > 0 {
		result["logicalThreads"] = logicalThreads
	}
	if len(physicalCores) > 0 {
		result["physicalCores"] = len(physicalCores)
	}
	return result
}

func parseZFSARC(body []byte) (uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == "size" {
			value, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
			if err != nil {
				return 0, err
			}
			return value, nil
		}
	}
	return 0, errors.New("ZFS ARC size is unavailable")
}

func (collector *Collector) collectSystem(metrics *protocol.Metrics, capabilities map[string]protocol.Capability) {
	system := map[string]any{"operatingSystem": "linux", "architecture": runtime.GOARCH}
	if body, err := readFile(collector.root, "proc/sys/kernel/osrelease"); err == nil {
		system["kernel"] = strings.TrimSpace(string(body))
	}
	if body, err := readFile(collector.root, "etc/os-release"); err == nil {
		values := parseOSRelease(body)
		if values["ID"] != "" {
			system["distribution"] = values["ID"]
		}
		if values["VERSION_ID"] != "" {
			system["distributionVersion"] = values["VERSION_ID"]
		}
		if values["PRETTY_NAME"] != "" {
			system["distributionName"] = values["PRETTY_NAME"]
		}
	}
	metrics.System = system
	if body, err := readFile(collector.root, "proc/cpuinfo"); err == nil {
		if metrics.CPU == nil {
			metrics.CPU = map[string]any{}
		}
		for key, value := range parseCPUInfo(body) {
			metrics.CPU[key] = value
		}
	}
	capabilities["host.system"] = available("procfs and os-release")
}
