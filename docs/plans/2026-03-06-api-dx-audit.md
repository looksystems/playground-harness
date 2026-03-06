# API & DX Audit: Agent Harness

## Context

The agent-harness library exposes a public API across PHP and TypeScript for building LLM agents with tools, skills, hooks, middleware, events, and a virtual shell. This audit identifies DX friction points, naming inconsistencies, type safety gaps, and cross-language parity issues — then proposes concrete fixes.

---

## 1. Cross-Language Naming Inconsistency (High Priority)

The PHP and TypeScript APIs use different naming conventions for the same operations. This is the single biggest DX issue — users switching between languages (or reading docs) hit constant friction.

| Operation | PHP | TypeScript | Issue |
|-----------|-----|-----------|-------|
| Register tool | `registerTool()` | `register_tool()` | snake_case in TS is non-idiomatic |
| Unregister tool | `unregisterTool()` | `unregister_tool()` | same |
| Tools schema | `toolsSchema()` | `_tools_schema()` | snake + underscore prefix |
| Execute tool | `executeTool()` | `_execute_tool()` | snake + underscore prefix |
| Emit hook | `emit()` (public) | `_emit()` (pseudo-private) | visibility mismatch |
| Run pre | `runPre()` | `_run_pre()` | snake + underscore prefix |
| Run post | `runPost()` | `_run_post()` | snake + underscore prefix |
| Register event | `registerEvent()` | `register_event()` | snake_case in TS |
| Default events | `$defaultEvents` | `default_events` | snake_case in TS |

**Recommendation:** Normalize TypeScript to idiomatic camelCase across the board:
- `register_tool()` → `registerTool()`
- `unregister_tool()` → `unregisterTool()`
- `_tools_schema()` → `toolsSchema()`
- `_execute_tool()` → `executeTool()`
- `_emit()` → `emit()`
- `_run_pre()` → `runPre()`
- `_run_post()` → `runPost()`
- `register_event()` → `registerEvent()`
- `default_events` → `defaultEvents`

**Files:** `src/typescript/uses-tools.ts`, `src/typescript/has-hooks.ts`, `src/typescript/has-middleware.ts`, `src/typescript/emits-events.ts`, `src/typescript/has-shell.ts`, and all corresponding test files.

---

## 2. Underscore-Prefix "Private" Convention in TypeScript (High Priority)

TypeScript has `private`/`protected` keywords. Using `_` prefix is a JavaScript-era convention that's unnecessary and makes the API look unpolished.

**Current offenders:** `_tools`, `_hooks`, `_middleware`, `_emit`, `_run_pre`, `_run_post`, `_execute_tool`, `_tools_schema`, `_ensureHasSkills`, `_skillManager`

**Recommendation:** Use proper TypeScript access modifiers. For mixin properties that need cross-mixin access, either:
- Keep them public with clear names (e.g., `hooks`, `middlewareStack`)
- Or accept that mixin composition in TS requires public fields and document accordingly

**Note:** The SkillManager directly accesses `this.agent._middleware` (line 222-235 of `has-skills.ts`) which is fragile. This should go through a proper API.

**Files:** All `src/typescript/*.ts` files using `_` prefix.

---

## 3. `method_exists()` / `typeof` Guards (Medium Priority)

Both PHP and TypeScript traits/mixins are riddled with runtime capability checks:

```php
// PHP - appears 15+ times across traits
if (method_exists($this, 'emit')) {
    $this->emit(HookEvent::ToolRegister, $tool);
}
```

```typescript
// TS - appears 10+ times across mixins
if (typeof (this as any)._emit === "function") {
    void (this as any)._emit(HookEvent.TOOL_REGISTER, toolDef);
}
```

This is a code smell from trait composition without contracts. Every trait independently checks whether sibling traits exist.

**Recommendation:** Two options:
- **(A) Accept it as inherent to the mixin pattern** — but at minimum, extract helper methods like `emitIfAvailable()` to reduce duplication
- **(B) Define a capability interface** (e.g., `HasEmitter`) that traits can declare dependency on, shifting the check to mount-time rather than every call

Option A is simpler and keeps the current architecture. Option B is cleaner but more invasive.

---

## 4. No Way to Remove Hooks or Middleware (Medium Priority)

**Hooks:** `on()` returns `void`. There's no `off()` or way to unsubscribe a callback. The SkillManager can't cleanly remove hooks when unmounting a skill — they leak.

**Middleware:** `use()` adds middleware but there's no `removeMiddleware()`. The PHP `SkillManager::rebuildPromptMiddleware()` references `removeMiddleware()` (line 220-221) which doesn't exist on the `HasMiddleware` trait. The TS version works around this by directly mutating `this.agent._middleware` (an internal array).

**Recommendation:**
- `on()` should return a dispose/unsubscribe function: `on(event, cb): () => void`
- Add `removeMiddleware(mw: Middleware): void` to `HasMiddleware`

**Files:** `src/php/HasHooks.php`, `src/php/HasMiddleware.php`, `src/typescript/has-hooks.ts`, `src/typescript/has-middleware.ts`

---

## 5. Weak Typing of `agent` References (Medium Priority)

The `agent` field is typed as `object`/`mixed`/`any` in multiple places:

| Location | PHP Type | TS Type |
|----------|----------|---------|
| `RunContext.agent` | `object` | `any` |
| `SkillContext.agent` | `mixed` | `any` |
| `SkillManager.__construct($agent)` | `mixed` | `any` |

Users accessing `$ctx->agent` get zero autocomplete or type safety.

**Recommendation:** Either:
- Use `BaseAgent` as the type (minimum useful contract)
- Or introduce an `AgentInterface` that declares the methods traits may call (`registerTool`, `on`, `use`, `emit`, etc.) and type against that

**Files:** `src/php/RunContext.php`, `src/php/SkillContext.php`, `src/php/SkillManager.php`, `src/typescript/has-skills.ts`

---

## 6. `Skill.hooks()` Return Type Mismatch (Low-Medium Priority)

**PHP:** Returns `array<string, array<int, callable>>` — hooks keyed by raw string values.
**TypeScript:** Returns `Partial<Record<HookEvent, Array<...>>>` — hooks keyed by HookEvent enum.

The PHP version forces the SkillManager to do `HookEvent::from($eventValue)` to convert strings back to enums. Users writing skills in PHP must use raw strings like `'run_start'` instead of `HookEvent::RunStart->value`.

**Recommendation:** PHP `Skill::hooks()` should accept `HookEvent` keys directly:
```php
public function hooks(): array
// Return: array<HookEvent, list<callable>>
// Usage: [HookEvent::RunStart => [fn() => ...]]
```

Or keep string keys but change `SkillManager` to not convert — the string values are already what `HasHooks` uses internally.

**Files:** `src/php/Skill.php`, `src/php/SkillManager.php`

---

## 7. ToolDef Factory Inconsistency (Low Priority)

**PHP:** `ToolDef::make()` static factory + constructor
**TypeScript:** `defineTool()` free function (identity function that just returns its argument)

The TS `defineTool()` does literally nothing — it's `return def`. It exists only for discoverability.

**Recommendation:** Either:
- Remove `defineTool()` from TS (it adds no value) and let users construct `ToolDef` objects directly
- Or add a `ToolDef.make()` static method in TS to match PHP, with actual validation

---

## 8. `emit()` Swallows All Errors Silently (Low Priority)

PHP `HasHooks::emit()` catches all `\Throwable` and silently discards it. TypeScript logs a `console.warn`. Neither lets the user opt into error visibility.

**Recommendation:** At minimum, emit errors through a dedicated `HookEvent::Error` or similar mechanism so users can observe hook failures if they want to.

---

## 9. Missing Fluent/Chainable API (Low Priority)

Methods like `registerTool()`, `on()`, `use()`, `mount()` all return `void`. Setup code becomes vertical:

```php
$agent->registerTool($tool1);
$agent->registerTool($tool2);
$agent->on(HookEvent::RunStart, $cb);
$agent->use($mw);
```

**Recommendation:** Return `$this`/`this` from mutator methods to enable:
```php
$agent->registerTool($tool1)
      ->registerTool($tool2)
      ->on(HookEvent::RunStart, $cb)
      ->use($mw);
```

Low priority because the current style is perfectly functional.

---

## 10. Shell Auto-Registration Side Effect (Low Priority)

`initHasShell()` silently registers an `exec` tool when `UsesTools` is composed. This is a surprise side effect — calling a shell init method shouldn't modify the tool registry without the user knowing.

**Recommendation:** Either:
- Document this clearly
- Or make it opt-in: `initHasShell(registerTool: true)`
- Or separate: `initHasShell()` + `registerShellTool()`

---

## 11. `EventType` vs `HookEvent` Naming Confusion (Low Priority)

- `HookEvent` = lifecycle events (run_start, tool_call, etc.)
- `EventType` = structured events emitted inline in LLM output

Both use "event" in the name. A user seeing `registerEvent()` and `on(HookEvent::...)` has to learn the distinction.

**Recommendation:** Rename `EventType` to `StructuredEvent` or `OutputEvent` to distinguish from lifecycle hooks.

---

## Summary by Priority

| # | Issue | Priority | Effort |
|---|-------|----------|--------|
| 1 | Cross-language naming inconsistency | High | Medium |
| 2 | Underscore-prefix convention in TS | High | Medium |
| 3 | `method_exists`/`typeof` guards everywhere | Medium | Low-Medium |
| 4 | No hook/middleware removal API | Medium | Low |
| 5 | Weak typing of `agent` references | Medium | Low |
| 6 | `Skill.hooks()` return type mismatch | Low-Med | Low |
| 7 | ToolDef factory inconsistency | Low | Low |
| 8 | `emit()` swallows errors silently | Low | Low |
| 9 | Missing fluent/chainable API | Low | Low |
| 10 | Shell auto-registration side effect | Low | Low |
| 11 | EventType vs HookEvent naming | Low | Low |

## Verification

After implementing changes:
1. Run PHP tests: `vendor/bin/phpunit`
2. Run TypeScript tests: `npx vitest`
3. Verify exports in `src/typescript/index.ts` match renamed symbols
4. Search for any direct `_property` access patterns in tests that would break
