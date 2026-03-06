# 12. Virtual Shell Architecture

Date: 2026-03-05

## Status

Accepted

## Context

Agent tools are typically built as individual function definitions — one tool per query pattern. Vercel's d0 agent demonstrated that replacing many specialized tools with a single bash-like tool over a semantic filesystem improved task success rate from 80% to 100%. The key insight: LLMs already understand Unix commands deeply. Mounting context as files and letting the model use `grep`, `cat`, `find`, `jq` is more effective than building bespoke tools for every data access pattern.

We need to bring this capability into the harness as a first-class feature while maintaining the security guarantee that no real shell or filesystem is ever accessed.

## Decision

Add three standalone components and one convenience mixin:

1. **VirtualFS** — in-memory filesystem (flat key-value store, directories inferred by prefix)
2. **Shell** — command interpreter over a VirtualFS with recursive-descent parser, AST evaluator, 30 built-in commands, pipes, redirects, control flow (`if/elif/else`, `for`, `while`, `case`), `&&`/`||`, variable assignment, command substitution, arithmetic expansion, and parameter expansion
3. **ShellRegistry** — global singleton for named shell configurations (agents receive clones)
4. **HasShell** — convenience mixin providing `agent.fs`, `agent.shell`, `agent.exec()`

The components are layered:

- VirtualFS and Shell are fully standalone — usable without any agent
- ShellRegistry is a global singleton for sharing shell configurations across agents
- HasShell is an optional mixin that wires Shell into the agent, auto-registering the `exec` tool when `UsesTools` is also composed

Shell configurations in the registry serve as templates. Agents always receive a clone, so modifications (adding files, changing env) don't affect the template or other agents.

HasShell works independently of UsesTools but auto-registers the `exec` tool when both mixins are present.

## Consequences

- Agents gain a powerful context exploration capability through a single tool
- The VirtualFS and Shell are useful as standalone utilities outside the agent context
- The registry pattern enables shared shell configurations without coupling agents to each other
- All three language implementations must maintain the pure-emulation security guarantee — no real shell or filesystem access
- StandardAgent gains a new optional mixin, increasing its surface area
- Future hardening (readonly mode, size limits, path jailing, per-command timeouts, audit logging) is deferred but documented as requirements
