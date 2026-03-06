# HasCommands Mixin — Custom Slash Commands

## Context

ADR 0017 established that the skill system stays in examples, and that promoting specific capabilities as core mixins is the preferred path. We're adding `HasCommands` as a new core mixin (the 6th) that enables user-facing slash commands (like `/help`, `/reset`) and optionally exposes them to the LLM as tools. This is separate but consistent with the existing shell command registration (ADR 0021).

## Design Summary

- **`CommandDef`** — data structure mirroring `ToolDef`: name, description, handler, parameters, `llm_visible` flag
- **`HasCommands` mixin** — registers/unregisters slash commands, executes them, parses `/command` from text
- **`@command` decorator** (Python) — mirrors `@tool` for ergonomic registration
- **LLM visibility** — configurable per-command (`llm_visible`) and per-agent (`llm_commands_enabled`), enabled by default. Auto-registers as tools with `slash_` prefix
- **`SlashCommandMiddleware`** — opt-in middleware that intercepts user messages starting with `/`
- **4 new hook events** — `slash_command_register`, `slash_command_unregister`, `slash_command_call`, `slash_command_result`

## Prerequisite: Add `unregister_tool()` to UsesTools

The `TOOL_UNREGISTER` hook event exists but the method doesn't. Add it to all 3 languages so `HasCommands.unregister_slash_command()` can cleanly remove auto-registered tools.

**Files:** `src/python/uses_tools.py`, `src/typescript/uses-tools.ts`, `src/php/UsesTools.php`

## Implementation Steps

### Step 1: Hook Events (all 3 languages)

Add to `HookEvent` enum:

| Event | Value |
|---|---|
| `SLASH_COMMAND_REGISTER` | `"slash_command_register"` |
| `SLASH_COMMAND_UNREGISTER` | `"slash_command_unregister"` |
| `SLASH_COMMAND_CALL` | `"slash_command_call"` |
| `SLASH_COMMAND_RESULT` | `"slash_command_result"` |

**Files:** `src/python/has_hooks.py`, `src/typescript/has-hooks.ts`, `src/php/HookEvent.php`

### Step 2: Add `unregister_tool()` to UsesTools

Python: `self._tools.pop(name, None)` + fire-and-forget `TOOL_UNREGISTER` hook
TypeScript: `this._tools.delete(name)` + void emit
PHP: `unset($this->tools[$name])` + emit

**Files:** `src/python/uses_tools.py`, `src/typescript/uses-tools.ts`, `src/php/UsesTools.php`

### Step 3: CommandDef (all 3 languages)

**Python** (`src/python/has_commands.py`):
```python
@dataclass
class CommandDef:
    name: str
    description: str
    handler: Callable
    parameters: dict[str, Any]
    llm_visible: bool = True
```

**TypeScript** (`src/typescript/has-commands.ts`):
```typescript
export interface CommandDef {
  name: string;
  description: string;
  execute: (args: Record<string, any>) => any | Promise<any>;
  parameters: Record<string, any>;
  llmVisible?: boolean; // default true
}
```

**PHP** (`src/php/CommandDef.php`):
```php
class CommandDef {
    public function __construct(
        public readonly string $name,
        public readonly string $description,
        public readonly array $parameters,
        private readonly \Closure $execute,
        public readonly bool $llmVisible = true,
    ) {}
    public function execute(array $args): string { return ($this->execute)($args); }
}
```

### Step 4: HasCommands Mixin (Python first, then TS/PHP)

**API surface** (consistent across languages):

| Method | Purpose |
|---|---|
| `__init_has_commands__(llm_commands_enabled=True)` | Lazy init |
| `register_slash_command(fn_or_def)` | Register command; auto-register tool if visible |
| `unregister_slash_command(name)` | Remove command + its tool |
| `execute_slash_command(name, args) -> str` | Run handler with hook emissions |
| `intercept_slash_command(text) -> (name, args) \| None` | Parse `/cmd args` from text |
| `commands` property | Read-only access to registered commands |

**Key behaviors:**
- Follows lazy init pattern: `_ensure_has_commands()` guard
- Auto-registers tool as `slash_{name}` when `llm_visible=True` and `llm_commands_enabled=True` and `UsesTools` is composed
- Fire-and-forget hook emission via `_emit_fire_and_forget` (reuse pattern from HasShell)
- `intercept_slash_command` parsing: text starts with `/name`, rest is args. Simple commands → `{"input": "rest"}`. Commands with schema → `key=value` parsing

**Files:** `src/python/has_commands.py`, `src/typescript/has-commands.ts`, `src/php/HasCommands.php`

### Step 5: `@command` Decorator (Python only)

Mirror `@tool` from `uses_tools.py`. Attaches `_command_meta` dict. `register_slash_command()` detects it and builds schema via `_build_param_schema()`.

**File:** `src/python/has_commands.py` (same file as CommandDef)

### Step 6: SlashCommandMiddleware (all 3 languages)

Extends `BaseMiddleware`. In `pre()`: checks if last user message starts with `/`, calls `intercept_slash_command()`, executes, replaces message content with result.

**Files:** `src/python/slash_command_middleware.py`, `src/typescript/slash-command-middleware.ts`, `src/php/SlashCommandMiddleware.php`

### Step 7: Update StandardAgent (all 3 languages)

Add `HasCommands` to composition chain (placed after UsesTools/HasHooks).

**Files:** `src/python/standard_agent.py`, `src/typescript/standard-agent.ts`, `src/php/StandardAgent.php`

### Step 8: Update Exports

**Files:** `src/typescript/index.ts`

### Step 9: Tests (all 3 languages)

Test classes following `test_has_shell.py` pattern:

1. **Standalone** — register, unregister, execute, intercept parsing
2. **With UsesTools** — tool auto-registration, llm_visible=False, agent-level disable, unregister removes tool
3. **With HasHooks** — all 4 hook events fire correctly
4. **Middleware** — intercepts slash messages, passes through regular messages

**Files:** `tests/python/test_has_commands.py`, `tests/typescript/has-commands.test.ts`, `tests/php/HasCommandsTest.php`

### Step 10: ADR 0023

Document the design decision, distinction from shell commands, and rationale.

**File:** `docs/adr/0023-has-commands-mixin.md`

## Key Design Distinctions from Shell Commands

| | Shell Commands (HasShell) | Slash Commands (HasCommands) |
|---|---|---|
| Scope | Virtual shell interpreter | Agent-level directives |
| Handler signature | `(args: list, stdin: str) -> ExecResult` | `(args: dict) -> str` |
| Composability | Pipes, redirects, control flow | None (standalone) |
| LLM access | Via `exec` tool | Auto-registered as individual tools |
| User access | Not directly (via LLM tool use) | `/command` in user messages |
| Hook prefix | `command_*` | `slash_command_*` |

## Verification

1. **Python tests:** `python -m pytest tests/python/test_has_commands.py -v`
2. **TypeScript tests:** `npx vitest run tests/typescript/has-commands.test.ts`
3. **PHP tests:** `./vendor/bin/phpunit tests/php/HasCommandsTest.php`
4. **All existing tests still pass:** `python -m pytest tests/python/ -v && npx vitest run && ./vendor/bin/phpunit`
