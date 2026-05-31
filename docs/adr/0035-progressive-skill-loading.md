# ADR 0035: Progressive (Lazy-Loaded) Skills

Date: 2026-05-31

## Status

Accepted

## Context

The `HasSkills` mixin (ADR 0024) treats every skill as a **developer-mounted,
eager bundle**: mounting a skill immediately runs its `setup()`, registers its
`tools()`/`middleware()`/`hooks()`/`commands()`, and pins its `instructions`
into the system prompt via `SkillPromptMiddleware`. The model never discovers
or loads a skill on its own — it only ever sees the result of a decision the
developer already made.

That is the right default for a handful of always-on skills. It scales poorly
when an agent should have *many* skills on hand: every mounted skill's full
instruction body sits in the system prompt for every turn, whether or not the
current task needs it. Context is spent linearly in the number of mounted
skills.

The [agentskills.io](https://agentskills.io) standard centres on **progressive
disclosure**: at startup the model sees only each skill's `name` +
`description`; the full body (and its resources) load **on demand, when a task
matches**. Many skills stay on hand for a small, fixed context cost.

## Decision

Add progressive disclosure as an **opt-in** flag on the existing code-defined
skill system, mirrored across all four languages. The eager path is unchanged.

### Opt-in `progressive` flag

A skill declares itself progressive:

- **Python** — `progressive` property on `Skill`, default `False`.
- **TypeScript** — `readonly progressive: boolean = false` field on `Skill`.
- **PHP** — `public bool $progressive = false;` on `Skill`.
- **Go** — a narrow optional capability interface
  `Progressive interface { Progressive() bool }`, type-asserted at mount time,
  consistent with the other capability interfaces (ADR 0031). The required
  `Skill` interface is **not** widened.

A progressive skill **must** carry a non-empty `description` — it is all the
model sees before activation. Registering one with an empty description raises
a clear error.

### Two-phase mount

The skill manager splits mounting into two phases:

1. **Register (mount of a progressive skill).** Store the skill and its
   per-mount config. Run **no** `setup()` and register **no** contributions.
   Ensure the `load_skill` loader tool is registered on the agent. The prompt
   middleware renders the skill in *discovery* form (`## {name}\n{description}`
   plus a "not loaded — call `load_skill('{name}')`" hint). Dependencies are
   **not** resolved yet.

2. **Activate (`load_skill(name)`).** Resolve the skill's dependencies and run
   the normal mount path (`setup()` → register tools/middleware/hooks/commands),
   mark it loaded, emit `SKILL_SETUP`/`SKILL_MOUNT`, switch the prompt to the
   **full instructions**, and **return the instructions string** from the tool
   so the body lands in context for the current turn too. When no progressive
   skill remains pending, the loader tool is unregistered.

Eager skills (the default) emit `SKILL_SETUP`/`SKILL_MOUNT` at mount time, as
before; progressive skills emit them at activation.

### Conditional `load_skill` tool

A single `load_skill(name: string) -> string` tool, built by the manager and
registered directly on the agent. It is **gated on the pending count**:
registered when the first pending progressive skill appears, unregistered when
the last one activates. The model therefore only sees the loader tool when
there is actually something to load.

### One-way activation

Activation is **one-way**: there is no progressive *de*-activation. This
deliberately sidesteps the known best-effort middleware/hook-unmount limitation
(ADR 0024, "Known limitations") — unloading would have to remove middleware and
hooks, which neither the Python nor the Go registries support surgically.

### Filesystem `SKILL.md` deferred

This round keeps skill bodies in the code `instructions` field. A filesystem
`SKILL.md` loader (reading bodies and bundled resources from disk, the other
half of the agentskills.io model) is **out of scope** and left for a future
ADR. The two-phase mount and the discovery/activation prompt split are the
foundation a `SKILL.md` loader would build on.

## Consequences

- **Context cost becomes flat in skill count.** Many progressive skills cost
  only their name + description until used.
- **The model drives loading.** Discovery metadata plus the `load_skill` tool
  let the model activate a skill when a task matches, rather than the developer
  pre-deciding.
- **Eager behaviour is untouched.** Skills that do not set `progressive` behave
  exactly as before; all existing eager-skill tests pass unchanged.
- **Activation is irreversible** for the lifetime of the agent. Acceptable
  given the unmount limitation; revisit if/when surgical middleware/hook removal
  lands.
- **Two emit sites for lifecycle hooks.** Eager skills emit at mount; progressive
  skills emit at activation. Observers see `SKILL_SETUP`/`SKILL_MOUNT` exactly
  once per skill, when it actually becomes active.

## See also

- [ADR 0024](0024-has-skills-mixin.md) — HasSkills Mixin (the eager baseline)
- [ADR 0031](0031-go-struct-embedding-composition.md) — the Go capability-interface model
- [docs/guides/skills.md](../guides/skills.md) — the Progressive skills section
