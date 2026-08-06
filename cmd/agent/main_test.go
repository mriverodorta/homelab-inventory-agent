package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionDoesNotRequireConfiguration(t *testing.T) {
	if err := run(context.Background(), []string{"-version"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentCodeSourcesAreExclusiveAndBounded(t *testing.T) {
	if err := run(context.Background(), []string{"-enrollment-code", "one", "-enrollment-code-file", "/tmp/other"}); err == nil {
		t.Fatal("multiple enrollment-code sources were accepted")
	}
	file := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(file, make([]byte, 513), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-enrollment-code-file", file}); err == nil {
		t.Fatal("oversized enrollment-code file was accepted")
	}
}
