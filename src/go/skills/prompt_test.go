package skills_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/middleware"
	"agent-harness/go/skills"
)

// instructionsSkill returns non-empty instructions so the prompt
// middleware will include its section.
type instructionsSkill struct {
	skills.Base
	n     string
	instr string
}

func (s instructionsSkill) Name() string         { return s.n }
func (s instructionsSkill) Instructions() string { return s.instr }

// ---------------------------------------------------------------------------
// Empty / no-instruction cases
// ---------------------------------------------------------------------------

func TestPrompt_EmptyManager_ReturnsMessagesUnchanged(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	mw := skills.NewPromptMiddleware(m)

	in := []middleware.Message{{Role: "user", Content: "hi"}}
	out, err := mw.Pre(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestPrompt_SkillWithoutInstructions_ReturnsMessagesUnchanged(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	require.NoError(t, m.Mount(context.Background(), &bareSkill{}, nil))

	mw := skills.NewPromptMiddleware(m)
	in := []middleware.Message{{Role: "user", Content: "hi"}}
	out, err := mw.Pre(context.Background(), in, nil)
	require.NoError(t, err)
	assert.Equal(t, in, out, "skill with empty instructions must not inject a block")
}

// ---------------------------------------------------------------------------
// Append to existing system message
// ---------------------------------------------------------------------------

func TestPrompt_AppendsToExistingSystemMessage(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	sk := instructionsSkill{n: "foo", instr: "do the foo"}
	require.NoError(t, m.Mount(context.Background(), sk, nil))

	mw := skills.NewPromptMiddleware(m)
	in := []middleware.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "hi"},
	}
	out, err := mw.Pre(context.Background(), in, nil)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "user", out[1].Role)

	sys := out[0]
	assert.Equal(t, "system", sys.Role)
	// Prefix should be the original system content.
	assert.True(t, strings.HasPrefix(sys.Content, "You are helpful."), "got %q", sys.Content)
	// Then the separator and block.
	assert.Contains(t, sys.Content, "\n\n---\n**Available Skills:**\n\n")
	assert.Contains(t, sys.Content, "## foo\ndo the foo")
}

func TestPrompt_AppendsDoesNotMutateOriginal(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	sk := instructionsSkill{n: "foo", instr: "i"}
	require.NoError(t, m.Mount(context.Background(), sk, nil))

	mw := skills.NewPromptMiddleware(m)
	in := []middleware.Message{
		{Role: "system", Content: "original"},
		{Role: "user", Content: "hi"},
	}
	_, err := mw.Pre(context.Background(), in, nil)
	require.NoError(t, err)
	// Caller's slice must be unchanged.
	assert.Equal(t, "original", in[0].Content)
}

// ---------------------------------------------------------------------------
// Prepend when no system message
// ---------------------------------------------------------------------------

func TestPrompt_PrependsWhenNoSystemMessage(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	sk := instructionsSkill{n: "foo", instr: "do the foo"}
	require.NoError(t, m.Mount(context.Background(), sk, nil))

	mw := skills.NewPromptMiddleware(m)
	in := []middleware.Message{{Role: "user", Content: "hi"}}
	out, err := mw.Pre(context.Background(), in, nil)
	require.NoError(t, err)
	require.Len(t, out, 2)

	assert.Equal(t, "system", out[0].Role)
	// The leading blank-line separator is stripped when there is no
	// pre-existing system message.
	assert.False(t, strings.HasPrefix(out[0].Content, "\n"), "leading newlines should be trimmed")
	assert.True(t, strings.HasPrefix(out[0].Content, "---\n**Available Skills:**"))
	assert.Contains(t, out[0].Content, "## foo\ndo the foo")
	assert.Equal(t, "user", out[1].Role)
}

// ---------------------------------------------------------------------------
// Multiple skills
// ---------------------------------------------------------------------------

func TestPrompt_MultipleSkillsJoinedWithBlankLine(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	require.NoError(t, m.Mount(context.Background(), instructionsSkill{n: "alpha", instr: "A"}, nil))
	require.NoError(t, m.Mount(context.Background(), instructionsSkill{n: "beta", instr: "B"}, nil))

	mw := skills.NewPromptMiddleware(m)
	out, err := mw.Pre(context.Background(), []middleware.Message{{Role: "system", Content: "sys"}}, nil)
	require.NoError(t, err)
	assert.Contains(t, out[0].Content, "## alpha\nA\n\n## beta\nB")
}

// ---------------------------------------------------------------------------
// Post is a no-op
// ---------------------------------------------------------------------------

func TestPrompt_PostIsNoOp(t *testing.T) {
	fa := newFakeAgent()
	m := skills.NewManager(fa)
	mw := skills.NewPromptMiddleware(m)

	msg := middleware.Message{Role: "assistant", Content: "hi"}
	out, err := mw.Post(context.Background(), msg, nil)
	require.NoError(t, err)
	assert.Equal(t, msg, out)
}
