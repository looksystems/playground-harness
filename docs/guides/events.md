# Events

Inline events are structured messages the LLM emits inline in its text output using `---event ... ---` YAML blocks. A small state-machine parser pulls the blocks out of the token stream and routes the parsed payloads through a message bus; the rest of the text (events stripped) flows on to the user. The LLM never has to call a separate tool or switch to a structured-output mode — it just writes YAML in the middle of a sentence, and the framework does the separation.

This guide covers the concepts that hold across all four implementations and shows the idiomatic API in each language. For the full language API reference, see the per-language guides ([Python](python.md), [TypeScript](typescript.md), [PHP](php.md), [Go](go.md)).

## Why inline YAML

Most LLM SDKs offer "structured output" only as a whole-response mode: the model returns JSON instead of prose, and the caller has to choose between one or the other. The inline-events approach breaks that binary. The model writes natural-language text and drops a YAML block whenever it needs to emit structured data — progress updates, code artifacts, form submissions, tool invocations. The parser handles the framing; the caller sees a clean text stream on one channel and `ParsedEvent` values on another.

Three properties make this work reliably:

- **LLMs produce YAML comfortably.** It appears frequently in training data and tolerates minor whitespace drift better than JSON.
- **The framing is lexical.** `---event` on its own line opens; `---` on its own line closes. No nesting, no escape rules.
- **Streaming is opt-in per field.** Small events arrive whole; large events (generated code, long messages) can stream a single designated field.

## Anatomy of an event

An event type declares a few things:

| Part | Purpose |
|------|---------|
| **Name** | The `type:` value the LLM writes in the YAML body. Also the key used to subscribe on the bus. |
| **Description** | Short human-readable summary used in the generated prompt section. |
| **Schema** | Field-name → type hint map (`"string"`, `"integer"`, or nested map). Used to render the prompt example, not enforced at parse time. |
| **Instructions** | Optional free-text paragraph appended to the prompt section for this event type. |
| **Streaming** | Optional `StreamConfig`; when `mode == "streaming"`, one named field in the schema streams incrementally. |

The schema is documentation for the LLM, not a validator. The parser accepts whatever YAML the model produces as long as it has a `type:` key matching a registered event; unrecognised keys pass through to `event.data`.

## The wire format

Two delimiters frame the event block, each on its own line:

```
Before the event.
---event
type: progress
step: fetching
percent: 25
---
After the event.
```

Given that input, the parser emits `"Before the event.\nAfter the event.\n"` on the clean-text channel and dispatches a `ParsedEvent(type="progress", data={"type": "progress", "step": "fetching", "percent": 25})` to the bus.

Rules:

- `---event` must be the entire trimmed line content to open a block. Anything else is plain text.
- `---` (three dashes, nothing else on the line) closes the block.
- Any lines between the delimiters are accumulated as the YAML body.
- Malformed YAML, missing `type:` field, or an unknown event type causes the parser to skip the block — it is passed through as text rather than raising.

The state machine is three-state: `TEXT`, `EVENT_BODY`, `STREAMING`. It operates on complete lines, pulling from a rolling `line_buffer` that accumulates across tokens. This keeps the parser stable against token boundaries that fall mid-line.

## Defining an event type

Each language ships a struct/class for the event type definition. The examples below define the same `progress` event in each language.

**Python:**

```python
from src.python.event_stream_parser import EventType

progress_event = EventType(
    name="progress",
    description="Report task progress",
    schema={"step": "string", "percent": "integer"},
    instructions="Emit one progress event per major step.",
)
```

**TypeScript:**

```typescript
import { StructuredEvent } from "./event-stream-parser.js";

const progressEvent: StructuredEvent = {
  name: "progress",
  description: "Report task progress",
  schema: { step: "string", percent: "integer" },
  instructions: "Emit one progress event per major step.",
};
```

**PHP:**

```php
use AgentHarness\StructuredEvent;

$progressEvent = new StructuredEvent(
    name: 'progress',
    description: 'Report task progress',
    schema: ['step' => 'string', 'percent' => 'integer'],
    instructions: 'Emit one progress event per major step.',
);
```

**Go:**

```go
import "agent-harness/go/events"

progressEvent := events.EventType{
    Name:         "progress",
    Description:  "Report task progress",
    Schema:       map[string]any{"step": "string", "percent": "integer"},
    Instructions: "Emit one progress event per major step.",
}
```

Schema values are freeform strings — they become the `<placeholder>` text in the generated prompt example. Nested maps render as YAML sub-blocks in the prompt.

## Streaming fields

A streaming event has `StreamConfig(mode="streaming", stream_fields=[...])` attached. When the parser sees enough of the event body that the streaming field is present, it fires the event immediately — with the structured fields already populated — and pipes every subsequent line into an async iterator (or channel) bound to that field.

**Python:**

```python
from src.python.event_stream_parser import EventType, StreamConfig

code_event = EventType(
    name="code_output",
    description="Stream generated code",
    schema={"language": "string", "code": "string"},
    streaming=StreamConfig(mode="streaming", stream_fields=["code"]),
)
```

**Go:**

```go
codeEvent := events.EventType{
    Name:        "code_output",
    Description: "Stream generated code",
    Schema:      map[string]any{"language": "string", "code": "string"},
    Streaming: events.StreamConfig{
        Mode:         "streaming",
        StreamFields: []string{"code"},
    },
}
```

### The last-field convention (ADR 0004)

The streaming field **MUST** be declared last in the YAML block. The parser treats every line after the streaming field is first detected as stream content, right up to the closing `---`. If a non-streaming field appeared after the streaming field, the parser would feed its YAML line into the stream iterator instead of parsing it as structured data.

So for the example above, the LLM must emit:

```
---event
type: code_output
language: python
code: |
  def greet(name):
      return f"hello, {name}"
---
```

Not:

```
---event
type: code_output
code: "def greet..."   # streaming field, but NOT last
language: python        # would end up in the stream iterator
---
```

The convention keeps the parser simple and is called out in ADR 0004 — its negative consequence is exactly this ordering constraint, plus the limit of one streaming field per event.

## Registering events on the agent

Each agent holds a registry of known event types plus a `default_events` list controlling which are active per run.

**Python:**

```python
agent.register_event(progress_event)
agent.register_event(code_event)
agent.default_events = ["progress", "code_output"]
```

**TypeScript:**

```typescript
agent.registerEvent(progressEvent);
agent.registerEvent(codeEvent);
agent.defaultEvents = ["progress", "code_output"];
```

**PHP:**

```php
$agent->registerEvent($progressEvent);
$agent->registerEvent($codeEvent);
$agent->defaultEvents = ['progress', 'code_output'];
```

**Go (fluent builder):**

```go
a, _ := agent.NewBuilder("gpt-4o").
    Client(client).
    Event(progressEvent).
    Event(codeEvent).
    DefaultEvents("progress", "code_output").
    Build(ctx)
```

Go also exposes `agent.Events.Register(...)` and `agent.Events.SetDefaults(...)` for post-build mutation.

## Per-run defaults and overrides (ADR 0006)

`default_events` configures the active set per run by default. The `run()` method accepts an optional override that replaces the default set for that call. The override can include both string names (resolved against the registry) and ad-hoc `EventType` instances that were never registered — supporting rapid experiments and one-off events without polluting the registry.

**Python:**

```python
# Default: use whatever default_events says.
await agent.run(messages)

# Override: replace for this run only. Mix of registered name + ad-hoc type.
await agent.run(messages, events=["progress", ad_hoc_event])
```

**Go:**

```go
// Default run.
_, _ = a.Run(ctx, messages, nil)

// Override: string names and EventType instances are both accepted.
_, _ = a.Run(ctx, messages, []any{"progress", adHocEvent})
```

The merge-vs-replace question is settled: the override replaces the default set entirely. Pass the union explicitly if you want both.

## Subscribing to events on the bus

The `MessageBus` is pub/sub keyed by event name. Handlers receive the event and a reference to the bus so they can publish follow-up events. Use `"*"` as the key to match every event.

**Python:**

```python
async def on_progress(event, bus):
    print(f"progress: {event.data['percent']}%")

agent.bus.subscribe("progress", on_progress)
agent.bus.subscribe("*", lambda e, b: print(f"any event: {e.type}"))
```

**TypeScript:**

```typescript
agent.bus.subscribe("progress", async (event, bus) => {
  console.log(`progress: ${event.data.percent}%`);
});
agent.bus.subscribe("*", async (event, bus) => {
  console.log(`any event: ${event.type}`);
});
```

**PHP:**

```php
$agent->getBus()->subscribe('progress', function ($event, $bus) {
    echo "progress: {$event->data['percent']}%\n";
});
$agent->getBus()->subscribe('*', function ($event, $bus) {
    echo "any event: {$event->type}\n";
});
```

**Go:**

```go
a.EventBus().Subscribe("progress", func(ctx context.Context, ev events.ParsedEvent, bus *events.MessageBus) error {
    fmt.Printf("progress: %v%%\n", ev.Data["percent"])
    return nil
})
a.EventBus().Subscribe("*", func(ctx context.Context, ev events.ParsedEvent, bus *events.MessageBus) error {
    fmt.Printf("any event: %s\n", ev.Type)
    return nil
})
```

The Go `Subscribe` returns a `CancelFunc` that removes the subscription when called. Python, TypeScript, and PHP do not currently return handles — reset the bus or restart the agent to clear subscriptions.

### Consuming a streaming field

The streaming field is delivered via async iteration. Structured fields are available immediately on `event.data`.

**Python:**

```python
async def on_code(event, bus):
    print(f"language: {event.data['language']}")
    async for chunk in event.stream:
        sys.stdout.write(chunk)

agent.bus.subscribe("code_output", on_code)
```

**Go:**

```go
a.EventBus().Subscribe("code_output", func(ctx context.Context, ev events.ParsedEvent, bus *events.MessageBus) error {
    fmt.Printf("language: %v\n", ev.Data["language"])
    for chunk := range ev.Stream {
        fmt.Print(chunk)
    }
    return nil
})
```

Consumers **MUST** drain the stream or the parser blocks waiting for back-pressure to clear. If you are not going to use the stream, range over it and discard the chunks.

## Depth limit (ADR 0005)

Handlers are allowed to publish new events — useful for cross-cutting logging, event chains ("on `progress`, publish `metric.progress`"), and adapters that translate one event into another. To prevent runaway cascades, the bus tracks publish depth with an atomic counter. When a handler publishes while the bus is already dispatching, the counter increments; when the outer `publish` returns, it decrements.

Default max depth is **10** (configurable via `WithMaxDepth` in Go, constructor arg in Python/TS/PHP). Once exceeded, the bus logs a warning and drops the event rather than recursing further.

## Parser → bus wiring

The parser and the bus are decoupled. The parser turns a token stream into `(clean_text, events)`; the bus routes `ParsedEvent`s to handlers. Something has to connect the two — call this the "wire" — and the defaults differ by language.

- **Python:** the parser is **not** wired to the bus by default. `EmitsEvents` ships both components but leaves the connection as an integrator's concern. The canonical wiring is a one-liner that calls `parser.on_event(lambda e: asyncio.create_task(bus.publish(e)))`. This is a documented TODO on the Python side; Go fixes it.
- **TypeScript / PHP:** the `Run` loop wires the parser to the bus automatically when any events are registered. The agent streams through the parser, clean text goes into the assistant message, and each parsed event is published to `agent.bus` before control returns.
- **Go:** the `Host` type in `src/go/events/host.go` owns the parser, the bus, and the registry together. The `Agent.Run` loop feeds the LLM token stream through `Host.Parser()` and publishes every parsed event to `Host.Bus()`. This makes the Bus available without any opt-in plumbing — which is a deliberate divergence from Python's mixin-style composition.

If you want Python to behave like Go, do this once after registering events:

```python
agent.events_parser.on_event(lambda e: asyncio.create_task(agent.bus.publish(e)))
```

## Event prompt injection

The LLM has to know which events it can emit and what shape they take. That knowledge is injected into the system prompt as a dedicated section, built from the active `EventType`s.

`BuildPrompt(activeEvents)` (Go) / `EmitsEvents._build_event_prompt(event_types)` (Python) generates a section that looks like:

```
# Event Emission

You can emit structured events inline in your response using the following format:

## Event: progress
Description: Report task progress
Format:
```
---event
type: progress
step: <string>
percent: <integer>
---
```
Emit one progress event per major step.
```

Installation is the other asymmetry:

- **Python / TypeScript / PHP:** the caller installs a prompt middleware explicitly (or relies on the built-in `Run` path to inject it). Python leaves this up to the application; the helper `_build_event_prompt` is available for manual wiring.
- **Go:** the fluent builder auto-installs `events.PromptMiddleware` the first time `.Event(...)` is called, so the LLM always receives instructions matching the currently-active set.

Either way, the prompt is rebuilt per run using `ResolveActive`/`_resolve_active_events`, so per-run overrides take effect without reinstalling anything.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|-----|
| Event-type struct | `EventType` dataclass | `StructuredEvent` interface | `StructuredEvent` class | `events.EventType` struct |
| StreamConfig shape | `StreamConfig(mode, stream_fields)` | `{ mode, streamFields }` | `new StreamConfig(mode, streamFields)` | `events.StreamConfig{Mode, StreamFields}` |
| Register on agent | `agent.register_event(et)` | `agent.registerEvent(et)` | `$agent->registerEvent($et)` | `builder.Event(et)` / `agent.Events.Register(et)` |
| Default-set attribute | `agent.default_events` | `agent.defaultEvents` | `$agent->defaultEvents` | `builder.DefaultEvents(...)` / `agent.Events.SetDefaults(...)` |
| Bus subscribe | `agent.bus.subscribe(name, h)` | `agent.bus.subscribe(name, h)` | `$agent->getBus()->subscribe($name, $h)` | `a.EventBus().Subscribe(name, h)` |
| Wildcard syntax | `"*"` | `"*"` | `'*'` | `"*"` |
| Parser-to-bus default | manual (documented TODO) | automatic in `run` | automatic in `run` | automatic via `Host` |
| Depth limit | `MessageBus(max_depth=10)` | `new MessageBus({ maxDepth: 10 })` | `new MessageBus(maxDepth: 10)` | `NewBus(WithMaxDepth(10))` |
| Ad-hoc events per run | `events=[...]` on `run(...)` | `events: [...]` on `run(...)` | `events: [...]` on `run(...)` | `events []any` arg on `Run(...)` |
| Streaming channel | `AsyncIterator[str]` via `asyncio.Queue` | `AsyncIterable<string>` via `createChannel()` | `Generator` | `<-chan string` |
| Subscribe returns cancel | no | no | no | yes (`CancelFunc`) |

## End-to-end example

Register both events, subscribe handlers, run the agent, observe dispatch. Python for brevity; the TS/PHP shapes are nearly identical.

```python
import asyncio, sys
from src.python.event_stream_parser import EventType, StreamConfig

progress_event = EventType(
    name="progress",
    description="Report task progress",
    schema={"step": "string", "percent": "integer"},
)
code_event = EventType(
    name="code_output",
    description="Stream generated code",
    schema={"language": "string", "code": "string"},
    streaming=StreamConfig(mode="streaming", stream_fields=["code"]),
)

agent.register_event(progress_event)
agent.register_event(code_event)
agent.default_events = ["progress", "code_output"]

async def on_progress(event, bus):
    print(f"[{event.data['step']}] {event.data['percent']}%")

async def on_code(event, bus):
    print(f"// {event.data['language']}")
    async for chunk in event.stream:
        sys.stdout.write(chunk)

agent.bus.subscribe("progress", on_progress)
agent.bus.subscribe("code_output", on_code)

await agent.run([{"role": "user", "content": "Write a Python greet function."}])
```

The LLM writes progress updates interleaved with a streaming code block; the parser separates them; the handlers format them for display. The user-facing text channel shows only the prose outside the event blocks.

## Known limitations

- **Last-field convention constrains schema ordering.** The streaming field must be the last field declared in the YAML body (ADR 0004 negative consequence). Event authors must document this when sharing event schemas.
- **One streaming field per event.** The parser stops structured-field detection once it sees the streaming field. Multi-stream events are out of scope; use separate event types instead.
- **Malformed YAML silently skips.** If `yaml.safe_load` raises, the parser logs a warning and either treats the block as plain text or drops it, depending on state. Matches Python's reference behaviour; no exception propagates to the caller.
- **Unknown event types are ignored.** If the LLM emits `type: foobar` and `foobar` is not registered, the parser passes the whole block through as text. There is no "unknown event" callback; consumers who want that should wildcard-subscribe and keep their own registry.
- **No parser-to-bus wiring in Python by default.** Historical; Go's `Host` type fixes this and TS/PHP wire it implicitly in `run`. Python integrators must wire the parser callback to `bus.publish` themselves.
- **Streams must be drained.** A handler that subscribes to a streaming event but does not iterate `event.stream` will block the parser. If you do not need the stream, drain it and discard.
- **Subscription cancellation is Go-only.** Only Go's `Subscribe` returns a `CancelFunc`. The other languages require clearing the whole bus or restarting the agent.

## See also

- [ADR 0002](../adr/0002-inline-yaml-events.md) — Why inline YAML blocks vs separate API / JSON / custom DSL
- [ADR 0003](../adr/0003-buffered-by-default-streaming.md) — Buffered-by-default; streaming opt-in per event
- [ADR 0004](../adr/0004-last-field-convention.md) — Streaming field must be last; ordering constraint
- [ADR 0005](../adr/0005-standalone-message-bus.md) — Pub/sub, wildcards, cycle detection via depth counter
- [ADR 0006](../adr/0006-per-run-event-control.md) — `default_events` plus per-run overrides, ad-hoc types
- [Python guide: Events](python.md#events) · [TypeScript guide: Events](typescript.md#events) · [PHP guide: Events](php.md#events) · [Go guide: Events](go.md#events)
- [Skills guide](skills.md) — Skills can contribute event-type declarations alongside tools and middleware
- [Middleware guide](middleware.md) — The event prompt injection is itself a middleware; auto-installed in Go, opt-in elsewhere
