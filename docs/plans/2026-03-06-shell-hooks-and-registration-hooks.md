# Plan: Shell Hooks + Additional Useful Hooks

## Context

The `HookEvent` enum has 10 agent-level lifecycle events but zero shell-level events. When `HasShell.exec()` runs a command, there's no way for observers (loggers, metrics, guardrails) to know. Similarly, command/tool registration changes are invisible to hooks. This plan adds shell execution hooks and suggests other high-value hook events.

## Approach: Extend the Existing HookEvent Enum

Add new values to the existing `HookEvent` enum rather than creating a separate system. Rationale:
- Single observation point for all consumers (loggers, metrics subscribe once)
- ADR-0015 treats enum updates as deliberate friction, not prohibition
- Consistent API: `agent.on(HookEvent.SHELL_CALL, cb)` just like any other hook

## Hook Naming Review

### Existing naming analysis

The 10 current hooks use `subject_action` as the dominant pattern, but with inconsistencies:

| Hook | Pattern | Issue |
|------|---------|-------|
| `run_start` / `run_end` | subject_action | Clean |
| `llm_request` / `llm_response` | subject_noun | "request"/"response" are nouns, not actions |
| `tool_call` / `tool_result` / `tool_error` | subject_action/noun | Mixed — "call" is an action, "result"/"error" are nouns |
| `token_stream` | subject_noun | "stream" is a noun |
| `retry` | bare action | No subject prefix |
| `error` | bare noun | No subject prefix |

**Decision: do not rename the existing 10 hooks.** They are established API. Instead, align all new hooks to the cleanest existing pattern: `subject_action` where possible, using noun pairs (`subject_before`/`subject_after`) for pre/post pairs.

### New hook naming — coherent scheme

All new hooks use consistent `subject_action` naming, grouped by subject:

**Shell execution** — mirrors the `tool_call`/`tool_result` pattern:

| New Name | Old Proposal | Why changed |
|----------|-------------|-------------|
| `shell_call` | `shell_exec` | Parallels `tool_call` — same verb for same concept (before execution) |
| `shell_result` | `shell_result` | Parallels `tool_result` — no change needed |
| `shell_not_found` | `unknown_command` | Uses `shell_` prefix for grouping; `not_found` is the standard Unix concept (exit 127). `unknown_command` had no subject prefix and read as `adjective_noun` |
| `shell_cwd` | `cwd_change` | Groups with other `shell_` events. "Change" is implicit — the hook only fires when cwd changes |

**Registration** — parallel `subject_register`/`subject_unregister` pairs:

| New Name | Old Proposal | Why changed |
|----------|-------------|-------------|
| `tool_register` | `tool_register` | No change — extends existing `tool_` prefix naturally |
| `tool_unregister` | `tool_unregister` | No change |
| `command_register` | `command_register` | No change — "command" is the correct subject (shell commands, not to be confused with `shell_` execution events) |
| `command_unregister` | `command_unregister` | No change |

### Complete hook inventory (18 total)

**Agent lifecycle** (4):
`run_start`, `run_end`, `retry`, `error`

**LLM interaction** (3):
`llm_request`, `llm_response`, `token_stream`

**Tool execution** (3):
`tool_call`, `tool_result`, `tool_error`

**Tool management** (2, new):
`tool_register`, `tool_unregister`

**Shell execution** (4, new):
`shell_call`, `shell_result`, `shell_not_found`, `shell_cwd`

**Command management** (2, new):
`command_register`, `command_unregister`

## New Hook Events

### Phase 1: Shell execution hooks

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `SHELL_CALL` | Before `HasShell.exec()` dispatches to `Shell.exec()` | `(command: string)` |
| `SHELL_RESULT` | After `Shell.exec()` returns | `(command: string, result: ExecResult)` |
| `SHELL_NOT_FOUND` | Shell encounters an unrecognized command (exit 127) | `(commandName: string)` |

### Phase 2: Registration hooks

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `COMMAND_REGISTER` | `HasShell.registerCommand()` called | `(name: string)` |
| `COMMAND_UNREGISTER` | `HasShell.unregisterCommand()` called | `(name: string)` |
| `TOOL_REGISTER` | `UsesTools.register_tool()` called | `(toolDef)` |
| `TOOL_UNREGISTER` | `UsesTools.unregister_tool()` called | `(name: string)` |

### Phase 3: CWD change hook

| Event | Fires when | Arguments |
|-------|-----------|-----------|
| `SHELL_CWD` | Shell cwd changes after exec() | `(oldCwd: string, newCwd: string)` |

## Sync/Async Resolution

`Shell.exec()` is sync in all 3 languages. `_emit()` is async in Python/TypeScript. Resolution:

- **Shell class stays untouched** — pure, sync, no hooks
- **HasShell mixin** wraps exec() with hook emission
- **TypeScript**: `void this._emit(...)` — fire-and-forget, Promise floats
- **Python**: `asyncio.get_running_loop().create_task(self._emit(...))` wrapped in try/except RuntimeError (no-op outside async context)
- **PHP**: `$this->emit(...)` — already synchronous, no tension

All hook calls are guarded with runtime duck-typing checks (e.g., `typeof (this as any)._emit === "function"`, `hasattr(self, "_emit")`, `method_exists($this, 'emit')`) — consistent with existing patterns in HasShell (see tool auto-registration guards).

### SHELL_NOT_FOUND: Sync Callback on Shell

`SHELL_NOT_FOUND` fires inside `Shell._evalCommand()` (sync, deep in the eval loop). Since this is inside the Shell class (not HasShell), we add an **optional sync callback** on Shell:

- Add `onNotFound?: (cmdName: string) => void` property to Shell
- Call it in `_evalCommand()` when handler lookup fails (before returning exit 127)
- `HasShell._initHasShell()` wires this callback to emit the `SHELL_NOT_FOUND` hook via fire-and-forget

This is precise — fires for every unknown command in pipelines, loops, and control flow, not just top-level.

## Files to Modify

### Phase 1 (Shell hooks)

**HookEvent enum** (add `SHELL_CALL`, `SHELL_RESULT`, `SHELL_NOT_FOUND`):
- `src/typescript/has-hooks.ts`
- `src/python/has_hooks.py`
- `src/php/HookEvent.php`

**Shell class** (add `onNotFound` callback):
- `src/typescript/shell.ts` — add optional callback property, call in `_evalCommand()`
- `src/python/shell.py` — same pattern
- `src/php/Shell.php` — same pattern

**HasShell mixin** (wrap exec with hook emission, wire unknown command callback):
- `src/typescript/has-shell.ts` — modify `exec()`, import `HookEvent`, wire callback in `_initHasShell()`
- `src/python/has_shell.py` — modify `exec()`, add `_emit_fire_and_forget` helper, import `asyncio` and `HookEvent`, wire callback
- `src/php/HasShell.php` — modify `execCommand()`, wire callback in `initHasShell()`

**Tests**:
- `tests/typescript/has-shell.test.ts`
- `tests/python/test_has_shell.py`
- `tests/php/HasShellTest.php`

**ADR**:
- `docs/adr/0022-shell-and-registration-hooks.md` (new)

### Phase 2 (Registration hooks)

**HookEvent enum** (add `COMMAND_REGISTER`, `COMMAND_UNREGISTER`, `TOOL_REGISTER`, `TOOL_UNREGISTER`):
- Same 3 enum files as Phase 1

**HasShell mixin** (wrap registerCommand/unregisterCommand):
- Same 3 HasShell files

**UsesTools mixin** (wrap register_tool/unregister_tool):
- `src/typescript/uses-tools.ts`
- `src/python/uses_tools.py`
- `src/php/UsesTools.php`

**Tests**: Extend existing test files for HasShell + UsesTools

### Phase 3 (CWD change)

**HookEvent enum** (add `SHELL_CWD`):
- Same 3 enum files

**HasShell mixin** — compare `shell.cwd` before/after `exec()`, emit if changed:
- Same 3 HasShell files (no Shell modification needed)

**Tests**: Extend HasShell test files

## Implementation Details

### TypeScript Shell class — `shell.ts` unknown command callback

```typescript
// New property on Shell class
onNotFound?: (cmdName: string) => void;

// In _evalCommand(), when handler lookup fails:
if (!handler) {
  this.env["?"] = "127";
  if (this.onNotFound) {
    this.onNotFound(cmdName);
  }
  return makeResult("", `${cmdName}: command not found\n`, 127);
}
```

### TypeScript `has-shell.ts` changes

```typescript
import { HookEvent } from "./has-hooks.js";

// In _initHasShell(), after shell is created/assigned, wire the callback:
if (typeof (this as any)._emit === "function") {
  const self = this as any;
  this._shell!.onNotFound = (cmdName: string) => {
    void self._emit(HookEvent.SHELL_NOT_FOUND, cmdName);
  };
}

// Updated exec():
exec(command: string): ExecResult {
  if (typeof (this as any)._emit === "function") {
    void (this as any)._emit(HookEvent.SHELL_CALL, command);
  }
  const oldCwd = this.shell.cwd;
  const result = this.shell.exec(command);
  if (typeof (this as any)._emit === "function") {
    void (this as any)._emit(HookEvent.SHELL_RESULT, command, result);
    if (this.shell.cwd !== oldCwd) {
      void (this as any)._emit(HookEvent.SHELL_CWD, oldCwd, this.shell.cwd);
    }
  }
  return result;
}
```

### Python `has_shell.py` changes

```python
import asyncio
from src.python.has_hooks import HookEvent

def _emit_fire_and_forget(self, event: HookEvent, *args: Any) -> None:
    if not hasattr(self, "_emit"):
        return
    try:
        loop = asyncio.get_running_loop()
        loop.create_task(self._emit(event, *args))
    except RuntimeError:
        pass

# In __init_has_shell__(), after shell is created:
if hasattr(self, "_emit"):
    self._shell.on_not_found = lambda cmd_name: (
        self._emit_fire_and_forget(HookEvent.SHELL_NOT_FOUND, cmd_name)
    )

def exec(self, command: str) -> ExecResult:
    self._emit_fire_and_forget(HookEvent.SHELL_CALL, command)
    old_cwd = self.shell.cwd
    result = self.shell.exec(command)
    self._emit_fire_and_forget(HookEvent.SHELL_RESULT, command, result)
    if self.shell.cwd != old_cwd:
        self._emit_fire_and_forget(HookEvent.SHELL_CWD, old_cwd, self.shell.cwd)
    return result
```

### PHP `HasShell.php` changes

```php
// In initHasShell(), after shell is created:
if (method_exists($this, 'emit')) {
    $self = $this;
    $this->shell->onNotFound = function (string $cmdName) use ($self) {
        $self->emit(HookEvent::ShellNotFound, $cmdName);
    };
}

public function execCommand(string $command): ExecResult
{
    if (method_exists($this, 'emit')) {
        $this->emit(HookEvent::ShellCall, $command);
    }
    $oldCwd = $this->shell()->cwd;
    $result = $this->shell()->exec($command);
    if (method_exists($this, 'emit')) {
        $this->emit(HookEvent::ShellResult, $command, $result);
        if ($this->shell()->cwd !== $oldCwd) {
            $this->emit(HookEvent::ShellCwd, $oldCwd, $this->shell()->cwd);
        }
    }
    return $result;
}
```

## Documentation Updates

### New ADR: `docs/adr/0022-shell-and-registration-hooks.md`

Document the decision to extend HookEvent with shell-level, registration, and CWD hooks.

### Update ADR index: `docs/adr/README.md`

Add row under **Mixins** section.

### Update `docs/overview.md`

Update lifecycle hooks bullet from "10 hook events" to "18 hook events" and add the new events to the list.

### Update `docs/architecture.md`

1. Update HasHooks row from "10 hook events" to "18 hook events"
2. Update HasShell description mentioning shell hook emission

### Update language guides

Update event counts and lists in `docs/guides/python.md`, `docs/guides/typescript.md`, `docs/guides/php.md`.

### Update ADR 0015

Add a note in the Consequences section about the expansion to 18 events.

## Verification

1. **Unit tests**: Compose `HasHooks + HasShell`, register hooks, call `exec()`, verify callbacks received correct args
2. **Fire-and-forget timing**: TS/Python tests flush microtask queue before asserting
3. **Isolation**: Verify hooks don't fire when `HasHooks` is not composed
4. **Error resilience**: Verify throwing hook doesn't break `exec()` return value
5. **SHELL_CWD**: Run `exec("cd /tmp")`, verify hook fires with old/new paths
6. **SHELL_NOT_FOUND**: Run `exec("nonexistent arg1")`, verify hook fires with `"nonexistent"`. Also test inside a pipeline: `exec("echo hi | bogus")` should fire for `"bogus"`
7. **Registration hooks**: Call `registerCommand("foo", handler)`, verify `COMMAND_REGISTER` hook fires
8. **Run existing test suites** to ensure no regressions
9. **Doc review**: Verify all event counts are consistent across docs (18 total)
