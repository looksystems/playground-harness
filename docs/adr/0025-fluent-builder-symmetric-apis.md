# 25. Fluent Builder and Symmetric Mixin APIs

**Status:** Accepted

## Context

The mixin architecture (ADR 0001) successfully decomposed agent capabilities into independent, composable units. However, the public APIs had several DX gaps:

- **No fluent chaining.** Registration methods (`on()`, `use()`, `register_tool()`) returned `None`/`void`, forcing multi-line imperative setup.
- **Asymmetric APIs.** Most mixins had `register` methods but lacked corresponding `unregister`/removal methods. Python's `HasHooks` had no `remove_hook()`, `HasMiddleware` had no `remove_middleware()`, and `EmitsEvents` had no `unregister_event()`.
- **No read-only accessors.** Inspecting registered hooks, middleware, tools, or events required accessing private internals (`_hooks`, `_middleware`, `_tools`).
- **Duplicated helpers.** Identical `_call_fn` and `tryEmit` functions were copied across 3-4 mixins in each language.
- **SkillManager coupled to internals.** The skill manager directly mutated `_middleware` and `_hooks` lists on the agent instead of using public APIs.
- **Verbose setup.** Configuring a fully-loaded agent required 10+ imperative statements — readable but tedious for common patterns.

## Decision

### 1. Shared Utilities

Extract duplicated helpers into per-language utility modules (`_utils.py`, `utils.ts`, `Helpers.php`) to DRY the codebase. Each mixin imports from the shared module instead of defining its own copy.

### 2. Symmetric Mixin APIs

Every mixin that has a `register`/`add` method gets a corresponding `unregister`/`remove` method and a read-only accessor:

| Mixin | Register | Remove | Accessor |
|-------|----------|--------|----------|
| HasHooks | `on()` | `remove_hook()` | `hooks` |
| HasMiddleware | `use()` | `remove_middleware()` | `middleware` |
| UsesTools | `register_tool()` | `unregister_tool()` | `tools` |
| EmitsEvents | `register_event()` | `unregister_event()` | `events` |

All registration and removal methods return `self`/`this`/`static` for fluent chaining. Read-only accessors return copies so external mutation cannot corrupt internal state.

### 3. Fluent Returns

In Python, `on()` now returns `Self` (was `None`). In TypeScript, `on()` continues to return the unsubscribe function (changing this would break existing code), but `removeHook()` returns `this`. In PHP, `on()` continues to return the unsubscribe `Closure`, and `removeHook()` returns `static`.

### 4. SkillManager Decoupling

SkillManager now uses public APIs (`remove_hook()`, `remove_middleware()`, `removeMiddleware()`) to clean up skill contributions on unmount, instead of directly mutating private `_hooks` and `_middleware` collections. A `_prepend_middleware()` internal method was added to Python's `HasMiddleware` for the specific case of prompt middleware needing first position.

### 5. Builder Facade

A new `AgentBuilder` class in each language provides declarative, fluent configuration:

```python
agent = await (
    StandardAgent.build("gpt-4")
    .system("You are helpful")
    .tools(search, calc)
    .middleware(AuthMiddleware())
    .on(HookEvent.RUN_START, on_start)
    .skill(WebBrowsingSkill())
    .create()
)
```

- All methods except `create()` are synchronous and return the builder.
- `create()` is async in Python/TypeScript (skill mounting), sync in PHP.
- `create()` instantiates `StandardAgent`, calls mixin registration methods in order, mounts skills last.
- Entry point is `StandardAgent.build(model)` static factory.

The builder is a thin convenience layer — it calls the same public mixin APIs that direct usage does. No new capabilities are introduced.

## Consequences

**Positive:**

- Client code reads fluently: chained setup, symmetric register/unregister pairs.
- Read-only accessors provide safe introspection without exposing internals.
- SkillManager no longer couples to mixin implementation details.
- Shared utilities eliminate 4 copies of identical helper functions per language.
- Builder reduces common multi-mixin setup from 10+ lines to a single expression.

**Negative:**

- Python's `on()` return type changed from `None` to `Self`. Existing code that ignores the return value is unaffected, but code that asserted `on()` returns `None` would break.
- Internal `_tools` renamed to `_tools_registry` in Python to avoid collision with the new `tools` property.
- Builder adds a new class per language, though it's thin (~100 lines each).
