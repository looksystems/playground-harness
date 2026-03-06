# 15. Lifecycle Hooks

Date: 2026-03-05

## Status

Accepted

## Context

Agents need extensibility points for logging, metrics, UI updates, guardrails, and debugging. These are cross-cutting concerns that shouldn't be baked into the core agent loop. We needed a way for external code to observe and react to agent lifecycle events without modifying the agent itself.

Two patterns were considered:

1. **Event-based hooks** — subscribe to named events, multiple listeners per event, fire-and-forget
2. **Middleware** — sequential pipeline where each step transforms data and passes it to the next

Both are useful but serve different purposes. Hooks are for observation and side effects. Middleware is for transformation. We implemented both as separate mixins.

## Decision

The `HasHooks` mixin provides a pub/sub system with 10 lifecycle events covering the full agent run cycle:

| Event | Fires when |
|-------|-----------|
| `run_start` | Agent run begins |
| `run_end` | Agent run completes |
| `llm_request` | Before sending messages to the LLM |
| `llm_response` | After receiving LLM response |
| `tool_call` | Before executing a tool |
| `tool_result` | After tool execution succeeds |
| `tool_error` | After tool execution fails |
| `retry` | Before retrying after an error |
| `token_stream` | On each streaming token |
| `error` | On unrecoverable error |

Key design choices:

- **Concurrent dispatch** (Python/TypeScript) — all subscribers for an event fire via `asyncio.gather` / `Promise.allSettled`. A slow logging hook doesn't block a metrics hook. Errors in one hook don't prevent others from running.
- **Sequential dispatch** (PHP) — synchronous `foreach` loop, matching PHP's execution model.
- **Sync/async interop** — handlers can be sync or async functions. The framework detects via `inspect.isawaitable` (Python) or `Promise.resolve` wrapping (TypeScript) and handles both transparently.
- **No return values** — hooks are observers. They cannot modify the data flowing through the agent. Use middleware for transformation.

## Consequences

- Clean separation between observation (hooks) and transformation (middleware)
- Multiple independent consumers can react to the same event without coordination
- Concurrent dispatch in Python/TypeScript means hook ordering is not guaranteed — don't rely on one hook running before another
- The 10-event set covers the full lifecycle without being so granular that it's confusing
- Adding new events requires updating the `HookEvent` enum, which is a deliberate friction point to prevent event proliferation
- The event set was later expanded to 18 events with [ADR 0022](0022-shell-and-registration-hooks.md), adding shell execution, registration, and CWD change hooks, then to 22 events with [ADR 0023](0023-has-commands-mixin.md), adding four `slash_command_*` hooks.
