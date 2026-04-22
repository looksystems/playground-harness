# Bashkit Cross-Language Parity: In-Process Integration Options

## Context

Python has rich in-process bashkit integration via PyO3 (`BashkitPythonDriver`): stateful shell sessions, custom commands as builtins via `ScriptedTool`, no subprocess overhead. TypeScript and PHP are limited to stateless CLI subprocess wrapping (`bashkit -c`), which means no shell state persistence between `exec()` calls and no custom command support.

Prior plans (`2026-03-07-bashkit-ipc-driver.md` and `2026-03-07-bashkit-native-driver.md`) were built against fictional APIs — a `bashkit-cli --jsonrpc` mode and a `libashkit` C library that don't exist. Those were replaced with real implementations in the revision plan (`2026-03-09-bashkit-integration-revision.md`). This document evaluates two feasible approaches to close the TS/PHP capability gap.

### Current State (per ADR 0027)

| Capability | Python | TypeScript | PHP |
|-----------|--------|------------|-----|
| In-process execution | Yes (PyO3) | No (subprocess) | No (subprocess) |
| State persistence between exec() | Yes | No | No |
| Custom command callbacks | Yes (ScriptedTool) | No | No |
| VFS sync | Preamble/epilogue | Preamble/epilogue | Preamble/epilogue |

---

## Option A: C FFI Shared Library (`bashkit-ffi`)

### What It Is

A new Rust crate (`bashkit-ffi`) that wraps bashkit core and exports an `extern "C"` API as a shared library (`.so`/`.dylib`/`.dll`). TypeScript and PHP load this library via their respective FFI mechanisms and call bashkit in-process.

### Upstream Work Required

A new `bashkit-ffi` crate must be created upstream (or maintained as a fork):

- `cdylib` Cargo target producing a platform-specific shared library
- `cbindgen`-generated C header (`bashkit.h`) for consumers
- ~10-12 exported functions covering the API surface below
- Stable C ABI commitment — function signatures, struct layouts, error conventions

### API Surface

Opaque handle-based design (callers never see Rust internals):

```c
// Lifecycle
BashHandle*  bashkit_new(const char* cwd);
void         bashkit_free(BashHandle* handle);

// Execution
BashResult*  bashkit_exec(BashHandle* handle, const char* command);
const char*  bashkit_result_stdout(BashResult* result);
const char*  bashkit_result_stderr(BashResult* result);
int          bashkit_result_exit_code(BashResult* result);
void         bashkit_result_free(BashResult* result);

// Tool/custom commands
ToolHandle*  bashkit_tool_new(BashHandle* handle);
void         bashkit_tool_register(ToolHandle* handle, const char* name,
                                   CommandCallback callback, void* userdata);
void         bashkit_tool_free(ToolHandle* handle);

// Environment
void         bashkit_set_env(BashHandle* handle, const char* key, const char* val);
const char*  bashkit_get_env(BashHandle* handle, const char* key);
```

### Our Work Per Language

**Node.js (TypeScript):**
- Use [`koffi`](https://koffi.dev/) (pure-JS FFI, no native compilation needed) to load the shared library
- Bind each C function to a JS-callable wrapper
- `BashkitFFIDriver` implementing `ShellDriver` contract

**PHP:**
- Use built-in `FFI` extension (available since PHP 7.4)
- Load the shared library and declare functions from the C header
- `BashkitFFIDriver` implementing `ShellDriverInterface`

### Callback Mechanism

Custom command callbacks cross the FFI boundary via C function pointers:

1. Host registers a callback function pointer + userdata with `bashkit_tool_register`
2. When bashkit encounters the command during execution, it invokes the function pointer
3. Parameters are passed as a JSON-serialized string (avoids complex struct marshalling)
4. Callback returns a JSON-serialized result string
5. `koffi` (Node) and PHP `FFI` both support creating C-callable function pointers from host closures

### Trade-offs

- **Performance**: In-process, no IPC overhead — equivalent to Python's PyO3 integration
- **Memory management**: Manual alloc/free discipline required — every `bashkit_*_new` needs a corresponding `bashkit_*_free`; leaks or double-frees are possible
- **Upstream dependency**: Requires upstream to create and maintain a C ABI crate, or we maintain a fork
- **ABI stability**: Any change to the C API requires updating bindings in both TS and PHP
- **Distribution**: One shared library artifact per platform (linux-x64, darwin-arm64, etc.) must be built and distributed
- **Build complexity**: CI needs cross-compilation or per-platform build matrix for the shared library

---

## Option B: JSON-RPC Daemon (`bashkit-rpc`)

### What It Is

A standalone Rust binary (`bashkit-rpc`) that wraps bashkit core and exposes it via JSON-RPC 2.0 over stdin/stdout (newline-delimited). TS/PHP spawn this process and communicate over pipes — similar to how LSP and DAP work.

### Architecture Layers

```
┌─────────────────────────────────┐
│  Host (TS/PHP)                  │
│  Thin JSON-RPC client           │
│  read/write JSON lines          │
├─────────────────────────────────┤
│  bashkit-rpc binary             │
│  ┌───────────────────────────┐  │
│  │ 3. JSON-RPC transport     │  │  ← bulk of code, stable interface
│  │    parse/dispatch/respond │  │
│  ├───────────────────────────┤  │
│  │ 2. Adapter trait (~100 ln)│  │  ← insulates from bashkit API changes
│  │    maps RPC → bashkit     │  │
│  ├───────────────────────────┤  │
│  │ 1. bashkit core           │  │  ← upstream, untouched
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```

The three-layer design means:
- **Layer 1** (bashkit core) is never modified — just a Cargo dependency
- **Layer 2** (adapter) absorbs upstream API changes in ~100 lines
- **Layer 3** (JSON-RPC transport) is stable and never needs to change for bashkit reasons

### RPC Surface

```jsonc
// Session lifecycle
{ "method": "bash.create",  "params": { "cwd": "/" } }           → { "result": { "id": "sess_1" } }
{ "method": "bash.exec",    "params": { "id": "sess_1", "command": "echo hi" } } → { "result": { "stdout": "hi\n", "exit_code": 0 } }
{ "method": "bash.destroy", "params": { "id": "sess_1" } }       → { "result": null }

// Tool lifecycle
{ "method": "tool.create",           "params": { "id": "sess_1" } }                    → { "result": { "tool_id": "tool_1" } }
{ "method": "tool.register_command", "params": { "tool_id": "tool_1", "name": "fetch" } } → { "result": null }
{ "method": "tool.exec",             "params": { "tool_id": "tool_1", "command": "fetch https://example.com" } } → ...
{ "method": "tool.env",              "params": { "tool_id": "tool_1", "key": "PATH" } }   → { "result": { "value": "/usr/bin" } }
{ "method": "tool.destroy",          "params": { "tool_id": "tool_1" } }                → { "result": null }
```

### Reverse Callbacks (Bidirectional JSON-RPC)

Custom commands require the daemon to call back into the host process:

1. Host sends `tool.exec` with a command that triggers a registered custom command
2. Daemon sends a **request** (not a response) to the host via stdout:
   ```jsonc
   { "jsonrpc": "2.0", "id": "cb_1", "method": "callback.invoke", "params": { "name": "fetch", "args": ["https://example.com"] } }
   ```
3. Host dispatches to its local handler, then writes a **response** back on stdin:
   ```jsonc
   { "jsonrpc": "2.0", "id": "cb_1", "result": { "output": "...", "exit_code": 0 } }
   ```
4. Daemon receives the callback result and continues execution
5. When the command finishes, daemon sends the original `tool.exec` response

This is standard bidirectional JSON-RPC — both sides can initiate requests. The host must multiplex between reading responses to its own requests and handling incoming callback requests.

### Our Work Per Language

**TypeScript:**
- Spawn `bashkit-rpc` via `child_process.spawn`, pipe stdin/stdout
- JSON-RPC client: write JSON lines to stdin, read JSON lines from stdout
- Multiplex callback handling (incoming requests during pending `tool.exec`)
- `BashkitRPCDriver` implementing `ShellDriver`

**PHP:**
- Spawn via `proc_open`, same pipe-based communication
- JSON-RPC client with callback multiplexing
- `BashkitRPCDriver` implementing `ShellDriverInterface`

### Trade-offs

- **No upstream changes**: `bashkit-rpc` is our own binary, depends on bashkit as a library
- **Insulation**: Adapter layer absorbs upstream API changes — transport layer stays stable
- **Automatic memory management**: Process owns all Rust objects; no alloc/free in TS/PHP
- **Debugging**: JSON-RPC messages are human-readable, can be logged/replayed
- **IPC overhead**: ~1ms per exec() call for serialization + pipe I/O (negligible for typical use)
- **Process lifecycle**: Must manage spawning, health-checking, and cleanup of the daemon process
- **Well-understood pattern**: Same architecture as LSP servers, DAP adapters, and many dev tools
- **State persistence**: Daemon process stays alive between exec() calls, so shell state naturally persists

---

## Comparison

| Dimension | Option A: C FFI (`bashkit-ffi`) | Option B: JSON-RPC (`bashkit-rpc`) |
|-----------|--------------------------------|-------------------------------------|
| **Upstream dependency** | High — requires new `bashkit-ffi` crate with C ABI | None — we build `bashkit-rpc` as a dependency consumer |
| **Insulation from changes** | Low — C ABI changes break all consumers | High — adapter layer absorbs changes in ~100 lines |
| **Custom commands** | Yes, via C function pointers | Yes, via bidirectional JSON-RPC callbacks |
| **State persistence** | Yes (in-process, same handle) | Yes (long-lived daemon process) |
| **Performance** | Best — zero IPC overhead | ~1ms per exec() (pipe serialization) |
| **Distribution** | Shared library per platform | Single static binary per platform |
| **Memory safety** | Manual alloc/free — leak/double-free risk | Automatic — Rust process owns everything |
| **Implementation complexity** | Medium — FFI bindings + callback marshalling | Medium — JSON-RPC client + callback multiplexing |
| **Debugging** | Hard — crashes in FFI boundary are opaque | Easy — JSON messages are logged/inspectable |
| **Build complexity** | High — cross-compile shared lib + header gen | Low — standard Rust binary |
| **Language support** | Node.js (`koffi`), PHP (`FFI` ext) | Any language with stdin/stdout pipes |
| **Precedent** | `tree-sitter`, `libgit2` | LSP, DAP, `rust-analyzer` |

---

## Recommendation

**Pursue Option B (JSON-RPC daemon) first.**

Rationale:

1. **No upstream gatekeeping.** We can build and ship `bashkit-rpc` without waiting for or convincing upstream to create and maintain a C ABI crate. This is the single most important differentiator — it removes an external dependency from our timeline.

2. **Change insulation.** The adapter layer pattern means bashkit API changes are absorbed in one place (~100 lines), not propagated across FFI bindings in two languages. Given that bashkit is actively developed, this matters.

3. **Memory safety by default.** No manual alloc/free discipline means no category of bugs that only manifests under certain call patterns in production.

4. **Proven architecture.** stdin/stdout JSON-RPC is the dominant pattern for dev tool integration (LSP, DAP, formatters, linters). The operational characteristics are well-understood.

5. **Performance is sufficient.** ~1ms IPC overhead per exec() is negligible compared to the shell execution time itself. Our agents are not executing thousands of rapid-fire shell commands where this would compound.

6. **Option A remains viable as an optimization.** If profiling later shows IPC overhead matters, a `bashkit-ffi` crate could be introduced alongside the RPC driver. The `ShellDriverFactory` already supports multiple registered drivers — both could coexist with the resolver preferring FFI when available and falling back to RPC.

### Suggested Next Steps

1. Design the `bashkit-rpc` binary (Rust crate structure, adapter trait, JSON-RPC transport)
2. Implement a minimal `bash.create` / `bash.exec` / `bash.destroy` surface
3. Build the TypeScript `BashkitRPCDriver` as the first consumer
4. Add bidirectional callbacks for custom command support
5. Port to PHP
6. Update ADR 0027 with the new driver tier
