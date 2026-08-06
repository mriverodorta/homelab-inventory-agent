package inventoryscan

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Scanner interface {
	Collect(context.Context, protocol.HostRef) (protocol.HardwareSnapshot, error)
}

var nonIdentifier = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func fieldKey(value string) string {
	parts := nonIdentifier.Split(strings.TrimSpace(value), -1)
	if len(parts) == 0 {
		return "value"
	}
	result := strings.ToLower(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		result += string(runes)
	}
	if result == "" || result == "__proto__" || result == "constructor" || result == "prototype" {
		return "value"
	}
	return result
}

func boundedString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}

func parseDMI(body []byte) []protocol.HardwareComponent {
	type dmiSection struct {
		title  string
		values map[string]any
	}
	var sections []dmiSection
	var current *dmiSection
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line := strings.TrimRight(rawLine, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "Handle ") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if len(line) > 0 && line[0] != ' ' && !strings.Contains(trimmed, ":") {
			sections = append(sections, dmiSection{title: trimmed, values: map[string]any{}})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found || strings.TrimSpace(value) == "" {
			continue
		}
		value = boundedString(value)
		if strings.EqualFold(value, "Not Specified") || strings.EqualFold(value, "Unknown") || strings.EqualFold(value, "No Module Installed") {
			continue
		}
		current.values[fieldKey(key)] = value
	}
	kindByTitle := map[string]string{
		"BIOS Information": "bios", "System Information": "system", "Base Board Information": "motherboard",
		"Chassis Information": "chassis", "Processor Information": "cpu", "Physical Memory Array": "memory-array",
		"Memory Device": "memory", "System Power Supply": "power-supply",
	}
	counts := map[string]int{}
	components := make([]protocol.HardwareComponent, 0, len(sections))
	for _, section := range sections {
		kind := kindByTitle[section.title]
		if kind == "" || len(section.values) == 0 {
			continue
		}
		if kind == "memory" {
			if _, present := section.values["size"]; !present {
				continue
			}
		}
		counts[kind]++
		locator := fmt.Sprintf("%s-%d", kind, counts[kind])
		for _, key := range []string{"locator", "bankLocator", "socketDesignation", "designation"} {
			if value, present := section.values[key]; present && fmt.Sprint(value) != "" {
				locator = boundedString(fmt.Sprint(value))
				break
			}
		}
		components = append(components, protocol.HardwareComponent{Kind: kind, Locator: locator, Values: section.values})
	}
	return components
}

func parseKenvSMBIOS(values map[string]string) []protocol.HardwareComponent {
	groups := []struct {
		kind    string
		locator string
		prefix  string
		fields  map[string]string
	}{
		{kind: "bios", locator: "bios-1", prefix: "smbios.bios.", fields: map[string]string{"vendor": "vendor", "version": "version", "reldate": "releaseDate"}},
		{kind: "system", locator: "system-1", prefix: "smbios.system.", fields: map[string]string{"maker": "manufacturer", "product": "productName", "version": "version", "serial": "serialNumber", "uuid": "uuid"}},
		{kind: "motherboard", locator: "motherboard-1", prefix: "smbios.planar.", fields: map[string]string{"maker": "manufacturer", "product": "productName", "version": "version", "serial": "serialNumber"}},
		{kind: "chassis", locator: "chassis-1", prefix: "smbios.chassis.", fields: map[string]string{"maker": "manufacturer", "type": "type", "version": "version", "serial": "serialNumber"}},
	}
	components := make([]protocol.HardwareComponent, 0, len(groups))
	for _, group := range groups {
		componentValues := map[string]any{}
		for source, destination := range group.fields {
			value := boundedString(values[group.prefix+source])
			if value == "" || strings.EqualFold(value, "unknown") || strings.EqualFold(value, "not specified") {
				continue
			}
			componentValues[destination] = value
		}
		if len(componentValues) > 0 {
			components = append(components, protocol.HardwareComponent{Kind: group.kind, Locator: group.locator, Values: componentValues})
		}
	}
	return components
}
