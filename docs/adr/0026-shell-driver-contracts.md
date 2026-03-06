# ADR 0026: Shell and Filesystem Driver Contracts

## Status

Accepted

## Context

The virtual shell and filesystem are implemented independently in Python, TypeScript, and PHP (~2500 lines each). This creates maintenance burden, correctness risk from divergence, and a performance ceiling from interpreted-language implementations.

We want to enable swappable shell backends (e.g., bashkit, a Rust-based POSIX shell) while retaining the existing pure-language implementations as the zero-dependency default.

## Decision

Introduce two open contracts:

**FilesystemDriver** — abstracts the virtual filesystem. Methods: `write`, `write_lazy`, `read`, `read_text`, `exists`, `remove`, `is_dir`, `listdir`, `find`, `stat`, `clone`.

**ShellDriver** — abstracts the shell interpreter. Receives a `FilesystemDriver`. Methods: `exec`, `register_command`, `unregister_command`, `clone`. Properties: `fs`, `cwd`, `env`.

**ShellDriverFactory** — resolves driver names to instances. Supports `register(name, factory)` for user-defined drivers. Global default with per-agent override via `HasShell` init and `AgentBuilder.driver()`.

Existing `VirtualFS` and `Shell` classes become the "builtin" driver implementations, satisfying these contracts without code changes beyond adding the interface declaration.

The contracts are open — users can implement custom drivers (e.g., Docker-backed, WASM-based, or IPC-to-native).

## Consequences

- HasShell mixin delegates to ShellDriver/FilesystemDriver instead of directly to Shell/VirtualFS
- `agent.fs` returns a FilesystemDriver, `agent.shell` returns a ShellDriver
- Existing tests continue to pass — builtin driver has identical behavior
- New drivers can be registered at runtime via ShellDriverFactory
- AgentBuilder gains a `driver(name)` method
- VFS ownership model: host owns, sync before/after exec for external drivers
