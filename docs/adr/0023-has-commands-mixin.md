# 23. HasCommands Mixin — Custom Slash Commands

Date: 2026-03-06

## Status

Accepted

## Context

ADR 0017 established that the skill system remains in examples, and that promoting specific capabilities as core mixins is the preferred path. Users and middleware need a way to register agent-level directives — slash commands like `/help`, `/reset`, `/status` — that are distinct from shell commands (ADR 0021) and optionally visible to the LLM as tools.

Shell commands (HasShell) serve a different purpose: they model a virtual shell interpreter with pipes, redirects, and control flow. Slash commands are standalone agent directives with simple `(args: dict) -> str` handlers.

## Decision

Add `HasCommands` as the 6th core mixin, with the following design:

### CommandDef

A data structure mirroring `ToolDef`:

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Command name (used as `/name`) |
| `description` | string | Human-readable description |
| `handler` | callable | `(args) -> string` |
| `parameters` | schema | JSON Schema for arguments |
| `llm_visible` | bool | Whether to auto-register as a tool (default: true) |

### HasCommands Mixin API

| Method | Purpose |
|--------|---------|
| `register_slash_command(def)` | Register a command; auto-register tool if visible |
| `unregister_slash_command(name)` | Remove command and its auto-registered tool |
| `execute_slash_command(name, args)` | Run handler with hook emissions |
| `intercept_slash_command(text)` | Parse `/cmd args` from text |
| `commands` | Read-only access to registered commands |

### LLM Visibility

Commands are optionally exposed to the LLM as tools with a `slash_` prefix:

- **Per-command:** `llm_visible` flag on `CommandDef` (default: true)
- **Per-agent:** `llm_commands_enabled` flag on init (default: true)
- Auto-registration requires both flags true and `UsesTools` composed

### Hook Events

Four new events following the register/unregister and call/result patterns:

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `slash_command_register` | Command registered | `(command_def)` |
| `slash_command_unregister` | Command unregistered | `(name)` |
| `slash_command_call` | Before handler executes | `(name, args)` |
| `slash_command_result` | After handler returns | `(name, result)` |

### SlashCommandMiddleware

Opt-in middleware that intercepts user messages starting with `/`:

1. Checks if the last user message starts with `/`
2. Calls `intercept_slash_command()` to parse command and args
3. Executes the command
4. Replaces the message content with the result

### Prerequisite: `unregister_tool()`

The `TOOL_UNREGISTER` hook event existed but the method did not. Added `unregister_tool(name)` to `UsesTools` in all three languages so `unregister_slash_command()` can cleanly remove auto-registered tools.

### Python `@command` Decorator

Mirrors the `@tool` decorator for ergonomic registration. Attaches `_command_meta` to the function, and `register_slash_command()` detects it to build the schema automatically.

## Design Distinctions from Shell Commands

| | Shell Commands (HasShell) | Slash Commands (HasCommands) |
|---|---|---|
| Scope | Virtual shell interpreter | Agent-level directives |
| Handler signature | `(args: list, stdin: str) -> ExecResult` | `(args: dict) -> str` |
| Composability | Pipes, redirects, control flow | None (standalone) |
| LLM access | Via `exec` tool | Auto-registered as individual tools |
| User access | Not directly (via LLM tool use) | `/command` in user messages |
| Hook prefix | `shell_*` / `command_*` | `slash_command_*` |

## Consequences

- Agents gain a structured way to expose user-facing commands
- LLMs can invoke slash commands as tools without additional wiring
- The middleware approach keeps slash command interception opt-in
- Hook events provide observability into command lifecycle
- The `unregister_tool()` addition completes the UsesTools API symmetry
