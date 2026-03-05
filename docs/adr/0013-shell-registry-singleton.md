# 13. Shell Registry as Global Singleton

Date: 2026-03-05

## Status

Accepted

## Context

Agents need to use pre-configured shell environments. A "data-explorer" shell might have specific schemas mounted and only read-only commands enabled. Multiple agents might share the same base configuration but each needs its own working copy to avoid cross-contamination.

Three patterns were considered:

1. **Global singleton** — `ShellRegistry.register("name", shell)` / `ShellRegistry.get("name")`
2. **Per-instance registry** — a registry object passed to each agent
3. **No registry** — agents construct shells directly

## Decision

Use a global singleton with `reset()` for testing. This matches established patterns like logging registries and is the simplest option that supports the use case.

The registry stores shell templates. `get()` always returns a deep clone (VirtualFS contents, cwd, env, allowed_commands), so agents can freely modify their copy without affecting the template or other agents.

## Consequences

- Simple API — register once, use anywhere
- Clone-on-get prevents cross-agent contamination
- Global state makes unit tests require `reset()` in setup/teardown
- Not suitable for multi-tenant scenarios where different tenants need isolated registries (but that's a future concern, not a current one)
