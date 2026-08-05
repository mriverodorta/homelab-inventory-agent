//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type gpuAccumulator struct {
	latest map[string]any
	sums   map[string]float64
	counts map[string]uint64
}

type gpuAggregation struct {
	mu      sync.Mutex
	devices map[string]*gpuAccumulator
	lastErr error
	started bool
}

func newGPUAggregation() gpuAggregation {
	return gpuAggregation{devices: map[string]*gpuAccumulator{}}
}

func numericMetric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func (aggregation *gpuAggregation) add(samples []map[string]any, err error) {
	aggregation.mu.Lock()
	defer aggregation.mu.Unlock()
	if err != nil {
		aggregation.lastErr = err
		return
	}
	aggregation.lastErr = nil
	for _, sample := range samples {
		id := fmt.Sprint(sample["id"])
		if id == "" {
			continue
		}
		device := aggregation.devices[id]
		if device == nil {
			device = &gpuAccumulator{sums: map[string]float64{}, counts: map[string]uint64{}}
			aggregation.devices[id] = device
		}
		device.latest = cloneMap(sample)
		for key, value := range sample {
			if number, ok := numericMetric(value); ok {
				device.sums[key] += number
				device.counts[key]++
			}
		}
	}
}

func (aggregation *gpuAggregation) drain() ([]map[string]any, error) {
	aggregation.mu.Lock()
	defer aggregation.mu.Unlock()
	if len(aggregation.devices) == 0 {
		return nil, aggregation.lastErr
	}
	result := make([]map[string]any, 0, len(aggregation.devices))
	for _, device := range aggregation.devices {
		sample := cloneMap(device.latest)
		for key, sum := range device.sums {
			if count := device.counts[key]; count > 0 {
				sample[key] = sum / float64(count)
			}
		}
		result = append(result, sample)
	}
	aggregation.devices = map[string]*gpuAccumulator{}
	return result, nil
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (collector *Collector) sampleGPU() {
	samples, err := collector.collectGPUs()
	collector.gpu.add(samples, err)
}

func (collector *Collector) Start(ctx context.Context, contract protocol.Contract) {
	collector.gpu.mu.Lock()
	if collector.gpu.started {
		collector.gpu.mu.Unlock()
		return
	}
	collector.gpu.started = true
	collector.gpu.mu.Unlock()

	interval := contract.Collection.GPUSampleIntervalSeconds
	if interval < 3 {
		interval = 4
	}
	collector.sampleGPU()
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collector.sampleGPU()
			}
		}
	}()
}

func (collector *Collector) collectAggregatedGPUs() ([]map[string]any, error) {
	result, err := collector.gpu.drain()
	if len(result) > 0 {
		return result, nil
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	return collector.collectGPUs()
}
