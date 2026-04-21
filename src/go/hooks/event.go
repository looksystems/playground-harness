// Package hooks provides a thread-safe event registry modelled after the
// Python HasHooks mixin. Handlers are invoked concurrently on Emit, mirroring
// asyncio.gather(..., return_exceptions=True) semantics: panics in individual
// handlers are recovered and logged without preventing other handlers from
// running.
package hooks

// Event is an opaque string that identifies a hook event. String values are
// the snake_case names from the Python HookEvent enum and must not be changed
// because external consumers may compare against them.
type Event string

const (
	RunStart          Event = "run_start"
	RunEnd            Event = "run_end"
	LLMRequest        Event = "llm_request"
	LLMResponse       Event = "llm_response"
	ToolCall          Event = "tool_call"
	ToolResult        Event = "tool_result"
	ToolError         Event = "tool_error"
	Retry             Event = "retry"
	TokenStream       Event = "token_stream"
	Error             Event = "error"
	ShellCall         Event = "shell_call"
	ShellResult       Event = "shell_result"
	ShellNotFound     Event = "shell_not_found"
	ShellCwd          Event = "shell_cwd"
	ToolRegister      Event = "tool_register"
	ToolUnregister    Event = "tool_unregister"
	CommandRegister   Event = "command_register"
	CommandUnregister Event = "command_unregister"
	SkillMount        Event = "skill_mount"
	SkillUnmount      Event = "skill_unmount"
	SkillSetup        Event = "skill_setup"
	SkillTeardown     Event = "skill_teardown"
	ShellStdoutChunk  Event = "shell_stdout_chunk"
	ShellStderrChunk  Event = "shell_stderr_chunk"
)
