package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mriverodorta/homelab-inventory-agent/internal/transport"
	protocol "github.com/mriverodorta/homelab-inventory-agent/protocol/v1"
)

func touch(path string) {
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		log.Printf("write marker %s: %v", path, err)
	}
}

func contract() protocol.Contract {
	digest, err := protocol.BundleDigest()
	if err != nil {
		log.Fatal(err)
	}
	return protocol.Contract{
		ProtocolMajor:      protocol.CurrentMajor,
		Revision:           1,
		IssuedAt:           time.Now().UTC(),
		SchemaBundleDigest: digest,
		Collection: protocol.CollectionPolicy{
			HostIntervalSeconds:          60,
			ServiceIntervalSeconds:       600,
			StorageHealthIntervalSeconds: 3600,
			GPUSampleIntervalSeconds:     4,
		},
		Limits: protocol.PayloadLimits{
			CompressedBytes:   262144,
			DecompressedBytes: 1048576,
			OfflineSamples:    60,
			OfflineBytes:      10485760,
		},
		Privacy: protocol.PrivacyPolicy{ContainersEnabled: true},
	}
}

func agentHandler(failureFile, heartbeatMarker string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/contracts/current", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(contract())
	})
	mux.HandleFunc("POST /api/agent/hosts/server/7/activate", func(response http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(failureFile); err == nil {
			response.WriteHeader(http.StatusUnauthorized)
			_, _ = response.Write([]byte(`{"message":"injected activation failure"}`))
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(transport.ActivationResponse{
			DeviceID: 1, ProtocolMajor: protocol.CurrentMajor,
			ContractURL:  "/api/agent/contracts/current",
			HeartbeatURL: "/api/agent/hosts/server/7/heartbeats",
		})
	})
	mux.HandleFunc("POST /api/agent/hosts/server/7/heartbeats", func(response http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		touch(heartbeatMarker)
		response.Header().Set("Content-Type", "application/json")
		sequence := request.Header.Get("X-Homelab-Agent-Sequence")
		_, _ = response.Write([]byte(`{"ok":true,"sequence":` + sequence + `,"receivedAt":"2026-08-09T00:00:00Z"}`))
	})
	return mux
}

func runtimeHandler(runtimeMarker string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"Version":"29.1.3","ApiVersion":"1.52","MinAPIVersion":"1.44"}`))
	})
	mux.HandleFunc("GET /v1.52/containers/json", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("all") != "0" {
			http.Error(response, "invalid all query", http.StatusBadRequest)
			return
		}
		touch(runtimeMarker)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`[]`))
	})
	return mux
}

func main() {
	agentAddress := flag.String("agent-address", "127.0.0.1:8080", "agent API listen address")
	runtimeAddress := flag.String("runtime-address", "127.0.0.1:2375", "container runtime listen address")
	failureFile := flag.String("failure-file", "/tmp/hli-fail-activation", "activation failure marker")
	heartbeatMarker := flag.String("heartbeat-marker", "/tmp/hli-heartbeat", "heartbeat marker")
	runtimeMarker := flag.String("runtime-marker", "/tmp/hli-runtime", "runtime request marker")
	flag.Parse()

	runtimeServer := &http.Server{Addr: *runtimeAddress, Handler: runtimeHandler(*runtimeMarker), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := runtimeServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	agentServer := &http.Server{Addr: *agentAddress, Handler: agentHandler(*failureFile, *heartbeatMarker), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("test fixture listening on %s and %s", *agentAddress, *runtimeAddress)
	if err := agentServer.ListenAndServe(); err != nil {
		log.Fatal(fmt.Errorf("serve agent fixture: %w", err))
	}
}
