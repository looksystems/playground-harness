# Revised Bashkit Integration Plan

## Context

Our Phase 2 (BashkitIPCDriver) and Phase 3 (BashkitNativeDriver) implementations were built against fictional APIs — a `libashkit` C library and `bashkit-cli --jsonrpc` mode that don't exist in the actual bashkit project. This plan replaces those fictional drivers with real implementations based on what bashkit actually provides.

### What bashkit actually offers:
- **Rust library**: `bashkit` crate with `Bash::builder().fs(custom_fs).build()` and `ScriptedTool`
- **Python bindings**: `bashkit-python` (PyO3) — `Bash`, `BashTool`, `ScriptedTool` classes, in-memory VFS
- **CLI**: `bashkit -c 'command'` for one-shot execution
- **MCP server**: `bashkit mcp` (stateless, new Bash per call)
- **No C library**, no JSON-RPC IPC protocol, no TypeScript/PHP bindings

### How everruns integrates:
- Rust server: direct library dependency, implements `FileSystem` trait via `SessionFileSystemAdapter` (zero sync overhead, direct delegation to DB)
- SDK (Python/TS): pure HTTP client, bash execution is server-side

---

## Plan

### 1. Python: Replace with `BashkitPythonDriver`

**Replace** `BashkitNativeDriver` and `BashkitIPCDriver` with a single `BashkitPythonDriver` that wraps `bashkit-python` (PyO3 package).

**Files to modify/create:**
- `src/python/bashkit_python_driver.py` — new driver (replaces `bashkit_native_driver.py` and `bashkit_ipc_driver.py`)
- `src/python/bashkit_driver.py` — update resolver to use `BashkitPythonDriver`
- `tests/python/test_bashkit_python_driver.py` — new tests
- Delete: `src/python/bashkit_native_driver.py`, `src/python/bashkit_ipc_driver.py`
- Delete: `tests/python/test_bashkit_native_driver.py`, `tests/python/test_bashkit_ipc_driver.py`

**Implementation:**
```python
from bashkit import Bash, ScriptedTool

class BashkitPythonDriver(ShellDriver):
    def __init__(self, cwd="/", env=None):
        self._bash = Bash()
        self._fs_driver = BuiltinFilesystemDriver()
        self._dirty_files: set[str] = set()
        # ... track dirty state for lazy sync

    def exec(self, command):
        self._sync_dirty_to_bashkit()      # write only changed files
        result = self._bash.execute_sync(command)
        self._sync_changes_from_bashkit()   # read back new/modified files
        return ExecResult(...)

    def register_command(self, name, handler):
        # Use ScriptedTool for callback registration
        # Or track commands and rebuild ScriptedTool on exec
```

**VFS strategy: Hybrid lazy sync**
- Track dirty files in our `FilesystemDriver` (files written since last exec)
- Before exec: write only dirty files into bashkit via `bash.execute_sync("cat > /path <<'HARNESS_EOF'\n...\nHARNESS_EOF")`
- After exec: list files in bashkit VFS, diff against pre-exec snapshot, apply changes back to our `FilesystemDriver`
- Reset dirty tracking after each sync

**Custom commands:**
- `ScriptedTool` is the bashkit mechanism for registering Python callbacks as builtins
- Our `register_command()` maps to `ScriptedTool.add_tool(name, desc, callback=handler)`
- When commands are registered, use `ScriptedTool` instead of bare `Bash` for execution

### 2. TypeScript: Replace with `BashkitCLIDriver`

**Replace** `bashkit-ipc-driver.ts` and `bashkit-native-driver.ts` with `bashkit-cli-driver.ts` using `bashkit -c` subprocess.

**Files to modify/create:**
- `src/typescript/bashkit-cli-driver.ts` — new driver
- `src/typescript/bashkit-driver.ts` — update resolver
- `tests/typescript/bashkit-cli-driver.test.ts` — new tests
- Delete: `src/typescript/bashkit-ipc-driver.ts`, `src/typescript/bashkit-native-driver.ts`
- Delete: corresponding test files

**Implementation:**
```typescript
class BashkitCLIDriver implements ShellDriver {
    async exec(command: string): Promise<ExecResult> {
        this.syncDirtyToBashkit();
        const result = spawnSync('bashkit', ['-c', command]);
        return { stdout: result.stdout, stderr: result.stderr, exitCode: result.status };
    }
}
```

**Limitations (documented):**
- Stateless: new Bash instance per `exec()` call (no persistent shell state between calls)
- No custom command registration (no `ScriptedTool` equivalent from CLI)
- VFS sync writes files via temp script, reads back via stdout
- Suitable for simple script execution, not for stateful shell sessions

### 3. PHP: Replace with `BashkitCLIDriver`

Same pattern as TypeScript — CLI subprocess driver.

**Files to modify/create:**
- `src/php/BashkitCLIDriver.php` — new driver
- `src/php/BashkitDriver.php` — update resolver
- `tests/php/BashkitCLIDriverTest.php` — new tests
- Delete: `src/php/BashkitIPCDriver.php`, `src/php/BashkitNativeDriver.php`
- Delete: corresponding test files

### 4. Update Resolver (`BashkitDriver`)

**All languages:** Simplify the resolver.

- **Python**: `BashkitDriver.resolve()` → check if `bashkit` package is importable → `BashkitPythonDriver` or error
- **TypeScript/PHP**: `BashkitDriver.resolve()` → check if `bashkit` binary is on PATH → `BashkitCLIDriver` or error

No more native-vs-IPC fallback chain — each language has exactly one real path.

### 5. Update Documentation

- **ADR 0027**: Update to reflect actual bashkit capabilities and our revised approach. Document the gap between our original design and reality.
- **Design doc**: Archive `docs/plans/2026-03-07-bashkit-ipc-driver.md` and `docs/plans/2026-03-07-bashkit-native-driver.md` with a note about the fictional API issue
- **README/guides**: Update bashkit sections to reflect real installation requirements (`pip install bashkit` for Python, `cargo install bashkit-cli` for TS/PHP)

### 6. Delete Fictional Code

Remove all code built against non-existent APIs:
- `BashkitIPCDriver` (all languages) — `bashkit-cli --jsonrpc` doesn't exist
- `BashkitNativeDriver` (all languages) — `libashkit` C library doesn't exist
- C API type definitions, library discovery logic, C callback wrappers
- Bidirectional JSON-RPC event loop
- All associated test files (mock-based tests against fictional APIs)

---

## Verification

1. **Python**: `pip install bashkit` then run `BashkitPythonDriver` tests
   - Exec basic commands, verify stdout/stderr/exit_code
   - Write file via our VFS, exec `cat` in bashkit, verify content synced
   - Exec `echo x > /file` in bashkit, verify file appears in our VFS
   - Register custom command, exec script that uses it
   - Run `.venv/bin/pytest tests/python/ -q`

2. **TypeScript/PHP**: Install `bashkit-cli` then run CLI driver tests
   - Exec `echo hello`, verify output
   - One-shot execution (no state persistence between calls — by design)
   - Run `npx vitest run` and `php vendor/bin/phpunit tests/php/ --no-coverage`

3. **Contract compliance**: Existing `ShellDriver` contract tests should pass with new drivers
4. **Docs**: Review updated ADR 0027 for accuracy
