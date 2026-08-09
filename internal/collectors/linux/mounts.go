//go:build linux

package linux

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type mountRecord struct {
	MountID    uint64
	ParentID   uint64
	MajorMinor string
	Root       string
	MountPoint string
	Options    []string
	Optional   []string
	FSType     string
	Source     string
	Super      []string
}

func readMountInfo(path string) ([]mountRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make([]mountRecord, 0, 32)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		record, valid := parseMountInfoLine(scanner.Text())
		if valid {
			result = append(result, record)
		}
		if len(result) >= 256 {
			break
		}
	}
	return result, scanner.Err()
}

func parseMountInfoLine(line string) (mountRecord, bool) {
	fields := strings.Fields(line)
	separator := -1
	for index, field := range fields {
		if field == "-" {
			separator = index
			break
		}
	}
	if separator < 6 || separator+3 >= len(fields) {
		return mountRecord{}, false
	}
	mountID, mountErr := strconv.ParseUint(fields[0], 10, 64)
	parentID, parentErr := strconv.ParseUint(fields[1], 10, 64)
	if mountErr != nil || parentErr != nil {
		return mountRecord{}, false
	}
	return mountRecord{
		MountID: mountID, ParentID: parentID, MajorMinor: fields[2],
		Root: decodeMountField(fields[3]), MountPoint: decodeMountField(fields[4]),
		Options: splitComma(fields[5]), Optional: append([]string(nil), fields[6:separator]...),
		FSType: fields[separator+1], Source: decodeMountField(fields[separator+2]),
		Super: splitComma(fields[separator+3]),
	}, true
}

func splitComma(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func decodeMountField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func collectMountedFilesystems(path string) ([]map[string]any, error) {
	mounts, err := readMountInfo(path)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, min(len(mounts), 256))
	for _, mount := range mounts {
		if len(result) >= 256 {
			break
		}
		var status syscall.Statfs_t
		if err := syscall.Statfs(mount.MountPoint, &status); err != nil {
			continue
		}
		blockSize := uint64(status.Bsize)
		entry := map[string]any{
			"mountId": mount.MountID, "parentId": mount.ParentID,
			"majorMinor": mount.MajorMinor, "root": mount.Root,
			"mountPoint": mount.MountPoint, "source": mount.Source, "fsType": mount.FSType,
			"options": mount.Options, "superOptions": mount.Super,
			"readOnly":       contains(mount.Options, "ro"),
			"totalBytes":     status.Blocks * blockSize,
			"availableBytes": status.Bavail * blockSize, "freeBytes": status.Bfree * blockSize,
			"usedBytes": (status.Blocks - status.Bfree) * blockSize,
		}
		if len(mount.Optional) > 0 {
			entry["optionalFields"] = mount.Optional
		}
		result = append(result, entry)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no readable mounted filesystems")
	}
	return result, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
