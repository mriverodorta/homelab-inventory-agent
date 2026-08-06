package freebsd

import (
	"strconv"
	"strings"
)

func parsePCI(body []byte) []map[string]any {
	var result []map[string]any
	var current map[string]any
	flush := func() {
		if len(current) > 1 && len(result) < 128 {
			result = append(result, current)
		}
		current = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] != ' ' && strings.Contains(line, "@pci") {
			flush()
			selector, properties, found := strings.Cut(line, ":")
			if !found {
				continue
			}
			name := strings.SplitN(selector, "@", 2)[0]
			if name == "" || len(name) > 64 {
				continue
			}
			current = map[string]any{"name": name}
			for _, property := range strings.Fields(properties) {
				key, value, found := strings.Cut(property, "=")
				if found && (key == "class" || key == "vendor" || key == "device" || key == "subvendor" || key == "subdevice") {
					current[key+"Id"] = value
				}
			}
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "'")
		if len(value) > 256 {
			value = value[:256]
		}
		switch key {
		case "vendor", "device", "class", "subclass":
			current[key] = value
		}
	}
	flush()
	return result
}

func parseGEOMDisks(body []byte) []map[string]any {
	var result []map[string]any
	var current map[string]any
	flush := func() {
		if len(current) > 1 && len(result) < 128 {
			result = append(result, current)
		}
		current = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Geom name:") {
			flush()
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "Geom name:"))
			if name != "" && len(name) <= 128 {
				current = map[string]any{"name": name}
			}
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "mediasize":
			fields := strings.Fields(value)
			if len(fields) > 0 {
				if parsed, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
					current["capacityBytes"] = parsed
				}
			}
		case "sectorsize":
			if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
				current["sectorBytes"] = parsed
			}
		case "descr":
			if value != "" {
				if len(value) > 256 {
					value = value[:256]
				}
				current["description"] = value
			}
		case "rotationrate":
			if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
				current["rotationRPM"] = parsed
			}
		}
	}
	flush()
	return result
}
