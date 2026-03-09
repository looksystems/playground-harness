# Bashkit VFS Sync Improvements Plan

## Context

Live integration testing revealed critical bugs in the Python BashkitPythonDriver's VFS sync, and confirmed that TS/PHP CLI drivers have no VFS sync at all. Investigation of the bashkit MCP server and everruns reference architecture informed the approach:

- **bashkit MCP** (`bashkit mcp`): Single stateless `bash` tool, no filesystem tools, no sessions — too minimal for stateful integration
- **everruns pattern**: Direct Rust library with `SessionFileSystemAdapter` implementing bashkit's `FileSystem` trait, zero-overhead delegation to DB — ideal but requires Rust-level integration we can't do
- **Conclusion**: Fix Python sync bugs, add CLI-based VFS sync to TS/PHP using preamble/epilogue pattern, defer MCP approach

---

## Phase 1: Fix Python VFS Sync (Critical)

**File**: `src/python/bashkit_python_driver.py`

### Bug 1: `_sync_bashkit_to_vfs()` never called
`exec()` (line 202) syncs TO bashkit but never calls `_sync_bashkit_to_vfs()` after execution. Files created/modified by bashkit commands never propagate back to host VFS.

**Fix**: Add `self._sync_bashkit_to_vfs(executor)` after `executor.execute_sync(command)` in `exec()`.

### Bug 2: Incomplete printf escaping
`_sync_dirty_to_bashkit()` (line 141) only escapes `\` and `'` — misses control chars, `%` (printf interprets), binary content.

**Fix**: Switch to base64:
```python
import base64
encoded = base64.b64encode(content.encode()).decode()
commands.append(f"mkdir -p $(dirname '{path}') && printf '%s' '{encoded}' | base64 -d > '{path}'")
```

### Bug 3: Inefficient after-sync
`_sync_bashkit_to_vfs()` runs `find / -type f` then individual `cat` per file (N+1 shell executions).

**Fix**: Batch into single command with marker-delimited output:
```python
script = "find / -type f -exec sh -c 'for f; do printf \"===FILE:%s===\\n\" \"$f\"; cat \"$f\"; done' _ {} +"
result = executor.execute_sync(script)
# Parse marker-delimited output
```

### Tests to add
- `tests/python/test_bashkit_python_driver.py`: Unit tests for sync-back (verify `driver.fs.read_text()` reflects bashkit changes)
- `tests/python/test_bashkit_live.py`: Live round-trip tests (`driver.fs.write()` → exec cat → verify; exec echo > file → `driver.fs.read_text()` → verify)

### Docs to update
- `docs/guides/bashkit.md`: Update VFS sync section to reflect base64 approach and bidirectional sync
- `docs/adr/0027-bashkit-driver-integration.md`: Update VFS sync description

---

## Phase 2: Add VFS Sync to TS/PHP CLI Drivers

Same preamble/epilogue pattern — pack VFS state into each CLI invocation.

### TypeScript: `src/typescript/bashkit-cli-driver.ts`

1. **Add dirty tracking**: Port `_DirtyTrackingFS` wrapper pattern from Python (wrap `BuiltinFilesystemDriver`, track writes/removes)

2. **Sync TO bashkit (preamble)**: Before exec, generate script that creates dirty files via base64:
   ```typescript
   const preamble = this.buildSyncPreamble(); // base64 file creation commands
   const fullScript = preamble ? `${preamble}\n${command}` : command;
   ```

3. **Sync FROM bashkit (epilogue)**: Append file-dump epilogue with unique marker:
   ```typescript
   const marker = `__HARNESS_FS_SYNC_${Date.now()}__`;
   const epilogue = `; echo '${marker}'; find / -type f -exec sh -c '...' _ {} +`;
   ```
   Parse output to split user stdout from sync data. Strip sync data from returned stdout.

4. **Limitation**: Shell state (variables, functions) still doesn't persist — documented as known limitation.

### PHP: `src/php/BashkitCLIDriver.php`

Mirror TypeScript implementation exactly.

### Tests
- `tests/typescript/bashkit-cli-driver.test.ts`: Add sync tests with `_execOverride` mock
- `tests/typescript/bashkit-live.test.ts`: Add live VFS round-trip tests
- `tests/php/BashkitCLIDriverTest.php`: Add sync tests with `execOverride` mock
- `tests/php/BashkitLiveTest.php`: Add live VFS round-trip tests

### Docs
- `docs/guides/bashkit.md`: Update capabilities table (VFS sync: all languages)
- `docs/adr/0027-bashkit-driver-integration.md`: Update per-language capabilities table
- `docs/comparison.md`: Update if needed

---

## Phase 3: Document MCP Assessment (No Code)

Add a "Future Directions" section to ADR 0027:

- bashkit MCP is currently a single stateless `bash` tool — insufficient for stateful sessions or VFS sync
- If bashkit adds session support or FS tools to MCP upstream, a `BashkitMCPDriver` could provide Python-equivalent capabilities for TS/PHP
- The everruns pattern (Rust-level `FileSystem` trait implementation) is the gold standard but requires direct library integration
- The `ShellDriver` contract already supports plugging in new driver types without API changes

---

## File Summary

| File | Change |
|------|--------|
| `src/python/bashkit_python_driver.py` | Fix 3 VFS sync bugs |
| `src/typescript/bashkit-cli-driver.ts` | Add dirty tracking + preamble/epilogue sync |
| `src/php/BashkitCLIDriver.php` | Add dirty tracking + preamble/epilogue sync |
| `tests/python/test_bashkit_python_driver.py` | Add sync-back unit tests |
| `tests/python/test_bashkit_live.py` | Add live VFS round-trip tests |
| `tests/typescript/bashkit-cli-driver.test.ts` | Add sync tests |
| `tests/typescript/bashkit-live.test.ts` | Add live VFS sync tests |
| `tests/php/BashkitCLIDriverTest.php` | Add sync tests |
| `tests/php/BashkitLiveTest.php` | Add live VFS sync tests |
| `docs/adr/0027-bashkit-driver-integration.md` | Update sync docs + future directions |
| `docs/guides/bashkit.md` | Update VFS sync section |

---

## Verification

1. `.venv/bin/pytest tests/python/ -q` — all pass including new sync tests
2. `npx vitest run` — all pass including new sync tests
3. `php vendor/bin/phpunit tests/php/ --no-coverage` — all pass including new sync tests
4. Live VFS round-trip in all 3 languages:
   - `driver.fs.write("/test.txt", "hello")` → exec `cat /test.txt` → stdout contains "hello"
   - exec `echo world > /created.txt` → `driver.fs.read_text("/created.txt")` returns "world\n"
5. Special character handling: files with newlines, quotes, backslashes, unicode sync correctly
