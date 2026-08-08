package services

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	name      string
	arguments []string
	output    []byte
	err       error
	responses map[string]struct {
		output []byte
		err    error
	}
}

func (runner *fakeRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	runner.name = name
	runner.arguments = append([]string(nil), arguments...)
	if runner.responses != nil {
		response, found := runner.responses[strings.Join(append([]string{name}, arguments...), " ")]
		if !found {
			return nil, errors.New("unexpected command")
		}
		return response.output, response.err
	}
	return runner.output, runner.err
}

func TestSystemdUsesFixedArgumentsAndNormalizesUnits(t *testing.T) {
	runner := &fakeRunner{output: []byte("Id=docker.service\nDescription=Docker\nActiveState=active\nSubState=running\nUnitFileState=enabled\nMemoryCurrent=1024\nCPUUsageNSec=1000000000\nNRestarts=2\nTasksCurrent=3\nTasksMax=100\nResult=success\n\nId=ssh.service\nDescription=SSH\nActiveState=inactive\nSubState=dead\nUnitFileState=disabled\nCPUUsageNSec=0\n\n")}
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	collector := &Systemd{runner: runner, timeout: 1e9, now: func() time.Time { return now }, previousCPU: map[string]uint64{}}
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "systemctl" || !reflect.DeepEqual(runner.arguments, []string{"show", "--type=service", "--all", "--no-pager", "--property=Id,Description,ActiveState,SubState,UnitFileState,FragmentPath,MemoryCurrent,MemoryPeak,CPUUsageNSec,NRestarts,TasksCurrent,TasksMax,Result,ActiveEnterTimestamp,InactiveEnterTimestamp"}) {
		t.Fatalf("unsafe command invocation: %s %#v", runner.name, runner.arguments)
	}
	if len(result) != 2 || result[0].Name != "docker" || result[0].ActiveState != "active" || result[0].Enabled == nil || !*result[0].Enabled || result[0].MemoryCurrent == nil || *result[0].MemoryCurrent != 1024 {
		t.Fatalf("unexpected services: %#v", result)
	}
	now = now.Add(10 * time.Minute)
	runner.output = []byte("Id=docker.service\nDescription=Docker\nActiveState=active\nSubState=running\nCPUUsageNSec=61000000000\n\n")
	second, err := collector.Collect(context.Background())
	if err != nil || second[0].CPUPercent == nil || *second[0].CPUPercent != 10 {
		t.Fatalf("service CPU interval mismatch: %#v %v", second, err)
	}
}

func TestSystemdClassifiesLocalManualAndBaseServices(t *testing.T) {
	property := "--property=Id,Description,ActiveState,SubState,UnitFileState,FragmentPath,MemoryCurrent,MemoryPeak,CPUUsageNSec,NRestarts,TasksCurrent,TasksMax,Result,ActiveEnterTimestamp,InactiveEnterTimestamp"
	runner := &fakeRunner{responses: map[string]struct {
		output []byte
		err    error
	}{
		"systemctl show --type=service --all --no-pager " + property: {output: []byte(
			"Id=custom.service\nActiveState=active\nFragmentPath=/etc/systemd/system/custom.service\n\n" +
				"Id=docker.service\nActiveState=active\nFragmentPath=/lib/systemd/system/docker.service\n\n" +
				"Id=cron.service\nActiveState=active\nFragmentPath=/lib/systemd/system/cron.service\n\n" +
				"Id=generated.service\nActiveState=active\nFragmentPath=/run/systemd/generator/generated.service\n\n")},
		"/usr/bin/apt-mark showmanual": {output: []byte("docker-ce\n")},
		"/usr/bin/dpkg-query --search /lib/systemd/system/docker.service /lib/systemd/system/cron.service": {output: []byte(
			"docker-ce: /lib/systemd/system/docker.service\ncron: /lib/systemd/system/cron.service\n")},
	}}
	collector := &Systemd{runner: runner, timeout: time.Second, now: time.Now, previousCPU: map[string]uint64{}}
	services, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	classifications := map[string]string{}
	for _, service := range services {
		classifications[service.Name] = service.Classification
	}
	if classifications["custom"] != "user-installed" || classifications["docker"] != "user-installed" || classifications["cron"] != "system" || classifications["generated"] != "unknown" {
		t.Fatalf("unexpected classifications: %#v", classifications)
	}
}

func TestSystemdClassificationDegradesPackageLookupFailure(t *testing.T) {
	units := []map[string]string{{"Id": "docker.service", "FragmentPath": "/lib/systemd/system/docker.service"}}
	runner := &fakeRunner{responses: map[string]struct {
		output []byte
		err    error
	}{"/usr/bin/apt-mark showmanual": {err: errors.New("not installed")}}}
	if got := classifySystemdUnits(context.Background(), runner, units)["docker"]; got != "unknown" {
		t.Fatalf("package lookup failure was guessed as %q", got)
	}
}

func TestSystemdFailsClosedOnUnknownOrUnavailableOutput(t *testing.T) {
	collector := NewSystemd()
	collector.runner = &fakeRunner{err: errors.New("not installed")}
	if _, err := collector.Collect(context.Background()); err == nil {
		t.Fatal("missing systemd was not reported")
	}
}
