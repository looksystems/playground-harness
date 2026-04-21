package shell_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/hooks"
	"agent-harness/go/shell"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/shell/vfs"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForCount polls f until it returns >= target or the timeout elapses.
// Used to assert that EmitAsync fire-and-forget handlers have run.
func waitForCount(t *testing.T, f func() int64, target int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f() >= target {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected count >= %d, got %d", target, f())
}

// eventSpy records every argument list delivered for a given event.
type eventSpy struct {
	mu    sync.Mutex
	fired []([]any)
}

func (s *eventSpy) handler() hooks.Handler {
	return func(_ context.Context, args ...any) {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Deep-copy the args so later mutation doesn't affect what we saw.
		copied := make([]any, len(args))
		copy(copied, args)
		s.fired = append(s.fired, copied)
	}
}

func (s *eventSpy) count() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.fired))
}

func (s *eventSpy) snapshot() [][]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]any, len(s.fired))
	copy(out, s.fired)
	return out
}

// ---------------------------------------------------------------------------
// NewHost / construction
// ---------------------------------------------------------------------------

func TestNewHost_NilDriver_UsesBuiltinDefault(t *testing.T) {
	h := shell.NewHost(nil)
	require.NotNil(t, h, "NewHost must not return nil")
	require.NotNil(t, h.Driver, "NewHost(nil) must install a default builtin driver")

	// Prove it actually works — echo is a builtin command.
	res, err := h.Exec(context.Background(), "echo hi")
	require.NoError(t, err)
	assert.Equal(t, "hi\n", res.Stdout)
	assert.Equal(t, 0, res.ExitCode)
}

func TestNewHost_CustomDriver_Wrapped(t *testing.T) {
	d := builtin.NewBuiltinShellDriver(builtin.WithCWD("/tmp"))
	h := shell.NewHost(d)
	assert.Same(t, d, h.Driver)
	assert.Equal(t, "/tmp", h.Driver.CWD())
}

// ---------------------------------------------------------------------------
// Exec delegation + hook emission
// ---------------------------------------------------------------------------

func TestHost_Exec_DelegatesToDriver(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)

	res, err := h.Exec(context.Background(), "echo hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", res.Stdout)
}

func TestHost_Exec_EmitsShellCallAndResult(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	callSpy := &eventSpy{}
	resultSpy := &eventSpy{}
	hub.On(hooks.ShellCall, callSpy.handler())
	hub.On(hooks.ShellResult, resultSpy.handler())

	res, err := h.Exec(context.Background(), "echo fired")
	require.NoError(t, err)
	assert.Equal(t, "fired\n", res.Stdout)

	waitForCount(t, callSpy.count, 1)
	waitForCount(t, resultSpy.count, 1)

	callArgs := callSpy.snapshot()[0]
	require.Len(t, callArgs, 1)
	assert.Equal(t, "echo fired", callArgs[0])

	resultArgs := resultSpy.snapshot()[0]
	require.Len(t, resultArgs, 2)
	assert.Equal(t, "echo fired", resultArgs[0])
	gotResult, ok := resultArgs[1].(shell.ExecResult)
	require.True(t, ok, "second ShellResult arg must be ExecResult")
	assert.Equal(t, "fired\n", gotResult.Stdout)
}

func TestHost_Exec_EmitsShellCwdOnCdChange(t *testing.T) {
	d := builtin.NewBuiltinShellDriver(builtin.WithCWD("/"))
	// Make /tmp exist so cd can succeed.
	mkdir, err := d.Exec(context.Background(), "mkdir /tmp")
	require.NoError(t, err)
	require.Equal(t, 0, mkdir.ExitCode, "mkdir failed: %q", mkdir.Stderr)

	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	cwdSpy := &eventSpy{}
	hub.On(hooks.ShellCwd, cwdSpy.handler())

	res, err := h.Exec(context.Background(), "cd /tmp")
	require.NoError(t, err)
	require.Equal(t, 0, res.ExitCode, "cd should succeed; stderr=%q", res.Stderr)

	waitForCount(t, cwdSpy.count, 1)
	args := cwdSpy.snapshot()[0]
	require.Len(t, args, 2)
	assert.Equal(t, "/", args[0])
	assert.Equal(t, "/tmp", args[1])
}

func TestHost_Exec_NoShellCwdWhenCwdUnchanged(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	cwdSpy := &eventSpy{}
	hub.On(hooks.ShellCwd, cwdSpy.handler())

	_, err := h.Exec(context.Background(), "echo stay")
	require.NoError(t, err)

	// Give any fire-and-forget handlers a chance to run; assert none did.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(0), cwdSpy.count())
}

func TestHost_Exec_NilHub_DoesNotPanic(t *testing.T) {
	h := shell.NewHost(builtin.NewBuiltinShellDriver())
	// No SetHub call — Hub is nil.
	res, err := h.Exec(context.Background(), "echo ok")
	require.NoError(t, err)
	assert.Equal(t, "ok\n", res.Stdout)
}

// ---------------------------------------------------------------------------
// RegisterCommand / UnregisterCommand emit hooks
// ---------------------------------------------------------------------------

func TestHost_RegisterCommand_EmitsHookAndIsExecutable(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	regSpy := &eventSpy{}
	hub.On(hooks.CommandRegister, regSpy.handler())

	h.RegisterCommand("hello", func(_ []string, _ string) shell.ExecResult {
		return shell.ExecResult{Stdout: "world\n", ExitCode: 0}
	})

	waitForCount(t, regSpy.count, 1)
	args := regSpy.snapshot()[0]
	require.Len(t, args, 1)
	assert.Equal(t, "hello", args[0])

	// The registered command must run.
	res, err := h.Exec(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, "world\n", res.Stdout)
}

func TestHost_UnregisterCommand_EmitsHook(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	// Register then unregister.
	h.RegisterCommand("goodbye", func(_ []string, _ string) shell.ExecResult {
		return shell.ExecResult{Stdout: "bye\n"}
	})

	unregSpy := &eventSpy{}
	hub.On(hooks.CommandUnregister, unregSpy.handler())

	h.UnregisterCommand("goodbye")
	waitForCount(t, unregSpy.count, 1)
	args := unregSpy.snapshot()[0]
	require.Len(t, args, 1)
	assert.Equal(t, "goodbye", args[0])
}

// ---------------------------------------------------------------------------
// NotFoundHandler hook wiring
// ---------------------------------------------------------------------------

func TestHost_NotFoundHandler_EmitsShellNotFound(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	h := shell.NewHost(d)
	hub := hooks.NewHub()
	h.SetHub(hub)

	nfSpy := &eventSpy{}
	hub.On(hooks.ShellNotFound, nfSpy.handler())

	res, err := h.Exec(context.Background(), "definitely_not_a_real_command")
	require.NoError(t, err)
	// Default driver behaviour: exit 127, stderr like "<cmd>: command not found".
	assert.NotEqual(t, 0, res.ExitCode)

	waitForCount(t, nfSpy.count, 1)
	args := nfSpy.snapshot()[0]
	require.GreaterOrEqual(t, len(args), 1)
	assert.Equal(t, "definitely_not_a_real_command", args[0])
}

// ---------------------------------------------------------------------------
// ShellTool — exec tool exposed to the LLM
// ---------------------------------------------------------------------------

// mockDriver is a minimal Driver used to exercise ShellTool formatting
// branches without relying on a real shell.
type mockDriver struct {
	result shell.ExecResult
}

func (m *mockDriver) FS() vfs.FilesystemDriver { return vfs.NewBuiltinFilesystemDriver() }
func (m *mockDriver) CWD() string              { return "/" }
func (m *mockDriver) Env() map[string]string   { return map[string]string{} }
func (m *mockDriver) Exec(_ context.Context, _ string) (shell.ExecResult, error) {
	return m.result, nil
}
func (m *mockDriver) ExecStream(_ context.Context, _ string) (<-chan shell.ExecStreamEvent, error) {
	return nil, shell.ErrStreamingUnsupported
}
func (m *mockDriver) RegisterCommand(_ string, _ shell.CmdHandler) {}
func (m *mockDriver) UnregisterCommand(_ string)                   {}
func (m *mockDriver) Clone() shell.Driver                          { return &mockDriver{result: m.result} }
func (m *mockDriver) NotFoundHandler() shell.NotFoundHandler       { return nil }
func (m *mockDriver) SetNotFoundHandler(_ shell.NotFoundHandler)   {}
func (m *mockDriver) Capabilities() map[string]bool                { return map[string]bool{} }

func TestHost_ShellTool_Schema(t *testing.T) {
	h := shell.NewHost(&mockDriver{})
	def := h.ShellTool()

	assert.Equal(t, "exec", def.Name)
	assert.NotEmpty(t, def.Description)
	// Description should enumerate at least the canonical commands & operators.
	assert.Contains(t, def.Description, "ls")
	assert.Contains(t, def.Description, "pipes")

	require.NotNil(t, def.Parameters)
	assert.Equal(t, "object", def.Parameters["type"])
	props, ok := def.Parameters["properties"].(map[string]any)
	require.True(t, ok)
	cmd, ok := props["command"].(map[string]any)
	require.True(t, ok, "properties.command must be present")
	assert.Equal(t, "string", cmd["type"])

	req, ok := def.Parameters["required"].([]string)
	require.True(t, ok, "required must be []string")
	assert.Equal(t, []string{"command"}, req)
}

// shellToolArgs mirrors Python exec args shape.
func runShellTool(t *testing.T, h *shell.Host, command string) string {
	t.Helper()
	def := h.ShellTool()
	require.NotNil(t, def.Execute)
	argBytes, err := json.Marshal(map[string]string{"command": command})
	require.NoError(t, err)
	out, err := def.Execute(context.Background(), argBytes)
	require.NoError(t, err)
	s, ok := out.(string)
	require.True(t, ok, "ShellTool handler must return a string")
	return s
}

func TestHost_ShellTool_Formatting_NoOutput(t *testing.T) {
	h := shell.NewHost(&mockDriver{result: shell.ExecResult{}})
	assert.Equal(t, "(no output)", runShellTool(t, h, "anything"))
}

func TestHost_ShellTool_Formatting_StdoutOnly(t *testing.T) {
	h := shell.NewHost(&mockDriver{result: shell.ExecResult{Stdout: "hello\n"}})
	assert.Equal(t, "hello\n", runShellTool(t, h, "echo hello"))
}

func TestHost_ShellTool_Formatting_StderrOnly(t *testing.T) {
	h := shell.NewHost(&mockDriver{result: shell.ExecResult{Stderr: "oops\n"}})
	assert.Equal(t, "[stderr] oops\n", runShellTool(t, h, "broken"))
}

func TestHost_ShellTool_Formatting_NonZeroExit(t *testing.T) {
	h := shell.NewHost(&mockDriver{result: shell.ExecResult{Stdout: "partial\n", ExitCode: 2}})
	assert.Equal(t, "partial\n[exit code: 2]", runShellTool(t, h, "failing"))
}

func TestHost_ShellTool_Formatting_StdoutAndStderrAndExit(t *testing.T) {
	h := shell.NewHost(&mockDriver{result: shell.ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 3}})
	assert.Equal(t, "out\n[stderr] err\n[exit code: 3]", runShellTool(t, h, "failing"))
}

// ---------------------------------------------------------------------------
// SetHub re-plumbing
// ---------------------------------------------------------------------------

func TestHost_SetHub_ReplacesHubLive(t *testing.T) {
	h := shell.NewHost(builtin.NewBuiltinShellDriver())
	hub1 := hooks.NewHub()
	hub2 := hooks.NewHub()

	var fired1, fired2 atomic.Int64
	hub1.On(hooks.ShellCall, func(context.Context, ...any) { fired1.Add(1) })
	hub2.On(hooks.ShellCall, func(context.Context, ...any) { fired2.Add(1) })

	h.SetHub(hub1)
	_, err := h.Exec(context.Background(), "echo one")
	require.NoError(t, err)
	waitForCount(t, fired1.Load, 1)

	h.SetHub(hub2)
	_, err = h.Exec(context.Background(), "echo two")
	require.NoError(t, err)
	waitForCount(t, fired2.Load, 1)

	// The first hub should still only have one firing.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(1), fired1.Load())
}
