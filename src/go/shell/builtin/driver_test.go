package builtin_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/shell"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/shell/vfs"
)

// helpers

func newDriver(t *testing.T) *builtin.BuiltinShellDriver {
	t.Helper()
	return builtin.NewBuiltinShellDriver()
}

func execOK(t *testing.T, d *builtin.BuiltinShellDriver, cmd string) shell.ExecResult {
	t.Helper()
	r, err := d.Exec(context.Background(), cmd)
	require.NoError(t, err)
	return r
}

// ---------------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------------

func TestNewBuiltinShellDriver_Defaults(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	assert.Equal(t, "/", d.CWD())
	assert.Equal(t, map[string]string{}, d.Env())
	assert.NotNil(t, d.FS())
}

func TestWithCWD(t *testing.T) {
	d := builtin.NewBuiltinShellDriver(builtin.WithCWD("/tmp"))
	assert.Equal(t, "/tmp", d.CWD())
}

func TestWithEnv(t *testing.T) {
	d := builtin.NewBuiltinShellDriver(builtin.WithEnv(map[string]string{"FOO": "bar"}))
	env := d.Env()
	assert.Equal(t, "bar", env["FOO"])
}

func TestWithFS(t *testing.T) {
	fs := vfs.NewBuiltinFilesystemDriver()
	d := builtin.NewBuiltinShellDriver(builtin.WithFS(fs))
	assert.Equal(t, fs, d.FS())
}

// ---------------------------------------------------------------------------
// Exec — basic execution
// ---------------------------------------------------------------------------

func TestExec_Echo(t *testing.T) {
	d := newDriver(t)
	r := execOK(t, d, "echo hi")
	assert.Equal(t, "hi\n", r.Stdout)
	assert.Equal(t, 0, r.ExitCode)
}

func TestExec_EnvPersists(t *testing.T) {
	d := newDriver(t)
	execOK(t, d, "export FOO=bar")
	r := execOK(t, d, "echo $FOO")
	assert.Equal(t, "bar\n", r.Stdout)
}

func TestExec_CWD_Unchanged_On_Error(t *testing.T) {
	d := newDriver(t)
	before := d.CWD()
	r, err := d.Exec(context.Background(), "cd /nonexistent_dir_xyz")
	require.NoError(t, err)
	// cd should fail and CWD should be unchanged.
	assert.NotEqual(t, 0, r.ExitCode, "expected non-zero exit from cd to nonexistent dir")
	assert.Equal(t, before, d.CWD(), "CWD should be unchanged after failed cd")
}

func TestExec_CWD_Changes_When_Dir_Exists(t *testing.T) {
	fs := vfs.NewBuiltinFilesystemDriver()
	// Create a file so that /foo is treated as a directory in the VFS.
	require.NoError(t, fs.WriteString("/foo/bar.txt", "content"))

	d := builtin.NewBuiltinShellDriver(builtin.WithFS(fs))
	r := execOK(t, d, "cd /foo")
	assert.Equal(t, 0, r.ExitCode, "cd should succeed when dir exists")
	assert.Equal(t, "/foo", d.CWD())
}

func TestExec_EmptyCommand(t *testing.T) {
	d := newDriver(t)
	r, err := d.Exec(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, shell.ExecResult{}, r)
}

func TestExec_ExitCode(t *testing.T) {
	d := newDriver(t)
	r := execOK(t, d, "false")
	assert.Equal(t, 1, r.ExitCode)
}

// ---------------------------------------------------------------------------
// RegisterCommand / UnregisterCommand
// ---------------------------------------------------------------------------

func TestRegisterCommand(t *testing.T) {
	d := newDriver(t)
	d.RegisterCommand("greet", func(args []string, stdin string) shell.ExecResult {
		if len(args) == 0 {
			return shell.ExecResult{Stdout: "hi\n"}
		}
		return shell.ExecResult{Stdout: "hi " + args[0] + "\n"}
	})

	r := execOK(t, d, "greet world")
	assert.Equal(t, "hi world\n", r.Stdout)
}

func TestUnregisterCommand(t *testing.T) {
	d := newDriver(t)
	d.RegisterCommand("greet", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "hi\n"}
	})
	d.UnregisterCommand("greet")

	r, err := d.Exec(context.Background(), "greet")
	require.NoError(t, err)
	assert.Equal(t, 127, r.ExitCode)
}

// ---------------------------------------------------------------------------
// NotFoundHandler
// ---------------------------------------------------------------------------

func TestNotFoundHandler(t *testing.T) {
	d := newDriver(t)
	called := false
	d.SetNotFoundHandler(func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		called = true
		result := shell.ExecResult{Stdout: "caught: " + cmd + "\n", ExitCode: 0}
		return &result
	})

	r := execOK(t, d, "unknowncmd")
	assert.True(t, called, "not-found handler should have been called")
	assert.Equal(t, "caught: unknowncmd\n", r.Stdout)
}

func TestNotFoundHandler_FallThrough(t *testing.T) {
	d := newDriver(t)
	d.SetNotFoundHandler(func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		return nil // fall through to default
	})

	r, err := d.Exec(context.Background(), "unknowncmd")
	require.NoError(t, err)
	assert.Equal(t, 127, r.ExitCode)
}

func TestNotFoundHandler_GetSet(t *testing.T) {
	d := newDriver(t)
	assert.Nil(t, d.NotFoundHandler())

	h := shell.NotFoundHandler(func(ctx context.Context, cmd string, args []string, stdin string) *shell.ExecResult {
		return nil
	})
	d.SetNotFoundHandler(h)
	assert.NotNil(t, d.NotFoundHandler())

	d.SetNotFoundHandler(nil)
	assert.Nil(t, d.NotFoundHandler())
}

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

func TestClone_IndependentEnv(t *testing.T) {
	d := newDriver(t)
	execOK(t, d, "export ORIG=1")

	cloned := d.Clone().(*builtin.BuiltinShellDriver)
	execOK(t, cloned, "export CLONED=2")

	// Clone should see ORIG but not original's post-clone changes.
	assert.Equal(t, "1", d.Env()["ORIG"])
	assert.Equal(t, "", d.Env()["CLONED"], "original should not see clone's exports")

	assert.Equal(t, "1", cloned.Env()["ORIG"], "clone should inherit ORIG")
	assert.Equal(t, "2", cloned.Env()["CLONED"])

	// Mutate original after clone; clone should not see it.
	execOK(t, d, "export ORIG=changed")
	assert.Equal(t, "changed", d.Env()["ORIG"])
	assert.Equal(t, "1", cloned.Env()["ORIG"], "clone ORIG should remain '1'")
}

func TestClone_IndependentCWD(t *testing.T) {
	fs := vfs.NewBuiltinFilesystemDriver()
	require.NoError(t, fs.WriteString("/a/x.txt", ""))
	require.NoError(t, fs.WriteString("/b/y.txt", ""))

	d := builtin.NewBuiltinShellDriver(builtin.WithFS(fs))
	execOK(t, d, "cd /a")

	cloned := d.Clone().(*builtin.BuiltinShellDriver)
	execOK(t, cloned, "cd /b")

	assert.Equal(t, "/a", d.CWD())
	assert.Equal(t, "/b", cloned.CWD())
}

func TestClone_UserCommandsCopied(t *testing.T) {
	d := newDriver(t)
	d.RegisterCommand("ping", func(args []string, stdin string) shell.ExecResult {
		return shell.ExecResult{Stdout: "pong\n"}
	})

	cloned := d.Clone().(*builtin.BuiltinShellDriver)
	r := execOK(t, cloned, "ping")
	assert.Equal(t, "pong\n", r.Stdout, "clone should have inherited user commands")
}

func TestClone_ImplementsDriverInterface(t *testing.T) {
	d := newDriver(t)
	var iface shell.Driver = d.Clone()
	assert.NotNil(t, iface)
}

// ---------------------------------------------------------------------------
// ExecStream
// ---------------------------------------------------------------------------

func TestExecStream_StdoutAndExit(t *testing.T) {
	d := newDriver(t)
	ch, err := d.ExecStream(context.Background(), "echo hello")
	require.NoError(t, err)

	events := collectStream(ch)
	require.GreaterOrEqual(t, len(events), 1)

	// Expect a stdout event then an exit event.
	var stdout, exit *shell.ExecStreamEvent
	for i := range events {
		switch events[i].Kind {
		case shell.StreamStdout:
			e := events[i]
			stdout = &e
		case shell.StreamExit:
			e := events[i]
			exit = &e
		}
	}
	require.NotNil(t, stdout, "expected StreamStdout event")
	assert.Equal(t, "hello\n", stdout.Data)
	require.NotNil(t, exit, "expected StreamExit event")
	assert.Equal(t, 0, exit.ExitCode)
}

func TestExecStream_Stderr(t *testing.T) {
	d := newDriver(t)
	ch, err := d.ExecStream(context.Background(), "unknowncmdxyz")
	require.NoError(t, err)

	events := collectStream(ch)
	var stderr *shell.ExecStreamEvent
	for i := range events {
		if events[i].Kind == shell.StreamStderr {
			e := events[i]
			stderr = &e
		}
	}
	require.NotNil(t, stderr, "expected StreamStderr event for unknown command")
	assert.Contains(t, stderr.Data, "command not found")
}

func TestExecStream_ChannelClosed(t *testing.T) {
	d := newDriver(t)
	ch, err := d.ExecStream(context.Background(), "echo test")
	require.NoError(t, err)

	// Drain and verify channel is closed.
	for range ch {
	}
	// Reading from a closed channel should give zero value immediately.
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	default:
		// closed and drained — also fine
	}
}

func TestExecStream_ExitOnlyWhenNoOutput(t *testing.T) {
	d := newDriver(t)
	// "true" produces no output.
	ch, err := d.ExecStream(context.Background(), "true")
	require.NoError(t, err)

	events := collectStream(ch)
	// Should have exactly 1 event: exit.
	require.Len(t, events, 1)
	assert.Equal(t, shell.StreamExit, events[0].Kind)
	assert.Equal(t, 0, events[0].ExitCode)
}

func collectStream(ch <-chan shell.ExecStreamEvent) []shell.ExecStreamEvent {
	var events []shell.ExecStreamEvent
	for e := range ch {
		events = append(events, e)
	}
	return events
}

// ---------------------------------------------------------------------------
// Capabilities
// ---------------------------------------------------------------------------

func TestCapabilities(t *testing.T) {
	d := newDriver(t)
	caps := d.Capabilities()
	assert.True(t, caps[shell.CapCustomCommands])
	assert.True(t, caps[shell.CapStateful])
	assert.False(t, caps[shell.CapStreaming])
	assert.False(t, caps[shell.CapRemote])
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrentExec_Serialised(t *testing.T) {
	d := newDriver(t)
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(val int) {
			defer wg.Done()
			_, _ = d.Exec(context.Background(), fmt.Sprintf("export FOO=%d", val))
		}(i)
	}
	wg.Wait()
	// After all goroutines finish, FOO should be some integer value (no torn state).
	env := d.Env()
	val, ok := env["FOO"]
	assert.True(t, ok, "FOO should be set after concurrent exports")
	// Verify it's a valid integer (no torn reads / partial writes).
	var parsed int
	_, err := fmt.Sscanf(val, "%d", &parsed)
	assert.NoError(t, err, "FOO=%q should be a clean integer", val)
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

func TestFactory_DefaultCreate(t *testing.T) {
	// The init() in driver.go registers "builtin" and sets it as default.
	// Importing this package (via the test binary) triggers that init.
	d, err := shell.DefaultFactory.Create("", nil)
	require.NoError(t, err)
	require.NotNil(t, d)
	_, ok := d.(*builtin.BuiltinShellDriver)
	assert.True(t, ok, "default factory should return *BuiltinShellDriver")
}

func TestFactory_NamedCreate(t *testing.T) {
	d, err := shell.DefaultFactory.Create("builtin", nil)
	require.NoError(t, err)
	_, ok := d.(*builtin.BuiltinShellDriver)
	assert.True(t, ok)
}

func TestFactory_WithCWD(t *testing.T) {
	d, err := shell.DefaultFactory.Create("builtin", map[string]any{"cwd": "/tmp"})
	require.NoError(t, err)
	assert.Equal(t, "/tmp", d.CWD())
}

func TestFactory_WithEnv(t *testing.T) {
	env := map[string]string{"MY_VAR": "hello"}
	d, err := shell.DefaultFactory.Create("builtin", map[string]any{"env": env})
	require.NoError(t, err)
	assert.Equal(t, "hello", d.Env()["MY_VAR"])
}

func TestFactory_WithFS(t *testing.T) {
	fs := vfs.NewBuiltinFilesystemDriver()
	d, err := shell.DefaultFactory.Create("builtin", map[string]any{"fs": fs})
	require.NoError(t, err)
	assert.Equal(t, fs, d.FS())
}

func TestFactory_UnknownDriver(t *testing.T) {
	_, err := shell.DefaultFactory.Create("nonexistent_driver_xyz", nil)
	assert.Error(t, err)
}
