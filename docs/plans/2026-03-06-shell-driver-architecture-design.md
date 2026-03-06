# Shell Driver Architecture Design

## Problem

The virtual shell and filesystem are implemented independently in Python, TypeScript, and PHP (~2500 lines each). This creates:

- **Maintenance burden** — bugs fixed in one language but not others, feature drift
- **Correctness risk** — three implementations may diverge on edge cases
- **Performance ceiling** — interpreted language implementations can't match native speed

## Goals

1. Introduce a driver/adapter pattern so the shell backend is swappable
2. Retain existing pure-language implementations as the default (zero-dependency) driver
3. Enable native backends (e.g., bashkit) as optional drivers for POSIX compliance and performance
4. Keep the `agent.fs` and `agent.exec()` APIs unchanged from the user's perspective

## Non-Goals

- Replacing the existing shell implementations (they become the "builtin" driver)
- Conformance testing between drivers (different drivers, different capabilities)
- Building the bashkit binding crates (that's a separate effort)

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Pattern | Driver/adapter | Keeps existing code, adds optionality |
| Driver selection | Global default + per-agent override | Flexibility without complexity |
| Driver interface | Open/public — users can implement custom drivers | Extensibility for unforeseen backends (Docker, WASM, etc.) |
| Contracts | Two separate: `FilesystemDriver` + `ShellDriver` | Interface segregation; mix-and-match FS and shell backends |
| VFS ownership | Host owns, sync before/after exec | Works with both FFI and IPC; `writeLazy()` works naturally; simple mental model |
| Bashkit resolution | Single "bashkit" driver auto-selects native ext vs IPC | User doesn't think about transport — just says `driver("bashkit")` |
| Custom command callbacks (IPC) | Bidirectional JSON-RPC | Only approach that preserves full composability (custom commands in pipes, redirects, control flow) |

---

## Contracts

### FilesystemDriver

The filesystem contract. The builtin implementation is the current `VirtualFS` (flat dict, lazy providers, path normalization).

```
FilesystemDriver {
    write(path: string, content: string)
    writeLazy(path: string, provider: () -> string)
    read(path: string) -> string
    readText(path: string) -> string
    exists(path: string) -> bool
    remove(path: string)
    isDir(path: string) -> bool
    listdir(path: string) -> string[]
    find(root: string, pattern: string) -> string[]
    stat(path: string) -> {path, type, size?}
    clone() -> FilesystemDriver
}
```

### ShellDriver

The shell interpreter contract. Receives a `FilesystemDriver` to operate on.

```
ShellDriver {
    fs: FilesystemDriver
    cwd: string
    env: dict<string, string>
    exec(command: string) -> ExecResult
    registerCommand(name: string, handler: CommandHandler)
    unregisterCommand(name: string)
    clone() -> ShellDriver
}

ExecResult {
    stdout: string
    stderr: string
    exitCode: int
}

CommandHandler = (args: string[], stdin: string?) -> string
```

### Driver Resolution

```
ShellDriverFactory {
    static default: string = "builtin"
    static resolve(name: string, fs: FilesystemDriver) -> ShellDriver
    static register(name: string, factory: (FilesystemDriver) -> ShellDriver)
}
```

---

## Driver Implementations

### Builtin Driver (default)

The current pure-language shell and VirtualFS. No external dependencies.

```
BuiltinFilesystemDriver  = current VirtualFS class (dict-backed, lazy providers)
BuiltinShellDriver       = current Shell class (tokenizer, parser, evaluator, 30 builtins)
```

No code changes needed — the existing classes just implement the new contracts.

### Bashkit Driver (optional)

A single driver name that auto-resolves between native extension and IPC:

```
BashkitDriver.resolve(fs):
    if bashkit native extension available:
        return BashkitNativeDriver(fs)
    else if bashkit-cli binary on PATH:
        return BashkitIPCDriver(fs)
    else:
        raise "bashkit not found — install the extension or binary"
```

#### Native Extension Path

Uses per-language binding libraries to call bashkit in-process:

| Language | Binding | Callable storage | GIL/thread handling |
|----------|---------|-----------------|---------------------|
| Python | PyO3 | `Py<PyAny>` | `Python::attach()` to acquire GIL |
| TypeScript | napi-rs | `ThreadsafeFunction` | Schedules on Node event loop |
| PHP | ext-php-rs | `PhpClosure` | Single-threaded, no concerns |

**VFS sync (host-owns model):**

```
exec(command):
    1. Snapshot host FilesystemDriver → serialize all files
    2. Load snapshot into bashkit's InMemoryFs
    3. Run command in bashkit
    4. Diff bashkit's FS after execution
    5. Apply changes back to host FilesystemDriver
    6. Return ExecResult
```

**Custom command callbacks (native):**

```rust
// Each binding crate wraps host-language callables into this signature:
type CommandCallback = Box<dyn Fn(&ToolArgs) -> Result<String, String> + Send + Sync>;

// Python example:
let callback = move |args: &ToolArgs| -> Result<String, String> {
    Python::attach(|py| {
        let params = json_to_py(py, &args.params);
        let result = py_cb.call1(py, (params, args.stdin))?;
        result.extract::<String>(py)
    })
};
```

#### IPC Fallback Path

Spawns `bashkit-cli` as a subprocess, communicates via JSON-RPC over stdin/stdout.

**Protocol:**

```
Host → bashkit-cli:  {"id":1, "method":"exec", "params":{"cmd":"...", "fs":{...}}}
bashkit-cli → Host:  {"id":1, "result":{"stdout":"...", "exitCode":0, "fs_changes":{...}}}
```

**VFS sync (serialized in request/response):**

```
exec(command):
    1. Serialize host FilesystemDriver files as JSON dict
    2. Send exec request with FS snapshot
    3. Read responses in event loop:
       - If callback request → run registered handler, send response
       - If exec result → apply fs_changes to host FS, return ExecResult
```

**Custom command callbacks (bidirectional JSON-RPC):**

```
Host → bashkit:  {"id":1, "method":"exec", "params":{"cmd":"echo hi | my-cmd"}}
bashkit → Host:  {"id":100, "method":"invoke_command", "params":{"name":"my-cmd", "args":[], "stdin":"hi"}}
Host → bashkit:  {"id":100, "result":"processed: hi"}
bashkit → Host:  {"id":1, "result":{"stdout":"processed: hi", "exitCode":0}}
```

The host runs a simple event loop that dispatches callback requests to registered handlers and resolves pending exec calls when results arrive.

---

## Integration with HasShell Mixin

The `HasShell` mixin wires the driver into the agent. Its API doesn't change — only the internal wiring.

```
HasShell mixin:
    - Reads driver config (per-agent or global default)
    - Creates FilesystemDriver + ShellDriver via factory
    - Exposes agent.fs, agent.shell, agent.exec()
    - Delegates registerCommand/unregisterCommand to ShellDriver
    - Emits hooks (shell_call, shell_result, etc.) as before
```

**Per-agent override via builder:**

```python
agent = StandardAgent.build("gpt-4") \
    .driver("bashkit") \
    .create()
```

**Global default:**

```python
ShellDriverFactory.default = "bashkit"
```

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────┐
│  HasShell Mixin                                     │
│                                                     │
│  agent.fs  → FilesystemDriver                       │
│  agent.shell → ShellDriver                          │
│  agent.exec(cmd) → shell.exec(cmd)                  │
│                                                     │
│  ShellDriverFactory.resolve(config, fs) → driver    │
└───────────────┬─────────────────────────────────────┘
                │
        ┌───────┴───────┐
        │               │
   "builtin"        "bashkit"
        │               │
        ▼               ▼
┌──────────────┐  ┌─────────────────────────┐
│ BuiltinShell │  │ BashkitDriver.resolve() │
│ BuiltinFS    │  │                         │
│              │  │  ┌─ native ext? ──────┐ │
│ Current impl │  │  │  PyO3 / napi-rs /  │ │
│ No changes   │  │  │  ext-php-rs        │ │
│              │  │  └────────────────────┘ │
│              │  │                         │
│              │  │  ┌─ fallback ─────────┐ │
│              │  │  │  bashkit-cli       │ │
│              │  │  │  JSON-RPC over     │ │
│              │  │  │  stdin/stdout      │ │
│              │  │  └────────────────────┘ │
└──────────────┘  └─────────────────────────┘

Both satisfy:
  FilesystemDriver { write, read, exists, ... }
  ShellDriver { exec, registerCommand, ... }
```

---

## Data Flow: Custom Command Over IPC

```
Python                              bashkit-cli
──────                              ──────────

agent.register_command(
  "my-cmd", handler)
    │
    ├─ store handler locally
    └─ send register ──────────►  register "my-cmd" as external

agent.exec(
  "echo hi | my-cmd")
    │
    ├─ sync VFS snapshot
    └─ send exec ──────────────►  tokenize → parse → evaluate
                                    │
                                    ├─ "echo hi" → stdout: "hi"
                                    ├─ pipe to "my-cmd"
                                    ├─ "my-cmd" is external
                                    │
                   invoke_command ◄──┘  {"name":"my-cmd","stdin":"hi"}
    │
    ├─ run handler("hi")
    ├─ result: "processed: hi"
    │
    └─ send result ────────────►  continue evaluation
                                    │
                   exec result  ◄───┘  {"stdout":"processed: hi"}
    │
    └─ apply FS changes
    └─ return ExecResult
```

---

## Existing Projects

### bashkit (everruns/bashkit)

Rust-based sandboxed bash interpreter for multi-tenant environments. The strongest candidate for a native backend driver.

- POSIX-compliant (IEEE 1003.1-2024), 100+ builtins
- Virtual filesystem: InMemoryFs, OverlayFs, MountableFs
- Resource limits (max commands, loop iterations, function depth)
- Python bindings exist (PyO3), with `ScriptedTool.add_tool()` for custom command callbacks
- No TypeScript or PHP bindings yet
- Young project (~3 weeks since HN announcement)
- Crate structure: bashkit (core), bashkit-python, bashkit-cli, bashkit-eval, bashkit-bench

### mvdan/sh

Go-based shell parser/formatter/interpreter. Mature (powers shfmt).

- POSIX + Bash support, virtual FS handler hooks (OpenHandler, ReadDirHandler, StatHandler, ExecHandler)
- Go runtime overhead (~10MB+) makes shared library approach heavier than Rust
- Could serve as an alternative backend driver

### Other

- Flash (raphamorim/flash) — Rust POSIX shell, younger, no built-in VFS hooks
- rust-vfs (manuel-woelker/rust-vfs) — standalone virtual filesystem crate

---

## Implementation Scope

### Phase 1: Contracts

Define `FilesystemDriver` and `ShellDriver` interfaces in all three languages. Wrap existing `VirtualFS` and `Shell` as `BuiltinFilesystemDriver` and `BuiltinShellDriver`. Add `ShellDriverFactory` with global default + per-agent override. No behavioral change — just the abstraction layer.

### Phase 2: Bashkit IPC Driver

Build `BashkitIPCDriver` in all three languages. Requires `bashkit-cli` with a `--jsonrpc` mode (may need contributing upstream or forking). Includes VFS sync (snapshot in request, changes in response) and bidirectional JSON-RPC for custom command callbacks.

### Phase 3: Bashkit Native Drivers

Build per-language binding crates:
- `bashkit-node` (napi-rs) — ~300 lines
- `bashkit-php` (ext-php-rs) — ~300 lines
- Python bindings already exist via `bashkit-python`

Build `BashkitNativeDriver` wrappers in each language that use the binding when available, with automatic fallback to IPC.

---

## Open Questions

1. **bashkit-cli JSON-RPC mode** — does bashkit-cli support this today, or does it need to be added?
2. **VFS sync granularity** — full snapshot vs dirty-tracking. Start with full snapshot, optimize later if needed.
3. **Upstream contribution vs fork** — should we contribute Node/PHP bindings to bashkit, or maintain them separately?
4. **ShellRegistry integration** — how do named shell configurations (templates) interact with driver selection? Likely: registry stores driver name + config alongside the shell template.
