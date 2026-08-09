//go:build linux

package linux

import "testing"

func TestParseMountInfoLinePreservesTopology(t *testing.T) {
	record, ok := parseMountInfoLine(`36 25 8:2 /root\040dir /data\040disk rw,relatime shared:7 - ext4 /dev/sda2 rw,errors=remount-ro`)
	if !ok {
		t.Fatal("expected mountinfo line to parse")
	}
	if record.MajorMinor != "8:2" || record.Root != "/root dir" || record.MountPoint != "/data disk" || record.Source != "/dev/sda2" {
		t.Fatalf("unexpected mount record: %#v", record)
	}
	if record.FSType != "ext4" || !contains(record.Options, "rw") || len(record.Optional) != 1 {
		t.Fatalf("mount metadata was not retained: %#v", record)
	}
}
