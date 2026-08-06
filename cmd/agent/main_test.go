package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

type commandScanner struct {
	snapshot protocol.HardwareSnapshot
}

func TestVersionDoesNotRequireConfiguration(t *testing.T) {
	if err := run(context.Background(), []string{"-version"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentCodeSourcesAreExclusiveAndBounded(t *testing.T) {
	if err := run(context.Background(), []string{"-enrollment-code", "one", "-enrollment-code-file", "/tmp/other"}); err == nil {
		t.Fatal("multiple enrollment-code sources were accepted")
	}
	file := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(file, make([]byte, 513), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-enrollment-code-file", file}); err == nil {
		t.Fatal("oversized enrollment-code file was accepted")
	}
}

func (scanner commandScanner) Collect(_ context.Context, host protocol.HostRef) (protocol.HardwareSnapshot, error) {
	snapshot := scanner.snapshot
	snapshot.Host = host
	return snapshot, nil
}

func TestInventoryCommandDoesNotCreateOrReadAgentIdentityBeforeConfirmation(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	stateDirectory := filepath.Join(directory, "state")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`{"endpoint":"https://inventory.example.com","host":{"type":"server","id":1},"stateDirectory":%q}`, stateDirectory)), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := effectiveUserID
	effectiveUserID = func() int { return 0 }
	t.Cleanup(func() { effectiveUserID = previous })
	scanner := commandScanner{snapshot: protocol.HardwareSnapshot{
		ProtocolMajor: 1, CollectedAt: time.Now().UTC(),
		Components: []protocol.HardwareComponent{{Kind: "motherboard", Locator: "board-1", Values: map[string]any{"serialNumber": "PRIVATE"}}},
	}}
	var output strings.Builder
	if err := runInventory(context.Background(), []string{"-config", configPath}, strings.NewReader("no\n"), &output, scanner); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDirectory, "identity.json")); !os.IsNotExist(err) {
		t.Fatalf("privileged command touched the private identity: %v", err)
	}
	if strings.Contains(output.String(), "PRIVATE") || !strings.Contains(output.String(), "was not sent") {
		t.Fatalf("privileged preview leaked data or omitted cancellation: %s", output.String())
	}
}

func TestInventoryCommandRequiresRoot(t *testing.T) {
	previous := effectiveUserID
	effectiveUserID = func() int { return 1000 }
	t.Cleanup(func() { effectiveUserID = previous })
	err := runInventory(context.Background(), nil, strings.NewReader("no\n"), &strings.Builder{}, commandScanner{})
	if err == nil || !strings.Contains(err.Error(), "elevated privileges") {
		t.Fatalf("unprivileged scan was allowed: %v", err)
	}
}
