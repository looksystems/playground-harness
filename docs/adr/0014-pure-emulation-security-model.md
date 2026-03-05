# 14. Pure Emulation Security Model for Virtual Shell

Date: 2026-03-05

## Status

Accepted

## Context

Giving an LLM a shell tool raises obvious security concerns. The just-bash project itself is a sandboxed emulator, but even sandboxed real shells have a large attack surface. Our harness needs the shell capability without any risk of OS-level side effects.

## Decision

All shell commands are pure functions in the host language operating on the in-memory VirtualFS. No real shell is ever invoked. The security boundaries are:

- **No process spawning** — no `subprocess`, `child_process`, `exec()`, `proc_open`
- **No real filesystem access** — all reads/writes go to an in-memory dict/Map/array
- **No network access** — no HTTP calls, sockets, or fetch
- **No code execution** — no eval, awk, or function definition support
- **Output truncation** — configurable `max_output` prevents context flooding
- **Command allowlisting** — restrict available commands per Shell instance

The Python `shlex` import is used solely for `shlex.split()` (argument tokenization). The `os` import is used solely for `os.path.normpath` (path resolution). Neither touches the real filesystem or shell.

### Future hardening (documented requirements, not yet implemented)

- **Readonly mode** — prevent writes to the VFS entirely
- **Size limits** — cap total VFS storage to prevent memory exhaustion
- **Path jailing** — restrict writes to specific prefixes while keeping other paths read-only
- **Per-command timeouts** — prevent expensive regex operations from blocking
- **Audit logging** — record all executed commands for observability

## Consequences

- Zero risk of OS-level side effects from LLM-driven shell commands
- The 23-command subset covers the vast majority of context exploration patterns
- Some commands (awk, while loops, functions) are intentionally absent — this is a feature, not a limitation
- Users who need full bash can use the real just-bash library externally; the harness shell is for safe, in-memory context exploration
