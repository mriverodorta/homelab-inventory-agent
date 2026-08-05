package transport

import (
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func testContract(t *testing.T) protocol.Contract {
	t.Helper()
	digest, err := protocol.BundleDigest()
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Contract{
		ProtocolMajor: protocol.CurrentMajor, Revision: 1, IssuedAt: time.Now().UTC(), SchemaBundleDigest: digest,
		Collection: protocol.CollectionPolicy{HostIntervalSeconds: 60, ServiceIntervalSeconds: 600, StorageHealthIntervalSeconds: 3600, GPUSampleIntervalSeconds: 4},
		Limits:     protocol.PayloadLimits{CompressedBytes: 262144, DecompressedBytes: 1048576, OfflineSamples: 60, OfflineBytes: 10485760},
	}
}

func TestFetchContractUsesETagAndRejectsWrongSchema(t *testing.T) {
	contract := testContract(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("If-None-Match") == `"current"` {
			response.WriteHeader(http.StatusNotModified)
			return
		}
		response.Header().Set("ETag", `"current"`)
		_ = json.NewEncoder(response).Encode(contract)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	received, etag, notModified, err := client.FetchContract(context.Background(), "")
	if err != nil || received.Revision != 1 || etag != `"current"` || notModified {
		t.Fatalf("contract fetch: %#v %q %v %v", received, etag, notModified, err)
	}
	_, _, notModified, err = client.FetchContract(context.Background(), etag)
	if err != nil || !notModified || requests != 2 {
		t.Fatalf("etag fetch: %v %v %d", notModified, err, requests)
	}

	contract.SchemaBundleDigest = strings.Repeat("0", 64)
	_, _, _, err = client.FetchContract(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("wrong schema accepted: %v", err)
	}
}

func TestActivateAndSendSignedHeartbeat(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/agent/hosts/server/3/activate":
			if request.Header.Get("Authorization") != "Bearer token" {
				t.Fatal("activation token missing")
			}
			_ = json.NewEncoder(response).Encode(ActivationResponse{DeviceID: 8, ProtocolMajor: 1, ContractURL: "/contract", HeartbeatURL: "/heartbeat"})
		case "/api/agent/hosts/server/3/heartbeats":
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			digestBytes := sha256.Sum256(body)
			digest := hex.EncodeToString(digestBytes[:])
			if request.Header.Get("X-Homelab-Agent-Content-Sha256") != digest || request.Header.Get("Content-Encoding") != "gzip" {
				t.Fatal("heartbeat digest or encoding missing")
			}
			sequence, _ := strconv.ParseUint(request.Header.Get("X-Homelab-Agent-Sequence"), 10, 64)
			signature, _ := base64.StdEncoding.DecodeString(request.Header.Get("X-Homelab-Agent-Signature"))
			canonical := CanonicalRequest(request.Method, request.URL.Path, request.Header.Get("X-Homelab-Agent-Timestamp"), sequence, digest)
			if !ed25519.Verify(publicKey, canonical, signature) {
				t.Fatal("heartbeat signature invalid")
			}
			reader, gzipErr := gzip.NewReader(strings.NewReader(string(body)))
			if gzipErr != nil {
				t.Fatal(gzipErr)
			}
			_, _ = io.Copy(io.Discard, reader)
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	activation, err := client.Activate(context.Background(), protocol.HostRef{Type: protocol.HostServer, ID: 3}, "token", protocol.Activation{
		ProtocolMajor: 1, AgentVersion: "test", PublicKey: "key", Capabilities: map[string]protocol.Capability{},
	})
	if err != nil || activation.DeviceID != 8 {
		t.Fatalf("activate: %#v %v", activation, err)
	}
	var body strings.Builder
	writer := gzip.NewWriter(&body)
	_, _ = writer.Write([]byte(`{"protocolMajor":1}`))
	_ = writer.Close()
	if err := client.SendHeartbeat(context.Background(), protocol.HostRef{Type: protocol.HostServer, ID: 3}, 8, privateKey, 1, []byte(body.String())); err != nil {
		t.Fatal(err)
	}
}
