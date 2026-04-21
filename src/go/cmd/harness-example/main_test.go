package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_DryRun exercises the full demo path without network access.
// It asserts:
//   - run() returns nil (successful exit)
//   - final output contains the expected assistant text
//   - hook log captures RunStart and ToolCall at least twice
func TestRun_DryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), &stdout, &stderr, []string{
		"--dry-run",
		"--verbose",
	})
	require.NoError(t, err)

	out := stdout.String()
	errOut := stderr.String()

	// The dry-run fake client returns this text on the third call.
	assert.Contains(t, out, "README.md", "final assistant text should mention README.md")
	assert.Contains(t, out, "numbers.txt", "final assistant text should mention numbers.txt")

	// In verbose mode hook logs go to stderr.
	// RunStart must appear at least once, ToolCall at least twice
	// (once for ls /data, once for cat /data/numbers.txt).
	assert.Contains(t, errOut, string(hookRunStart),
		"verbose hook log must include run_start")

	toolCallCount := strings.Count(errOut, string(hookToolCall))
	assert.GreaterOrEqual(t, toolCallCount, 2,
		"tool_call hook should fire at least twice (ls + cat)")
}

// TestRun_DryRun_NoVerbose verifies that without --verbose the hook log
// is suppressed and the result is still printed.
func TestRun_DryRun_NoVerbose(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), &stdout, &stderr, []string{"--dry-run"})
	require.NoError(t, err)

	out := stdout.String()
	assert.NotEmpty(t, out, "stdout must contain the final assistant reply")
	// Without verbose, stderr should be empty (no hook log).
	assert.Empty(t, stderr.String(), "stderr must be empty without --verbose")
}
