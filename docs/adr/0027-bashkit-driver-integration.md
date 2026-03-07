# ADR 0027: Bashkit Driver Integration

## Status

Proposed

## Context

ADR 0026 introduced `ShellDriver` and `FilesystemDriver` contracts with a swappable driver architecture. The "builtin" driver wraps our existing pure-language shell implementations. We now need to define how an external backend — specifically [bashkit](https://github.com/everruns/bashkit), a Rust-based sandboxed POSIX shell — integrates as a driver across Python, TypeScript, and PHP.

bashkit provides:

- POSIX-compliant shell (IEEE 1003.1-2024), 100+ builtins
- Virtual filesystem (`InMemoryFs`, `OverlayFs`, `MountableFs`)
- Resource limits (max commands, loop iterations, function depth)
- Python bindings via PyO3 (`bashkit-python`), with `ScriptedTool.add_tool()` for custom command callbacks
- CLI tool (`bashkit-cli`) for subprocess-based usage
- No TypeScript or PHP bindings yet

The key challenges are: (1) integrating across three host languages with different FFI capabilities, (2) synchronizing the host-owned VirtualFS with bashkit's internal filesystem, and (3) supporting custom command callbacks registered in the host language but invoked from within bashkit during shell evaluation.

## Decision

Register a single `"bashkit"` driver name that auto-resolves between two transport paths: native extension (in-process FFI) and IPC fallback (subprocess JSON-RPC). The host's `FilesystemDriver` remains the source of truth, with snapshot-based sync before and after each `exec()` call.

### Auto-Resolution

```
ShellDriverFactory.create("bashkit"):
    if native extension available:
        return BashkitNativeDriver(opts)
    elif bashkit-cli binary on PATH:
        return BashkitIPCDriver(opts)
    else:
        raise "bashkit not found"
```

Users say `driver("bashkit")` and get the best available transport. No need to choose between native and IPC explicitly.

### Native Extension Path

Each language uses its ecosystem's Rust binding library to call bashkit in-process:

| Language | Binding Library | Callable Storage | Concurrency Handling |
|----------|----------------|-----------------|---------------------|
| Python | PyO3 (`bashkit-python`) | `Py<PyAny>` | `Python::attach()` to acquire GIL |
| TypeScript | napi-rs (`bashkit-node`) | `ThreadsafeFunction` | Schedules callback on Node event loop |
| PHP | ext-php-rs (`bashkit-php`) | `PhpClosure` | Single-threaded, no special handling |

Python bindings already exist upstream. TypeScript and PHP bindings would need to be built (~300 lines each).

**Custom command callbacks (native):**

When the host registers a command via `register_command(name, handler)`, the driver wraps the host-language callable into bashkit's `CommandCallback` signature:

```rust
type CommandCallback = Box<dyn Fn(&ToolArgs) -> Result<String, String> + Send + Sync>;
```

Each binding crate handles the language-specific callable invocation:

- **Python:** Acquires GIL via `Python::attach()`, converts args to Python objects, calls `Py<PyAny>`, extracts string result
- **TypeScript:** Calls `ThreadsafeFunction` which schedules execution on the Node event loop, awaits the result via a channel
- **PHP:** Calls `PhpClosure` directly (single-threaded, no synchronization needed)

Custom commands registered this way compose fully with bashkit's pipes, redirects, and control flow — they behave identically to built-in commands.

### IPC Fallback Path

Spawns `bashkit-cli` as a long-lived subprocess, communicating via bidirectional JSON-RPC over stdin/stdout. This path works identically across all three languages since it only requires process spawning and JSON serialization.

**Protocol:**

```
Host → bashkit-cli:  {"id":1, "method":"exec", "params":{"cmd":"...", "fs":{...}}}
bashkit-cli → Host:  {"id":1, "result":{"stdout":"...", "exitCode":0, "fs_changes":{...}}}
```

**Custom command callbacks (IPC):**

Bidirectional JSON-RPC enables bashkit to invoke host-registered commands mid-evaluation:

```
Host → bashkit:  {"id":1, "method":"exec", "params":{"cmd":"echo hi | my-cmd"}}
bashkit → Host:  {"id":100, "method":"invoke_command", "params":{"name":"my-cmd", "args":[], "stdin":"hi"}}
Host → bashkit:  {"id":100, "result":"processed: hi"}
bashkit → Host:  {"id":1, "result":{"stdout":"processed: hi", "exitCode":0}}
```

The host runs a simple event loop that dispatches callback requests to locally registered handlers and resolves pending exec calls when results arrive. This preserves full composability — custom commands work in pipes, redirects, and control flow even over IPC.

**Requires:** `bashkit-cli` to support a `--jsonrpc` mode (may need upstream contribution or fork).

### VFS Synchronization (Host-Owns Model)

The host's `FilesystemDriver` is the source of truth. Before each `exec()`, the driver snapshots the host FS and loads it into bashkit. After execution, filesystem changes are diffed and applied back.

```
exec(command):
    1. Snapshot host FilesystemDriver → serialize all files as dict
    2. Load snapshot into bashkit's InMemoryFs
    3. Run command in bashkit
    4. Diff bashkit's FS after execution
    5. Apply changes (creates, updates, deletes) back to host FilesystemDriver
    6. Return ExecResult {stdout, stderr, exitCode}
```

This model was chosen over alternatives (bashkit-owns, shared-memory) because:

- Works identically for both native and IPC transports
- `write_lazy()` providers resolve naturally (host reads them during snapshot)
- No shared mutable state between host and bashkit
- Simple mental model: host FS is always authoritative

Full snapshot is used initially. Dirty-tracking optimization can be added later if profiling shows serialization as a bottleneck.

### Per-Language Summary

| Aspect | Python | TypeScript | PHP |
|--------|--------|------------|-----|
| **Native binding** | `bashkit-python` (PyO3, exists) | `bashkit-node` (napi-rs, to build) | `bashkit-php` (ext-php-rs, to build) |
| **IPC fallback** | `subprocess.Popen` + JSON-RPC | `child_process.spawn` + JSON-RPC | `proc_open` + JSON-RPC |
| **Callback mechanism (native)** | GIL acquire → call `Py<PyAny>` | `ThreadsafeFunction` → event loop | Direct `PhpClosure` call |
| **Callback mechanism (IPC)** | Bidirectional JSON-RPC | Bidirectional JSON-RPC | Bidirectional JSON-RPC |
| **VFS sync** | Dict serialization | Object serialization | Array serialization |
| **Async considerations** | `asyncio` subprocess for IPC | Native async subprocess | Blocking (synchronous PHP) |
| **Driver class** | `BashkitDriver(ShellDriver)` | `BashkitDriver implements ShellDriver` | `BashkitDriver implements ShellDriverInterface` |
| **Install** | `pip install bashkit` | `npm install bashkit` | `pecl install bashkit` (native only) |

## Consequences

- A single `"bashkit"` driver name works across all three languages with automatic transport selection
- Users get POSIX compliance and 100+ builtins without changing their agent code — just `driver("bashkit")`
- Custom commands registered via `register_command()` work transparently over both native and IPC paths
- The IPC path ensures bashkit is usable even when native extensions can't be installed (CI environments, restricted platforms)
- Native extensions require per-language binding crates; Python exists, TypeScript and PHP need to be built
- `bashkit-cli` may need a `--jsonrpc` mode contributed upstream
- VFS snapshot serialization adds per-exec overhead proportional to filesystem size; acceptable for typical agent workloads, optimizable later via dirty-tracking
