# Python Developer Guide

## Overview

The Python implementation of the agent harness framework uses `async`/`await` throughout, [litellm](https://github.com/BerriAI/litellm) for LLM calls, and multiple inheritance for mixin composition. Each capability (hooks, middleware, tools, events) is provided by an independent mixin class that can be composed as needed.

## Installation

The project depends on **litellm** and **pyyaml**. Configuration lives in `pyproject.toml`, and tests run with **pytest-asyncio**.

```bash
pip install litellm pyyaml
pip install pytest pytest-asyncio  # for development
```

## Creating an Agent

### Using StandardAgent (all capabilities)

`StandardAgent` combines every mixin into a single ready-to-use class:

```python
from src.python.standard_agent import StandardAgent

agent = StandardAgent(model="gpt-4", system="You are a helpful assistant.")
result = await agent.run([{"role": "user", "content": "Hello"}])
```

### Custom composition (only what you need)

Pick the mixins your agent actually requires:

```python
from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.uses_tools import UsesTools

class MyAgent(BaseAgent, HasHooks, UsesTools):
    pass
```

This gives you lifecycle hooks and tool support without middleware or event streaming.

## Lifecycle Hooks

Ten hook events are defined in `HookEvent(str, Enum)`:

`run_start`, `run_end`, `llm_request`, `llm_response`, `tool_call`, `tool_result`, `tool_error`, `retry`, `token_stream`, `error`.

Register handlers with `agent.on()`:

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.RUN_START, lambda: print("Run started"))
agent.on(HookEvent.TOOL_CALL, lambda name, args: print(f"Calling {name}"))
```

Hooks dispatch concurrently via `asyncio.gather` with `return_exceptions=True`. Both sync and async callbacks are supported.

## Middleware

Middleware forms a sequential pipeline. Each middleware implements `pre()` to process messages before the LLM call and `post()` to process the response after.

```python
from src.python.has_middleware import BaseMiddleware

class LoggingMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        print(f"Sending {len(messages)} messages")
        return messages

    async def post(self, message, context):
        print(f"Got: {message.get('content', '')[:50]}")
        return message

agent.use(LoggingMiddleware())
```

Middleware executes in the order it is registered via `agent.use()`.

## Tools

Two registration methods are available: the `@tool` decorator or the `ToolDef` dataclass.

### Decorator approach

```python
from src.python.uses_tools import tool

@tool(description="Add two numbers")
def add(a: int, b: int) -> int:
    return a + b

agent.register_tool(add)
```

The JSON schema for tool parameters is auto-generated from type hints. Both sync and async tool functions are supported.

## Events

Register event types, configure defaults, and build prompts for the LLM.

```python
from src.python.event_stream_parser import EventType, StreamConfig

progress_event = EventType(
    name="progress",
    description="Report task progress",
    schema={"percent": "integer", "message": "string"},
)
agent.register_event(progress_event)
agent.default_events = ["progress"]
```

## Streaming Events

For streaming events, set the mode to `"streaming"` and declare the stream fields:

```python
code_event = EventType(
    name="code_output",
    description="Stream generated code",
    schema={"language": "string", "code": "string"},
    streaming=StreamConfig(mode="streaming", stream_fields=["code"]),
)
```

The streaming field must be the last field in the YAML block. The parser fires the event immediately when the streaming field is detected, providing an `AsyncIterator[str]` via `event.stream`.

## Message Bus

Subscribe to events by type or use a wildcard to catch everything:

```python
async def on_progress(event, bus):
    print(f"Progress: {event.data['percent']}%")

agent.bus.subscribe("progress", on_progress)
agent.bus.subscribe("*", lambda e, b: print(f"Any event: {e.type}"))
```

Handlers receive the bus as their second argument and can publish new events. Cycle detection enforces a max depth of 10 (configurable).

## Key Patterns

- **Lazy initialization** -- Mixins use `hasattr` checks and `__init_has_X__()` methods to avoid MRO `__init__` conflicts.
- **Async throughout** -- All agent methods are async. Both sync and async callbacks are supported; the framework checks via `inspect.isawaitable`.
- **asyncio.Queue for streaming** -- Streaming events use an `asyncio.Queue`. The parser pushes lines and the consumer pulls them via `AsyncIterator`.
