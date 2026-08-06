package services

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type rcFixtureRunner struct {
	responses map[string]struct {
		body []byte
		err  error
	}
	calls []string
}

func (runner *rcFixtureRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, arguments...), " ")
	runner.calls = append(runner.calls, key)
	response, found := runner.responses[key]
	if !found {
		return nil, errors.New("unexpected command: " + key)
	}
	return response.body, response.err
}

func TestRCdCollectsEnabledServicesWithoutProcessInspection(t *testing.T) {
	runner := &rcFixtureRunner{responses: map[string]struct {
		body []byte
		err  error
	}{
		"/usr/sbin/service -e":                          {body: []byte("/usr/local/etc/rc.d/dnsmasq\n/usr/local/etc/rc.d/crowdsec_firewall\n")},
		"/usr/sbin/service dnsmasq onestatus":           {body: []byte("dnsmasq is running as pid 71349.\n")},
		"/usr/sbin/service crowdsec_firewall onestatus": {body: []byte("crowdsec_firewall is not running.\n"), err: errors.New("exit status 1")},
	}}
	collector := NewRCdWithRunner(runner)
	services, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 || services[0].Name != "crowdsec_firewall" || services[0].ActiveState != "inactive" || services[1].Name != "dnsmasq" || services[1].ActiveState != "active" {
		t.Fatalf("unexpected rc.d services: %#v", services)
	}
	for _, service := range services {
		if service.MemoryCurrent != nil || service.CPUPercent != nil || service.TaskCount != nil {
			t.Fatalf("hidden process resources were overstated: %#v", service)
		}
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "ps ") || strings.Contains(call, "procstat") || strings.Contains(call, "sockstat") {
			t.Fatalf("rc.d collector inspected processes: %s", call)
		}
	}
}

func TestRCdRejectsUnsafeOrExcessiveServiceListings(t *testing.T) {
	unsafe := &rcFixtureRunner{responses: map[string]struct {
		body []byte
		err  error
	}{"/usr/sbin/service -e": {body: []byte("/usr/local/etc/rc.d/../../configctl\n")}}}
	if _, err := NewRCdWithRunner(unsafe).Collect(context.Background()); err == nil {
		t.Fatal("unsafe rc.d service path was accepted")
	}

	var listing strings.Builder
	for index := 0; index < 513; index++ {
		listing.WriteString("/etc/rc.d/service")
		listing.WriteString(string(rune('a' + index%26)))
		listing.WriteByte('\n')
	}
	excessive := &rcFixtureRunner{responses: map[string]struct {
		body []byte
		err  error
	}{"/usr/sbin/service -e": {body: []byte(listing.String())}}}
	if _, err := NewRCdWithRunner(excessive).Collect(context.Background()); err == nil {
		t.Fatal("excessive rc.d service count was accepted")
	}
}
