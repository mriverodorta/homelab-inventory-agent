package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	commandrunner "github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const maxRCdOutput = 256 * 1024

var rcServiceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,255}$`)

type rcService struct {
	name           string
	classification string
}

func classifyRCdPath(path string) string {
	switch {
	case path == "/usr/local/etc/rc.d" || strings.HasPrefix(path, "/usr/local/etc/rc.d/"):
		return "user-installed"
	case path == "/etc/rc.d" || strings.HasPrefix(path, "/etc/rc.d/"):
		return "system"
	default:
		return "unknown"
	}
}

type RCd struct {
	runner  commandrunner.Runner
	timeout time.Duration
}

func NewRCd() *RCd {
	return NewRCdWithRunner(commandrunner.Exec{MaxOutput: maxRCdOutput})
}

func NewRCdWithRunner(runner commandrunner.Runner) *RCd {
	return &RCd{runner: runner, timeout: 10 * time.Second}
}

func (collector *RCd) Collect(ctx context.Context) ([]protocol.Service, error) {
	collectionContext, cancel := context.WithTimeout(ctx, collector.timeout)
	defer cancel()

	output, err := collector.runner.Run(collectionContext, "/usr/sbin/service", "-e")
	if err != nil {
		return nil, fmt.Errorf("list enabled rc.d services: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) > 513 {
		return nil, errors.New("rc.d service count exceeds the protocol limit")
	}
	services := make([]rcService, 0, len(lines))
	seen := map[string]struct{}{}
	for _, line := range lines {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errors.New("rc.d returned an unsafe service path")
		}
		name := filepath.Base(path)
		if !rcServiceNamePattern.MatchString(name) {
			return nil, errors.New("rc.d returned an unsafe service name")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		services = append(services, rcService{name: name, classification: classifyRCdPath(path)})
	}
	if len(services) > 512 {
		return nil, errors.New("rc.d service count exceeds the protocol limit")
	}
	sort.Slice(services, func(first, second int) bool { return services[first].name < services[second].name })

	result := make([]protocol.Service, 0, len(services))
	for _, service := range services {
		statusContext, statusCancel := context.WithTimeout(collectionContext, time.Second)
		body, statusErr := collector.runner.Run(statusContext, "/usr/sbin/service", service.name, "onestatus")
		statusCancel()
		statusText := strings.ToLower(string(body))
		activeState, subState := "unknown", "status-unavailable"
		switch {
		case statusErr == nil:
			activeState, subState = "active", "running"
		case strings.Contains(statusText, "not running") || strings.Contains(statusText, "is stopped"):
			activeState, subState = "inactive", "stopped"
		case errors.Is(collectionContext.Err(), context.DeadlineExceeded):
			activeState, subState = "unknown", "collection-timeout"
		}
		enabled := true
		result = append(result, protocol.Service{
			Name: service.name, ActiveState: activeState, SubState: subState, Enabled: &enabled,
			Classification: service.classification,
		})
	}
	return result, nil
}
