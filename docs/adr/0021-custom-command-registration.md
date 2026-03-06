# 21. Custom Command Registration for Virtual Shell

Date: 2026-03-06

## Status

Accepted

## Context

The virtual shell ships with 30 built-in commands, but agents often need domain-specific operations beyond file exploration — `deploy`, `validate`, `query`, etc. Without an extension mechanism, every new operation requires either adding a separate tool definition (fragmenting the agent's interface) or patching the Shell class itself (not scalable for per-agent customization).

Custom commands should feel identical to built-ins from the LLM's perspective — invoked through the same `exec` tool, composable with pipes, redirects, and control flow. The registration API should work at both the Shell level (for standalone usage) and the HasShell mixin level (for agent usage).

## Decision

### Storage

Custom commands are stored in a separate `_customCommands` map (TS: `Map<string, CmdHandler>`, Python: `dict`, PHP: `array`), distinct from `_builtins`. Both maps are consulted during command dispatch — custom commands are also inserted into `_builtins` for uniform lookup. The separate storage exists so that `clone()` can replay custom registrations on the new shell, and `unregisterCommand()` can distinguish custom commands from built-ins.

A `_builtinNames` set records the original 30 command names at construction time. This set is immutable after construction and is used solely to guard against unregistering built-in commands.

### Registration

`registerCommand(name, handler)` stores the handler in `_customCommands` and adds it to `_builtins`. If `_allowedCommands` is set (restricted mode), the name is also added to the allow-list so the command is actually reachable.

Custom commands can override built-in commands. This is intentional — it allows agents to replace default behavior (e.g., a custom `cat` that adds access logging). The original built-in is still recorded in `_builtinNames`, so unregistering the override is allowed.

### Unregistration

`unregisterCommand(name)` removes the command from `_customCommands` and `_builtins`. If `_allowedCommands` is set, the name is also removed from it. Attempting to unregister a built-in command (one present in `_builtinNames`) throws an error.

### Clone behavior

`clone()` creates a fresh Shell from the same options, then replays all `_customCommands` entries via `registerCommand()` on the clone. This means custom commands survive `ShellRegistry.get()` (which clones), ensuring agents based on a registry template inherit the template's custom commands.

### Handler signature

The handler signature matches the existing internal `CmdHandler` type — `(args: string[], stdin: string) => ExecResult`. This was already the internal type; we now export it from the TypeScript module for external use.

### HasShell delegation

`HasShell` gains `registerCommand()` and `unregisterCommand()` methods that delegate directly to the underlying Shell instance. This allows agent-level registration without reaching into `agent.shell`.

## Consequences

- Agents can expose domain-specific operations through the same `exec` tool interface, keeping the LLM's tool surface minimal
- Custom commands compose naturally with pipes, redirects, variable substitution, and control flow — no special handling required
- The `_builtinNames` guard prevents accidental removal of core commands
- Clone-on-get from ShellRegistry preserves custom commands, so registry templates can include domain commands
- The `CmdHandler` type is now part of the public API surface in TypeScript; Python and PHP use their language-native callable conventions
- Custom commands that override built-ins could confuse debugging if not documented — this is the caller's responsibility
