# TypeScript Developer Guide

## Overview

The TypeScript implementation of the agent harness framework uses function-based mixins for composable agent capabilities, the OpenAI SDK for LLM calls, and async/await throughout. Each capability (hooks, middleware, tools, events) lives in its own mixin, and you compose only what you need.

## Installation

Dependencies are declared in `src/typescript/package.json`:

- **Runtime**: `openai`, `yaml`
- **Dev**: `vitest`, `typescript`
- **Module format**: ESM (ES2022 target)

```bash
cd src/typescript
npm install
```

## Creating an Agent

### Using StandardAgent (all capabilities)

`StandardAgent` bundles every mixin into a single ready-to-use class:

```typescript
import { StandardAgent } from "./standard-agent.js";

const agent = new StandardAgent({
  model: "gpt-4",
  system: "You are a helpful assistant.",
});
const result = await agent.run([{ role: "user", content: "Hello" }]);
```

`StandardAgent` is composed as:

```typescript
const StandardAgent = EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))));
```

### Custom composition (only what you need)

Pick the mixins you actually need and compose them yourself:

```typescript
import { BaseAgent } from "./base-agent.js";
import { HasHooks } from "./has-hooks.js";
import { UsesTools } from "./uses-tools.js";

const MyAgent = UsesTools(HasHooks(BaseAgent));
const agent = new MyAgent({ model: "gpt-4" });
```

## Lifecycle Hooks

The `HookEvent` enum defines the same 10 lifecycle events as the Python implementation. Dispatch uses `Promise.allSettled` so all registered callbacks run concurrently and a single failure does not short-circuit the rest.

```typescript
import { HookEvent } from "./has-hooks.js";

agent.on(HookEvent.RUN_START, () => console.log("Run started"));
agent.on(HookEvent.TOOL_CALL, (name, args) => console.log(`Calling ${name}`));
```

## Middleware

Middleware implements the `Middleware` interface, which has two optional methods: `pre()` runs before the LLM call and `post()` runs after.

```typescript
import { Middleware } from "./has-middleware.js";

const logger: Middleware = {
  async pre(messages, context) {
    console.log(`Sending ${messages.length} messages`);
    return messages;
  },
  async post(message, context) {
    console.log(`Got: ${message.content?.slice(0, 50)}`);
    return message;
  },
};
agent.use(logger);
```

## Tools

Use the `defineTool()` helper with the explicit `ToolDef` interface to register tools on an agent.

```typescript
import { defineTool } from "./uses-tools.js";

const addTool = defineTool({
  name: "add",
  description: "Add two numbers",
  parameters: {
    type: "object",
    properties: { a: { type: "number" }, b: { type: "number" } },
    required: ["a", "b"],
  },
  execute: ({ a, b }) => a + b,
});
agent.register_tool(addTool);
```

## Events

Define structured event types and register them on an agent that includes the `EmitsEvents` mixin.

```typescript
import { EventType, StreamConfig } from "./emits-events.js";

const progressEvent: EventType = {
  name: "progress",
  description: "Report task progress",
  schema: { percent: "integer", message: "string" },
};
agent.register_event(progressEvent);
agent.default_events = ["progress"];
```

## Streaming Events

An event type can declare streaming support. Fields listed in `streamFields` will be delivered incrementally as an `AsyncIterable<string>`.

```typescript
const codeEvent: EventType = {
  name: "code_output",
  description: "Stream generated code",
  schema: { language: "string", code: "string" },
  streaming: { mode: "streaming", streamFields: ["code"] },
};
```

The parser uses `createChannel()` internally -- a lightweight async iterable backed by a buffer and promise-based signaling. The event's `stream` property is an `AsyncIterable<string>`.

## Message Bus

The message bus provides pub/sub communication between agents and external consumers. Use `"*"` to subscribe to all event types.

```typescript
import { MessageBus } from "./message-bus.js";

agent.bus.subscribe("progress", async (event, bus) => {
  console.log(`Progress: ${event.data.percent}%`);
});
agent.bus.subscribe("*", async (event, bus) => {
  console.log(`Any event: ${event.type}`);
});
```

## Key Patterns

**Function-based mixins.** Each mixin is a function of the form `HasHooks<TBase extends Constructor>(Base: TBase)` that returns an anonymous class extending `Base`. Composition reads right-to-left: in `EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))`, `HasHooks` is applied first and `EmitsEvents` last.

**Constructor\<T\> type.** The generic base for all mixin functions:

```typescript
type Constructor<T = {}> = new (...args: any[]) => T;
```

**createChannel().** A custom async iterable primitive for streaming. Push values in, consume via `for await`, and close to signal end of stream.

**Promise.allSettled.** Hook dispatch runs all callbacks concurrently, collecting errors without short-circuiting. This ensures one failing hook does not prevent others from executing.
