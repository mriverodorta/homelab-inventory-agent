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
	if first != "6991de825d245d5906d64a137f51fd52ed820c97c5f093a0935434a0130c06ec" {
		t.Fatalf("canonical bundle changed without updating the pinned application contract: %q", first)
	}
}
