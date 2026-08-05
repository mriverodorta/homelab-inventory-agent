package command

import (
	"context"
	"strings"
	"testing"
)

func TestExecBoundsOutputWithoutShell(t *testing.T) {
	runner := Exec{MaxOutput: 4}
	if _, err := runner.Run(context.Background(), "printf", "%s", "12345"); err == nil {
		t.Fatal("oversized command output was accepted")
	}
	output, err := (Exec{MaxOutput: 16}).Run(context.Background(), "printf", "%s", "safe")
	if err != nil || strings.TrimSpace(string(output)) != "safe" {
		t.Fatalf("fixed command failed: %q %v", output, err)
	}
}
