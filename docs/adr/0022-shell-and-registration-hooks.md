# 22. Shell, Registration, and CWD Hooks

Date: 2026-03-06

## Status

Accepted

## Context

The `HasHooks` mixin provides 10 lifecycle events for agent-level observations (run start/end, LLM request/response, tool call/result/error, retry, token stream, error). However, shell-level operations — command execution, unknown commands, working directory changes — and registration changes (tools, commands) are invisible to hook subscribers.

Observers such as loggers, metrics collectors, and guardrails need visibility into these operations. For example, a security guardrail might want to inspect every shell command before execution, or a logger might track tool registrations.

## Decision

Extend the `HookEvent` enum with 8 new events, grouped by subject:

**Shell execution** (mirrors the existing `tool_call`/`tool_result` pattern):

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `shell_call` | Before `HasShell.exec()` dispatches to `Shell.exec()` | `(command)` |
| `shell_result` | After `Shell.exec()` returns | `(command, result)` |
| `shell_not_found` | Shell encounters an unrecognized command (exit 127) | `(commandName)` |
| `shell_cwd` | Shell cwd changes after exec | `(oldCwd, newCwd)` |

**Registration** (parallel register/unregister pairs):

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `command_register` | `HasShell.registerCommand()` called | `(name)` |
| `command_unregister` | `HasShell.unregisterCommand()` called | `(name)` |
| `tool_register` | `UsesTools.register_tool()` called | `(toolDef)` |
| `tool_unregister` | `UsesTools.unregister_tool()` called | `(name)` |

### Sync/async resolution

`Shell.exec()` is synchronous in all three languages, but `_emit()` is async in Python and TypeScript. Resolution:

- **Shell class stays pure and sync** — no hooks inside Shell itself
- **HasShell mixin** wraps `exec()` with hook emission
- **TypeScript**: `void this._emit(...)` — fire-and-forget, Promise floats
- **Python**: `asyncio.get_running_loop().create_task(self._emit(...))` wrapped in try/except RuntimeError (no-op outside async context)
- **PHP**: `$this->emit(...)` — already synchronous, no tension

### `shell_not_found` via sync callback

`shell_not_found` must fire inside `Shell._evalCommand()` (deep in the eval loop — inside pipelines, loops, and control flow). Since this is inside the Shell class (not HasShell), we add an optional sync callback `onNotFound` on Shell:

- Shell calls it when handler lookup fails (before returning exit 127)
- `HasShell` wires this callback during initialization to emit the hook via fire-and-forget

All hook calls are guarded with runtime duck-typing checks (`typeof (this as any)._emit === "function"`, `hasattr(self, "_emit")`, `method_exists($this, 'emit')`) — consistent with existing patterns in HasShell for tool auto-registration.

## Consequences

- Total hook count increases from 10 to 18
- Shell operations become observable without modifying the Shell class's sync contract
- Registration changes are trackable for audit logging and dynamic capability management
- The `onNotFound` callback adds a small coupling point between Shell and HasShell, but it's optional and null by default
- Fire-and-forget semantics mean hook errors cannot block shell execution, maintaining the principle that hooks are observers, not interceptors
