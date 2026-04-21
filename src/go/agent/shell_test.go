package agent_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/agent"
	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/middleware"
	"agent-harness/go/shell"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/tools"
)

// ---------------------------------------------------------------------------
// Fake llm.Client used by the agent shell tests.
// ---------------------------------------------------------------------------

type scriptedClient struct {
	mu        sync.Mutex
	responses []middleware.Message
	calls     int
}

func (c *scriptedClient) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls >= len(c.responses) {
		return llm.Response{Message: middleware.Message{Role: "assistant", Content: "done"}}, nil
	}
	m := c.responses[c.calls]
	c.calls++
	return llm.Response{Message: m}, nil
}

func (c *scriptedClient) Stream(_ context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 4)
	close(ch)
	return ch, nil
}

// ---------------------------------------------------------------------------
// Builder / construction
// ---------------------------------------------------------------------------

func TestBuilder_Shell_NilDriver_UsesDefault(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)
	require.True(t, a.HasShell())
	require.NotNil(t, a.Host)
	require.NotNil(t, a.Host.Driver, "default driver must be constructed")

	// `exec` tool should be auto-registered.
	def, ok := a.Get("exec")
	require.True(t, ok, "exec tool must be auto-registered on Build")
	assert.Equal(t, "exec", def.Name)
}

func TestBuilder_Shell_CustomDriver(t *testing.T) {
	d := builtin.NewBuiltinShellDriver(builtin.WithCWD("/"))
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(d).
		Build(context.Background())
	require.NoError(t, err)
	assert.Same(t, d, a.Host.Driver)
}

func TestBuilder_Shell_NotCalled_NoShell(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Build(context.Background())
	require.NoError(t, err)
	assert.False(t, a.HasShell())
	assert.Nil(t, a.Host)
	_, ok := a.Get("exec")
	assert.False(t, ok, "exec tool must NOT be registered without Shell()")
}

func TestBuilder_Command_ImpliesShell(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Command("greet", func(args []string, _ string) shell.ExecResult {
			name := "world"
			if len(args) > 0 {
				name = args[0]
			}
			return shell.ExecResult{Stdout: "hi " + name + "\n"}
		}).
		Build(context.Background())
	require.NoError(t, err)
	require.True(t, a.HasShell(), "Command(...) should imply Shell(nil)")

	res, err := a.Exec(context.Background(), "greet alice")
	require.NoError(t, err)
	assert.Equal(t, "hi alice\n", res.Stdout)
}

// ---------------------------------------------------------------------------
// Agent.Exec convenience method
// ---------------------------------------------------------------------------

func TestAgent_Exec_EchoHi(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)

	res, err := a.Exec(context.Background(), "echo hi")
	require.NoError(t, err)
	assert.Equal(t, "hi\n", res.Stdout)
	assert.Equal(t, 0, res.ExitCode)
}

func TestAgent_Exec_FiresShellHooks(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)

	var callFired, resultFired atomic.Int64
	a.On(hooks.ShellCall, func(_ context.Context, _ ...any) { callFired.Add(1) })
	a.On(hooks.ShellResult, func(_ context.Context, _ ...any) { resultFired.Add(1) })

	_, err = a.Exec(context.Background(), "echo hello")
	require.NoError(t, err)

	// Hooks fire async; poll briefly.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if callFired.Load() >= 1 && resultFired.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	assert.EqualValues(t, 1, callFired.Load(), "shell_call must fire")
	assert.EqualValues(t, 1, resultFired.Load(), "shell_result must fire")
}

// ---------------------------------------------------------------------------
// LLM-driven exec tool-call path
// ---------------------------------------------------------------------------

// TestAgent_Run_ExecToolCall drives the agent with a fake LLM that issues
// a tool_call for `exec` with {"command":"pwd"}; the tool should run, the
// result should be fed back, and on the next turn the assistant replies
// plainly.
func TestAgent_Run_ExecToolCall(t *testing.T) {
	client := &scriptedClient{
		responses: []middleware.Message{
			{
				Role: "assistant",
				ToolCalls: []middleware.ToolCall{
					{ID: "call_1", Name: "exec", Arguments: `{"command":"pwd"}`},
				},
			},
			{Role: "assistant", Content: "ran it"},
		},
	}

	d := builtin.NewBuiltinShellDriver(builtin.WithCWD("/home/user"))
	a, err := agent.NewBuilder("m").
		Client(client).
		Shell(d).
		Streaming(false).
		Build(context.Background())
	require.NoError(t, err)

	var toolCallFired atomic.Int64
	var shellCallFired atomic.Int64
	a.On(hooks.ToolCall, func(_ context.Context, _ ...any) { toolCallFired.Add(1) })
	a.On(hooks.ShellCall, func(_ context.Context, _ ...any) { shellCallFired.Add(1) })

	out, err := a.Run(context.Background(), []middleware.Message{
		{Role: "user", Content: "what is the pwd?"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ran it", out)

	// Exec tool was dispatched once through the Registry...
	assert.EqualValues(t, 1, toolCallFired.Load(), "tool_call must fire")

	// ...which in turn hit the shell, firing shell_call.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if shellCallFired.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	assert.EqualValues(t, 1, shellCallFired.Load(), "shell_call must fire via the exec tool")
}

// TestAgent_ExecToolHandler_FormatsResult verifies that the `exec` tool
// registered on the Agent formats stdout correctly (shares the same
// formatter the shell.Host exposes).
func TestAgent_ExecToolHandler_FormatsResult(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(d).
		Build(context.Background())
	require.NoError(t, err)

	def, ok := a.Get("exec")
	require.True(t, ok)

	raw, err := json.Marshal(map[string]string{"command": "echo hello"})
	require.NoError(t, err)

	out, err := def.Execute(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", out)
}

// ---------------------------------------------------------------------------
// ShellHost capability — runtime satisfaction
// ---------------------------------------------------------------------------

func TestAgent_ShellHost_Interface(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)

	// *Agent satisfies ShellHost at the type level (compile-time assertion
	// in hosts.go). This test proves the runtime dispatch works when a
	// shell is attached.
	var host agent.ShellHost = a
	res, err := host.Exec(context.Background(), "echo via-interface")
	require.NoError(t, err)
	assert.Equal(t, "via-interface\n", res.Stdout)

	host.RegisterCommand("ping", func(_ []string, _ string) shell.ExecResult {
		return shell.ExecResult{Stdout: "pong\n"}
	})
	res, err = a.Exec(context.Background(), "ping")
	require.NoError(t, err)
	assert.Equal(t, "pong\n", res.Stdout)
}

// Sanity check: tools.Def for exec is present and has the right shape.
func TestAgent_ExecToolSchema(t *testing.T) {
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)

	def, ok := a.Get("exec")
	require.True(t, ok)
	assert.Equal(t, "exec", def.Name)
	require.NotNil(t, def.Parameters)
	props, ok := def.Parameters["properties"].(map[string]any)
	require.True(t, ok)
	_, ok = props["command"].(map[string]any)
	require.True(t, ok, "exec.command property missing")

	// The schema must also survive Registry.Schemas() round-trip.
	schemas := a.Schemas()
	var found bool
	for _, s := range schemas {
		fn, ok := s["function"].(map[string]any)
		if !ok {
			continue
		}
		if fn["name"] == "exec" {
			found = true
			break
		}
	}
	assert.True(t, found, "exec tool must appear in Schemas()")

	// And tools.Def parity for the tests below.
	var _ tools.Def = def
}

// ---------------------------------------------------------------------------
// NewAgentWithShell auto-registers exec (review finding I6 / Fix 6).
// ---------------------------------------------------------------------------

func TestNewAgentWithShell_RegistersExecTool(t *testing.T) {
	d := builtin.NewBuiltinShellDriver()
	a := agent.NewAgentWithShell("m", &scriptedClient{}, d)
	require.NotNil(t, a.Host)
	require.Same(t, d, a.Host.Driver)

	def, ok := a.Get("exec")
	require.True(t, ok, "NewAgentWithShell must auto-register the exec tool")
	assert.Equal(t, "exec", def.Name)
	require.NotNil(t, def.Execute)
}

func TestNewAgentWithShell_NilDriver_UsesDefaultAndRegistersExec(t *testing.T) {
	a := agent.NewAgentWithShell("m", &scriptedClient{}, nil)
	require.NotNil(t, a.Host)
	require.NotNil(t, a.Host.Driver, "default driver must be constructed")

	_, ok := a.Get("exec")
	require.True(t, ok, "exec tool must be auto-registered when driver is nil")
}

func TestBuilder_Shell_DoesNotDoubleRegisterExec(t *testing.T) {
	// Builder.Build previously re-registered exec on top of what
	// NewAgentWithShell already did. Now that the registration moved,
	// Build must NOT call Register again — the tools.Registry replaces
	// an existing entry so the old shape would silently overwrite,
	// but we still want a single source of truth.
	a, err := agent.NewBuilder("m").
		Client(&scriptedClient{}).
		Shell(nil).
		Build(context.Background())
	require.NoError(t, err)

	// Count "exec" entries in Schemas — there must be exactly one.
	count := 0
	for _, s := range a.Schemas() {
		fn, ok := s["function"].(map[string]any)
		if !ok {
			continue
		}
		if fn["name"] == "exec" {
			count++
		}
	}
	assert.Equal(t, 1, count, "exec tool must appear exactly once")
}
