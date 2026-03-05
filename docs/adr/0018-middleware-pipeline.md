# 18. Middleware Pipeline

Date: 2026-03-05

## Status

Accepted

## Context

Agents need a way to transform messages flowing in and out of the LLM. Common use cases include injecting system prompts, sanitizing user input, filtering sensitive data from responses, adding guardrails, and reformatting output. Unlike hooks (which observe without modifying), middleware must be able to change the data passing through the pipeline.

Two pipeline styles were considered:

1. **Express-style `next()` chaining** — each middleware calls `next()` to pass control, can wrap pre/post logic around it
2. **Dual-phase `pre`/`post`** — separate methods for inbound (messages to LLM) and outbound (response from LLM), executed sequentially

We chose dual-phase for simplicity and predictability.

## Decision

The `HasMiddleware` mixin provides a sequential pipeline with two phases:

### The Middleware contract

| Method | Input | Output | When |
|--------|-------|--------|------|
| `pre(messages, context)` | Message array | Transformed message array | Before sending to LLM |
| `post(message, context)` | Single response message | Transformed response message | After receiving from LLM |

### Key design choices

- **Sequential execution** — middleware runs in registration order via `agent.use()`. Each middleware's output becomes the next middleware's input. This makes the pipeline deterministic and easy to reason about, unlike hooks which fire concurrently.
- **Both methods optional** (TypeScript/PHP) — middleware can implement just `pre` or just `post`. Python provides `BaseMiddleware` with pass-through defaults for both.
- **Sync/async interop** — middleware methods can be sync or async in Python (`inspect.isawaitable`) and TypeScript (`Promise.resolve` wrapping). PHP is synchronous only.
- **No short-circuiting** — every middleware in the stack runs. A guardrail middleware that wants to block a request should raise an exception (or return an empty/error message), not silently skip remaining middleware.
- **Shared context object** — the `context` parameter lets middleware share state within a single run without coupling to each other. The framework passes the same context dict through all `pre` calls and all `post` calls.

### Why not Express-style `next()`

Express-style middleware is powerful but introduces complexity: forgetting to call `next()` silently breaks the chain, error handling requires try/catch around `next()`, and the call stack grows with middleware depth. The dual-phase approach is flatter and harder to misuse — you transform and return, nothing else.

### Registration

```python
agent.use(LoggingMiddleware())
agent.use(GuardrailMiddleware(rules=[...]))
agent.use(SystemPromptMiddleware(prompt="You are helpful."))
```

Order matters: middleware registered first runs first in `pre` and first in `post`. This differs from some frameworks that reverse order for `post` (onion model). We chose consistent ordering for simplicity.

## Consequences

- Clear separation from hooks: middleware transforms data, hooks observe it
- Sequential execution means middleware order is explicit and deterministic
- The `pre`/`post` split maps naturally to the request/response cycle of LLM calls
- No `next()` means middleware cannot wrap async lifecycle around the LLM call itself — this is intentional; hooks cover that via `llm_request`/`llm_response`
- Adding middleware is always `agent.use()`, making it discoverable and consistent across languages
