package inventoryscan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

const MaxSnapshotBytes = 2 << 20

type SnapshotSubmitter func(context.Context, protocol.HardwareSnapshot) error
type PeerAuthorizer func(*net.UnixConn) error

type Server struct {
	SocketPath string
	Host       protocol.HostRef
	OpaqueID   func(string, string) string
	Submit     SnapshotSubmitter
	Authorize  PeerAuthorizer
	Now        func() time.Time
}

type socketResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func (server *Server) Validate() error {
	if server.SocketPath == "" || !filepath.IsAbs(server.SocketPath) {
		return errors.New("inventory socket path must be absolute")
	}
	if err := protocol.ValidateHostRef(server.Host); err != nil {
		return err
	}
	if server.OpaqueID == nil || server.Submit == nil {
		return errors.New("inventory socket dependencies are incomplete")
	}
	return nil
}

func (server *Server) ListenAndServe(ctx context.Context) error {
	if err := server.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(server.SocketPath), 0o700); err != nil {
		return fmt.Errorf("create inventory socket directory: %w", err)
	}
	if err := os.Remove(server.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale inventory socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on inventory socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(server.SocketPath)
	}()
	if err := os.Chmod(server.SocketPath, 0o600); err != nil {
		return fmt.Errorf("protect inventory socket: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("accept inventory snapshot: %w", acceptErr)
		}
		if err := server.handle(ctx, connection); err != nil {
			_ = json.NewEncoder(connection).Encode(socketResponse{Message: err.Error()})
		} else {
			_ = json.NewEncoder(connection).Encode(socketResponse{OK: true})
		}
		_ = connection.Close()
	}
}

func (server *Server) handle(ctx context.Context, connection *net.UnixConn) error {
	authorize := server.Authorize
	if authorize == nil {
		authorize = authorizeRootPeer
	}
	if err := authorize(connection); err != nil {
		return err
	}
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	body, err := io.ReadAll(io.LimitReader(connection, MaxSnapshotBytes+1))
	if err != nil {
		return fmt.Errorf("read inventory snapshot: %w", err)
	}
	if len(body) == 0 || len(body) > MaxSnapshotBytes {
		return errors.New("inventory snapshot exceeds the size limit")
	}
	var snapshot protocol.HardwareSnapshot
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return fmt.Errorf("decode inventory snapshot: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("inventory snapshot contains trailing data")
	}
	if err := protocol.ValidateHardwareSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Host != server.Host {
		return errors.New("inventory snapshot does not match the configured host")
	}
	now := time.Now().UTC()
	if server.Now != nil {
		now = server.Now().UTC()
	}
	if snapshot.CollectedAt.Before(now.Add(-15*time.Minute)) || snapshot.CollectedAt.After(now.Add(2*time.Minute)) {
		return errors.New("inventory snapshot timestamp is outside the accepted window")
	}
	addOpaqueFingerprints(&snapshot, server.OpaqueID)
	if err := protocol.ValidateHardwareSnapshot(snapshot); err != nil {
		return fmt.Errorf("normalized inventory snapshot is invalid: %w", err)
	}
	return server.Submit(ctx, snapshot)
}

func addOpaqueFingerprints(snapshot *protocol.HardwareSnapshot, opaqueID func(string, string) string) {
	for index := range snapshot.Components {
		component := &snapshot.Components[index]
		keys := make([]string, 0, len(component.Values))
		for key := range component.Values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := []string{component.Kind, component.Locator}
		for _, key := range keys {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "serial") || strings.Contains(lower, "uuid") || strings.Contains(lower, "wwn") || strings.Contains(lower, "partnumber") {
				parts = append(parts, key, fmt.Sprint(component.Values[key]))
			}
		}
		if len(parts) == 2 {
			for _, key := range keys {
				parts = append(parts, key, fmt.Sprint(component.Values[key]))
			}
		}
		component.Values["opaqueFingerprint"] = opaqueID("hardware-component", strings.Join(parts, "\x00"))
	}
}

func Send(ctx context.Context, socketPath string, snapshot protocol.HardwareSnapshot) error {
	if err := protocol.ValidateHardwareSnapshot(snapshot); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	defer clearBytes(body)
	if len(body) > MaxSnapshotBytes {
		return errors.New("inventory snapshot exceeds the size limit")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to agent inventory socket: %w", err)
	}
	defer connection.Close()
	if _, err := connection.Write(body); err != nil {
		return fmt.Errorf("send inventory snapshot: %w", err)
	}
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		_ = unixConnection.CloseWrite()
	}
	var response socketResponse
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&response); err != nil {
		return fmt.Errorf("read agent inventory response: %w", err)
	}
	if !response.OK {
		return errors.New(response.Message)
	}
	return nil
}

func Confirm(reader io.Reader, writer io.Writer, snapshot protocol.HardwareSnapshot) (bool, error) {
	if err := WriteMaskedSummary(writer, snapshot); err != nil {
		return false, err
	}
	if _, err := fmt.Fprint(writer, "Send this hardware snapshot? [y/N] "); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func WriteMaskedSummary(writer io.Writer, snapshot protocol.HardwareSnapshot) error {
	counts := map[string]int{}
	for _, component := range snapshot.Components {
		counts[component.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	if _, err := fmt.Fprintf(writer, "Hardware snapshot for %s:%d\n", snapshot.Host.Type, snapshot.Host.ID); err != nil {
		return err
	}
	for _, kind := range kinds {
		if _, err := fmt.Fprintf(writer, "  %-18s %d\n", kind, counts[kind]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(writer, "Private serials and hardware identifiers will be sent only to this Homelab Inventory installation and never to the public registry.")
	return err
}

func Clear(snapshot *protocol.HardwareSnapshot) {
	for index := range snapshot.Components {
		for key := range snapshot.Components[index].Values {
			delete(snapshot.Components[index].Values, key)
		}
		snapshot.Components[index].Locator = ""
		snapshot.Components[index].Kind = ""
	}
	snapshot.Components = nil
}

func clearBytes(body []byte) {
	for index := range body {
		body[index] = 0
	}
}
