package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/hooks"
	"agent-harness/go/middleware"
	"agent-harness/go/skills"
	"agent-harness/go/tools"
)

// ---------------------------------------------------------------------------
// Test skills
// ---------------------------------------------------------------------------

// echoSkill advertises an `echo` tool that returns whatever `msg` was passed.
type echoSkill struct {
	skills.Base
}

func (echoSkill) Name() string         { return "echo" }
func (echoSkill) Instructions() string { return "Use echo(msg) to repeat something back." }

func (echoSkill) Tools() []tools.Def {
	return []tools.Def{{
		Name:        "echo",
		Description: "Echo a message back.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]any{"type": "string"},
			},
			"required": []string{"msg"},
		},
		Execute: func(_ context.Context, raw []byte) (any, error) {
			// Tiny JSON passthrough: just return the raw bytes as a
			// string; real tool would unmarshal.
			return string(raw), nil
		},
	}}
}

// setupTrackingSkill records setup/teardown calls.
type setupTrackingSkill struct {
	skills.Base
	setupSeen    bool
	teardownSeen bool
}

func (s *setupTrackingSkill) Name() string { return "tracker" }

func (s *setupTrackingSkill) Setup(_ context.Context, _ *skills.SkillContext) error {
	s.setupSeen = true
	return nil
}

func (s *setupTrackingSkill) Teardown(_ context.Context, _ *skills.SkillContext) error {
	s.teardownSeen = true
	return nil
}

// ---------------------------------------------------------------------------
// Builder.Skill / Skills / MountSkill
// ---------------------------------------------------------------------------

func TestBuilder_Skill_MountsAndRegistersTool(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "done"},
	})

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		Skill(echoSkill{}, nil).
		Build(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"echo"}, a.Skills.Mounted())
	_, ok := a.Get("echo")
	assert.True(t, ok, "skill's echo tool should be registered on the agent")

	// Prompt middleware was installed: the snapshot should be non-empty.
	assert.NotEmpty(t, a.Snapshot(), "skill prompt middleware should be installed")
}

func TestBuilder_Skills_Variadic(t *testing.T) {
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Streaming(false).
		Skills(echoSkill{}, &setupTrackingSkill{}).
		Build(context.Background())
	require.NoError(t, err)

	mounted := a.Skills.Mounted()
	assert.Contains(t, mounted, "echo")
	assert.Contains(t, mounted, "tracker")
}

func TestBuilder_MountSkill_Alias(t *testing.T) {
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Streaming(false).
		MountSkill(echoSkill{}, map[string]any{"k": 1}).
		Build(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{"echo"}, a.Skills.Mounted())
}

// ---------------------------------------------------------------------------
// Setup called at Build time
// ---------------------------------------------------------------------------

func TestBuilder_Skill_SetupCalled(t *testing.T) {
	tr := &setupTrackingSkill{}
	_, err := NewBuilder("m").
		Client(newFakeClient()).
		Streaming(false).
		Skill(tr, nil).
		Build(context.Background())
	require.NoError(t, err)

	assert.True(t, tr.setupSeen, "Setup should fire during Build")
}

// ---------------------------------------------------------------------------
// Prompt middleware injects skill instructions
// ---------------------------------------------------------------------------

func TestBuilder_Skill_PromptMiddlewareInjects(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(fakeCompleteStep{
		msg: middleware.Message{Role: "assistant", Content: "ok"},
	})

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		System("Be helpful.").
		Skill(echoSkill{}, nil).
		Build(context.Background())
	require.NoError(t, err)

	_, err = a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, fc.observeRequests, 1)
	msgs := fc.observeRequests[0].Messages
	require.NotEmpty(t, msgs)
	assert.Equal(t, "system", msgs[0].Role)
	// The base System prompt should still be present.
	assert.True(t, strings.HasPrefix(msgs[0].Content, "Be helpful."),
		"system message should retain the builder's System prompt; got %q", msgs[0].Content)
	assert.Contains(t, msgs[0].Content, "**Available Skills:**")
	assert.Contains(t, msgs[0].Content, "## echo")
	assert.Contains(t, msgs[0].Content, "Use echo(msg) to repeat something back.")
}

// ---------------------------------------------------------------------------
// End-to-end: skill contributes a tool and the LLM invokes it
// ---------------------------------------------------------------------------

// toolAdvertisingSkill mounts an "add" tool and verifies the LLM path.
type toolAdvertisingSkill struct {
	skills.Base
}

func (toolAdvertisingSkill) Name() string         { return "adder" }
func (toolAdvertisingSkill) Instructions() string { return "Use add(a,b) to sum." }

func (toolAdvertisingSkill) Tools() []tools.Def {
	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	return []tools.Def{tools.Tool(func(_ context.Context, args addArgs) (int, error) {
		return args.A + args.B, nil
	}, tools.Name("add"), tools.Description("add two ints"))}
}

func TestBuilder_Skill_EndToEnd_ToolCall(t *testing.T) {
	fc := newFakeClient()
	fc.scriptComplete(
		fakeCompleteStep{msg: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{{
				ID:        "tc_1",
				Name:      "add",
				Arguments: `{"a":40,"b":2}`,
			}},
		}},
		fakeCompleteStep{msg: middleware.Message{Role: "assistant", Content: "42"}},
	)

	a, err := NewBuilder("m").
		Client(fc).
		Streaming(false).
		Skill(toolAdvertisingSkill{}, nil).
		Build(context.Background())
	require.NoError(t, err)

	out, err := a.Run(context.Background(), []middleware.Message{{Role: "user", Content: "what is 40+2?"}})
	require.NoError(t, err)
	assert.Equal(t, "42", out)
	assert.Equal(t, 2, fc.completeCalls)

	// Second request should have carried the add tool's JSON result.
	require.Len(t, fc.observeRequests, 2)
	last := fc.observeRequests[1].Messages[len(fc.observeRequests[1].Messages)-1]
	assert.Equal(t, "tool", last.Role)
	assert.Equal(t, "42", last.Content)
}

// ---------------------------------------------------------------------------
// SkillHost compile-time assertion
// ---------------------------------------------------------------------------

func TestAgent_SatisfiesSkillHost(t *testing.T) {
	var _ SkillHost = (*Agent)(nil)
}

// ---------------------------------------------------------------------------
// Skill hook emission reaches the agent's hub
// ---------------------------------------------------------------------------

func TestBuilder_Skill_HooksFireOnAgentHub(t *testing.T) {
	// Listener is wired *after* Build so we do not race with the Mount
	// call inside Build — the initial SkillSetup / SkillMount events
	// fire synchronously during Build. We still exercise the pipe by
	// mounting a second skill post-Build.
	a, err := NewBuilder("m").
		Client(newFakeClient()).
		Streaming(false).
		Build(context.Background())
	require.NoError(t, err)

	seen := make(chan hooks.Event, 8)
	a.On(hooks.SkillMount, func(_ context.Context, _ ...any) { seen <- hooks.SkillMount })
	a.On(hooks.SkillSetup, func(_ context.Context, _ ...any) { seen <- hooks.SkillSetup })

	require.NoError(t, a.Skills.Mount(context.Background(), echoSkill{}, nil))

	// EmitAsync means handlers fire on a separate goroutine; wait for
	// at least two events (one setup, one mount).
	for i := 0; i < 2; i++ {
		select {
		case <-seen:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for skill hooks")
		}
	}
}
