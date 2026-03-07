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
- Building per-language Rust binding crates (replaced by shared C library FFI approach)

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

#### Native FFI Path

All three languages call bashkit through a single shared C library (`libashkit.so`/`libashkit.dylib`/`bashkit.dll`) using each language's built-in FFI mechanism:

| Language | FFI Mechanism | Callback Support | Library Loading |
|----------|--------------|-----------------|----------------|
| Python | `ctypes` (stdlib) | `ctypes.CFUNCTYPE` | `ctypes.CDLL` |
| TypeScript | `ffi-napi` + `ref-napi` | `ffi.Callback` | `ffi.Library()` |
| PHP | `FFI` (built-in 7.4+) | `FFI` closure binding | `FFI::cdef()` |

**C API surface:**

```c
typedef struct bashkit_ctx bashkit_t;
typedef const char* (*bashkit_command_cb)(const char* args_json, void* userdata);

bashkit_t*   bashkit_create(const char* config_json);
void         bashkit_destroy(bashkit_t* ctx);
const char*  bashkit_exec(bashkit_t* ctx, const char* request_json);
void         bashkit_register_command(bashkit_t* ctx, const char* name, bashkit_command_cb cb, void* userdata);
void         bashkit_unregister_command(bashkit_t* ctx, const char* name);
void         bashkit_free_string(const char* str);
```

**VFS sync (host-owns model):**

```
exec(command):
    1. Snapshot host FilesystemDriver → serialize all files as dict/object
    2. Send snapshot in exec request JSON
    3. Run command in bashkit
    4. Receive fs_changes (created/deleted) in response
    5. Apply changes back to host FilesystemDriver
    6. Return ExecResult
```

**Custom command callbacks (native):**

Each language wraps host-language callables into a C function pointer with the `(args_json, userdata)` signature. The callback receives `{"name":"...","args":[...],"stdin":"..."}` as JSON and returns stdout as a string. Python uses `ctypes.CFUNCTYPE` with prevented GC via stored references, TypeScript uses `ffi.Callback`, and PHP uses `FFI` closure binding.

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
│              │  │  ┌─ native FFI? ──────┐ │
│ Current impl │  │  │  ctypes / ffi-napi │ │
│ No changes   │  │  │  / PHP FFI        │ │
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

### Phase 3: Bashkit Native Drivers (Complete)

Instead of per-language binding crates (PyO3, napi-rs, ext-php-rs), we chose a shared C library approach: a single `libashkit` shared library (`libashkit.so`/`libashkit.dylib`/`bashkit.dll`) called via each language's built-in FFI mechanism:

- **Python**: `ctypes` (stdlib) — `ctypes.CFUNCTYPE` for callbacks, `ctypes.CDLL` for loading
- **TypeScript**: `ffi-napi` + `ref-napi` — `ffi.Callback` for callbacks, `ffi.Library()` for loading
- **PHP**: `FFI` (built-in 7.4+) — `FFI` closure binding for callbacks, `FFI::cdef()` for loading

This eliminates the need to build and maintain three separate Rust crates — one shared library serves all three languages.

`BashkitNativeDriver` is implemented in all three languages with: library discovery (`BASHKIT_LIB_PATH` env var → platform library paths → standard paths), VFS snapshot sync (host-owns model), C callback wrapping for custom commands, and `clone()` support. `BashkitDriver.resolve()` now prefers native FFI over IPC fallback.

---

## Open Questions

1. **bashkit-cli JSON-RPC mode** — does bashkit-cli support this today, or does it need to be added?
2. **VFS sync granularity** — full snapshot vs dirty-tracking. Start with full snapshot, optimize later if needed.
3. **Upstream contribution vs fork** — should we contribute Node/PHP bindings to bashkit, or maintain them separately?
4. **ShellRegistry integration** — how do named shell configurations (templates) interact with driver selection? Likely: registry stores driver name + config alongside the shell template.
