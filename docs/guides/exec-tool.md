# Exec Tool

The harness ships a single LLM-callable shell tool — registered by default as **`exec`** — that lets the model run any command supported by the active `ShellDriver`. It is the foundation the model uses to read, traverse, and mutate the virtual filesystem the agent owns. The [File Tools](file-tools.md) sit alongside it for typed file manipulation; the exec tool sits underneath, exposing the full shell surface (`ls`, `cat`, `grep`, pipes, redirects, control flow, custom commands) that the [Virtual Bash reference](virtual-bash-reference.md) catalogues.

This guide covers the wire surface (parameter shape, output format, registration trigger) and the `Bash`-named alias for prompt portability with Claude Code.

## Auto-registration

When `HasShell` is composed alongside `UsesTools` (or, in Go, when an agent is constructed with `NewAgentWithShell`), an `exec` tool is automatically registered on the agent's tool registry — no explicit call needed. The tool name is `exec` (lowercase). It accepts a single string parameter (`command`) and returns the formatted shell output as a string.

| Language | Auto-register condition | Opt-out |
|----------|------------------------|---------|
| Python | `HasShell.__init_has_shell__()` runs and `register_tool` exists on the agent | None — skip composition of `UsesTools` if you want the shell without the LLM-callable tool |
| TypeScript | `initHasShell()` runs with `UsesTools` mixed in | `initHasShell({ registerTool: false })` |
| PHP | `initHasShell()` runs with the `UsesTools` trait composed | `initHasShell(registerTool: false)` |
| Go | `NewAgentWithShell` constructor (or `Builder.Shell(...).Build()`) | Use `NewAgent` (no shell) or do not call `agent.Register(host.ShellTool())` for hand-built agents |

## Parameter shape

```json
{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "The shell command to execute"
    }
  },
  "required": ["command"]
}
```

A single required `command` string. No `timeout`, no `description`, no `run_in_background` — the harness deliberately runs everything to completion in the calling thread (or coroutine). For long-running work the right move is a custom command that returns immediately and emits events.

## Output format

The handler runs the command through `shell.exec(...)` and concatenates the result into a single string the LLM sees as the tool result:

| Part | When emitted |
|------|--------------|
| `stdout` | Verbatim if non-empty |
| `[stderr] <stderr>` | Appended if stderr is non-empty |
| `[exit code: N]` | Appended if the command exited with non-zero status |
| `(no output)` | Returned if all three of the above are empty |

This format is identical across the four implementations (verified by `host_test.go::TestHost_ShellTool_Formatting_*`, `test_has_shell.py::TestHasShellWithTools::test_exec_tool_works`, etc.). It is **not** the same shape as Claude Code's Bash tool result, which surfaces `stdout`/`stderr`/`exit_code` as separate fields — see [Divergences from Claude Code](#divergences-from-claude-code).

## The Bash alias

For prompts and skills authored against Claude Code's `Bash` tool, the harness exposes an opt-in alias that registers the same handler under the name `Bash` (capitalised). Both names route to the same shell, so an agent can have both registered simultaneously without contention.

| Language | Call |
|----------|------|
| Python | `agent.register_bash_alias()` |
| TypeScript | `agent.registerBashAlias()` |
| PHP | `$agent->registerBashAlias()` |
| Go | `reg.Register(host.BashAliasTool())` |

The alias is opt-in by design — registering both names doubles the tool surface the LLM sees in its prompt, which is the right tradeoff for Claude-Code-portable prompts but wasteful for agents that only need one name. Register only the alias (and not `exec`) by passing the opt-out flag at `HasShell` init and then calling `register_bash_alias()` yourself.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Tool name | `exec` | `exec` | `exec` | `exec` |
| Auto-register trigger | `HasShell.__init_has_shell__` + `UsesTools` mixed in | `initHasShell()` + `UsesTools` mixed in | `initHasShell()` + `UsesTools` trait composed | `NewAgentWithShell(...)` |
| Opt-out | Skip composing `UsesTools` | `initHasShell({ registerTool: false })` | `initHasShell(registerTool: false)` | Use `NewAgent` or hand-roll |
| Bash alias | `agent.register_bash_alias()` | `agent.registerBashAlias()` | `$agent->registerBashAlias()` | `reg.Register(host.BashAliasTool())` |
| Hooks fired | `SHELL_CALL`, `SHELL_RESULT`, `SHELL_CWD`, `TOOL_CALL`, `TOOL_RESULT`, `TOOL_ERROR` | same | same | same |
| Backend swap | Via `ShellDriverFactory` / driver injection at `__init_has_shell__` | Via `ShellDriverFactory` or `initHasShell({ shell })` | Via `ShellDriverFactory` or `initHasShell(shell: ...)` | Pass an alternate `Driver` to `NewAgentWithShell` / `Builder.Shell` |

The tool follows whichever `ShellDriver` is active. Switching the backend (builtin / bashkit / OpenShell) at agent construction transparently swaps what `exec` runs against — no re-registration needed.

## Divergences from Claude Code

- **Tool name is `exec`, not `Bash`.** Use `register_bash_alias()` (or the language equivalent) when you want the Claude Code name.
- **Output is a flat string, not a structured `{stdout, stderr, exit_code}` object.** The flat format predates Claude Code's structured shape and is preserved for backward compatibility with existing harness skills and prompts.
- **No `timeout` parameter.** Claude Code's Bash tool accepts an optional timeout in milliseconds; the harness has no per-call timeout (the active `ShellDriver` enforces its own iteration / output caps via `ShellSecurityPolicy`).
- **No `description` parameter.** Claude Code requires a 5-10 word `description` on every Bash call for telemetry; the harness does not.
- **No `run_in_background`.** All commands run to completion in the same coroutine / goroutine that called the tool.
- **Custom commands.** Both surfaces support custom commands, but the harness exposes them via `register_command(name, handler)` — they appear inside `exec` rather than as separate tool entries.

## Known limitations

- **`exec` and `Bash` registered together both appear in the LLM's tool list.** They are functionally identical but the LLM may pick either, occasionally calling each in the same turn. If you only want one name, register only one.
- **No middleware hook on tool execution.** Same limitation as all built-in tools; use `TOOL_CALL` / `TOOL_RESULT` / `TOOL_ERROR` hooks to observe.
- **Tool output is truncated by `ShellSecurityPolicy.max_output`** (default 16 KB). Very large stdout is silently cut. Adjust at driver init if your workload needs more.

## See also

- [Virtual Bash reference](virtual-bash-reference.md) — full syntax and command catalogue the `exec` tool exposes
- [Why Virtual Bash](why-virtual-bash.md) — design rationale for "one tool, many commands" over typed function tools
- [File Tools](file-tools.md) — the typed Read/Write/Edit/Glob/Grep companions that share the same `FilesystemDriver`
- [Tools guide](tools.md) — the registration model both this tool and the file tools sit on
- [ADR 0012 — Virtual Shell Architecture](../adr/0012-virtual-shell-architecture.md)
- [ADR 0026 — Shell and Filesystem Driver Contracts](../adr/0026-shell-driver-contracts.md)
- [Python guide: Exec Tool](python.md#exec-tool) · [TypeScript guide: Exec Tool](typescript.md#exec-tool) · [PHP guide: Exec Tool](php.md#exec-tool) · [Go guide: Exec Tool](go.md#exec-tool)
