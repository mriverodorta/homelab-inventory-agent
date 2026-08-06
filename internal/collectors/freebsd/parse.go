package freebsd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cpuCounters struct {
	user, nice, system, interrupt, idle uint64
}

func (counter cpuCounters) total() uint64 {
	return counter.user + counter.nice + counter.system + counter.interrupt + counter.idle
}

func (counter cpuCounters) percentage(previous cpuCounters) (float64, bool) {
	if counter.user < previous.user || counter.nice < previous.nice || counter.system < previous.system || counter.interrupt < previous.interrupt || counter.idle < previous.idle || counter.total() <= previous.total() {
		return 0, false
	}
	total := counter.total() - previous.total()
	idle := counter.idle - previous.idle
	return float64(total-idle) * 100 / float64(total), true
}

func parseSysctl(body []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) != "" {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func parseUint(value string) (uint64, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed, err == nil
}

func parseFloat(value string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed, err == nil
}

func parseCounter(value string) (cpuCounters, error) {
	fields := strings.Fields(value)
	if len(fields) != 5 {
		return cpuCounters{}, errors.New("FreeBSD CPU counter must contain five values")
	}
	values := make([]uint64, len(fields))
	for index, field := range fields {
		parsed, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuCounters{}, errors.New("FreeBSD CPU counter is invalid")
		}
		values[index] = parsed
	}
	return cpuCounters{user: values[0], nice: values[1], system: values[2], interrupt: values[3], idle: values[4]}, nil
}

func parsePerCPU(value string) ([]cpuCounters, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields)%5 != 0 {
		return nil, errors.New("FreeBSD per-CPU counters are invalid")
	}
	result := make([]cpuCounters, 0, len(fields)/5)
	for index := 0; index < len(fields); index += 5 {
		counter, err := parseCounter(strings.Join(fields[index:index+5], " "))
		if err != nil {
			return nil, err
		}
		result = append(result, counter)
	}
	return result, nil
}

func parseLoad(value string) ([]float64, error) {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "{"), "}"))
	fields := strings.Fields(value)
	if len(fields) != 3 {
		return nil, errors.New("FreeBSD load average is invalid")
	}
	result := make([]float64, 3)
	for index, field := range fields {
		parsed, err := strconv.ParseFloat(field, 64)
		if err != nil || parsed < 0 {
			return nil, errors.New("FreeBSD load average is invalid")
		}
		result[index] = parsed
	}
	return result, nil
}

func parseBootTime(value string) (time.Time, error) {
	marker := "sec ="
	index := strings.Index(value, marker)
	if index < 0 {
		return time.Time{}, errors.New("FreeBSD boot time is invalid")
	}
	remaining := strings.TrimSpace(value[index+len(marker):])
	end := strings.IndexAny(remaining, ", }")
	if end >= 0 {
		remaining = remaining[:end]
	}
	seconds, err := strconv.ParseInt(remaining, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, errors.New("FreeBSD boot time is invalid")
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func parseSwap(body []byte) map[string]any {
	var totalKB, usedKB uint64
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		total, totalOK := parseUint(fields[1])
		used, usedOK := parseUint(fields[2])
		if totalOK && usedOK && used <= total {
			totalKB += total
			usedKB += used
		}
	}
	result := map[string]any{"swapTotalBytes": totalKB * 1024, "swapUsedBytes": usedKB * 1024}
	if totalKB > 0 {
		result["swapUsedPercent"] = float64(usedKB) * 100 / float64(totalKB)
	}
	return result
}

func parseDF(body []byte) []map[string]any {
	var result []map[string]any
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		total, totalOK := parseUint(fields[1])
		used, usedOK := parseUint(fields[2])
		available, availableOK := parseUint(fields[3])
		if !totalOK || !usedOK || !availableOK {
			continue
		}
		result = append(result, map[string]any{
			"mountPoint": fields[len(fields)-1], "totalBytes": total * 1024,
			"usedBytes": used * 1024, "availableBytes": available * 1024,
		})
	}
	return result
}

type networkCounters struct {
	name                                                      string
	receivePackets, receiveErrors, receiveDrops, receiveBytes uint64
	transmitPackets, transmitErrors, transmitBytes            uint64
}

func parseNetwork(body []byte) []networkCounters {
	var result []networkCounters
	seen := map[string]struct{}{}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 12 || !strings.HasPrefix(fields[2], "<Link#") {
			continue
		}
		name := fields[0]
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		values := make([]uint64, 7)
		indexes := []int{4, 5, 6, 7, 8, 9, 10}
		valid := true
		for index, fieldIndex := range indexes {
			parsed, ok := parseUint(fields[fieldIndex])
			if !ok {
				valid = false
				break
			}
			values[index] = parsed
		}
		if !valid {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, networkCounters{
			name: name, receivePackets: values[0], receiveErrors: values[1], receiveDrops: values[2], receiveBytes: values[3],
			transmitPackets: values[4], transmitErrors: values[5], transmitBytes: values[6],
		})
	}
	return result
}

func parseIostat(body []byte) []map[string]any {
	var result []map[string]any
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		reads, readsOK := parseFloat(fields[1])
		writes, writesOK := parseFloat(fields[2])
		readKB, readOK := parseFloat(fields[3])
		writeKB, writeOK := parseFloat(fields[4])
		utilization, utilizationOK := parseFloat(fields[10])
		if !readsOK || !writesOK || !readOK || !writeOK || !utilizationOK {
			continue
		}
		result = append(result, map[string]any{
			"name": fields[0], "readOperationsPerSecond": reads, "writeOperationsPerSecond": writes,
			"readBytesPerSecond": readKB * 1024, "writeBytesPerSecond": writeKB * 1024,
			"utilizationPercent": utilization,
		})
	}
	return result
}

func parseTemperature(value string) (float64, error) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(value, "C"))
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || parsed < -100 || parsed > 250 {
		return 0, fmt.Errorf("invalid temperature %q", value)
	}
	return parsed, nil
}
