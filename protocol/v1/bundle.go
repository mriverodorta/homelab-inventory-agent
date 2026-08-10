package protocol

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

const LegacyBundleDigest = "6991de825d245d5906d64a137f51fd52ed820c97c5f093a0935434a0130c06ec"

//go:embed *.schema.json
var schemaFiles embed.FS

type Schema struct {
	Name    string
	Content []byte
}

func Schemas() ([]Schema, error) {
	entries, err := fs.ReadDir(schemaFiles, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded schemas: %w", err)
	}
	result := make([]Schema, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, readErr := schemaFiles.ReadFile(entry.Name())
		if readErr != nil {
			return nil, fmt.Errorf("read schema %s: %w", entry.Name(), readErr)
		}
		result = append(result, Schema{Name: entry.Name(), Content: content})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func BundleDigest() (string, error) {
	schemas, err := Schemas()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, schema := range schemas {
		_, _ = hash.Write([]byte(schema.Name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(schema.Content)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func IsCompatibleBundleDigest(value string) (bool, error) {
	current, err := BundleDigest()
	if err != nil {
		return false, err
	}
	return value == current || value == LegacyBundleDigest, nil
}

func SupportsMonitoringPolicy(value string) (bool, error) {
	current, err := BundleDigest()
	if err != nil {
		return false, err
	}
	return value == current, nil
}
