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
- **Iteration limits** — `for`/`while` loops share a counter capped at `maxIterations` (default 10,000)
- **Command substitution depth** — `$(...)` recursion limited to 10 levels
- **Expansion cap** — maximum 1,000 variable/command substitutions per `exec()` call, preventing expansion bombs
- **Variable size cap** — individual variable values capped at 64KB
- **Parser nesting depth** — AST nesting limited to 50 levels

The Python `os` import is used solely for `os.path.normpath` (path resolution). It does not touch the real filesystem.

### Future hardening (documented requirements, not yet implemented)

- **Readonly mode** — prevent writes to the VFS entirely
- **Size limits** — cap total VFS storage to prevent memory exhaustion
- **Path jailing** — restrict writes to specific prefixes while keeping other paths read-only
- **Per-command timeouts** — prevent expensive regex operations from blocking
- **Audit logging** — record all executed commands for observability

## Consequences

- Zero risk of OS-level side effects from LLM-driven shell commands
- The 30-command subset with control flow (`if/elif/else`, `for`, `while`, `case`), arithmetic `$((...))`, and parameter expansion covers the vast majority of shell scripts agents produce
- Some commands (awk, functions, source) are intentionally absent — this is a feature, not a limitation
- All flow control is bounded by iteration limits and nesting depth, preventing runaway execution
- Users who need full bash can use the real just-bash library externally; the harness shell is for safe, in-memory context exploration
