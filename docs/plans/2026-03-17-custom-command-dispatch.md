# Add Custom Command Dispatch to Remote Drivers

## Context

The `registerCommand()` / `unregisterCommand()` methods exist on OpenShellGrpcDriver (Python/TS/PHP) and BashkitCLIDriver (TS/PHP), and all advertise `"custom_commands"` in `capabilities()`. However, the registered commands are **never dispatched** — `exec()` sends every command string directly to the remote subprocess (SSH or bashkit CLI) without checking `_commands`.

Only `BashkitPythonDriver` (which uses a PyO3 `ScriptedTool`) and `BuiltinShellDriver` (which delegates to `Shell._builtins`) actually dispatch custom commands.

**Goal**: Add simple first-word command interception to all 5 affected driver implementations so registered custom commands actually work.

---

## Approach

Extract the first token from the command string. If it matches a registered command, dispatch locally (skip remote execution). Otherwise, fall through to the existing remote exec path.

### Shared helper: `_tryCustomCommand(command) -> ExecResult | null`

Each driver gets a private method with this logic:

```
1. Extract first whitespace-delimited token from `command`
2. If token is in `_commands` map:
   a. Split remaining string into args list
   b. Call handler(args, stdin="")
   c. Return ExecResult
3. If token NOT in map but `onNotFound` is set:
   - Don't call onNotFound here (command may be a valid remote command)
4. Return null (fall through to remote exec)
```

This is intentionally simple — no pipe/compound parsing. If the user writes `echo foo | mycmd`, it goes to SSH. Custom commands are designed for direct invocation like `mycmd --flag value`.

### Arg splitting

Use simple whitespace splitting (matching how `CmdHandler` signatures work across the codebase — `args: string[]`). The first token is stripped; remaining tokens become the args array.

---

## Files to Modify

### 1. `src/python/openshell_grpc_driver.py`
In `exec()`, before building preamble/epilogue, call `_try_custom_command(command)`. If it returns a result, return it directly (skip VFS sync — custom commands are local).

### 2. `src/typescript/openshell-grpc-driver.ts`
Same pattern. Add `_tryCustomCommand()` private method, call at top of `exec()`.

### 3. `src/php/OpenShellGrpcDriver.php`
Same pattern.

### 4. `src/typescript/bashkit-cli-driver.ts`
Same `_tryCustomCommand()` method and early return in `exec()`.

### 5. `src/php/BashkitCLIDriver.php`
Same `tryCustomCommand()` method and early return in `exec()`.

---

## Test Updates

### New tests to add (per driver, per language)

1. **Custom command executes locally**: Register a command, exec it, verify handler was called and result returned
2. **Custom command with args**: `exec("mycmd foo bar")` → handler receives `["foo", "bar"]`
3. **Unregistered command falls through**: Exec a non-registered command, verify it goes to remote (mock returns expected output)
4. **Unregister stops interception**: Register, unregister, exec → falls through to remote
5. **VFS sync skipped for custom commands**: Dirty files should NOT be cleared when a custom command is intercepted locally

---

## Verification

1. `pytest tests/python/ -v`
2. `npx vitest run`
3. `php vendor/bin/phpunit tests/php/`
