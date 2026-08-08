package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	commands    [][]string
	version     string
	failCommand string
}

func (runner *fakeRunner) Run(_ context.Context, name string, arguments ...string) error {
	command := append([]string{name}, arguments...)
	runner.commands = append(runner.commands, command)
	if strings.Join(command, " ") == runner.failCommand {
		return errors.New("injected command failure")
	}
	return nil
}

func (runner *fakeRunner) Output(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	if !reflect.DeepEqual(arguments, []string{"-version"}) {
		return nil, errors.New("unexpected executable arguments")
	}
	return []byte(runner.version + "\n"), nil
}

func releaseAsset(path string, body []byte) asset {
	digest := sha256.Sum256(body)
	return asset{Path: path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(body))}
}

func releaseServer(t *testing.T, releaseVersion string, bodies map[string][]byte) *httptest.Server {
	t.Helper()
	assets := make([]asset, 0, len(bodies))
	for name, body := range bodies {
		assets = append(assets, releaseAsset(name, body))
	}
	value := manifest{Version: releaseVersion, SourceRevision: strings.Repeat("a", 40), ProtocolMajor: 1, Assets: assets}
	manifestBody, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/agent/releases/current":
			_ = json.NewEncoder(response).Encode(currentRelease{
				Version: releaseVersion, SourceRevision: value.SourceRevision, ProtocolMajor: 1,
				ManifestURL: "/api/agent/releases/" + releaseVersion + "/manifest.json",
			})
		case "/api/agent/releases/" + releaseVersion + "/manifest.json":
			_, _ = response.Write(manifestBody)
		default:
			prefix := "/api/agent/releases/" + releaseVersion + "/"
			body, exists := bodies[strings.TrimPrefix(request.URL.Path, prefix)]
			if !strings.HasPrefix(request.URL.Path, prefix) || !exists {
				http.NotFound(response, request)
				return
			}
			_, _ = response.Write(body)
		}
	}))
}

func updateFixture(t *testing.T, releaseVersion string) (Options, *fakeRunner, string, string, string) {
	t.Helper()
	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "bin", "homelab-inventory-agent")
	servicePath := filepath.Join(directory, "service", "agent.service")
	stateDirectory := filepath.Join(directory, "state")
	for _, item := range []string{filepath.Dir(binaryPath), filepath.Dir(servicePath), stateDirectory} {
		if err := os.MkdirAll(item, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicePath, []byte("old-service"), 0o644); err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(stateDirectory, "identity.json")
	if err := os.WriteFile(identityPath, []byte("private-identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := releaseServer(t, releaseVersion, map[string][]byte{
		"homelab-inventory-agent-linux-amd64":   []byte("new-binary"),
		"homelab-inventory-agent-freebsd-amd64": []byte("new-freebsd-binary"),
		"homelab-inventory-agent.service":       []byte("new-service"),
		"homelab_inventory_agent":               []byte("new-freebsd-service"),
	})
	t.Cleanup(server.Close)
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"endpoint":`+stringMustMarshal(t, server.URL)+`,"host":{"type":"server","id":7},"stateDirectory":`+stringMustMarshal(t, stateDirectory)+`}`), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{version: releaseVersion}
	selected := &platform{
		os: "linux", arch: "amd64", binaryPath: binaryPath, servicePath: servicePath,
		binaryAsset: "homelab-inventory-agent-linux-amd64", serviceAsset: "homelab-inventory-agent.service",
		stop: []string{"service", "stop"}, reload: []string{"service", "reload"},
		start: []string{"service", "start"}, health: []string{"service", "health"}, serviceMode: 0o644,
	}
	return Options{
		ConfigPath: configPath, CurrentVersion: "0.1.4", EffectiveUserID: func() int { return 0 },
		platform: selected, runner: runner, lockPath: filepath.Join(directory, "update.lock"),
	}, runner, binaryPath, servicePath, identityPath
}

func TestRunUsesFreeBSDServiceTransaction(t *testing.T) {
	options, runner, binaryPath, servicePath, _ := updateFixture(t, "0.1.5")
	options.platform = &platform{
		os: "freebsd", arch: "amd64", binaryPath: binaryPath, servicePath: servicePath,
		binaryAsset: "homelab-inventory-agent-freebsd-amd64", serviceAsset: "homelab_inventory_agent",
		stop:   []string{"service", "homelab_inventory_agent", "stop"},
		start:  []string{"service", "homelab_inventory_agent", "restart"},
		health: []string{"service", "homelab_inventory_agent", "onestatus"}, serviceMode: 0o555,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(binaryPath); string(body) != "new-freebsd-binary" {
		t.Fatalf("FreeBSD executable was not installed: %q", body)
	}
	if body, _ := os.ReadFile(servicePath); string(body) != "new-freebsd-service" {
		t.Fatalf("FreeBSD rc.d service was not installed: %q", body)
	}
	want := [][]string{
		{"service", "homelab_inventory_agent", "stop"},
		{"service", "homelab_inventory_agent", "restart"},
		{"service", "homelab_inventory_agent", "onestatus"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("FreeBSD service transaction mismatch: %#v", runner.commands)
	}
}

func stringMustMarshal(t *testing.T, value string) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRunReplacesExecutableAndServiceWithoutTouchingState(t *testing.T) {
	options, runner, binaryPath, servicePath, identityPath := updateFixture(t, "0.1.5")
	result, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || !result.UpdateAvailable {
		t.Fatalf("update result mismatch: %#v", result)
	}
	if body, _ := os.ReadFile(binaryPath); string(body) != "new-binary" {
		t.Fatalf("executable was not replaced: %q", body)
	}
	if body, _ := os.ReadFile(servicePath); string(body) != "new-service" {
		t.Fatalf("service was not replaced: %q", body)
	}
	if body, _ := os.ReadFile(identityPath); string(body) != "private-identity" {
		t.Fatalf("private state changed: %q", body)
	}
	wantCommands := [][]string{{"service", "stop"}, {"service", "reload"}, {"service", "start"}, {"service", "health"}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("service transaction mismatch: %#v", runner.commands)
	}
}

func TestRunRollsBackBothFilesWhenUpdatedServiceIsUnhealthy(t *testing.T) {
	options, runner, binaryPath, servicePath, identityPath := updateFixture(t, "0.1.5")
	runner.failCommand = "service health"
	if _, err := Run(context.Background(), options); err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("health failure did not fail update: %v", err)
	}
	if body, _ := os.ReadFile(binaryPath); string(body) != "old-binary" {
		t.Fatalf("executable rollback failed: %q", body)
	}
	if body, _ := os.ReadFile(servicePath); string(body) != "old-service" {
		t.Fatalf("service rollback failed: %q", body)
	}
	if body, _ := os.ReadFile(identityPath); string(body) != "private-identity" {
		t.Fatalf("state changed during rollback: %q", body)
	}
}

func TestRunCheckDoesNotStopOrWrite(t *testing.T) {
	options, runner, binaryPath, _, _ := updateFixture(t, "0.1.5")
	options.CheckOnly = true
	result, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.Updated || len(runner.commands) != 0 {
		t.Fatalf("check mutated the installation: %#v %#v", result, runner.commands)
	}
	if body, _ := os.ReadFile(binaryPath); string(body) != "old-binary" {
		t.Fatal("check replaced the executable")
	}
}

func TestRunDoesNothingWhenCurrentReleaseIsNotNewer(t *testing.T) {
	options, runner, binaryPath, _, _ := updateFixture(t, "0.1.4")
	result, err := Run(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdateAvailable || result.Updated || len(runner.commands) != 0 {
		t.Fatalf("current installation was modified: %#v %#v", result, runner.commands)
	}
	if body, _ := os.ReadFile(binaryPath); string(body) != "old-binary" {
		t.Fatal("current executable was replaced")
	}
}

func TestRunRefusesCrossOriginCurrentManifest(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer evil.Close()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(currentRelease{Version: "0.1.5", ProtocolMajor: 1, ManifestURL: evil.URL + "/manifest.json"})
	}))
	defer server.Close()
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"endpoint":`+stringMustMarshal(t, server.URL)+`,"host":{"type":"server","id":1},"stateDirectory":`+stringMustMarshal(t, directory)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Options{
		ConfigPath: configPath, CurrentVersion: "0.1.4", CheckOnly: true,
		EffectiveUserID: func() int { return 0 }, platform: &platform{}, runner: &fakeRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "crossed") {
		t.Fatalf("cross-origin release was accepted: %v", err)
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		first, second string
		want          int
	}{
		{"0.1.4", "0.1.5", -1}, {"0.2.0", "0.1.9", 1}, {"1.0.0", "1.0.0", 0},
		{"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1},
	}
	for _, test := range cases {
		got, err := compareVersions(test.first, test.second)
		if err != nil || got != test.want {
			t.Fatalf("compare %s %s = %d, %v; want %d", test.first, test.second, got, err, test.want)
		}
	}
}
