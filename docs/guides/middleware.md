# Middleware

Middleware is the transformation layer that sits between the agent run loop and every LLM call. Each registered middleware can inspect and rewrite the outgoing message array before the request is sent, and inspect and rewrite the single assistant message that comes back. Unlike hooks, which observe without modifying, middleware can mutate state and its return value replaces what the next stage receives.

This guide covers the concepts that hold across all four implementations and shows the idiomatic API in each language. For the full language API reference, see the per-language guides ([Python](python.md), [TypeScript](typescript.md), [PHP](php.md), [Go](go.md)).

## Middleware versus hooks

| | Middleware | Hooks |
|---|---|---|
| **Purpose** | Transform data flowing into and out of the LLM | Observe lifecycle events |
| **Return value** | Mutated messages / message (replaces input) | Ignored |
| **Short-circuit** | Yes — an error aborts the run | No — hooks run to completion regardless |
| **Concurrency** | Sequential (each middleware waits for the previous) | Concurrent (handlers run in parallel) |
| **Use for** | Prompt injection, logging, filtering, guardrails | Metrics, audit logs, tracing |

If you only need to observe a request or response without changing it, prefer hooks. Reserve middleware for cases where you need to change the data.

## The Pre/Post model

Every middleware participates in two phases per LLM call:

| Phase | Input | Output | When |
|-------|-------|--------|------|
| **Pre** | Full message array | Transformed message array | Before the request is sent to the LLM |
| **Post** | Single assistant message | Transformed assistant message | After the response is received from the LLM |

`Pre` runs once per LLM call over the complete message history plus any system message. This is where you inject context, trim old messages, or add cache hints. `Post` runs once per LLM call over the single assistant reply. This is where you reformat output, filter sensitive content, or append metadata.

Both phases can mutate and return. Both can raise (or return an error in Go) — doing so short-circuits the chain and propagates the error up through `Run`.

## Execution order

Middleware runs sequentially in registration order. If you register A, then B, then C:

```
Pre:  A → B → C → LLM call
Post: A → B → C (applied to the assistant response)
```

Post runs in the same direction as Pre — A first, C last. This is a deliberate design choice. Reversing Post (the onion model used by many HTTP frameworks) requires mental context-switching when reasoning about the pipeline. Keeping both phases in the same order means the first-registered middleware is always first to see the data, whether inbound or outbound.

This matches the Python implementation exactly — `_run_pre` and `_run_post` both iterate `self._middleware` in forward order (see `has_middleware.py`). The Go `Chain.RunPre` and `Chain.RunPost` do the same.

## The middleware contract per language

### Python

The `Middleware` protocol defines two async methods. `BaseMiddleware` provides no-op defaults for both halves, so you can subclass and override only what you need:

```python
from src.python.has_middleware import Middleware, BaseMiddleware
from typing import Any

# Protocol (structural typing — any object with these methods qualifies):
class Middleware(Protocol):
    async def pre(self, messages: list[dict], context: Any) -> list[dict]: ...
    async def post(self, message: dict, context: Any) -> dict: ...

# No-op base for subclassing:
class BaseMiddleware:
    async def pre(self, messages: list[dict], context: Any) -> list[dict]:
        return messages
    async def post(self, message: dict, context: Any) -> dict:
        return message
```

`context` is the run context — a dict-like object shared across all middleware in a single run. Both sync and async implementations are supported; the framework detects `inspect.isawaitable` and wraps accordingly.

### TypeScript

The `Middleware` interface has two optional async methods. Any object literal or class satisfying the shape qualifies:

```typescript
import { Middleware } from "./has-middleware.js";

// Interface shape:
interface Middleware {
  pre?(messages: Message[], context: any): Promise<Message[]> | Message[];
  post?(message: Message, context: any): Promise<Message> | Message;
}
```

Both methods are optional — implement only `pre`, only `post`, or both. The dispatcher skips missing methods rather than calling a no-op.

### PHP

The `Middleware` interface is synchronous. `BaseMiddleware` provides pass-through defaults:

```php
use AgentHarness\Middleware;
use AgentHarness\BaseMiddleware;

// Interface:
interface Middleware {
    public function pre(array $messages, mixed $context): array;
    public function post(array $message, mixed $context): array;
}

// No-op base:
abstract class BaseMiddleware implements Middleware {
    public function pre(array $messages, mixed $context): array {
        return $messages;
    }
    public function post(array $message, mixed $context): array {
        return $message;
    }
}
```

PHP is the only language where the middleware contract is synchronous throughout.

### Go

The `Middleware` interface uses a `context.Context` for cancellation and an untyped `runCtx` for the per-run context (typed as `any` to avoid a circular import with the agent package). `middleware.Base` is an embeddable no-op struct. `middleware.MiddlewareFunc` adapts two plain functions into a `Middleware` without requiring a named type:

```go
// Interface:
type Middleware interface {
    Pre(ctx context.Context, messages []Message, runCtx any) ([]Message, error)
    Post(ctx context.Context, message Message, runCtx any) (Message, error)
}

// No-op embed:
type Base struct{}
func (Base) Pre(_ context.Context, msgs []Message, _ any) ([]Message, error) { return msgs, nil }
func (Base) Post(_ context.Context, msg Message, _ any) (Message, error)     { return msg, nil }

// Functional adapter:
mw := middleware.NewMiddlewareFunc(preFn, postFn) // nil = no-op for that phase
```

## Defining a middleware

A logging middleware that prints the message count before the call and a content preview after:

**Python:**

```python
from src.python.has_middleware import BaseMiddleware

class LoggingMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        print(f"[pre]  {len(messages)} message(s) outbound")
        return messages

    async def post(self, message, context):
        preview = (message.get("content") or "")[:60]
        print(f"[post] assistant: {preview!r}")
        return message
```

**TypeScript:**

```typescript
import { Middleware } from "./has-middleware.js";

const loggingMiddleware: Middleware = {
  async pre(messages, context) {
    console.log(`[pre]  ${messages.length} message(s) outbound`);
    return messages;
  },
  async post(message, context) {
    const preview = (message.content ?? "").slice(0, 60);
    console.log(`[post] assistant: ${JSON.stringify(preview)}`);
    return message;
  },
};
```

**PHP:**

```php
use AgentHarness\BaseMiddleware;

class LoggingMiddleware extends BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        echo "[pre]  " . count($messages) . " message(s) outbound\n";
        return $messages;
    }

    public function post(array $message, mixed $context): array
    {
        $preview = substr($message['content'] ?? '', 0, 60);
        echo "[post] assistant: " . json_encode($preview) . "\n";
        return $message;
    }
}
```

**Go:**

```go
import (
    "context"
    "fmt"
    "agent-harness/go/middleware"
)

type LoggingMiddleware struct{ middleware.Base }

func (LoggingMiddleware) Pre(_ context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
    fmt.Printf("[pre]  %d message(s) outbound\n", len(msgs))
    return msgs, nil
}

func (LoggingMiddleware) Post(_ context.Context, msg middleware.Message, _ any) (middleware.Message, error) {
    preview := msg.Content
    if len(preview) > 60 {
        preview = preview[:60]
    }
    fmt.Printf("[post] assistant: %q\n", preview)
    return msg, nil
}
```

## Installation

Register middleware on an agent with `use()`. Each call appends to the chain; order matters.

**Python:**

```python
agent.use(LoggingMiddleware())
agent.use(GuardrailMiddleware())

# Via the builder:
agent = await (
    StandardAgent.build("gpt-4")
    .middleware(LoggingMiddleware())
    .middleware(GuardrailMiddleware())
    .create()
)
```

**TypeScript:**

```typescript
agent.use(loggingMiddleware);
agent.use(guardrailMiddleware);

// Via the builder:
const agent = await StandardAgent.build("gpt-4")
  .middleware(loggingMiddleware)
  .middleware(guardrailMiddleware)
  .create();
```

**PHP:**

```php
$agent->use(new LoggingMiddleware());
$agent->use(new GuardrailMiddleware());

// Via the builder:
$agent = StandardAgent::build('gpt-4')
    ->middleware(new LoggingMiddleware())
    ->middleware(new GuardrailMiddleware())
    ->create();
```

**Go:**

```go
// Via the builder (preferred):
a, _ := agent.NewBuilder("gpt-4o").
    Use(LoggingMiddleware{}).
    Use(GuardrailMiddleware{}).
    Build(ctx)

// After Build, on the embedded Chain directly:
a.Chain.Use(LoggingMiddleware{})
```

To remove a specific middleware in Python and PHP, call `remove_middleware(mw)` / `$agent->removeMiddleware($mw)` with the same instance. TypeScript exposes `removeMiddleware(mw)`. Go has no removal API — see [Known limitations](#known-limitations).

## Use cases

### Prompt injection

The most common use of `Pre` is injecting additional context into the system prompt. Two built-in middleware do this:

**`SkillPromptMiddleware`** (Python, auto-installed in Go) reads the currently-mounted skills on every LLM call and appends a `## <skill-name>` block for each skill that has instructions. Because it reads the live skill list on each call, mounting or unmounting a skill takes effect without reinstalling the middleware:

```python
from src.python.has_skills import SkillPromptMiddleware

agent.use(SkillPromptMiddleware(agent.skills))
```

The Go builder installs the equivalent `skills.NewPromptMiddleware(manager)` automatically the first time `.Skill(...)` is called.

**Event prompt middleware** injects YAML format instructions when event types are registered. The builder installs it automatically in both Python and Go whenever events are queued — you do not wire it manually. The generated block looks like:

```
# Event Emission

You can emit structured events inline in your response using the following format:

## Event: progress
Description: Report task progress
Format:
---event
type: progress
step: <string>
percent: <integer>
---
```

For your own prompt injection, override `Pre` and splice a system message:

```python
class SystemPromptMiddleware(BaseMiddleware):
    def __init__(self, text: str):
        self._text = text

    async def pre(self, messages, context):
        messages = [dict(m) for m in messages]
        for m in messages:
            if m.get("role") == "system":
                m["content"] = m["content"] + "\n\n" + self._text
                return messages
        return [{"role": "system", "content": self._text}] + messages
```

### Logging and audit

Record every request and response for compliance or debugging. Keep all I/O internal to the middleware so errors do not propagate:

```python
class AuditMiddleware(BaseMiddleware):
    def __init__(self, store):
        self._store = store

    async def pre(self, messages, context):
        self._store.record_request(messages)
        return messages

    async def post(self, message, context):
        self._store.record_response(message)
        return message
```

### Retry wrapping

Retry logic belongs at the LLM client layer, not in middleware. Middleware does not have the ability to re-invoke the LLM call — it only sees the messages going in and the response coming out. In Go, wrap the client with `llm.WithRetry`:

```go
import "agent-harness/go/llm"

retrying := llm.WithRetry(client, llm.RetryConfig{MaxAttempts: 3})
a, _ := agent.NewBuilder("gpt-4o").Client(retrying).Build(ctx)
```

In Python and TypeScript, retry wraps the underlying LLM call inside the base agent's `_call_llm` method or at the provider level. Do not attempt to implement retry inside a middleware — you will end up duplicating run loop state.

### Rate limiting

Apply backpressure in `Pre` before any tokens are sent. The middleware holds the request until a token is available and returns the messages unchanged:

```python
import asyncio

class RateLimitMiddleware(BaseMiddleware):
    def __init__(self, rps: float):
        self._interval = 1.0 / rps
        self._last = 0.0

    async def pre(self, messages, context):
        import time
        now = time.monotonic()
        wait = self._interval - (now - self._last)
        if wait > 0:
            await asyncio.sleep(wait)
        self._last = time.monotonic()
        return messages
```

### Message trimming

Drop oldest non-system messages to stay within a context window. Run this in `Pre` after prompt-injection middleware so injected content counts toward the budget:

```python
class TrimMiddleware(BaseMiddleware):
    def __init__(self, max_messages: int):
        self._max = max_messages

    async def pre(self, messages, context):
        system = [m for m in messages if m.get("role") == "system"]
        other  = [m for m in messages if m.get("role") != "system"]
        if len(other) > self._max:
            other = other[-self._max:]
        return system + other
```

### Prompt caching (Anthropic)

Anthropic's API supports cache-control headers on messages and system prompts. Add them in `Pre` using the Anthropic provider option rather than middleware when the cache boundary is static. For dynamic cache boundaries — where the boundary changes based on runtime state — a middleware can stamp `cache_control` metadata onto the last eligible message:

```python
class CacheControlMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        messages = [dict(m) for m in messages]
        # Mark the last user message as a cache checkpoint.
        for m in reversed(messages):
            if m.get("role") == "user":
                m["cache_control"] = {"type": "ephemeral"}
                break
        return messages
```

For static system prompt caching, configure it at the provider level rather than adding middleware overhead on every request.

## Short-circuit on error

If any middleware returns an error (Go) or raises an exception (Python / TypeScript / PHP), the chain stops and the error propagates up through `Run` to the caller. No subsequent middleware in the chain runs. The LLM is not called if the error occurs in `Pre`; the assistant message is not appended to history if the error occurs in `Post`.

Use this only for genuine abort conditions — input validation failures, policy violations, or unrecoverable I/O errors. For logging and observability, catch exceptions internally and return the unmodified data:

```python
# Wrong — a logging error aborts the whole run:
async def post(self, message, context):
    self._sink.write(message)   # raises if the sink is down
    return message

# Right — log failures are non-fatal:
async def post(self, message, context):
    try:
        self._sink.write(message)
    except Exception as exc:
        logger.warning("audit write failed: %s", exc)
    return message
```

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Contract | `Middleware` Protocol + `BaseMiddleware` class | `Middleware` interface (object literal) | `Middleware` interface + `BaseMiddleware` abstract class | `middleware.Middleware` interface + `middleware.Base` embed |
| `Pre` signature | `async def pre(messages, ctx)` | `async pre?(messages, ctx)` | `pre(array, mixed): array` | `Pre(ctx, []Message, any) ([]Message, error)` |
| `Post` signature | `async def post(message, ctx)` | `async post?(message, ctx)` | `post(array, mixed): array` | `Post(ctx, Message, any) (Message, error)` |
| Async | Yes (`async/await`) | Yes (`Promise`) | No (synchronous) | No (blocking; use goroutines inside if needed) |
| Both methods required | Yes (via BaseMiddleware no-ops) | No (each is optional) | Yes (via BaseMiddleware no-ops) | Yes (via Base embed no-ops) |
| Error short-circuit | `raise` an exception | `throw` an error | `throw` an exception | `return ..., err` |
| Functional adapter | No | No | No | `middleware.MiddlewareFunc` / `NewMiddlewareFunc` |
| Remove API | `remove_middleware(mw)` | `removeMiddleware(mw)` | `removeMiddleware($mw)` | None |
| Registration | `agent.use(mw)` | `agent.use(mw)` | `$agent->use($mw)` | `a.Chain.Use(mw)` or `Builder.Use(mw)` |

## Known limitations

Shared across all languages:

- **No per-handler remove in Go.** The `middleware.Chain` has no removal method. Once registered, a middleware stays for the lifetime of the chain. This is tracked as a known gap. If you need conditional middleware, implement the condition inside the middleware rather than removing and re-adding it.
- **Registration order is mount order.** When skills contribute middleware via `middleware()`, those entries are appended to the chain at mount time. The order in which skills are mounted determines where their middleware sits in the chain. Mount order-sensitive skills with this in mind.
- **No middleware reset API.** None of the languages expose a method to clear the entire chain short of creating a new agent. Design middleware that is safe to run on every LLM call from agent construction onwards.
- **No middleware removal on skill unmount.** Unmounting a skill removes its tools and commands, but the middleware it contributed remains in the chain. This matches Python's current behaviour and is tracked as a known gap in both implementations. Do not unmount-and-remount skills that contribute middleware if ordering matters.

## See also

- [ADR 0018](../adr/0018-middleware-pipeline.md) — the middleware pipeline design and the choice of dual-phase over Express-style `next()` chaining
- [Skills guide](skills.md) — how skills contribute middleware, and how `SkillPromptMiddleware` works
- [Python guide: Middleware](python.md#middleware) · [TypeScript guide: Middleware](typescript.md#middleware) · [PHP guide: Middleware](php.md#middleware) · [Go guide: Middleware](go.md#middleware)
