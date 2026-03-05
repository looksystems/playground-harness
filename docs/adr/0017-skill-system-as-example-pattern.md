# 17. Skill System as Example Pattern

Date: 2026-03-05

## Status

Accepted

## Context

After building the core mixins (hooks, middleware, tools, events), we needed a higher-level abstraction for bundling related capabilities together. A "web browsing" capability needs tools (fetch_page), prompt instructions ("you can browse the web"), lifecycle management (HTTP session setup/teardown), and possibly middleware or hooks. Scattering these across separate `register_tool`, `agent.use`, and `agent.on` calls is error-prone and hard to reuse.

The original design doc proposed `HasSkills` as a core mixin. We built the skill system across all three languages (`examples/skills.*`) but then had to decide whether to promote it to `src/` or keep it as a pattern.

## Decision

Keep the skill system in `examples/` as a documented pattern, not a core mixin. The reasons:

1. **The core mixins are sufficient.** `UsesTools`, `HasHooks`, `HasMiddleware`, and `EmitsEvents` provide all the building blocks. The `Skill` base class is a convenience wrapper, not a new capability.

2. **Skill patterns vary too much.** Some skills are pure tools (MathSkill). Some are middleware-only (GuardrailSkill). Some need async lifecycle (WebBrowsingSkill). Some combine everything. A single `Skill` base class imposes structure that may not fit all use cases.

3. **Dependency resolution adds complexity.** The topological sort for skill dependencies is useful but brings edge cases (circular deps, diamond deps, version conflicts) that we don't want in the core framework without strong demand.

4. **HasShell proves the alternative.** The virtual shell was promoted to core as a mixin (`HasShell`) rather than a skill. It's simpler, more composable, and doesn't require the skill system machinery. This validates the approach of building specific capability mixins rather than a generic skill framework.

### What the skill system provides (in examples)

| Component | Purpose |
|-----------|---------|
| `Skill` base class | Bundle tools + instructions + middleware + hooks + lifecycle |
| `SkillContext` | Per-skill state bag (config, connections, caches) |
| `SkillManager` | Mount/unmount skills, dependency resolution, lifecycle ordering |
| `SkillPromptMiddleware` | Auto-inject skill instructions into system prompt |

### The Skill contract

- `name`, `description`, `version` — metadata (name auto-derives from class)
- `instructions` — injected into system prompt
- `tools()` — returns tool definitions
- `middleware()` — returns middleware instances
- `hooks()` — returns event-to-callback mappings
- `dependencies` — other skill classes, resolved transitively
- `setup(ctx)` / `teardown(ctx)` — async resource lifecycle

## Consequences

- The core framework stays minimal — 5 mixins (hooks, middleware, tools, events, shell) plus BaseAgent
- Users who need the skill pattern can copy from `examples/skills.*` and adapt
- Promoting specific capabilities (like HasShell) to core mixins is the preferred path for first-class features
- If strong demand emerges for a generic skill system, it can be promoted to core later with real-world usage to guide the API
- The examples serve as both documentation and starting points for customization
