# TypeScript Developer Guide

## Overview

The TypeScript implementation of the agent harness framework uses function-based mixins for composable agent capabilities, the OpenAI SDK for LLM calls, and async/await throughout. Each capability (hooks, middleware, tools, events) lives in its own mixin, and you compose only what you need.

## Installation

Dependencies are declared in the root `package.json`:

- **Runtime**: `openai`, `yaml`
- **Dev**: `vitest`, `typescript`
- **Module format**: ESM (ES2022 target)

```bash
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
const StandardAgent = HasSkills(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))));
```

### Using the Builder (declarative setup)

`StandardAgent.build()` provides a fluent interface for configuring an agent declaratively:

```typescript
import { StandardAgent } from "./standard-agent.js";
import { HookEvent } from "./has-hooks.js";

const agent = await StandardAgent.build("gpt-4")
  .system("You are a helpful assistant.")
  .maxTurns(10)
  .tools(searchTool, calcTool)
  .middleware(loggingMiddleware)
  .on(HookEvent.RUN_START, () => console.log("started"))
  .skill(new WebBrowsingSkill())
  .shell()
  .create();
```

All methods except `.create()` are synchronous and return the builder. `.create()` is `async` because skill mounting requires it.

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

The `HookEvent` enum defines 23 lifecycle events (the same 22 as Python plus `HOOK_ERROR`). Dispatch uses `Promise.allSettled` so all registered callbacks run concurrently and a single failure does not short-circuit the rest.

```typescript
import { HookEvent } from "./has-hooks.js";

agent.on(HookEvent.RUN_START, () => console.log("Run started"));
agent.on(HookEvent.TOOL_CALL, (name, args) => console.log(`Calling ${name}`));
```

Remove a hook with `removeHook()`:

```typescript
agent.removeHook(HookEvent.RUN_START, myCallback);
```

All registration methods return `this` for fluent chaining. Read-only state is available via public properties: `agent.hooks`, `agent.middlewareStack`, `agent.tools`, `agent.eventRegistry`.

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

Remove with `agent.removeMiddleware(logger)`.

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
agent.registerTool(addTool);
```

## Events

Define structured event types and register them on an agent that includes the `EmitsEvents` mixin.

```typescript
import { StructuredEvent, StreamConfig } from "./event-stream-parser.js";

const progressEvent: StructuredEvent = {
  name: "progress",
  description: "Report task progress",
  schema: { percent: "integer", message: "string" },
};
agent.registerEvent(progressEvent);
agent.defaultEvents = ["progress"];
```

## Streaming Events

An event type can declare streaming support. Fields listed in `streamFields` will be delivered incrementally as an `AsyncIterable<string>`.

```typescript
const codeEvent: StructuredEvent = {
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

## Virtual Shell

The `HasShell` mixin provides an in-memory virtual filesystem and shell interpreter. Mount context as files and let the agent explore with standard Unix commands. The shell supports 30 built-in commands, control flow (`if/elif/else`, `for`, `while`, `case/esac`), logical operators (`&&`, `||`), variable assignment, command substitution `$(...)`, arithmetic `$((...))`, parameter expansion (`${var:-default}`, `${#var}`, etc.), `test`/`[`/`[[`, and `printf`.

### Standalone usage

```typescript
import { VirtualFS } from "./virtual-fs.js";
import { Shell } from "./shell.js";

const fs = new VirtualFS();
fs.write("/data/users.json", JSON.stringify(users));
const shell = new Shell(fs);
const result = shell.exec("cat /data/users.json | jq '.[].name' | sort");
console.log(result.stdout);
```

### With an agent

```typescript
const MyAgent = HasShell(UsesTools(BaseAgent));

const agent = new MyAgent({ model: "gpt-4o" });
agent.fs.write("/data/schema.yaml", schemaContent);
const response = await agent.run("What tables reference user_id?");
```

### Shell registry

```typescript
import { ShellRegistry, Shell } from "./shell.js";
import { VirtualFS } from "./virtual-fs.js";

ShellRegistry.register("data-explorer", new Shell(
  new VirtualFS({ "/schema/users.yaml": schema }),
  { allowedCommands: new Set(["cat", "grep", "find", "ls", "jq", "head", "tail", "wc"]) },
));

const agent = new MyAgent({ model: "gpt-4o", shell: "data-explorer" });
agent.fs.write("/data/results.json", results);  // only this agent sees this
```

### Custom commands

Register domain-specific commands that work like built-ins — composable with pipes, redirects, and control flow:

```typescript
import { Shell, CmdHandler } from "./shell.js";

const shell = new Shell({ fs: new VirtualFS(), cwd: "/" });

shell.registerCommand("deploy", (args, stdin) => ({
  stdout: `Deployed ${args[0]} to ${args[1] ?? "production"}\n`,
  stderr: "",
  exitCode: 0,
}));

shell.exec("deploy my-app staging");          // works standalone
shell.exec("echo my-app | deploy $(cat -)");  // works with pipes and substitution

// With an agent — delegates to the underlying shell
agent.registerCommand("validate", (args, stdin) => ({
  stdout: isValid(stdin) ? "ok\n" : "invalid\n",
  stderr: "",
  exitCode: isValid(stdin) ? 0 : 1,
}));

// Unregister when no longer needed
shell.unregisterCommand("deploy");

// Built-ins cannot be unregistered
shell.unregisterCommand("echo"); // throws Error
```

Custom commands survive `clone()` and `ShellRegistry.get()`, so registry templates can include domain commands.

### Shell hooks

When `HasHooks` is also composed, shell operations emit lifecycle hooks:

```typescript
import { HookEvent } from "./has-hooks.js";

agent.on(HookEvent.SHELL_CALL, (cmd) => console.log(`Executing: ${cmd}`));
agent.on(HookEvent.SHELL_NOT_FOUND, (name) => console.log(`Unknown: ${name}`));
agent.on(HookEvent.SHELL_CWD, (old, newCwd) => console.log(`cd ${old} -> ${newCwd}`));
```

### Shell Drivers

The shell backend is swappable via `FilesystemDriver` and `ShellDriver` contracts. The built-in driver wraps `VirtualFS` and `Shell` and is used by default — no changes needed for existing code.

```typescript
import { ShellDriverFactory } from "./shell-driver.js";

// Set a global default driver
ShellDriverFactory.default = "bashkit";

// Per-agent via builder
const agent = await StandardAgent.build("gpt-4").driver("bashkit").create();

// Register a custom driver
ShellDriverFactory.register("my-driver", (opts) => new MyDriver(opts));
```

TypeScript lazy file providers are async (returning `Promise<string>`), allowing providers that fetch from APIs or databases. See [ADR 0012](../adr/0012-virtual-shell-architecture.md) and [ADR 0021](../adr/0021-custom-command-registration.md) for architecture details.

## Skills

The `HasSkills` mixin enables mountable capability bundles that combine tools, instructions, middleware, hooks, and lifecycle management into a single unit.

### Defining a skill

```typescript
import { Skill, SkillContext } from "./has-skills.js";

class WebBrowsingSkill implements Skill {
  name = "web_browsing";
  description = "Browse the web and extract content";
  version = "1.0.0";
  instructions = "You can browse the web using the fetch_page tool.";
  dependencies = [];

  async setup(ctx: SkillContext) {
    ctx.session = createHttpClient();
  }

  async teardown(ctx: SkillContext) {
    ctx.session.close();
  }

  tools() { return [fetchPageTool]; }
  middleware() { return []; }
  hooks() { return {}; }
  commands() { return {}; }
}
```

### Mounting skills

```typescript
agent.mount(new WebBrowsingSkill());
```

Mounting a skill resolves dependencies transitively, runs `setup()`, and registers all tools, middleware, hooks, and commands.

### Unmounting skills

```typescript
agent.unmount("web_browsing");
```

Unmounting runs `teardown()` and removes all tools, middleware, and hooks associated with the skill.

### SkillPromptMiddleware

Middleware that auto-injects mounted skill instructions into the system prompt:

```typescript
import { SkillPromptMiddleware } from "./has-skills.js";

agent.use(new SkillPromptMiddleware(agent.skills));
```

### Skill hooks

When `HasHooks` is also composed, skill operations emit lifecycle hooks:

```typescript
import { HookEvent } from "./has-hooks.js";

agent.on(HookEvent.SKILL_MOUNT, (name, skill) => console.log(`Mounted: ${name}`));
agent.on(HookEvent.SKILL_SETUP, (name, skill) => console.log(`Setting up: ${name}`));
```

See [ADR 0024](../adr/0024-has-skills-mixin.md) for design details.
