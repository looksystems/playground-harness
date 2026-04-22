# Hooks

Hooks are the observability layer of the agent harness. The `HasHooks` mixin (Go: `hooks.Hub`) exposes a pub/sub system where external code subscribes to named lifecycle events that the framework emits throughout an agent run. Handlers are observers — they cannot modify data in flight (use middleware for that) — but they can log, record metrics, update UIs, enforce guardrails, and trigger side effects.

This guide covers the concepts that hold across all four implementations and shows the idiomatic API in each language. For the full language API reference, see the per-language guides ([Python](python.md), [TypeScript](typescript.md), [PHP](php.md), [Go](go.md)).

## When to use hooks

Reach for hooks when you need to:

- **Log** — record every LLM request, tool call, or shell command without touching the agent loop.
- **Audit** — write a tamper-evident record of what the agent did (useful for skill lifecycle changes, tool registrations).
- **Observe** — feed a metrics system, update a progress bar, or stream partial output to a UI.
- **Side-effect subscription** — trigger an external notification when the run completes or an error occurs.

Hooks complement middleware. Middleware transforms; hooks observe.

## The 24 events

Events are grouped by the subsystem that emits them. The string values are canonical snake_case identifiers shared across all languages.

### Run lifecycle

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `RUN_START` | Agent run begins (before the first turn) | *(none)* |
| `RUN_END` | Agent run completes (after the last turn, including on error) | *(none)* |

### LLM

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `LLM_REQUEST` | Just before sending messages to the LLM | `messages` — the list being sent |
| `LLM_RESPONSE` | After receiving the LLM reply | `message` — the assistant message received |
| `RETRY` | Before a retry attempt | `attempt` (int), `error` |
| `TOKEN_STREAM` | On each streaming token as it arrives | `token` (str) |

### Tools

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `TOOL_CALL` | Before executing a tool | `name` (str), `args` (dict/map) |
| `TOOL_RESULT` | After a tool returns successfully | `name` (str), `result` |
| `TOOL_ERROR` | After a tool raises or returns an error | `name` (str), `error` |
| `TOOL_REGISTER` | A tool is registered on the agent | `name` (str) |
| `TOOL_UNREGISTER` | A tool is unregistered from the agent | `name` (str) |

### Shell

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `SHELL_CALL` | Before executing a shell command string | `cmd` (str) |
| `SHELL_RESULT` | After a shell command completes | `cmd` (str), `result` (`ExecResult`) |
| `SHELL_NOT_FOUND` | A shell command name is not recognised | `name` (str) |
| `SHELL_CWD` | The shell working directory changes | `old` (str), `new` (str) |
| `SHELL_STDOUT_CHUNK` | A chunk of stdout arrives (streaming drivers) | `chunk` (str) |
| `SHELL_STDERR_CHUNK` | A chunk of stderr arrives (streaming drivers) | `chunk` (str) |

### Commands

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `COMMAND_REGISTER` | A custom shell command is registered | `name` (str) |
| `COMMAND_UNREGISTER` | A custom shell command is unregistered | `name` (str) |

### Skills

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `SKILL_MOUNT` | A skill is mounted on the agent | `name` (str) |
| `SKILL_UNMOUNT` | A skill is unmounted | `name` (str) |
| `SKILL_SETUP` | A skill's `setup()` completes successfully | `name` (str) |
| `SKILL_TEARDOWN` | A skill's `teardown()` completes | `name` (str) |

### Generic error

| Event | Fires when | Handler args |
|-------|-----------|-------------|
| `ERROR` | An unrecoverable error occurs during the agent run | `error` |

## Subscribing

Subscribe by calling `on` with the event constant and a handler. All registration methods return the agent for fluent chaining.

**Python:**

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.RUN_START, lambda: print("run started"))
agent.on(HookEvent.TOOL_CALL, lambda name, args: print(f"calling {name}"))

# Fluent chaining
agent.on(HookEvent.RUN_START, on_start).on(HookEvent.RUN_END, on_end)

# Via the builder
from src.python.standard_agent import StandardAgent

agent = await (
    StandardAgent.build("gpt-4")
    .on(HookEvent.RUN_START, lambda: print("started"))
    .on(HookEvent.ERROR, lambda err: log.error("agent error: %s", err))
    .create()
)
```

**TypeScript:**

```typescript
import { HookEvent } from "./has-hooks.js";

agent.on(HookEvent.RUN_START, () => console.log("run started"));
agent.on(HookEvent.TOOL_CALL, (name, args) => console.log(`calling ${name}`));

// Via the builder
const agent = await StandardAgent.build("gpt-4")
  .on(HookEvent.RUN_START, () => console.log("started"))
  .on(HookEvent.ERROR, (err) => console.error("agent error:", err))
  .create();
```

**PHP:**

```php
use AgentHarness\HookEvent;

$agent->on(HookEvent::RunStart, function () {
    echo "run started\n";
});
$agent->on(HookEvent::ToolCall, function (string $name, array $args) {
    echo "calling {$name}\n";
});

// Via the builder
$agent = StandardAgent::build('gpt-4')
    ->on(HookEvent::RunStart, fn() => print("started\n"))
    ->on(HookEvent::Error, fn($err) => error_log("agent error: {$err}"))
    ->create();
```

**Go:**

```go
import "agent-harness/go/hooks"

// Via the builder (preferred at construction time)
a, err := agent.NewBuilder("gpt-4o").
    On(hooks.RunStart, func(ctx context.Context, args ...any) {
        fmt.Println("run started")
    }).
    On(hooks.ToolCall, func(ctx context.Context, args ...any) {
        name, _ := args[0].(string)
        fmt.Printf("calling tool: %s\n", name)
    }).
    Build(ctx)

// After Build — address the embedded Hub directly
a.Hub.On(hooks.RunEnd, func(ctx context.Context, args ...any) {
    fmt.Println("run ended")
})
```

## Handler signatures

Handlers receive positional arguments that depend on the event. The Python source (`has_hooks.py`) is the canonical definition; Go and TypeScript follow the same argument order using variadic `...any`.

Python handlers can be **sync or async** — the framework detects `inspect.isawaitable` and wraps accordingly. TypeScript handlers are always `async`-compatible because dispatch uses `Promise.allSettled`. PHP handlers are synchronous. Go handlers receive a `context.Context` as their first argument followed by variadic `any`.

```python
# Sync handler — fine
agent.on(HookEvent.TOOL_CALL, lambda name, args: print(name))

# Async handler — also fine
async def on_tool_call(name, args):
    await audit_log.write(name, args)

agent.on(HookEvent.TOOL_CALL, on_tool_call)
```

```go
// Go — always func(ctx context.Context, args ...any)
a.Hub.On(hooks.ToolCall, func(ctx context.Context, args ...any) {
    name, _ := args[0].(string)
    callArgs, _ := args[1].(map[string]any)
    log.Printf("tool call: %s %v", name, callArgs)
})
```

## Emit vs EmitAsync (fire-and-forget)

The framework emits most hooks **synchronously** via `Emit` — it dispatches all handlers concurrently and waits for all of them to complete before the agent loop continues. This ensures that by the time a `TOOL_CALL` handler returns, the hook has finished and the tool hasn't started yet. The ordering guarantee is event-level, not handler-level: handlers for the same event run concurrently with each other.

`EmitAsync` (Go) / the Python equivalent `emit_fire_and_forget` dispatches on a separate goroutine and returns immediately — the agent loop does not wait. The framework uses this for non-critical, high-frequency events where blocking would introduce noticeable latency:

- `SHELL_STDOUT_CHUNK` and `SHELL_STDERR_CHUNK` — emitted per streaming chunk; a slow log handler must not stall the shell read loop.
- `TOKEN_STREAM` — emitted per LLM token; same reasoning.

If you are emitting custom hooks from your own code, prefer fire-and-forget for side effects that should not block the caller:

```go
// Fire-and-forget — the current goroutine continues immediately
a.Hub.EmitAsync(ctx, hooks.RunStart)

// Synchronous — waits for all handlers
_ = a.Hub.Emit(ctx, hooks.ToolCall, "fetch_page", args)
```

```python
# Python — always via _emit (awaited); use asyncio.create_task for fire-and-forget
asyncio.create_task(agent._emit(HookEvent.TOKEN_STREAM, token))
```

## Panic and error isolation

A handler that panics or raises an exception is isolated. Other handlers for the same event continue running; the framework recovers, logs the error, and does not propagate the failure to the caller.

**Python** uses `asyncio.gather(..., return_exceptions=True)`:

```python
results = await asyncio.gather(
    *[call_fn(cb, *args) for cb in callbacks],
    return_exceptions=True,
)
for r in results:
    if isinstance(r, Exception):
        logger.warning("Hook %s error: %s", event.value, r)
```

**Go** wraps each goroutine in a deferred `recover()`:

```go
defer func() {
    if rec := recover(); rec != nil {
        buf := make([]byte, 4096)
        n := runtime.Stack(buf, false)
        log.Printf("hooks.Emit: recovered panic in handler for %q: %v\n%s",
            event, rec, buf[:n])
    }
}()
```

**TypeScript** uses `Promise.allSettled` — rejected promises are collected but never re-thrown.

**PHP** dispatch is a sequential `foreach`; each callback is wrapped in a try/catch and errors are logged, but remaining handlers still run.

This means: **write handlers defensively, but do not rely on the framework hiding programming errors**. A panic from a nil-pointer dereference will be caught and logged, but it will also silently skip whatever side effect the handler was supposed to perform.

## Concurrency

Handlers registered for the same event are dispatched **concurrently** in Python, TypeScript, and Go. The framework does not impose an ordering between handlers for the same event.

| Language | Mechanism | Waits for completion |
|----------|-----------|---------------------|
| Python | `asyncio.gather` | Yes (via `await`) |
| TypeScript | `Promise.allSettled` | Yes (via `await`) |
| Go | goroutines + `sync.WaitGroup` | Yes (via `wg.Wait()`) |
| PHP | sequential `foreach` | Yes (synchronous) |

If your handler reads or mutates shared state (a counter, a file, a DB connection), it must be **thread-safe**. Two `TOOL_CALL` handlers registered by different parts of your code can run at the same time.

```python
import threading

_counter = 0
_lock = threading.Lock()

def count_tool_calls(name, args):
    global _counter
    with _lock:
        _counter += 1
```

```go
var mu sync.Mutex
var count int

a.Hub.On(hooks.ToolCall, func(ctx context.Context, args ...any) {
    mu.Lock()
    count++
    mu.Unlock()
})
```

## Unsubscribing

### Python — per-handler removal

`remove_hook` removes a specific callback by identity. You must hold a reference to the original callable:

```python
def on_start():
    print("started")

agent.on(HookEvent.RUN_START, on_start)
# later…
agent.remove_hook(HookEvent.RUN_START, on_start)
```

Lambda functions cannot be removed unless you keep a reference:

```python
handler = lambda: print("started")
agent.on(HookEvent.RUN_START, handler)
agent.remove_hook(HookEvent.RUN_START, handler)   # works
```

### PHP — per-handler removal via returned Closure

`on()` returns a `\Closure` that removes the handler when called:

```php
$off = $agent->on(HookEvent::RunStart, function () { echo "started\n"; });
// later…
$off();   // removes just this handler

// Or by reference:
$agent->removeHook(HookEvent::RunStart, $callback);
```

### TypeScript — per-handler removal

`removeHook(event, callback)` removes a specific handler:

```typescript
const handler = () => console.log("started");
agent.on(HookEvent.RUN_START, handler);
agent.removeHook(HookEvent.RUN_START, handler);
```

### Go — event-level removal only

`Hub.Off(event)` removes **all** handlers for an event at once. There is no per-handler removal:

```go
a.Hub.On(hooks.RunStart, handlerA)
a.Hub.On(hooks.RunStart, handlerB)

a.Hub.Off(hooks.RunStart)   // removes both handlerA and handlerB
```

This is a deliberate divergence from Python. Go function values are not comparable, so per-handler removal would require either a token-based API (not yet implemented) or reflection. If you need selective removal, gate the work inside the handler itself using a flag or a closed-over context:

```go
var enabled atomic.Bool
enabled.Store(true)

a.Hub.On(hooks.RunStart, func(ctx context.Context, args ...any) {
    if !enabled.Load() {
        return
    }
    fmt.Println("started")
})

// To "remove" just this handler:
enabled.Store(false)
```

## Defensive snapshots

`Handlers(event)` (Go) and the `hooks` property (Python) return **copies** of the internal slice/list. Mutating the returned value does not affect the registry.

```go
hs := a.Hub.Handlers(hooks.ToolCall)
hs = append(hs, extraHandler)   // does not register extraHandler on the Hub
```

```python
snapshot = agent.hooks          # dict[HookEvent, list[Callable]] — a copy
snapshot[HookEvent.RUN_START].clear()   # does not affect the agent's registry
```

Emit takes its own snapshot at the start of each call, so handlers registered during a dispatch are not invoked for the current event (but will be for the next).

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Registration | `agent.on(event, fn)` | `agent.on(event, fn)` | `$agent->on(event, fn)` | `agent.Hub.On(event, fn)` / builder `.On(event, fn)` |
| Emit signature | `await agent._emit(event, *args)` | `await agent._emit(event, ...args)` | `$agent->emit(event, ...$args)` | `hub.Emit(ctx, event, args...)` |
| Async dispatch | `asyncio.gather` | `Promise.allSettled` | sequential `foreach` | goroutines + `sync.WaitGroup` |
| Fire-and-forget | `asyncio.create_task` | — | — | `hub.EmitAsync(ctx, event, args...)` |
| Per-handler remove | `agent.remove_hook(event, fn)` | `agent.removeHook(event, fn)` | `$off()` closure / `removeHook` | not supported; `Off` removes all |
| Handler type | sync or async callable | sync or async function | callable | `func(context.Context, ...any)` |
| Snapshot accessor | `agent.hooks` (property) | `agent.hooks` (property) | `$agent->getHooks()` | `hub.Handlers(event)` |

## Examples

### Log every tool call

**Python:**

```python
from src.python.has_hooks import HookEvent
import logging

log = logging.getLogger(__name__)

agent.on(HookEvent.TOOL_CALL, lambda name, args: log.info("tool_call name=%s", name))
agent.on(HookEvent.TOOL_RESULT, lambda name, result: log.info("tool_result name=%s", name))
agent.on(HookEvent.TOOL_ERROR, lambda name, err: log.warning("tool_error name=%s err=%s", name, err))
```

**Go:**

```go
a.Hub.On(hooks.ToolCall, func(ctx context.Context, args ...any) {
    name, _ := args[0].(string)
    log.Printf("tool_call name=%s", name)
})
a.Hub.On(hooks.ToolResult, func(ctx context.Context, args ...any) {
    name, _ := args[0].(string)
    log.Printf("tool_result name=%s", name)
})
a.Hub.On(hooks.ToolError, func(ctx context.Context, args ...any) {
    name, _ := args[0].(string)
    log.Printf("tool_error name=%s err=%v", name, args[1])
})
```

### Record skill lifecycle to an audit log

Useful for compliance scenarios where you need a record of which skills were active during a run.

**Python:**

```python
import json, time

audit = []

def audit_skill(event_name):
    def handler(name):
        audit.append({"event": event_name, "skill": name, "ts": time.time()})
    return handler

agent.on(HookEvent.SKILL_MOUNT,    audit_skill("skill_mount"))
agent.on(HookEvent.SKILL_UNMOUNT,  audit_skill("skill_unmount"))
agent.on(HookEvent.SKILL_SETUP,    audit_skill("skill_setup"))
agent.on(HookEvent.SKILL_TEARDOWN, audit_skill("skill_teardown"))

# After the run:
print(json.dumps(audit, indent=2))
```

**Go:**

```go
type auditEntry struct {
    Event string
    Skill string
    TS    time.Time
}

var mu sync.Mutex
var log []auditEntry

record := func(ev string) hooks.Handler {
    return func(ctx context.Context, args ...any) {
        name, _ := args[0].(string)
        mu.Lock()
        log = append(log, auditEntry{Event: ev, Skill: name, TS: time.Now()})
        mu.Unlock()
    }
}

a.Hub.On(hooks.SkillMount,    record("skill_mount"))
a.Hub.On(hooks.SkillUnmount,  record("skill_unmount"))
a.Hub.On(hooks.SkillSetup,    record("skill_setup"))
a.Hub.On(hooks.SkillTeardown, record("skill_teardown"))
```

### Progress bar integration via TOKEN_STREAM

Render a live character-count progress indicator while the LLM streams.

**TypeScript:**

```typescript
import { HookEvent } from "./has-hooks.js";

let tokenCount = 0;

agent.on(HookEvent.TOKEN_STREAM, (token: string) => {
  tokenCount += token.length;
  process.stdout.write(`\r[${tokenCount} chars received]`);
});

agent.on(HookEvent.RUN_END, () => {
  process.stdout.write("\n");
});
```

**Python:**

```python
import sys

token_count = 0

def on_token(token: str):
    global token_count
    token_count += len(token)
    sys.stdout.write(f"\r[{token_count} chars received]")
    sys.stdout.flush()

agent.on(HookEvent.TOKEN_STREAM, on_token)
agent.on(HookEvent.RUN_END, lambda: print())
```

## Known limitations

**Go `Off` removes all handlers for an event.**
`Hub.Off(event)` is an event-level operation — it clears every handler registered under that event, not a single callback. Python's `remove_hook`, TypeScript's `removeHook`, and PHP's returned closure all support per-handler removal. There is no Go equivalent. Workaround: gate work inside the handler via an atomic flag (see [Unsubscribing](#go--event-level-removal-only)).

**Skill unmount does not remove skill-contributed hook handlers.**
When a skill is unmounted, its contributed tools and shell commands are removed cleanly. However, any handlers the skill registered via `hooks()` are **not** removed from the hub. This matches Python's current behaviour — function identity isn't comparable in Go and hook handler provenance isn't tracked. Avoid unmount-and-remount of skills that contribute hooks; the handlers accumulate across remounts. See the [Skills guide](skills.md#known-limitations) for the same caveat on middleware.

**PHP dispatch is sequential, not concurrent.**
PHP's synchronous execution model means handlers for the same event run one after another. A blocking handler delays subsequent handlers. Design PHP hooks to be fast; offload slow I/O to a queue.

**Handler errors are logged but not surfaced.**
A handler that throws or panics has its error logged at `WARNING` level and execution continues. There is no way to propagate a handler error back to the caller. If you need error propagation, use middleware instead.

## See also

- [ADR 0015](../adr/0015-lifecycle-hooks.md) — original `HasHooks` design and event-vs-middleware split
- [Python guide: Lifecycle Hooks](python.md#lifecycle-hooks)
- [TypeScript guide: Lifecycle Hooks](typescript.md#lifecycle-hooks)
- [PHP guide: Lifecycle Hooks](php.md#lifecycle-hooks)
- [Go guide: Hooks](go.md#hooks)
- [Skills guide](skills.md) — skill lifecycle events (`SKILL_MOUNT`, `SKILL_SETUP`, etc.) and how skills contribute hook handlers
