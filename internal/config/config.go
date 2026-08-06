package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type Config struct {
	Endpoint       string           `json:"endpoint"`
	Host           protocol.HostRef `json:"host"`
	StateDirectory string           `json:"stateDirectory"`
	Filesystems    []string         `json:"filesystems,omitempty"`
	StorageHealth  StorageHealth    `json:"storageHealth,omitempty"`
	Containers     Containers       `json:"containers,omitempty"`
}

type StorageHealth struct {
	SMARTDevices []string `json:"smartDevices,omitempty"`
}

type Containers struct {
	Mode     string `json:"mode,omitempty"`
	Runtime  string `json:"runtime,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
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
	if err := c.Containers.validate(); err != nil {
		return err
	}
	return nil
}

func (c *Containers) validate() error {
	if c.Mode == "" {
		c.Mode = "disabled"
	}
	if c.Runtime == "" {
		c.Runtime = "docker"
	}
	if c.Mode != "disabled" && c.Mode != "proxy" && c.Mode != "socket" {
		return errors.New("containers.mode must be disabled, proxy, or socket")
	}
	if c.Runtime != "docker" && c.Runtime != "podman" {
		return errors.New("containers.runtime must be docker or podman")
	}
	if c.Mode == "disabled" {
		if c.Endpoint != "" {
			return errors.New("containers.endpoint must be empty when container collection is disabled")
		}
		return nil
	}
	if c.Mode == "proxy" {
		parsed, err := url.Parse(c.Endpoint)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("containers.endpoint must be a credential-free loopback HTTP URL in proxy mode")
		}
		hostname := parsed.Hostname()
		if hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1" {
			return errors.New("container proxy must listen on loopback")
		}
		if strings.TrimRight(parsed.Path, "/") != "" {
			return errors.New("container proxy endpoint cannot contain a path")
		}
		c.Endpoint = strings.TrimRight(parsed.String(), "/")
		return nil
	}
	clean := filepath.Clean(c.Endpoint)
	allowed := clean == "/var/run/docker.sock" || clean == "/run/docker.sock" || clean == "/run/podman/podman.sock"
	if !allowed && c.Runtime == "podman" {
		prefix := "/run/user/"
		rest := strings.TrimPrefix(clean, prefix)
		parts := strings.Split(rest, "/")
		if strings.HasPrefix(clean, prefix) && len(parts) == 3 && parts[1] == "podman" && parts[2] == "podman.sock" {
			_, err := strconv.ParseUint(parts[0], 10, 32)
			allowed = err == nil
		}
	}
	if !filepath.IsAbs(c.Endpoint) || clean != c.Endpoint || !allowed {
		return errors.New("containers.endpoint is not an allowed local runtime socket")
	}
	return nil
}
