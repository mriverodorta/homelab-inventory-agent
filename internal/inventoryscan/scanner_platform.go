//go:build linux || freebsd

package inventoryscan

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/command"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type fixedScanner struct {
	runner command.Runner
	now    func() time.Time
	scan   func(context.Context, command.Runner) ([]protocol.HardwareComponent, error)
}

func (scanner fixedScanner) Collect(ctx context.Context, host protocol.HostRef) (protocol.HardwareSnapshot, error) {
	if err := protocol.ValidateHostRef(host); err != nil {
		return protocol.HardwareSnapshot{}, err
	}
	if scanner.runner == nil || scanner.scan == nil {
		return protocol.HardwareSnapshot{}, errors.New("hardware scanner dependencies are incomplete")
	}
	components, err := scanner.scan(ctx, scanner.runner)
	if err != nil {
		return protocol.HardwareSnapshot{}, err
	}
	now := time.Now().UTC()
	if scanner.now != nil {
		now = scanner.now().UTC()
	}
	snapshot := protocol.HardwareSnapshot{ProtocolMajor: protocol.CurrentMajor, Host: host, CollectedAt: now, Components: components}
	if err := protocol.ValidateHardwareSnapshot(snapshot); err != nil {
		return protocol.HardwareSnapshot{}, err
	}
	return snapshot, nil
}

func parseLineRecords(kind string, body []byte, limit int) []protocol.HardwareComponent {
	var components []protocol.HardwareComponent
	for index, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(components) >= limit {
			continue
		}
		components = append(components, protocol.HardwareComponent{
			Kind: kind, Locator: kind + "-" + strconv.Itoa(index+1), Values: map[string]any{"description": boundedString(line)},
		})
	}
	return components
}

func runBounded(ctx context.Context, runner command.Runner, timeout time.Duration, name string, arguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := runner.Run(commandContext, name, arguments...)
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", name, err)
	}
	return body, nil
}
