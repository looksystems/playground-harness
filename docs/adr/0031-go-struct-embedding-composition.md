# ADR 0031: Go Struct Embedding and Agent Composition

**Status:** Accepted

## Context

Go lacks classes, inheritance, and function-based mixins. The harness's mixin composition model (ADR 0001) must be mapped onto Go idioms. Two options were considered:

1. **Interface-only composition** — define an `Agent` interface and wire dependencies via constructor injection, with no promotion.
2. **Anonymous struct embedding** — embed subsystem types directly so their methods promote onto `*Agent`, giving callers the same flat call-site API as Python/TypeScript mixins (`a.On(...)`, `a.Register(...)`, `a.Use(...)`).

The Python `StandardAgent` composes `HasHooks`, `HasMiddleware`, `UsesTools`, `EmitsEvents`, `HasShell`, and `HasSkills`. Each mixin contributes a flat method surface that consumers call on the agent directly without qualifying the subsystem.

Embedding was chosen because it preserves this flat call-site shape without boilerplate delegation methods. However, embedding all six subsystems creates method-name collision risks: Go's promotion rules require that promoted names be unambiguous across all embedded types.

## Decision

The `Agent` struct uses anonymous embedding for the three subsystems whose exported method names are mutually distinct, and named fields (with thin forwarder methods) for the remaining subsystems where collision would occur.

```go
type Agent struct {
    // Configuration fields ...
    Model      string
    System     string
    MaxTurns   int
    MaxRetries int
    Stream     bool

    *hooks.Hub
    *tools.Registry
    *middleware.Chain

    Events *events.Host    // named field (not embedded) — see Negative
    *shell.Host            // embedded; field name is Host; may be nil
    Skills *skills.Manager // named field — see Negative

    client llm.Client
}
```

### Embedded subsystems

`*hooks.Hub`, `*tools.Registry`, and `*middleware.Chain` are embedded anonymously. Their promoted method sets do not overlap:

| Type | Key promoted methods |
|------|---------------------|
| `hooks.Hub` | `On`, `Emit`, `EmitAsync` |
| `tools.Registry` | `Register`, `Execute`, `List`, `Get` |
| `middleware.Chain` | `Use`, `RunPre`, `RunPost` |

The unqualified type names (`Hub`, `Registry`, `Chain`) are deliberately distinct so Go's embedding rules have no ambiguity to resolve.

### Named fields and guarded embeddings

`*events.Host` and `*skills.Manager` are named fields rather than anonymous embeddings, to avoid method-name collisions. `*shell.Host` is embedded anonymously (accessible as `.Host`) but may be nil; the `HasShell()` predicate guards it. Thin forwarder methods on `*Agent` expose the subset of each subsystem's API that consumers need:

| Field | Forwarders on `*Agent` |
|-------|------------------------|
| `Events *events.Host` | `RegisterEvent`, `EventBus` |
| `*shell.Host` (embedded, nilable) | `HasShell` (nil-guard predicate); `Exec`, `RegisterCommand` promoted directly |
| `Skills *skills.Manager` | `MountSkill`, `MountedSkills` |

## Consequences

### Positive

- Callers get a flat, promotion-style API: `a.On(...)`, `a.Register(...)`, `a.Use(...)` with no subsystem qualifier.
- Subsystem types are eagerly initialised in `NewAgent`; there is no lazy-init fragility (contrast with Python ADR 0008).
- Method collisions are caught at compile time — no silent shadowing as in Python's MRO.
- `*Agent` is safe for concurrent `Run` invocations because all subsystems are concurrent-safe and `Run` keeps no per-run state on the agent itself.

### Negative

- `events.Host` and `skills.Manager` cannot be embedded because their public method names (`Register`, etc.) collide with `tools.Registry.Register`. They are named fields; the Agent type exposes thin forwarders (`RegisterEvent`, `EventBus`, `MountSkill`, `MountedSkills`) so consumers can still call them without reaching into the field directly. This is a deliberate narrowing of promoted surface in exchange for compile-time clarity.
- `shell.Host` is embedded anonymously (promoted method surface: `Exec`, `RegisterCommand`) but the field may be nil when no shell driver was provided. A nil dereference on an embedded pointer silently compiles but panics at runtime; the `HasShell()` predicate must be consulted before invoking shell methods. This is a trade-off: full promotion is kept (consumers write `a.Exec(...)`) at the cost of a runtime nil-guard requirement.
- The forwarder method surface must be kept in sync with the underlying subsystem APIs as they evolve. This is a small, bounded set of methods and the risk is low.
