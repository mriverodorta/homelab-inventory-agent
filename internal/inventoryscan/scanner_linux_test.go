//go:build linux

package inventoryscan

import "testing"

func TestParseLSBLKPreservesDiskIdentity(t *testing.T) {
	components := parseLSBLK([]byte(`{"blockdevices":[{"name":"nvme0n1","path":"/dev/nvme0n1","size":512110190592,"type":"disk","model":"Example SSD","serial":"PRIVATE-SSD","wwn":"PRIVATE-WWN","tran":"nvme","rota":false}]}`))
	if len(components) != 1 || components[0].Locator != "/dev/nvme0n1" || components[0].Values["serial"] != "PRIVATE-SSD" {
		t.Fatalf("lsblk storage identity mismatch: %#v", components)
	}
}
