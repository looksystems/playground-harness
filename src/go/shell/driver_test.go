package shell_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/shell"
	"agent-harness/go/shell/vfs"
)

// ---------------------------------------------------------------------------
// ExecResult
// ---------------------------------------------------------------------------

func TestExecResult_ZeroValue(t *testing.T) {
	var r shell.ExecResult
	assert.Equal(t, "", r.Stdout)
	assert.Equal(t, "", r.Stderr)
	assert.Equal(t, 0, r.ExitCode)
}

func TestExecResult_LiteralValues(t *testing.T) {
	r := shell.ExecResult{Stdout: "hello\n", Stderr: "err\n", ExitCode: 42}
	assert.Equal(t, "hello\n", r.Stdout)
	assert.Equal(t, "err\n", r.Stderr)
	assert.Equal(t, 42, r.ExitCode)
}

// ---------------------------------------------------------------------------
// StreamKind constants
// ---------------------------------------------------------------------------

func TestStreamKind_DistinctValues(t *testing.T) {
	kinds := []shell.StreamKind{shell.StreamStdout, shell.StreamStderr, shell.StreamExit}
	seen := make(map[shell.StreamKind]bool)
	for _, k := range kinds {
		require.False(t, seen[k], "duplicate StreamKind value: %d", k)
		seen[k] = true
	}
}

func TestStreamKind_StdoutIsZero(t *testing.T) {
	assert.Equal(t, shell.StreamKind(0), shell.StreamStdout)
}

// ---------------------------------------------------------------------------
// ExecStreamEvent
// ---------------------------------------------------------------------------

func TestExecStreamEvent_Fields(t *testing.T) {
	ev := shell.ExecStreamEvent{Kind: shell.StreamStdout, Data: "chunk"}
	assert.Equal(t, shell.StreamStdout, ev.Kind)
	assert.Equal(t, "chunk", ev.Data)
	assert.Equal(t, 0, ev.ExitCode)

	exit := shell.ExecStreamEvent{Kind: shell.StreamExit, ExitCode: 1}
	assert.Equal(t, shell.StreamExit, exit.Kind)
	assert.Equal(t, 1, exit.ExitCode)
}

// ---------------------------------------------------------------------------
// Capability constants
// ---------------------------------------------------------------------------

func TestCapabilityConstants(t *testing.T) {
	caps := []string{
		shell.CapCustomCommands,
		shell.CapStateful,
		shell.CapStreaming,
		shell.CapPolicies,
		shell.CapRemote,
	}
	seen := make(map[string]bool)
	for _, c := range caps {
		assert.NotEmpty(t, c)
		require.False(t, seen[c], "duplicate capability constant: %s", c)
		seen[c] = true
	}
}

// ---------------------------------------------------------------------------
// ErrStreamingUnsupported sentinel
// ---------------------------------------------------------------------------

func TestErrStreamingUnsupported_Sentinel(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", shell.ErrStreamingUnsupported)
	assert.True(t, errors.Is(err, shell.ErrStreamingUnsupported))
}

// ---------------------------------------------------------------------------
// Driver interface compile-check via stub
// ---------------------------------------------------------------------------

// stubDriver satisfies the Driver interface for compile-time verification.
type stubDriver struct {
	fs  vfs.FilesystemDriver
	env map[string]string
}

func (s *stubDriver) FS() vfs.FilesystemDriver { return s.fs }
func (s *stubDriver) CWD() string              { return "/" }
func (s *stubDriver) Env() map[string]string   { return copyMap(s.env) }
func (s *stubDriver) Exec(_ context.Context, _ string) (shell.ExecResult, error) {
	return shell.ExecResult{}, nil
}
func (s *stubDriver) ExecStream(_ context.Context, _ string) (<-chan shell.ExecStreamEvent, error) {
	return nil, shell.ErrStreamingUnsupported
}
func (s *stubDriver) RegisterCommand(_ string, _ shell.CmdHandler)  {}
func (s *stubDriver) UnregisterCommand(_ string)                    {}
func (s *stubDriver) Clone() shell.Driver                           { return &stubDriver{fs: s.fs} }
func (s *stubDriver) NotFoundHandler() shell.NotFoundHandler        { return nil }
func (s *stubDriver) SetNotFoundHandler(_ shell.NotFoundHandler)    {}
func (s *stubDriver) Capabilities() map[string]bool                 { return map[string]bool{} }

// Compile-time assertion that *stubDriver implements Driver.
var _ shell.Driver = (*stubDriver)(nil)

func TestDriverInterface_CompileCheck(t *testing.T) {
	d := &stubDriver{fs: vfs.NewBuiltinFilesystemDriver(), env: map[string]string{"HOME": "/root"}}
	assert.Equal(t, "/", d.CWD())
	assert.Equal(t, "/root", d.Env()["HOME"])
}

func TestDriverInterface_ExecStreamReturnsUnsupported(t *testing.T) {
	d := &stubDriver{}
	ch, err := d.ExecStream(context.Background(), "ls")
	assert.Nil(t, ch)
	assert.ErrorIs(t, err, shell.ErrStreamingUnsupported)
}

func TestCmdHandler_CallSignature(t *testing.T) {
	var called bool
	handler := shell.CmdHandler(func(args []string, stdin string) shell.ExecResult {
		called = true
		return shell.ExecResult{Stdout: "ok", ExitCode: 0}
	})
	result := handler([]string{"arg1"}, "")
	assert.True(t, called)
	assert.Equal(t, "ok", result.Stdout)
}

func TestNotFoundHandler_NilFallthrough(t *testing.T) {
	d := &stubDriver{}
	assert.Nil(t, d.NotFoundHandler())
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
