package packaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(body)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "..", ".."))
}

func run(t *testing.T, directory string, environment []string, name string, arguments ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	return command.CombinedOutput()
}

func TestReleaseBuildAndRootedInstaller(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets)
	if err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", rebuilt); err != nil {
		t.Fatalf("repeat release build: %s: %v", output, err)
	}
	for _, filename := range []string{
		"homelab-inventory-agent-linux-amd64", "homelab-inventory-agent-linux-arm64", "homelab-inventory-agent-freebsd-amd64",
		"homelab-inventory-agent.service", "homelab_inventory_agent", "install.sh", "install-freebsd.sh",
		"uninstall.sh", "uninstall-freebsd.sh", "version.txt", "checksums.txt", "manifest.json",
	} {
		if fileDigest(t, filepath.Join(assets, filename)) != fileDigest(t, filepath.Join(rebuilt, filename)) {
			t.Fatalf("release artifact %s is not reproducible", filename)
		}
	}
	for _, schema := range []string{"activation", "common", "container", "contract", "hardware-snapshot", "heartbeat", "metrics", "service", "storage-health"} {
		filename := filepath.Join("schemas", schema+".schema.json")
		if fileDigest(t, filepath.Join(assets, filename)) != fileDigest(t, filepath.Join(rebuilt, filename)) {
			t.Fatalf("release schema %s is not reproducible", filename)
		}
	}
	manifestBody, err := os.ReadFile(filepath.Join(assets, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var releaseManifest struct {
		Version        string `json:"version"`
		SourceRevision string `json:"sourceRevision"`
		ProtocolMajor  int    `json:"protocolMajor"`
		Assets         []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(manifestBody, &releaseManifest); err != nil {
		t.Fatal(err)
	}
	if releaseManifest.Version != "0.1.0-test" || releaseManifest.SourceRevision == "" || releaseManifest.ProtocolMajor != 1 || len(releaseManifest.Assets) < 19 {
		t.Fatalf("invalid release manifest: %#v", releaseManifest)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
	}
	output, err = run(t, root, environment, "sh", "packaging/install.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "one-time-code", "--version", "0.1.0-test",
		"--containers-mode", "proxy", "--containers-runtime", "docker", "--containers-endpoint", "http://127.0.0.1:2375")
	if err != nil {
		t.Fatalf("install release: %s: %v", output, err)
	}
	binary := filepath.Join(installRoot, "usr/local/sbin/homelab-inventory-agent")
	body, err := os.ReadFile(binary)
	if err != nil || len(body) < 1024 {
		t.Fatalf("installed binary mismatch: %d bytes %v", len(body), err)
	}
	config, err := os.ReadFile(filepath.Join(installRoot, "etc/homelab-inventory-agent/config.json"))
	if err != nil || !bytes.Contains(config, []byte(`"id":7`)) || bytes.Contains(config, []byte("one-time-code")) {
		t.Fatalf("installed configuration is unsafe: %s %v", config, err)
	}
	if !bytes.Contains(config, []byte(`"containers":{"mode":"proxy","runtime":"docker","endpoint":"http://127.0.0.1:2375"}`)) {
		t.Fatalf("container opt-in was not persisted: %s", config)
	}
	if _, err := os.Stat(filepath.Join(installRoot, "etc/systemd/system/homelab-inventory-agent.service")); err != nil {
		t.Fatalf("systemd unit missing: %v", err)
	}
	service, err := os.ReadFile(filepath.Join(installRoot, "etc/systemd/system/homelab-inventory-agent.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("User=homelab-inventory-agent"),
		[]byte("NoNewPrivileges=true"),
		[]byte("CapabilityBoundingSet=\n"),
		[]byte("ReadWritePaths=/var/lib/homelab-inventory-agent"),
	} {
		if !bytes.Contains(service, required) {
			t.Fatalf("systemd unit is missing %q", required)
		}
	}
}

func TestFreeBSDInstallerUsesPersistentOPNsenseIdentityAndUnprivilegedRCd(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=freebsd",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_OPNSENSE=1",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
	}
	output, err := run(t, root, environment, "sh", "packaging/install-freebsd.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "one-time-code", "--version", "0.1.0-test")
	if err != nil {
		t.Fatalf("install FreeBSD release: %s: %v", output, err)
	}
	config, err := os.ReadFile(filepath.Join(installRoot, "usr/local/etc/homelab-inventory-agent/config.json"))
	if err != nil || !bytes.Contains(config, []byte(`"stateDirectory":"/conf/homelab-inventory-agent"`)) || bytes.Contains(config, []byte("one-time-code")) {
		t.Fatalf("OPNsense configuration is unsafe: %s %v", config, err)
	}
	service, err := os.ReadFile(filepath.Join(installRoot, "usr/local/etc/rc.d/homelab_inventory_agent"))
	if err != nil || !bytes.Contains(service, []byte("-u homelab-inventory-agent")) || bytes.Contains(service, []byte("configctl")) || bytes.Contains(service, []byte("/conf/config.xml")) {
		t.Fatalf("OPNsense rc.d service is unsafe: %s %v", service, err)
	}
}

func TestInstallerRejectsTamperedAssets(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	service := filepath.Join(assets, "homelab-inventory-agent.service")
	if err := os.WriteFile(service, []byte("[Service]\nUser=root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + filepath.Join(t.TempDir(), "root"),
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
	}
	output, err := run(t, root, environment, "sh", "packaging/install.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "one-time-code", "--version", "0.1.0-test")
	if err == nil || !bytes.Contains(output, []byte("Checksum verification failed")) {
		t.Fatalf("tampered asset was accepted: %s: %v", output, err)
	}
}

func TestUpgradePreservesIdentityAndConfiguration(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	config := filepath.Join(installRoot, "etc/homelab-inventory-agent/config.json")
	identity := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/identity.json")
	for path, body := range map[string]string{
		config:   `{"endpoint":"https://inventory.example.com","host":{"type":"server","id":7}}`,
		identity: `{"privateKey":"do-not-replace"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install.sh",
		"--endpoint", "https://ignored.example.com", "--version", "0.1.0-test", "--upgrade"); err != nil {
		t.Fatalf("upgrade release: %s: %v", output, err)
	}
	for path, expected := range map[string]string{
		config:   `{"endpoint":"https://inventory.example.com","host":{"type":"server","id":7}}`,
		identity: `{"privateKey":"do-not-replace"}`,
	} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatalf("upgrade changed %s: %q %v", path, body, err)
		}
	}
}

func TestFreshSetupStagesExistingLinuxIdentityForReplacement(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	identity := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/identity.json")
	queuedHeartbeat := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/queue/pending.json")
	if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte(`{"deviceId":41,"privateKey":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(queuedHeartbeat), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queuedHeartbeat, []byte("stale-sequence"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "replacement-code", "--version", "0.1.0-test"); err != nil {
		t.Fatalf("fresh setup: %s: %v", output, err)
	}
	if _, err := os.Stat(identity); !os.IsNotExist(err) {
		t.Fatalf("fresh setup retained the stale identity: %v", err)
	}
	if _, err := os.Stat(queuedHeartbeat); !os.IsNotExist(err) {
		t.Fatalf("fresh setup retained identity-bound queue state: %v", err)
	}
}

func TestFreshSetupRollbackRestoresExistingLinuxIdentity(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	identity := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/identity.json")
	queuedHeartbeat := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/queue/pending.json")
	const previousIdentity = `{"deviceId":41,"privateKey":"restore-me"}`
	if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte(previousIdentity), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(queuedHeartbeat), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queuedHeartbeat, []byte("restore-sequence"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
		"HLI_TEST_FAIL_AFTER_INSTALL=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "replacement-code", "--version", "0.1.0-test"); err == nil {
		t.Fatalf("injected fresh setup failure succeeded: %s", output)
	}
	body, err := os.ReadFile(identity)
	if err != nil || string(body) != previousIdentity {
		t.Fatalf("rollback did not restore Linux identity: %q %v", body, err)
	}
	if info, err := os.Stat(identity); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rollback changed Linux identity permissions: %v %v", info, err)
	}
	if body, err := os.ReadFile(queuedHeartbeat); err != nil || string(body) != "restore-sequence" {
		t.Fatalf("rollback did not restore Linux queue state: %q %v", body, err)
	}
}

func TestUpgradeRollbackRestoresExistingFiles(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	binary := filepath.Join(installRoot, "usr/local/sbin/homelab-inventory-agent")
	config := filepath.Join(installRoot, "etc/homelab-inventory-agent/config.json")
	identity := filepath.Join(installRoot, "var/lib/homelab-inventory-agent/identity.json")
	service := filepath.Join(installRoot, "etc/systemd/system/homelab-inventory-agent.service")
	for _, path := range []string{binary, config, identity, service} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{binary: "previous-binary", config: "previous-config", identity: "identity", service: "previous-service"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=linux",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
		"HLI_TEST_FAIL_AFTER_INSTALL=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install.sh", "--endpoint", "https://inventory.example.com", "--version", "0.1.0-test", "--upgrade"); err == nil {
		t.Fatalf("injected upgrade failure succeeded: %s", output)
	}
	for path, expected := range map[string]string{binary: "previous-binary", config: "previous-config", service: "previous-service", identity: "identity"} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatalf("rollback changed %s: %q %v", path, body, err)
		}
	}
}

func TestFreeBSDUpgradeRollbackPreservesOPNsenseIdentity(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	binary := filepath.Join(installRoot, "usr/local/sbin/homelab-inventory-agent")
	config := filepath.Join(installRoot, "usr/local/etc/homelab-inventory-agent/config.json")
	identity := filepath.Join(installRoot, "conf/homelab-inventory-agent/identity.json")
	service := filepath.Join(installRoot, "usr/local/etc/rc.d/homelab_inventory_agent")
	for path, body := range map[string]string{
		binary: "previous-binary", config: "previous-config", identity: "opnsense-identity", service: "previous-service",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=freebsd",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_OPNSENSE=1",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
		"HLI_TEST_FAIL_AFTER_INSTALL=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install-freebsd.sh",
		"--endpoint", "https://inventory.example.com", "--version", "0.1.0-test", "--upgrade"); err == nil {
		t.Fatalf("injected FreeBSD upgrade failure succeeded: %s", output)
	}
	for path, expected := range map[string]string{
		binary: "previous-binary", config: "previous-config", identity: "opnsense-identity", service: "previous-service",
	} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatalf("FreeBSD rollback changed %s: %q %v", path, body, err)
		}
	}
}

func TestFreshSetupRollbackRestoresExistingOPNsenseIdentity(t *testing.T) {
	root := repositoryRoot(t)
	assets := filepath.Join(t.TempDir(), "assets")
	if output, err := run(t, root, nil, "sh", "scripts/build-release.sh", "0.1.0-test", assets); err != nil {
		t.Fatalf("build release: %s: %v", output, err)
	}
	installRoot := filepath.Join(t.TempDir(), "root")
	identity := filepath.Join(installRoot, "conf/homelab-inventory-agent/identity.json")
	queuedHeartbeat := filepath.Join(installRoot, "conf/homelab-inventory-agent/queue/pending.json")
	const previousIdentity = `{"deviceId":17,"privateKey":"opnsense-restore-me"}`
	if err := os.MkdirAll(filepath.Dir(identity), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte(previousIdentity), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(queuedHeartbeat), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queuedHeartbeat, []byte("opnsense-restore-sequence"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"HLI_INSTALL_ROOT=" + installRoot,
		"HLI_ASSET_DIR=" + assets,
		"HLI_TEST_OS=freebsd",
		"HLI_TEST_ARCH=amd64",
		"HLI_TEST_OPNSENSE=1",
		"HLI_TEST_SKIP_BINARY_EXEC=1",
		"HLI_TEST_FAIL_AFTER_INSTALL=1",
	}
	if output, err := run(t, root, environment, "sh", "packaging/install-freebsd.sh",
		"--endpoint", "https://inventory.example.com", "--host-type", "server", "--host-id", "7",
		"--enrollment-code", "replacement-code", "--version", "0.1.0-test"); err == nil {
		t.Fatalf("injected FreeBSD fresh setup failure succeeded: %s", output)
	}
	body, err := os.ReadFile(identity)
	if err != nil || string(body) != previousIdentity {
		t.Fatalf("rollback did not restore OPNsense identity: %q %v", body, err)
	}
	if info, err := os.Stat(identity); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("rollback changed OPNsense identity permissions: %v %v", info, err)
	}
	if body, err := os.ReadFile(queuedHeartbeat); err != nil || string(body) != "opnsense-restore-sequence" {
		t.Fatalf("rollback did not restore OPNsense queue state: %q %v", body, err)
	}
}
