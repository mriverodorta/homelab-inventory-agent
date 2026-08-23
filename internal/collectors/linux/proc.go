//go:build linux

package linux

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cpuCounters struct {
	Name               string
	User, Nice, System uint64
	Idle, IOWait, IRQ  uint64
	SoftIRQ, Steal     uint64
}

func (counter cpuCounters) validDelta(previous cpuCounters) bool {
	return counter.User >= previous.User && counter.Nice >= previous.Nice && counter.System >= previous.System &&
		counter.Idle >= previous.Idle && counter.IOWait >= previous.IOWait && counter.IRQ >= previous.IRQ &&
		counter.SoftIRQ >= previous.SoftIRQ && counter.Steal >= previous.Steal && counter.total() > previous.total()
}

func (counter cpuCounters) total() uint64 {
	return counter.User + counter.Nice + counter.System + counter.Idle + counter.IOWait + counter.IRQ + counter.SoftIRQ + counter.Steal
}

func (counter cpuCounters) idle() uint64 { return counter.Idle + counter.IOWait }

func parseCPUCounters(body []byte) ([]cpuCounters, error) {
	var result []cpuCounters
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 || (fields[0] != "cpu" && !strings.HasPrefix(fields[0], "cpu")) {
			continue
		}
		values := make([]uint64, 8)
		for index := range values {
			parsed, err := strconv.ParseUint(fields[index+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse %s counter: %w", fields[0], err)
			}
			values[index] = parsed
		}
		result = append(result, cpuCounters{
			Name: fields[0], User: values[0], Nice: values[1], System: values[2],
			Idle: values[3], IOWait: values[4], IRQ: values[5], SoftIRQ: values[6], Steal: values[7],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("no CPU counters found")
	}
	return result, nil
}

func percentage(current, previous cpuCounters) (float64, bool) {
	if !current.validDelta(previous) {
		return 0, false
	}
	totalDelta := current.total() - previous.total()
	idleDelta := current.idle() - previous.idle()
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta), true
}

func cpuBreakdown(current, previous cpuCounters) (map[string]any, bool) {
	if !current.validDelta(previous) {
		return nil, false
	}
	total := float64(current.total() - previous.total())
	percent := func(currentValue, previousValue uint64) float64 {
		return float64(currentValue-previousValue) * 100 / total
	}
	return map[string]any{
		"userPercent":   percent(current.User+current.Nice, previous.User+previous.Nice),
		"systemPercent": percent(current.System+current.IRQ+current.SoftIRQ, previous.System+previous.IRQ+previous.SoftIRQ),
		"ioWaitPercent": percent(current.IOWait, previous.IOWait),
		"stealPercent":  percent(current.Steal, previous.Steal),
		"idlePercent":   percent(current.Idle, previous.Idle),
	}, true
}

func parseMeminfo(body []byte) (map[string]any, error) {
	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		parsed, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse memory field %s: %w", key, err)
		}
		values[key] = parsed * 1024
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total == 0 || available > total {
		return nil, errors.New("memory totals are invalid")
	}
	result := map[string]any{
		"totalBytes": total, "availableBytes": available, "usedBytes": total - available,
		"cachedBytes": values["Cached"], "buffersBytes": values["Buffers"],
		"usedPercent":    float64(total-available) * 100 / float64(total),
		"swapTotalBytes": values["SwapTotal"], "swapFreeBytes": values["SwapFree"],
	}
	for source, target := range map[string]string{
		"MemFree": "freeBytes", "SReclaimable": "reclaimableBytes", "Shmem": "sharedBytes",
	} {
		if value, exists := values[source]; exists {
			result[target] = value
		}
	}
	if swapTotal := values["SwapTotal"]; swapTotal > 0 {
		swapUsed := swapTotal - values["SwapFree"]
		result["swapUsedBytes"] = swapUsed
		result["swapUsedPercent"] = float64(swapUsed) * 100 / float64(swapTotal)
	}
	return result, nil
}

func parseLoadAverage(body []byte) ([]float64, error) {
	fields := strings.Fields(string(body))
	if len(fields) < 3 {
		return nil, errors.New("load average is invalid")
	}
	result := make([]float64, 3)
	for index := range result {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil || value < 0 {
			return nil, errors.New("load average is invalid")
		}
		result[index] = value
	}
	return result, nil
}

func parseUptime(body []byte) (float64, error) {
	fields := strings.Fields(string(body))
	if len(fields) < 1 {
		return 0, errors.New("uptime is invalid")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return 0, errors.New("uptime is invalid")
	}
	return value, nil
}

func rate(current, previous uint64, elapsed time.Duration) (float64, bool) {
	if elapsed <= 0 || current < previous {
		return 0, false
	}
	return float64(current-previous) / elapsed.Seconds(), true
}

func readFile(root, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
}
