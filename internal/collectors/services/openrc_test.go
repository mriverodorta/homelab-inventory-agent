//go:build linux

package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type openRCFakeRunner struct {
	body []byte
	err  error
}

func (runner openRCFakeRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return runner.body, runner.err
}

func openRCFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNewLinuxSelectsOneSupportedManager(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run/openrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection := NewLinux(root)
	if selection.Manager != "openrc" || selection.Detail != "OpenRC" {
		t.Fatalf("OpenRC selection mismatch: %#v", selection)
	}
	if err := os.MkdirAll(filepath.Join(root, "run/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection = NewLinux(root)
	if selection.Manager != "" {
		t.Fatalf("ambiguous manager selection did not fail closed: %#v", selection)
	}
}

func TestOpenRCCollectsBoundedServiceFacts(t *testing.T) {
	root := t.TempDir()
	openRCFixture(t, root, "sbin/apk", "")
	openRCFixture(t, root, "etc/apk/world", "docker\n")
	openRCFixture(t, root, "etc/init.d/docker", "#!/sbin/openrc-run\ndescription=\"Docker daemon\"\n")
	openRCFixture(t, root, "etc/init.d/local-task", "#!/sbin/openrc-run\n")
	openRCFixture(t, root, "etc/init.d/sysctl", "#!/sbin/openrc-run\n")
	openRCFixture(t, root, "run/openrc/started/docker", "")
	openRCFixture(t, root, "run/openrc/failed/local-task", "")
	openRCFixture(t, root, "etc/runlevels/default/docker", "")
	collector := NewOpenRC(root)
	collector.runner = openRCFakeRunner{body: []byte("/etc/init.d/docker is owned by docker-28.0.0-r0\n/etc/init.d/sysctl is owned by openrc-0.62.6-r0\n")}
	services, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 3 {
		t.Fatalf("service count mismatch: %#v", services)
	}
	byName := map[string]int{}
	for index, service := range services {
		byName[service.Name] = index
		if service.Manager != "openrc" || service.MemoryCurrent != nil || service.CPUPercent != nil || service.RestartCount != nil {
			t.Fatalf("OpenRC service fabricated unsupported metrics: %#v", service)
		}
	}
	docker := services[byName["docker"]]
	if docker.ActiveState != "active" || docker.SubState != "started" || docker.Enabled == nil || !*docker.Enabled || docker.Classification != "user-installed" || docker.Description != "Docker daemon" {
		t.Fatalf("Docker service mismatch: %#v", docker)
	}
	local := services[byName["local-task"]]
	if local.ActiveState != "failed" || local.Classification != "user-installed" {
		t.Fatalf("local service mismatch: %#v", local)
	}
	if services[byName["sysctl"]].Classification != "system" {
		t.Fatalf("system service mismatch: %#v", services[byName["sysctl"]])
	}
}

func TestOpenRCMissingAPKLeavesClassificationUnknown(t *testing.T) {
	root := t.TempDir()
	openRCFixture(t, root, "etc/init.d/example", "#!/sbin/openrc-run\n")
	collector := NewOpenRC(root)
	collector.runner = openRCFakeRunner{err: errors.New("must not run")}
	services, err := collector.Collect(context.Background())
	if err != nil || len(services) != 1 || services[0].Classification != "unknown" {
		t.Fatalf("missing APK must remain non-fatal and unknown: services=%#v err=%v", services, err)
	}
}
