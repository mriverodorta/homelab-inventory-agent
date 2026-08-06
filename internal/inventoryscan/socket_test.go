package inventoryscan

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func testSnapshot() protocol.HardwareSnapshot {
	return protocol.HardwareSnapshot{
		ProtocolMajor: 1,
		Host:          protocol.HostRef{Type: protocol.HostServer, ID: 7},
		CollectedAt:   time.Now().UTC(),
		Components: []protocol.HardwareComponent{{
			Kind: "memory", Locator: "DIMM_A1",
			Values: map[string]any{"manufacturer": "Example", "serialNumber": "PRIVATE-1234", "sizeBytes": float64(8 << 30)},
		}},
	}
}

func TestSocketDoubleValidatesAndAddsOpaqueFingerprint(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "hli-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "inventory.sock")
	received := make(chan protocol.HardwareSnapshot, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &Server{
		SocketPath: path,
		Host:       protocol.HostRef{Type: protocol.HostServer, ID: 7},
		OpaqueID:   func(namespace, value string) string { return namespace + ":" + value[:6] },
		Authorize:  func(*net.UnixConn) error { return nil },
		Submit: func(_ context.Context, snapshot protocol.HardwareSnapshot) error {
			received <- snapshot
			return nil
		},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe(ctx) }()
	for attempt := 0; attempt < 100; attempt++ {
		if err := Send(context.Background(), path, testSnapshot()); err == nil {
			break
		} else if attempt == 99 {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot := <-received
	if got := snapshot.Components[0].Values["opaqueFingerprint"]; !strings.HasPrefix(got.(string), "hardware-component:") {
		t.Fatalf("opaque fingerprint missing: %#v", snapshot)
	}
	invalid := testSnapshot()
	invalid.Host.ID = 8
	if err := Send(context.Background(), path, invalid); err == nil || !strings.Contains(err.Error(), "configured host") {
		t.Fatalf("cross-host snapshot was not rejected: %v", err)
	}
	cancel()
	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
}

func TestConfirmationIsExplicitAndSummaryDoesNotExposeSerials(t *testing.T) {
	var output strings.Builder
	confirmed, err := Confirm(strings.NewReader("yes\n"), &output, testSnapshot())
	if err != nil || !confirmed {
		t.Fatalf("confirmation failed: %v %v", confirmed, err)
	}
	if strings.Contains(output.String(), "PRIVATE-1234") || !strings.Contains(output.String(), "memory") {
		t.Fatalf("summary leaked private data or omitted counts: %s", output.String())
	}
	confirmed, err = Confirm(strings.NewReader("\n"), &output, testSnapshot())
	if err != nil || confirmed {
		t.Fatalf("default confirmation was not deny: %v %v", confirmed, err)
	}
}
