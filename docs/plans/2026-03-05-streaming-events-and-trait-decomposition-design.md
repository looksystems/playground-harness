# Streaming Events & Trait-based Agent Decomposition

## Overview

Two related changes to the agent harness:

1. **Streaming event system** — LLMs emit structured YAML events inline in their text output. A parser extracts them, routes them through a message bus, and dispatches to handlers.
2. **Trait-based agent decomposition** — Break the monolithic agent into a thin BaseAgent + composable mixins (traits) for each concern.

## Architecture: Trait-based Agent

### Decomposition

The current monolithic agent splits into:

| Class | Responsibility |
|-------|---------------|
| `BaseAgent` | Message loop, LLM calls, retries, max turns |
| `HasMiddleware` | Middleware registration, ordered pre/post pipeline |
| `HasHooks` | Hook subscription, concurrent dispatch |
| `UsesTools` | Tool registration, schema generation, dispatch, parallel execution |
| `HasSkills` | Skill registry, lifecycle, dependency resolution, prompt injection |
| `EmitsEvents` | Event registry, parser, bus, per-run event resolution |

### BaseAgent Extension Points

Mixins override these to participate in the agent loop:

- `_handle_stream(stream)` — process/wrap the raw token stream
- `_handle_response(response, context)` — process a completed LLM response
- `_build_system_prompt(base_prompt, context)` — append to the system prompt
- `_on_run_start(context)` / `_on_run_end(context)` — lifecycle hooks

### Composition

```python
# Full-featured agent
class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, HasSkills, EmitsEvents):
    pass

# Minimal agent
class LightAgent(BaseAgent, HasHooks, UsesTools):
    pass
```

Language-idiomatic patterns: Python multiple inheritance, TypeScript mixins, PHP native traits.

## Architecture: Streaming Events

### Components

Four components, each a separate class:

1. **EventRegistry** — catalog of known event types on the agent
2. **EventStreamParser** — standalone, reusable stream parser
3. **MessageBus** — standalone, general-purpose pub/sub
4. **EmitsEvents mixin** — wires the above into the agent

### Event Type Definition

```python
EventType(
    name="user_response",
    description="Send a message directly to the user",
    schema={"data": {"message": "string"}},
    instructions="Use this when responding to the user directly",  # optional
    streaming=StreamConfig(mode="streaming", stream_fields=["data.message"]),
)
```

Fields:
- `name` — unique identifier
- `description` — when/why the LLM should emit this event
- `schema` — YAML schema describing the event's data fields
- `instructions` — additional emission guidance (optional)
- `streaming` — configures buffered vs streaming mode (optional, defaults to buffered)

### StreamConfig

- `mode: "buffered" | "streaming"` — default `"buffered"`
- `stream_fields: list[string]` — which data fields stream incrementally (only when mode is `"streaming"`)

Streaming fields must be the last field in the event block (last-field convention). One streaming field per event.

### LLM Event Emission Format

The LLM emits events inline in its text output using `---event` / `---` delimiters:

```
Let me check that for you.
---event
type: user_response
data:
  message: The weather is 72 degrees today.
---
```

Active event types are injected into the system prompt with name, description, schema, and emission instructions.

### EventStreamParser

Standalone, reusable component. Takes any token stream and produces clean text + parsed events.

```python
parser = EventStreamParser(event_types=[...])
parser.on_event(callback)

async for text_chunk in parser.wrap(llm_stream):
    # text_chunk contains non-event text only
    pass
```

Parser states:
1. **Text mode** — tokens pass through. Watching for `---event`.
2. **Header mode** — buffering YAML fields. Looking for streaming field or closing `---`.
3. **Streaming mode** — event published with async iterator. Tokens routed to iterator until `---`.

Emitted event object:

```python
class ParsedEvent:
    type: str                          # event name
    data: dict                         # parsed non-streaming fields
    stream: AsyncIterator[str] | None  # streaming field iterator (if applicable)
    raw: str | None                    # full raw YAML (buffered events only)
```

Edge cases:
- Malformed YAML → parser error event, raw content passed as text
- Unrecognized event type → passed through as plain text
- Incomplete event at end of stream → emitted as text

### Streaming Fields: Level 2 (Structured + Stream)

The parser does the heavy lifting:
- Buffers and parses all non-streaming fields as structured YAML
- When it hits the streaming field (last field), publishes the event immediately
- The streaming field is an async iterator that yields tokens until the closing `---`
- Handlers receive structured data + a stream, not raw text

### MessageBus

Standalone, general-purpose, in-memory pub/sub.

```python
bus = MessageBus()
bus.subscribe("user_response", handler)
bus.subscribe("*", wildcard_handler)
await bus.publish(event)
```

Handler signature:

```python
async def handler(event: ParsedEvent, bus: MessageBus) -> None:
    # bus passed so handlers can publish new events
    await bus.publish(another_event)
```

Properties:
- Bidirectional — handlers can publish back to the bus
- Cycle detection via depth counter (configurable `max_depth`, default 10)
- Multiple handlers per event type run concurrently
- Errors in one handler don't block others (logged, not raised)

### Agent Integration

The agent holds an EventRegistry + `default_events` config.

```python
agent = Agent(model="...", default_events=["user_response", "log_entry"])

agent.register_event(EventType(name="user_response", ...))
```

Per-run event control via `events` parameter on `run()`:
- If omitted, uses `default_events` from agent config
- Can override (replace defaults), extend (add ad-hoc events), or filter (subset by name)
- Ad-hoc event types defined inline don't need pre-registration

Wiring during `run()`:
1. Resolve active events (defaults + overrides + ad-hoc)
2. Inject event instructions into system prompt
3. Create `EventStreamParser` with active event types
4. Wrap LLM token stream through the parser
5. Parser feeds events to the agent's `MessageBus`
6. Clean text (events stripped) flows to existing `TOKEN_STREAM` hooks

### PHP Streaming Implementation

PHP uses Generators (the ecosystem-standard pattern used by openai-php/client, Prism PHP, Laravel AI SDK, etc.):

```php
foreach ($parser->parse($sseStream) as $event) {
    echo $event->data['status'];

    foreach ($event->stream as $token) {
        echo $token;
        flush();
    }
}
```

The pull-based Generator model drives the chain naturally: handler pulls → parser pulls → Guzzle reads SSE. No async machinery needed.

### Python / TypeScript Streaming

Python uses `async for` with `AsyncIterator`. TypeScript uses `AsyncIterable` with `for await...of`. Both follow the same Level 2 pattern — structured fields + async iterator for streaming fields.

## File Structure (per language)

```
base_agent.*           # BaseAgent + extension points
has_middleware.*        # HasMiddleware mixin
has_hooks.*            # HasHooks mixin
uses_tools.*           # UsesTools mixin
has_skills.*           # HasSkills mixin (refactored from skills.*)
emits_events.*         # EmitsEvents mixin (new)
event_stream_parser.*  # Standalone parser (new)
message_bus.*          # Standalone bus (new)
standard_agent.*       # StandardAgent composition
shell_skill.*          # Unchanged
```

## What Changes vs What's New

- **Refactor**: agent_harness splits into BaseAgent + HasMiddleware + HasHooks + UsesTools
- **Refactor**: skills code moves into HasSkills mixin + Skill base class
- **New**: EmitsEvents mixin, EventStreamParser, MessageBus, EventType definitions
- **Unchanged**: ShellSkill, VirtualFS, Shell interpreter

## Design Decisions

1. **Parser-centric approach** — parser owns delimiter detection, YAML parsing, and streaming field handling. Bus stays minimal and swappable.
2. **Last-field convention** — streaming fields must be last in the event block. Simplest for both parser and LLM.
3. **Level 2 streaming** — parser resolves structured fields, streams only declared streaming fields. Handlers get clean data.
4. **Mixin composition** — each concern is a mixin. BaseAgent is thin. StandardAgent composes all. Custom agents pick what they need.
5. **Generators for PHP** — pull-based iteration matches PHP's synchronous model and ecosystem conventions.
6. **Per-run event control** — `default_events` on agent config, overridable per `run()` call with support for ad-hoc event types.
