package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func loadMonitoringConfig(filePath string) (protocol.MonitoringConfig, error) {
	body, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return protocol.MonitoringConfig{}, nil
	}
	if err != nil {
		return protocol.MonitoringConfig{}, err
	}
	var config protocol.MonitoringConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return protocol.MonitoringConfig{}, err
	}
	if err := protocol.ValidateMonitoringConfig(config); err != nil {
		return protocol.MonitoringConfig{}, err
	}
	return config, nil
}

func writeMonitoringConfig(filePath string, config protocol.MonitoringConfig) error {
	if err := protocol.ValidateMonitoringConfig(config); err != nil {
		return err
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(filePath), ".monitoring-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filePath)
}
