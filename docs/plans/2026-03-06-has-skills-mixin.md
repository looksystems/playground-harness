# HasSkills Mixin — Promote Skill System to Core

## Context

ADR 0017 originally kept the skill system in `examples/` as a documented pattern. ADR 0023 added `HasCommands` as a simpler slash-command mixin. We're now reversing ADR 0017's decision: promoting the full Skill contract to a core mixin (`HasSkills`), replacing `HasCommands` entirely.

The skill system provides a richer abstraction: bundled tools + instructions + middleware + hooks + lifecycle + dependencies. The simpler `CommandDef`/`HasCommands` is subsumed — any slash command is just a skill with a single tool.

## What Changes

1. **Move** `examples/` → `docs/plans/artefacts/` (preserve as reference)
2. **Delete** `HasCommands`, `CommandDef`, `SlashCommandMiddleware`, `@command` decorator
3. **Create** `HasSkills` mixin with full Skill contract (Skill base, SkillContext, SkillManager, SkillPromptMiddleware)
4. **Rename** 4 hook events: `SLASH_COMMAND_*` → `SKILL_*`
5. **Update** StandardAgent composition, exports, tests, docs, ADRs

## Design: Skill Contract (from ADR 0017 examples)

### Skill Base Class

| Member | Purpose |
|---|---|
| `name` | Unique ID (auto-derived from class name: `WebBrowsingSkill` → `web_browsing`) |
| `description` | Human-readable description |
| `version` | Semver string (default `"0.1.0"`) |
| `instructions` | Injected into system prompt via `SkillPromptMiddleware` |
| `dependencies` | Other Skill classes required (resolved transitively) |
| `context` | `SkillContext` set by manager on mount |
| `setup(ctx)` | Async lifecycle — acquire resources |
| `teardown(ctx)` | Async lifecycle — release resources |
| `tools()` | Return list of tool definitions |
| `middleware()` | Return list of middleware instances |
| `hooks()` | Return dict of HookEvent → callbacks |

### SkillContext

Per-skill state bag: `{ skill, agent, config: dict, state: dict }`

### SkillManager

Manages skill lifecycle on an agent. Methods: `mount(skill, config?)`, `unmount(name)`, `shutdown()`, `skills` property.

Internally: topological dependency resolution, reverse-order teardown, prompt middleware rebuilding.

### SkillPromptMiddleware

Injects `instructions` from all mounted skills into the system prompt. Auto-managed by SkillManager.

## Hook Events

Rename 4 existing events:

| Old | New | Value |
|---|---|---|
| `SLASH_COMMAND_REGISTER` | `SKILL_MOUNT` | `"skill_mount"` |
| `SLASH_COMMAND_UNREGISTER` | `SKILL_UNMOUNT` | `"skill_unmount"` |
| `SLASH_COMMAND_CALL` | `SKILL_SETUP` | `"skill_setup"` |
| `SLASH_COMMAND_RESULT` | `SKILL_TEARDOWN` | `"skill_teardown"` |

**Files:** `src/python/has_hooks.py`, `src/typescript/has-hooks.ts`, `src/php/HookEvent.php`

## Implementation Steps

### Step 0: Move examples/ to docs/plans/artefacts/

```bash
mv examples/ docs/plans/artefacts/
```

Update any references in docs that point to `examples/`.

### Step 1: Rename Hook Events (all 3 languages)

Replace `SLASH_COMMAND_*` with `SKILL_*` in HookEvent enums.

**Files:** `src/python/has_hooks.py`, `src/typescript/has-hooks.ts`, `src/php/HookEvent.php`

### Step 2: Create HasSkills Mixin — Python

Replace `src/python/has_commands.py` with `src/python/has_skills.py`:

- `SkillContext` dataclass: `skill`, `agent`, `config: dict`, `state: dict`
- `Skill` ABC: metadata, lifecycle, composition methods (see contract above)
- `SkillPromptMiddleware`: extends `BaseMiddleware`, injects skill instructions in `pre()`
- `SkillManager`: `mount()`, `unmount()`, `shutdown()`, `_resolve_deps()`, `_rebuild_prompt_middleware()`
- `HasSkills` mixin class:
  - Lazy init: `_ensure_has_skills()` → creates `SkillManager(self)`
  - `mount(skill, config?)` → delegates to manager, fires `SKILL_MOUNT` hook
  - `unmount(name)` → delegates, fires `SKILL_UNMOUNT` hook
  - `shutdown()` → delegates
  - `skills` property → read-only dict from manager
- `@skill` decorator (Python only) — mirror `@command`, attaches `_skill_meta` for simple single-tool skills

Key difference from examples: `HasSkills` is a **mixin** (not monkey-patching). The `SkillManager` is created lazily inside the mixin, not externally.

**Reuse:** `_emit_fire_and_forget` pattern from current `has_commands.py:61-68`. `_build_param_schema` from `uses_tools.py`.

**File:** `src/python/has_skills.py` (new), delete `src/python/has_commands.py`

### Step 3: Create HasSkills Mixin — TypeScript

Replace `src/typescript/has-commands.ts` with `src/typescript/has-skills.ts`:

- `SkillContext` interface
- `Skill` abstract class
- `SkillPromptMiddleware` implementing `Middleware`
- `SkillManager` class
- `HasSkills` function-based mixin (HOC pattern per ADR 0009)

**File:** `src/typescript/has-skills.ts` (new), delete `src/typescript/has-commands.ts`

### Step 4: Create HasSkills Mixin — PHP

Replace `src/php/HasCommands.php` and `src/php/CommandDef.php` with:

- `src/php/Skill.php` — abstract class
- `src/php/SkillContext.php` — value object
- `src/php/SkillManager.php` — manager class
- `src/php/SkillPromptMiddleware.php` — middleware
- `src/php/HasSkills.php` — trait

Delete `src/php/CommandDef.php`, `src/php/HasCommands.php`

### Step 5: Delete SlashCommandMiddleware (all 3 languages)

The standalone `SlashCommandMiddleware` is replaced by `SkillPromptMiddleware` (auto-managed by SkillManager).

**Delete:** `src/python/slash_command_middleware.py`, `src/typescript/slash-command-middleware.ts`, `src/php/SlashCommandMiddleware.php`

### Step 6: Update StandardAgent (all 3 languages)

Replace `HasCommands` with `HasSkills` in composition chain.

- **Python:** `class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents, HasShell, HasSkills): pass`
- **TypeScript:** `export const StandardAgent = HasSkills(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))));`
- **PHP:** `use HasSkills;` (replace `use HasCommands;`)

**Files:** `src/python/standard_agent.py`, `src/typescript/standard-agent.ts`, `src/php/StandardAgent.php`

### Step 7: Update Exports

**TypeScript** `src/typescript/index.ts`: Export `HasSkills`, `Skill`, `SkillContext`, `SkillManager`, `SkillPromptMiddleware`. Remove `HasCommands`, `CommandDef`, `SlashCommandMiddleware`.

### Step 8: Tests (all 3 languages)

Replace `test_has_commands` with `test_has_skills`. Test classes:

1. **Skill base** — auto-name derivation, metadata defaults
2. **SkillManager standalone** — mount, unmount, shutdown, dependency resolution, duplicate mount skip
3. **With UsesTools** — skills auto-register tools, unmount removes tools
4. **With HasHooks** — all 4 hook events fire (SKILL_MOUNT, SKILL_UNMOUNT, SKILL_SETUP, SKILL_TEARDOWN)
5. **Lifecycle** — setup() called on mount, teardown() on unmount/shutdown, reverse order teardown
6. **Instructions** — SkillPromptMiddleware injects instructions into system messages
7. **Middleware/hooks from skill** — skill-provided middleware and hooks are registered on mount
8. **Dependencies** — transitive resolution, already-mounted deps skipped

**Files:** `tests/python/test_has_skills.py`, `tests/typescript/has-skills.test.ts`, `tests/php/HasSkillsTest.php`
**Delete:** `tests/python/test_has_commands.py`, `tests/typescript/has-commands.test.ts`, `tests/php/HasCommandsTest.php`

### Step 9: Update Documentation

**ADR updates:**
- `docs/adr/0017-skill-system-as-example-pattern.md` → Rewrite as "Skill System as Core Mixin". Status: **Superseded by ADR 0024**
- `docs/adr/0023-has-commands-mixin.md` → Rewrite. Status: **Superseded by ADR 0024**
- Write new `docs/adr/0024-has-skills-mixin.md` documenting this decision
- Update cross-references in ADRs 0001, 0009, 0015, 0016, 0022 (replace "commands"/"slash commands" with "skills", update hook counts to 22)
- `docs/adr/README.md` — update table

**Guide updates:**
- `docs/guides/python.md`, `docs/guides/typescript.md`, `docs/guides/php.md` — replace HasCommands sections with HasSkills
- `docs/overview.md`, `docs/architecture.md`, `docs/comparison.md` — update mixin lists

**Plan updates:**
- `docs/plans/2026-03-06-has-commands-mixin.md` — add note: superseded by HasSkills

## Key Design Distinctions

| | Shell Commands (HasShell) | Skills (HasSkills) |
|---|---|---|
| Scope | Virtual shell interpreter | Composable capability bundles |
| What it bundles | Single command handler | Tools + instructions + middleware + hooks + lifecycle |
| Dependencies | None | Transitive skill dependency resolution |
| Lifecycle | None | setup() / teardown() |
| LLM access | Via `exec` tool | Tools auto-registered individually |
| Hook prefix | `command_*` | `skill_*` |

## Verification

1. **Python tests:** `uv run python -m pytest tests/python/test_has_skills.py -v`
2. **TypeScript tests:** `npx vitest run tests/typescript/has-skills.test.ts`
3. **PHP tests:** `./vendor/bin/phpunit tests/php/HasSkillsTest.php`
4. **All existing tests still pass:** `uv run python -m pytest tests/python/ -v && npx vitest run && ./vendor/bin/phpunit`
5. **No stale references:** `grep -r "HasCommands\|SlashCommand\|slash_command\|CommandDef" src/ tests/ docs/` returns nothing
