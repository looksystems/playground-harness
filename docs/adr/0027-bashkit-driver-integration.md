# ADR 0027: Bashkit Driver Integration

## Status

Revised — replaced fictional IPC/Native drivers with real implementations (2026-03-09)

## Context

ADR 0026 introduced `ShellDriver` and `FilesystemDriver` contracts with a swappable driver architecture. The "builtin" driver wraps our existing pure-language shell implementations. We need to define how [bashkit](https://github.com/everruns/bashkit), a Rust-based sandboxed POSIX shell, integrates as a driver across Python, TypeScript, and PHP.

### What bashkit provides

- POSIX-compliant shell, 100+ builtins implemented in Rust
- Virtual filesystem (`InMemoryFs`, `OverlayFs`, `MountableFs`)
- Resource limits (max commands, loop iterations, function depth)
- **Python bindings** via PyO3 (`bashkit-python`): `Bash`, `BashTool`, `ScriptedTool` classes
- **CLI tool** (`bashkit -c 'command'`) for subprocess-based execution
- **MCP server** (`bashkit mcp`) for Model Context Protocol integration
- No C shared library, no TypeScript bindings, no PHP bindings

### How everruns integrates (reference architecture)

The bashkit authors (everruns) integrate at the Rust level: direct library dependency with a `SessionFileSystemAdapter` that implements bashkit's `FileSystem` trait, delegating reads/writes to a PostgreSQL-backed session store. This provides zero-overhead VFS sync. Their Python/TypeScript SDK is a pure HTTP client — bash execution is entirely server-side.

### Previous approach (superseded)

The original Phase 2 and Phase 3 implementations were built against APIs that do not exist in bashkit:
- **BashkitIPCDriver** assumed a `bashkit-cli --jsonrpc` mode with bidirectional JSON-RPC — this mode does not exist
- **BashkitNativeDriver** assumed a `libashkit` shared C library with a C API (`bashkit_create`, `bashkit_exec`, etc.) — no such library exists
- Both were tested entirely against mock objects, never against real bashkit

These have been replaced with implementations using bashkit's actual APIs.

## Decision

### Python: `BashkitPythonDriver` (PyO3 bindings)

Wraps the `bashkit` Python package (PyO3-compiled native extension):

- Uses `bashkit.Bash` for basic execution
- Switches to `bashkit.ScriptedTool` when custom commands are registered (callbacks become bash builtins)
- **VFS sync**: Hybrid lazy — tracks dirty files via `_DirtyTrackingFS` wrapper, syncs only changed files before exec, diffs bashkit VFS after exec
- **Custom commands**: `register_command(name, handler)` maps to `ScriptedTool.add_tool()` with signature adaptation

```python
from bashkit import Bash, ScriptedTool

class BashkitPythonDriver(ShellDriver):
    def exec(self, command):
        self._sync_dirty_to_bashkit()
        result = self._bash.execute_sync(command)
        self._sync_changes_from_bashkit()
        return ExecResult(...)
```

**Install**: `pip install bashkit`

### TypeScript & PHP: `BashkitCLIDriver` (CLI subprocess)

Uses `bashkit -c 'command'` for one-shot execution:

- Stateless: new bashkit process per `exec()` call
- No custom command support (commands registered locally but unavailable in subprocess)
- Suitable for simple script execution

```typescript
class BashkitCLIDriver implements ShellDriver {
    exec(command: string): ExecResult {
        const result = execSync(`bashkit -c ${JSON.stringify(command)}`);
        return { stdout: result, stderr: "", exitCode: 0 };
    }
}
```

**Install**: `cargo install bashkit-cli`

### Resolver

Each language has a single resolution path:

| Language | Resolver checks | Returns |
|----------|----------------|---------|
| Python | `import bashkit` succeeds | `BashkitPythonDriver` |
| TypeScript | `which bashkit` succeeds | `BashkitCLIDriver` |
| PHP | `which bashkit` succeeds | `BashkitCLIDriver` |

### Per-Language Capabilities

| Capability | Python | TypeScript | PHP |
|-----------|--------|------------|-----|
| In-process execution | Yes (PyO3) | No (subprocess) | No (subprocess) |
| State persistence between exec() | Yes | No | No |
| Custom command callbacks | Yes (ScriptedTool) | No | No |
| VFS sync | Hybrid lazy (bidirectional) | Preamble/epilogue (bidirectional) | Preamble/epilogue (bidirectional) |
| Async support | Yes (execute/execute_sync) | Sync only | Sync only |

### VFS Sync Strategies

**Python (in-process):**
- `_DirtyTrackingFS` wrapper tracks written/removed files
- Before exec: only dirty files are synced to bashkit via base64-encoded commands
- After exec: a single batched `find + base64` command reads all bashkit files back, diffs against VFS, and applies changes
- Content encoding uses base64 to handle special characters (quotes, backslashes, percent signs, newlines)

**TypeScript / PHP (CLI subprocess):**
- Same `DirtyTrackingFS` wrapper pattern as Python
- Before exec (preamble): dirty files are injected as base64-encoded shell commands prepended to the user's command
- After exec (epilogue): a `find + base64` command is appended after the user's command with a unique marker separator
- The driver parses stdout to split user output from sync data using the marker
- Exit code is preserved by capturing `$?` before the epilogue runs
- Shell state (variables, functions) still does NOT persist between exec() calls — each invocation is a new subprocess

## Consequences

- Python gets the richest integration: in-process, stateful, with custom command support
- TypeScript and PHP get VFS sync via preamble/epilogue — files persist between exec() calls but shell state does not
- The `ShellDriver` contract from Phase 1 remains unchanged — all drivers implement the same interface
- `registerCommand` in TS/PHP stores handlers locally but they won't be available in the bashkit subprocess

## Future Directions

- **bashkit MCP** (`bashkit mcp`) currently provides a single stateless `bash` tool — insufficient for stateful sessions or VFS sync. If bashkit adds session support or filesystem tools to its MCP server upstream, a `BashkitMCPDriver` could provide Python-equivalent capabilities for TS/PHP without subprocess overhead.
- The **everruns pattern** (Rust-level `FileSystem` trait implementation via `SessionFileSystemAdapter`) is the gold standard for VFS integration but requires direct Rust library dependency. This is not feasible for our multi-language architecture.
- The `ShellDriver` contract already supports plugging in new driver types without API changes — a future MCP or napi-rs driver would be a drop-in replacement.
