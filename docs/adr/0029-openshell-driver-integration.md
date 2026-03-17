# ADR 0029: OpenShell Driver Integration

## Status

Revised — updated to SSH-based communication for all languages (2026-03-17)

## Context

[NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) is a secure sandboxed runtime for AI agents. It provides:

- K3s/Docker container orchestration with declarative YAML policies
- SSH-based command execution inside isolated containers
- Multi-layer policy enforcement (filesystem, network, process, inference)
- Credential isolation via `inference.local` routing

Agent Harness has a swappable driver architecture (ADR 0026) with two drivers: `builtin` (in-memory VirtualFS) and `bashkit` (real bash via PyO3/subprocess). OpenShell is a natural third driver — a remote sandbox that provides stronger isolation guarantees.

### Original assumption (superseded)

The initial design assumed OpenShell exposed a **client-facing gRPC API** (`ExecSandbox`, `CreateSandbox`, `DeleteSandbox`) and planned:

- Python/PHP: gRPC directly (synchronous clients available)
- TypeScript: SSH via `spawnSync` as a workaround (Node.js gRPC is async, but `exec()` is synchronous)

After reviewing the real OpenShell architecture, we found that **gRPC is internal to the gateway control plane** — it is not exposed to end users. Sandboxes are Docker containers accessed via SSH (`openshell sandbox connect`). The original gRPC assumption was wrong.

### Design questions

1. **Communication protocol**: gRPC vs SSH — trade-offs and which matches reality
2. **Filesystem sync**: How to reuse the preamble/epilogue pattern across drivers
3. **Workspace scoping**: Real containers have thousands of system files — `find /` is not viable
4. **Contract compatibility**: `exec()` is synchronous — does the protocol support this?

## Decision

### Communication: SSH for all three languages

All three languages use **SSH via subprocess** for command execution.

- **Python**: `subprocess.run(["ssh", ...])` with `capture_output=True`
- **TypeScript**: `spawnSync("ssh", [...])` from `child_process`
- **PHP**: `proc_open("ssh ...")` with pipe descriptors

Test overrides (`grpc_override` / `_execOverride`) allow mock-based unit testing without SSH.

### Why SSH over gRPC

| Consideration | SSH | gRPC |
|---|---|---|
| **Matches reality** | OpenShell sandboxes are accessed via SSH | gRPC is internal to the gateway — no client API |
| **Dependencies** | Zero — `ssh` is universally available | `grpcio` (Py), `@grpc/grpc-js` (TS), `grpc` PECL (PHP) |
| **Sync contract** | Synchronous in all languages via subprocess | Async in Node.js — requires contract change or SSH fallback |
| **Auth** | SSH keys, agent forwarding, standard tooling | TLS certs, tokens — additional setup |
| **Debugging** | `ssh -v` is universally understood | Requires gRPC tooling (grpcurl, etc.) |
| **Firewall/proxy** | Port 22 often allowed; tunneling well-understood | Arbitrary ports; HTTP/2 can be tricky through proxies |

**SSH was chosen because it matches how OpenShell actually works, requires zero dependencies, and is synchronous everywhere.**

### What SSH gives up

- **Streaming**: `SHELL_STDOUT_CHUNK` / `SHELL_STDERR_CHUNK` hooks are declared as capabilities but cannot fire with SSH — stdout arrives all at once after the command completes. A future gRPC path would enable real-time streaming.
- **Connection pooling**: Each `exec()` spawns a new SSH process (~100-600ms overhead). gRPC's persistent HTTP/2 channel would amortize connection cost.
- **Structured errors**: SSH returns exit codes and stderr text. gRPC could return typed error responses with policy violation details and sandbox state.
- **Multiplexing**: One command per SSH connection vs multiple concurrent RPCs over one gRPC channel.

### Filesystem sync with workspace remapping

The sync mechanism is shared with bashkit via extracted utilities (`_remote_sync.py`, `remote-sync.ts`, `RemoteSyncTrait.php`). However, real containers exposed a problem the virtual-FS-based bashkit never hit: `find / -type f` scans the entire container filesystem (thousands of system files), causing timeouts.

The fix is **workspace remapping**:

- VFS path `/hello.txt` maps to `{workspace}/hello.txt` on the remote (default: `/home/sandbox/workspace`)
- The preamble writes files to workspace paths
- The epilogue runs `find {workspace}` instead of `find /`
- The sync-back strips the workspace prefix to restore VFS paths
- In mock mode, no remapping occurs — backwards compatible with existing tests

This is an architectural concern that would affect any real-container driver regardless of protocol choice.

### Sandbox lifecycle

- Lazy connection on first `exec()`
- Explicit `close()` releases resources
- Python supports context manager (`with` statement)
- `clone()` creates a new driver with cloned VFS

### Dependencies

Minimal — only SSH binary on PATH:
- **Python**: No extra packages (uses `subprocess`)
- **TypeScript**: No extra packages (uses `child_process`)
- **PHP**: No extra extensions (uses `proc_open`)

### Capabilities

The OpenShell driver advertises `{"custom_commands", "remote", "policies", "streaming"}` via the `capabilities()` method introduced in ADR 0030. Note that `streaming` is aspirational — it requires a future gRPC transport to actually deliver real-time chunks.

### Future: gRPC transport variant

If OpenShell exposes a client-facing gRPC API in the future, adding an `openshell-grpc` driver variant would be straightforward — the sync logic, VFS, workspace remapping, and test infrastructure are all protocol-agnostic. The SSH driver would remain as the default since it requires no dependencies. The `_rawExec()` method is the only protocol-specific code.

## Consequences

- Third driver option for users who need stronger isolation
- Shared sync utilities reduce duplication (DRY across bashkit + openshell)
- Workspace remapping adds a layer of path translation but avoids filesystem scan timeouts
- SSH-based communication is consistent across all three languages with zero dependencies
- Policy objects and driver capabilities introduce a formal security model (see ADR 0030)
- A Docker-based test sandbox (`tests/openshell-sandbox/Dockerfile`) enables live integration testing without the full OpenShell CLI
- Streaming capability is declared but not yet functional — requires gRPC transport
