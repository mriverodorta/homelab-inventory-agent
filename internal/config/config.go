package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Config struct {
	Endpoint       string           `json:"endpoint"`
	Host           protocol.HostRef `json:"host"`
	StateDirectory string           `json:"stateDirectory"`
	Filesystems    []string         `json:"filesystems,omitempty"`
	StorageHealth  StorageHealth    `json:"storageHealth,omitempty"`
}

type StorageHealth struct {
	SMARTDevices []string `json:"smartDevices,omitempty"`
}

func Load(filePath string) (Config, error) {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	var value Config
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Config{}, err
	}
	return value, nil
}

func (c *Config) Validate() error {
	if err := protocol.ValidateHostRef(c.Host); err != nil {
		return fmt.Errorf("invalid configured host: %w", err)
	}
	parsed, err := url.Parse(c.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("endpoint must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint cannot contain credentials, a query, or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path != "" {
		return errors.New("endpoint cannot contain a path")
	}
	c.Endpoint = strings.TrimRight(parsed.String(), "/")
	if !filepath.IsAbs(c.StateDirectory) {
		return errors.New("stateDirectory must be an absolute path")
	}
	if len(c.Filesystems) > 63 {
		return errors.New("filesystems cannot exceed 63 additional mounts")
	}
	seenFilesystems := map[string]struct{}{c.StateDirectory: {}}
	for index, mount := range c.Filesystems {
		clean := filepath.Clean(mount)
		if !filepath.IsAbs(mount) || clean != mount || len(clean) > 4096 {
			return fmt.Errorf("filesystems[%d] must be a normalized absolute path", index)
		}
		if _, duplicate := seenFilesystems[clean]; duplicate || clean == "/" {
			return fmt.Errorf("filesystems[%d] is duplicated or reserved", index)
		}
		seenFilesystems[clean] = struct{}{}
	}
	if len(c.StorageHealth.SMARTDevices) > 64 {
		return errors.New("smartDevices cannot exceed 64 entries")
	}
	seenDevices := map[string]struct{}{}
	for index, device := range c.StorageHealth.SMARTDevices {
		clean := filepath.Clean(device)
		if !filepath.IsAbs(device) || clean != device || !strings.HasPrefix(clean, "/dev/") || len(clean) > 255 {
			return fmt.Errorf("smartDevices[%d] must be a normalized absolute /dev path", index)
		}
		if _, duplicate := seenDevices[clean]; duplicate {
			return fmt.Errorf("smartDevices[%d] is duplicated", index)
		}
		seenDevices[clean] = struct{}{}
	}
	return nil
}
