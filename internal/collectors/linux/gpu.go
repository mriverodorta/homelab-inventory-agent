//go:build linux

package linux

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var gpuVendors = map[string]string{
	"0x10de": "NVIDIA",
	"0x1002": "AMD",
	"0x8086": "Intel",
}

func numericFile(filePath string, scale float64) (float64, bool) {
	value, err := strconv.ParseFloat(readTrimmed(filePath), 64)
	return value / scale, err == nil
}

func (collector *Collector) collectGPUs() ([]map[string]any, error) {
	drmRoot := filepath.Join(collector.root, "sys", "class", "drm")
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for _, entry := range entries {
		if len(result) == 16 {
			break
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "card") || strings.Contains(name, "-") {
			continue
		}
		deviceRoot := filepath.Join(drmRoot, name, "device")
		vendorID := strings.ToLower(readTrimmed(filepath.Join(deviceRoot, "vendor")))
		vendor, supported := gpuVendors[vendorID]
		if !supported {
			continue
		}
		gpu := map[string]any{
			"id": collector.opaque("gpu", name), "name": name, "vendor": vendor,
			"pciVendorId": vendorID, "pciDeviceId": strings.ToLower(readTrimmed(filepath.Join(deviceRoot, "device"))),
		}
		for file, metric := range map[string]string{
			"gpu_busy_percent":    "utilizationPercent",
			"mem_info_vram_used":  "memoryUsedBytes",
			"mem_info_vram_total": "memoryTotalBytes",
		} {
			if value, ok := numericFile(filepath.Join(deviceRoot, file), 1); ok {
				gpu[metric] = value
			}
		}
		hwmons, _ := filepath.Glob(filepath.Join(deviceRoot, "hwmon", "hwmon*"))
		for _, hwmon := range hwmons {
			if value, ok := numericFile(filepath.Join(hwmon, "temp1_input"), 1000); ok {
				gpu["temperatureC"] = value
			}
			if value, ok := numericFile(filepath.Join(hwmon, "power1_average"), 1_000_000); ok {
				gpu["powerWatts"] = value
			}
		}
		result = append(result, gpu)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no supported GPU sysfs device found")
	}
	return result, nil
}
