package hooks_test

import (
	"testing"

	"agent-harness/go/hooks"

	"github.com/stretchr/testify/assert"
)

func TestEventConstants(t *testing.T) {
	// Spot-check a representative selection of the 24 event constants.
	assert.Equal(t, "run_start", string(hooks.RunStart))
	assert.Equal(t, "run_end", string(hooks.RunEnd))
	assert.Equal(t, "llm_request", string(hooks.LLMRequest))
	assert.Equal(t, "llm_response", string(hooks.LLMResponse))
	assert.Equal(t, "tool_call", string(hooks.ToolCall))
	assert.Equal(t, "tool_result", string(hooks.ToolResult))
	assert.Equal(t, "tool_error", string(hooks.ToolError))
	assert.Equal(t, "retry", string(hooks.Retry))
	assert.Equal(t, "token_stream", string(hooks.TokenStream))
	assert.Equal(t, "error", string(hooks.Error))
	assert.Equal(t, "shell_call", string(hooks.ShellCall))
	assert.Equal(t, "shell_result", string(hooks.ShellResult))
	assert.Equal(t, "shell_not_found", string(hooks.ShellNotFound))
	assert.Equal(t, "shell_cwd", string(hooks.ShellCwd))
	assert.Equal(t, "tool_register", string(hooks.ToolRegister))
	assert.Equal(t, "tool_unregister", string(hooks.ToolUnregister))
	assert.Equal(t, "command_register", string(hooks.CommandRegister))
	assert.Equal(t, "command_unregister", string(hooks.CommandUnregister))
	assert.Equal(t, "skill_mount", string(hooks.SkillMount))
	assert.Equal(t, "skill_unmount", string(hooks.SkillUnmount))
	assert.Equal(t, "skill_setup", string(hooks.SkillSetup))
	assert.Equal(t, "skill_teardown", string(hooks.SkillTeardown))
	assert.Equal(t, "shell_stdout_chunk", string(hooks.ShellStdoutChunk))
	assert.Equal(t, "shell_stderr_chunk", string(hooks.ShellStderrChunk))
}
