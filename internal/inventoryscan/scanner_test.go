package inventoryscan

import (
	"strings"
	"testing"
)

func TestParseDMICollectsPopulatedComponentsAndPrivateIdentifiers(t *testing.T) {
	components := parseDMI([]byte(`BIOS Information
	Vendor: Dell Inc.
	Version: 1.2.3
System Information
	Manufacturer: Dell Inc.
	Product Name: OptiPlex 7090
	Serial Number: PRIVATE-SERIAL
Base Board Information
	Manufacturer: Dell Inc.
	Product Name: 0ABC12
	Serial Number: PRIVATE-BOARD
Processor Information
	Socket Designation: U3E1
	Version: Intel Core i5-10500T
Memory Device
	Size: No Module Installed
	Locator: DIMM_A1
Memory Device
	Size: 8192 MB
	Locator: DIMM_B1
	Manufacturer: Micron
	Serial Number: PRIVATE-DIMM
`))
	if len(components) != 5 {
		t.Fatalf("unexpected DMI components: %#v", components)
	}
	if components[2].Kind != "motherboard" || components[3].Locator != "U3E1" || components[4].Locator != "DIMM_B1" {
		t.Fatalf("DMI topology was not preserved: %#v", components)
	}
	encoded := strings.Join([]string{
		components[1].Values["serialNumber"].(string), components[2].Values["serialNumber"].(string), components[4].Values["serialNumber"].(string),
	}, " ")
	if !strings.Contains(encoded, "PRIVATE-SERIAL") || !strings.Contains(encoded, "PRIVATE-BOARD") || !strings.Contains(encoded, "PRIVATE-DIMM") {
		t.Fatalf("one-time scan lost private matching evidence: %s", encoded)
	}
}

func TestParseKenvSMBIOSPreservesOPNsenseBoardIdentity(t *testing.T) {
	components := parseKenvSMBIOS(map[string]string{
		"smbios.system.maker":   "Dell Inc.",
		"smbios.system.product": "OptiPlex 7090",
		"smbios.system.serial":  "PRIVATE-SYSTEM",
		"smbios.planar.maker":   "Dell Inc.",
		"smbios.planar.product": "0ABC12",
		"smbios.planar.serial":  "PRIVATE-BOARD",
	})
	if len(components) != 2 {
		t.Fatalf("unexpected kenv components: %#v", components)
	}
	if components[0].Kind != "system" || components[1].Kind != "motherboard" {
		t.Fatalf("kenv topology was not preserved: %#v", components)
	}
	if components[1].Values["serialNumber"] != "PRIVATE-BOARD" {
		t.Fatalf("kenv board identity was not preserved: %#v", components[1])
	}
}
