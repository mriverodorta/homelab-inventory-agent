//go:build linux

package inventoryscan

import "testing"

func TestParseLSBLKPreservesDiskIdentity(t *testing.T) {
	components := parseLSBLK([]byte(`{"blockdevices":[{"name":"nvme0n1","path":"/dev/nvme0n1","size":512110190592,"type":"disk","model":"Example SSD","serial":"PRIVATE-SSD","wwn":"PRIVATE-WWN","tran":"nvme","rota":false,"pttype":"gpt","children":[{"name":"nvme0n1p1","path":"/dev/nvme0n1p1","size":511000000000,"type":"part","fstype":"ext4","mountpoints":["/"]}]}]}`))
	if len(components) != 1 || components[0].Locator != "/dev/nvme0n1" || components[0].Values["serial"] != "PRIVATE-SSD" {
		t.Fatalf("lsblk storage identity mismatch: %#v", components)
	}
	children, ok := components[0].Values["children"].([]map[string]any)
	if !ok || len(children) != 1 || children[0]["fstype"] != "ext4" {
		t.Fatalf("lsblk topology was not retained: %#v", components[0].Values)
	}
}
