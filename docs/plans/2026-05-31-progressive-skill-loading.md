# Progressive (lazy-loaded) skills

## Context

Today's `Skill` is a **developer-mounted, eager bundle**: when a skill is mounted, its
`tools()`/`middleware()`/`hooks()`/`commands()` are registered immediately and its
`instructions` are always-on in the system prompt via `SkillPromptMiddleware`. The model
never discovers or loads a skill on its own — it only sees the result.

The [agentskills.io](https://agentskills.io) standard centres on **progressive disclosure**:
at startup the model sees only each skill's `name` + `description`; the full body (and its
resources) load **on demand, when a task matches**. That keeps many skills on hand for a small
context cost.

This change adds that capability *as an opt-in* on the existing code-defined skill system. A
skill can be marked **progressive**; when it is, mounting registers **nothing** but its
discovery metadata (name + description). A new agent tool, `load_skill`, lets the model activate
it — at which point its instructions *and* its tools/middleware/hooks/commands register, and its
full body enters context. The `load_skill` tool is registered **only when at least one mounted
progressive skill is still pending**, and removed once none remain.

Out of scope this round: a filesystem `SKILL.md` loader (bodies stay in the code `instructions`
field) and skill *de*-activation (activation is one-way, sidestepping the known best-effort
middleware/hook-unmount limitation).

## Design

Add one boolean to the skill surface and rework the manager into a two-phase mount:

1. **Register (mount of a progressive skill)** — store the skill + its per-mount config; run
   **no** `setup()` and register **no** contributions. Ensure the `load_skill` loader tool is
   registered on the agent. Rebuild the prompt middleware so the skill shows up in *discovery*
   form (`## {name}\n{description}` + a "not loaded — call `load_skill('{name}')`" hint).
2. **Activate (`load_skill(name)`)** — resolve the skill's dependencies and run the normal mount
   path (`setup()` → register tools/middleware/hooks/commands), mark it loaded, emit
   `SKILL_SETUP`/`SKILL_MOUNT`, rebuild the prompt middleware so it now renders the **full
   instructions**, and **return the instructions string** from the tool (so the body lands in
   context for the current turn too). Idempotent: re-loading a loaded skill just returns its body.
   When no progressive skill remains pending, unregister `load_skill`.

Eager skills (the default, `progressive == false`) behave exactly as today.

### Per-skill contract
- New `progressive` flag, default `false`, on the base skill (property/field/optional interface).
- A progressive skill **must** have a non-empty `description` (it's all the model sees pre-load) —
  registering one with an empty description raises a clear error.

### Prompt middleware
`SkillPromptMiddleware` is rebuilt to take two groups: **loaded** skills (eager + activated →
render full `instructions`, as today) and **pending** progressive skills (render
`## {name}\n{description}` + load hint). Keep it stateless — the manager classifies on each
rebuild.

### Loader tool
A single `load_skill(name: string) -> string` tool, built by the manager and registered directly
on the agent (`register_tool`/`unregister_tool` already exist and are used at
`has_skills.py:259`/`:196`). Gated on **pending** count: registered when the first pending
progressive skill appears, unregistered when the last one activates.

## Files to change (cross-language parity — all four)

Mirror the same two-phase design in each language. The Python file is the reference; the others
already track it line-for-line.

- **Python** — `src/python/has_skills.py`: add `progressive` property to `Skill`; split
  `SkillManager._mount_single` into `_register_deferred` + `_activate`; add loader-tool
  build/register/unregister; update `SkillPromptMiddleware` to two-group rendering; in
  `HasSkills.mount`, emit `SKILL_SETUP`/`SKILL_MOUNT` only for eager skills (progressive ones emit
  at activation). Tests: `tests/python/test_has_skills.py`.
- **TypeScript** — `src/typescript/has-skills.ts` (same structure). Tests:
  `tests/typescript/has-skills.test.ts`.
- **PHP** — `src/php/Skill.php` (`public bool $progressive = false;`),
  `src/php/SkillManager.php`, `src/php/SkillPromptMiddleware.php`, `src/php/HasSkills.php`. Tests:
  `tests/php/HasSkillsTest.php`.
- **Go** — `src/go/skills/skill.go` (new narrow optional interface
  `Progressive interface { Progressive() bool }`, type-asserted at mount — consistent with the
  existing capability-interface model; do **not** widen the required `Skill` interface),
  `src/go/skills/manager.go`, `src/go/skills/prompt.go`. Tests: `src/go/skills/manager_test.go`,
  `src/go/skills/prompt_test.go`, `src/go/agent/skills_test.go`.

### Docs
- New ADR `docs/adr/0034-progressive-skill-loading.md` (next free number; 0033 is the latest).
  Record: opt-in `progressive` flag, two-phase mount, conditional `load_skill` tool, one-way
  activation, why filesystem `SKILL.md` is deferred.
- `docs/guides/skills.md`: add a **Progressive skills** section (the discovery/activation/execution
  three-stage model, the `progressive` flag, the `load_skill` tool, the four-language surface).
  Cross-link from the "Instructions and the system prompt" section.

## Reused building blocks
- `SkillManager._resolve_deps` (`has_skills.py:292`) — reuse verbatim inside `_activate`.
- `SkillManager._rebuild_prompt_middleware` (`:313`) — extend to classify loaded vs pending.
- Agent `register_tool`/`unregister_tool` (already used at `:259`/`:196`) — for the loader tool.
- `ToolDef` (`src/python/uses_tools.py`) and each language's tool-def type — for `load_skill`.
- Existing `SKILL_SETUP`/`SKILL_MOUNT` hook events — reused; no new events needed.

## Verification
- **Unit tests (all four langs)** cover: (a) a progressive skill injects only name+description at
  mount, not its instructions; (b) its tools are **not** registered until activation; (c)
  `load_skill` appears only while a progressive skill is pending and disappears once none are;
  (d) calling `load_skill(name)` registers the skill's tools, switches the prompt to full
  instructions, returns the body, and is idempotent; (e) empty-description progressive skill is
  rejected; (f) eager skills are unchanged.
  - Python: `pytest tests/python/test_has_skills.py`
  - TS: `npm test -- has-skills` (or the repo's configured runner)
  - PHP: `vendor/bin/phpunit tests/php/HasSkillsTest.php`
  - Go: `go test ./src/go/skills/... ./src/go/agent/...`
- **End-to-end smoke** in one language (Python): build an agent, mount one eager + one progressive
  skill, assert the system prompt shows the progressive skill's description (not body) and no
  skill-specific tool; drive a tool call to `load_skill('<name>')`; assert the returned body, the
  newly-registered tool, and that a subsequent LLM call's prompt now carries the full instructions.
- Run the full suite per language to confirm no regression in the existing eager-skill tests.
