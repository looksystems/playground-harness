# Proxy Command Architecture: Feasibility Analysis & Design

## Context

Custom commands registered via `register_command()` only compose with pipes, redirects, and compound expressions in one driver: `BashkitPythonDriver` (where `ScriptedTool.add_tool()` registers handlers as real bashkit builtins). All other drivers — BashkitCLIDriver (TS/PHP), OpenShellGrpcDriver (Python/TS/PHP) — use first-word interception, which means `echo foo | mycmd` bypasses the handler entirely.

**The idea**: Instead of intercepting commands before they reach the shell, inject a **single proxy binary** (multicall-style) into the shell's `$PATH`. When the shell executes `mycmd arg1 arg2`, it runs the proxy, which connects back to a server in the agent/host process, dispatches to the registered handler, and returns the result. Because `mycmd` is a real executable, it composes naturally with everything the shell supports.

---

## Feasibility Assessment

### BashkitCLIDriver (TS/PHP): UNCERTAIN — likely not the right approach

Bashkit is a sandboxed POSIX shell. Each `exec()` spawns `bashkit -c 'cmd'` as a fresh subprocess operating on a virtual filesystem. The critical question: **can bashkit execute external binaries?**

If bashkit's command resolution is pure-virtual (only builtins + virtual filesystem scripts), then there is no `$PATH` to inject into and no `execve()` to call. The proxy binary approach **does not work**.

**Recommendation**: The already-planned `bashkit-rpc` daemon (Option B from the cross-language parity plan) is the correct solution for bashkit. It uses bidirectional JSON-RPC callbacks to make custom commands into real bashkit builtins — the same mechanism that works for Python's ScriptedTool, but out-of-process. The proxy binary approach is **complementary, not competing** — it solves a different driver's problem.

### OpenShellGrpcDriver (Python/TS/PHP): FEASIBLE

OpenShell sandboxes are real Linux containers with real filesystems. External binaries execute normally. The approach works cleanly:

- **Binary injection**: Upload the proxy binary via the existing VFS preamble mechanism (base64-encoded, one-time ~5MB upload on first use)
- **Callback channel (SSH transport)**: SSH remote port forwarding (`-R`) tunnels a Unix domain socket from inside the sandbox back to the host — clean, secure, no open network ports
- **Callback channel (gRPC transport)**: No clean callback path exists without also maintaining an SSH side-channel or requiring sandbox→host TCP access. **gRPC transport initially limited to first-word interception only**
- **Symlinks**: Created via preamble commands, persist across `exec()` calls in the sandbox

### General Feasibility Considerations

| Concern | Assessment |
|---------|-----------|
| **Latency** | ~2-10ms per custom command invocation (process spawn + IPC). Acceptable — comparable to normal command execution overhead |
| **Concurrency** | Pipeline stages run simultaneously; server must handle concurrent requests. Threaded/async server required |
| **stdin buffering** | Current handler contract already buffers full stdin as a string. Proxy does the same. Streaming is a future enhancement |
| **Binary distribution** | Cross-compiled static Go binary (~3-5MB). Ship with harness or upload on first use |
| **Security** | Session token via environment variable authenticates proxy→server requests. SSH forwarding keeps the channel private |
| **PHP server challenge** | PHP's synchronous model makes running a concurrent callback server hard. Requires `pcntl_fork()` or the `parallel` extension |

---

## Architecture Design

### The Proxy Binary

**Language**: Go — static compilation, trivial cross-compilation, small binary (~3-5MB with `CGO_ENABLED=0`), no runtime dependencies.

**Dispatch**: BusyBox-style. `basename(argv[0])` determines the command name. If invoked as the canonical name (`harness-proxy`), takes the command name as `argv[1]`.

**Behavior**:
1. Read `HARNESS_PROXY_SOCK` (socket path or `host:port`) and `HARNESS_PROXY_TOKEN` from environment
2. Determine command name from `argv[0]` (symlink) or `argv[1]` (direct)
3. Buffer all stdin
4. Connect to the callback server via UDS or TCP
5. Send JSON-RPC request: `{"method":"command.exec","params":{"name":"mycmd","args":["arg1"],"stdin":"...","token":"..."}}`
6. Receive response: `{"result":{"stdout":"...","stderr":"...","exitCode":0}}`
7. Write stdout/stderr, exit with handler's exit code

### The Agent-Side Callback Server

A lightweight concurrent server that dispatches incoming proxy requests to registered handlers.

**Per language**:
- **Python**: `socketserver.ThreadingUnixStreamServer` in a background thread (started before `exec()`, runs for driver lifetime)
- **TypeScript**: `net.createServer()` in a Worker thread (main thread blocks on `spawnSync`, worker handles proxy requests)
- **PHP**: `pcntl_fork()` + `stream_socket_server()` with `stream_select()` loop

**Lifecycle**: Server starts on first `register_command()`, runs for the driver instance's lifetime, stops on driver destruction/`close()`.

### IPC Protocol

JSON-RPC 2.0, newline-delimited, over:
- **UDS** for local drivers (BashkitCLI, if feasible) and SSH-forwarded OpenShell
- **TCP** as fallback for OpenShell when SSH forwarding is not available

Consistent with the `bashkit-rpc` plan which also uses JSON-RPC 2.0.

### ShellDriver Integration

**`register_command(name, handler)`**:
1. Store handler in `_commands` map (existing)
2. Register handler with the callback server
3. Create symlink in sandbox: `ln -sf /usr/local/bin/harness-proxy /usr/local/bin/{name}` (deferred to next `exec()` preamble)

**`unregister_command(name)`**:
1. Remove from `_commands` map (existing)
2. Unregister from callback server
3. Remove symlink (deferred to next `exec()` preamble)

**`exec(command)`**:
1. **Fast path**: first-word interception still runs first — if command is just `mycmd arg1`, dispatch locally (zero overhead, no proxy needed)
2. **Slow path**: command reaches the shell, which may invoke the proxy binary for custom commands embedded in pipes/compounds

This means simple invocations remain zero-overhead while pipe composition works through the proxy.

### OpenShell-Specific: SSH Remote Forwarding

When the driver uses SSH transport and has registered commands:

```python
# Instead of: ssh -p PORT user@host "command"
# Use: ssh -p PORT -R /tmp/harness-proxy.sock:LOCAL_SOCK user@host "command"
```

The `-R` flag creates a forwarded Unix socket inside the sandbox that tunnels back to the host's callback server. The proxy binary connects to `/tmp/harness-proxy.sock` inside the sandbox.

**One-time setup preamble** (on first exec after register_command):
```bash
printf '%s' 'BASE64_PROXY_BINARY' | base64 -d > /usr/local/bin/harness-proxy && \
chmod +x /usr/local/bin/harness-proxy && \
ln -sf /usr/local/bin/harness-proxy /usr/local/bin/mycmd && \
export HARNESS_PROXY_SOCK=/tmp/harness-proxy.sock && \
export HARNESS_PROXY_TOKEN=SESSION_SECRET
```

---

## Relationship to Existing Plans

| Approach | Solves for | Status |
|----------|-----------|--------|
| **bashkit-rpc** (cross-language parity plan, Option B) | BashkitCLIDriver pipe composition | Planned, not implemented |
| **Proxy binary** (this proposal) | OpenShellGrpcDriver pipe composition | New proposal |
| **First-word interception** (custom-command-dispatch plan) | Simple direct invocation, all drivers | Implemented |

These are **complementary layers**:
- First-word interception = fast path for simple `mycmd arg1` calls (all drivers)
- bashkit-rpc = pipe composition inside bashkit (BashkitCLI TS/PHP)
- Proxy binary = pipe composition inside real shell environments (OpenShell)

---

## Key Limitations

1. **gRPC transport**: No clean callback channel from sandbox → host without SSH side-channel. Proxy commands initially SSH-only.
2. **PHP concurrency**: Running a callback server alongside synchronous `exec()` requires `pcntl_fork()` — not universally available.
3. **First preamble size**: Injecting the proxy binary adds ~7MB (base64) to the first preamble. Subsequent calls only need symlink management.
4. **stdin buffering**: Large stdin (`cat 100MB | mycmd`) buffered entirely in the proxy binary and sent as JSON string. Matches current handler contract but is memory-intensive.
5. **Error opacity**: If the callback server is unreachable, the proxy binary exits with a connection error. The error message seen in the shell will be opaque.

---

## Implementation Phases (if pursued)

| Phase | Description | Scope |
|-------|-------------|-------|
| 1 | Build Go proxy binary with UDS + TCP client, JSON-RPC protocol, multicall dispatch | New: `cmd/harness-proxy/` |
| 2 | Python callback server (threaded UDS), handler registry | Modify: `openshell_grpc_driver.py` |
| 3 | OpenShell SSH integration: binary injection, symlink management, SSH `-R` forwarding | Modify: `openshell_grpc_driver.py`, `_remote_sync.py` |
| 4 | Port callback server to TypeScript (Worker thread) and PHP (pcntl_fork) | Modify: `openshell-grpc-driver.ts`, `OpenShellGrpcDriver.php` |
| 5 | New capability flag: `"proxy_commands"` reported when proxy is available | Modify: `drivers.py`, `drivers.ts` |
| 6 | Tests: unit (mock server), integration (real OpenShell sandbox with proxy) | New test files per language |

---

## Verification

1. **Unit tests**: Mock callback server, verify proxy binary sends correct JSON-RPC, verify handler dispatch
2. **Integration test**: In a real OpenShell sandbox, register a custom command, run `echo hello | mycmd | wc -c` and verify the pipeline works end-to-end
3. **Concurrency test**: `mycmd1 & mycmd2 & wait` — verify both commands dispatch concurrently
4. **Error test**: Kill the callback server, verify the proxy binary exits with a clear error
5. **Security test**: Attempt to connect to the callback server without the correct token — verify rejection
