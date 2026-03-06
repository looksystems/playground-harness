# 24. HasSkills Mixin

Date: 2026-03-06

## Status

Accepted

## Context

ADR 0017 kept the skill system in `examples/` as a documented pattern rather than a core mixin. ADR 0023 then added `HasCommands` as a core mixin for user-facing slash commands. In practice, skills provide a strictly richer abstraction that subsumes commands: any slash command is just a skill with a single tool and no lifecycle. Meanwhile, the full Skill contract -- bundling tools, instructions, middleware, hooks, dependencies, and lifecycle -- proved valuable enough in real usage to warrant promotion to core.

The examples in `examples/` demonstrated the pattern, and `HasCommands` proved the need for a higher-level registration mechanism. Combining both into `HasSkills` eliminates the artificial distinction between "commands" (simple directives) and "skills" (rich capability bundles) while keeping the API surface clean.

## Decision

Promote the full Skill contract to a core mixin (`HasSkills`), replacing `HasCommands`. The four `slash_command_*` hook events are renamed to reflect the broader skill lifecycle, keeping the total at 22 hook events.

### The Skill contract

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Skill identifier |
| `description` | string | Human-readable description |
| `version` | string | Semantic version |
| `instructions` | string | Injected into system prompt |
| `dependencies` | list | Other skill classes, resolved transitively |
| `context` | SkillContext | Per-skill state bag (config, connections, caches) |
| `setup(ctx)` | method | Async resource initialization |
| `teardown(ctx)` | method | Async resource cleanup |
| `tools()` | method | Returns tool definitions |
| `middleware()` | method | Returns middleware instances |
| `hooks()` | method | Returns event-to-callback mappings |

### HasSkills Mixin API

| Method | Purpose |
|--------|---------|
| `mount(skill)` | Mount a skill: resolve dependencies, run setup, register tools/middleware/hooks |
| `unmount(name)` | Unmount a skill: run teardown, remove tools/middleware/hooks |
| `skills` | Read-only access to mounted skills |

### Hook events (renamed from HasCommands)

| Old event (HasCommands) | New event (HasSkills) | Fires when |
|---|---|---|
| `slash_command_register` | `skill_mount` | Skill mounted |
| `slash_command_unregister` | `skill_unmount` | Skill unmounted |
| `slash_command_call` | `skill_setup` | Before skill setup executes |
| `slash_command_result` | `skill_teardown` | After skill teardown completes |

Total hook events remain at 22 (10 original + 8 from ADR 0022 + 4 skill events).

### SkillPromptMiddleware

Replaces `SlashCommandMiddleware`. Auto-injects mounted skill instructions into the system prompt, ensuring the LLM is aware of all active skill capabilities.

### StandardAgent composition

```
BaseAgent + HasMiddleware + HasHooks + UsesTools + EmitsEvents + HasShell + HasSkills
```

### Migration from examples/

The skill system code previously in `examples/` has been moved to `docs/plans/artefacts/` as historical reference. The canonical implementation now lives in `src/` as part of the core framework.

## Consequences

- Richer composition model: skills bundle tools, instructions, middleware, hooks, and lifecycle into a single mountable unit
- Commands are subsumed: any command is just a skill with a single tool and no lifecycle management
- 4 hook events renamed (`SLASH_COMMAND_*` to `SKILL_*`), total remains 22
- Dependency resolution (topological sort) is now part of the core framework, with cycle detection
- The `SkillPromptMiddleware` replaces `SlashCommandMiddleware`, providing automatic prompt injection for skill instructions
- ADR 0017 and ADR 0023 are both superseded
