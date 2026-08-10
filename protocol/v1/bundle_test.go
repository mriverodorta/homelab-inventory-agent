package protocol

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestEmbeddedSchemasAreValidAndDigestIsStable(t *testing.T) {
	schemas, err := Schemas()
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 9 {
		t.Fatalf("expected 9 schemas, got %d", len(schemas))
	}
	for _, schema := range schemas {
		var document map[string]any
		if err := json.Unmarshal(schema.Content, &document); err != nil {
			t.Fatalf("schema %s is invalid JSON: %v", schema.Name, err)
		}
		if document["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("schema %s does not declare draft 2020-12", schema.Name)
		}
	}
	first, err := BundleDigest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BundleDigest()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(first) {
		t.Fatalf("bundle digest is not deterministic: %q / %q", first, second)
	}
	if first != "97ea85ea215e8d35d2cf8c70c24d715d79e092391dd57f70b6b54ef9717e7495" {
		t.Fatalf("canonical bundle changed without updating the pinned application contract: %q", first)
	}
}

func TestBundleCompatibilityAllowsStaggeredApplicationAndAgentUpgrades(t *testing.T) {
	current, err := BundleDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{current, LegacyBundleDigest} {
		compatible, checkErr := IsCompatibleBundleDigest(digest)
		if checkErr != nil || !compatible {
			t.Fatalf("compatible digest %q rejected: %v", digest, checkErr)
		}
	}
	compatible, err := IsCompatibleBundleDigest("0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil || compatible {
		t.Fatalf("unsupported digest accepted: compatible=%v err=%v", compatible, err)
	}
	monitoring, err := SupportsMonitoringPolicy(current)
	if err != nil || !monitoring {
		t.Fatalf("current digest did not enable monitoring policy: %v", err)
	}
	monitoring, err = SupportsMonitoringPolicy(LegacyBundleDigest)
	if err != nil || monitoring {
		t.Fatalf("legacy digest enabled monitoring acknowledgement: %v", err)
	}
}
