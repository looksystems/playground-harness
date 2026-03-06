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

### Using the Builder (declarative setup)

`StandardAgent.build()` provides a fluent interface for configuring an agent declaratively:

```python
from src.python.standard_agent import StandardAgent
from src.python.has_hooks import HookEvent

agent = await (
    StandardAgent.build("gpt-4")
    .system("You are a helpful assistant.")
    .max_turns(10)
    .tools(search_tool, calc_tool)
    .middleware(LoggingMiddleware())
    .on(HookEvent.RUN_START, lambda: print("started"))
    .skill(WebBrowsingSkill())
    .shell(cwd="/workspace")
    .create()
)
```

All methods except `.create()` are synchronous and return the builder. `.create()` is `async` because skill mounting requires it.

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

Twenty-two hook events are defined in `HookEvent(str, Enum)`:

`run_start`, `run_end`, `llm_request`, `llm_response`, `tool_call`, `tool_result`, `tool_error`, `retry`, `token_stream`, `error`, `shell_call`, `shell_result`, `shell_not_found`, `shell_cwd`, `command_register`, `command_unregister`, `tool_register`, `tool_unregister`, `skill_mount`, `skill_unmount`, `skill_setup`, `skill_teardown`.

Register handlers with `agent.on()`:

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.RUN_START, lambda: print("Run started"))
agent.on(HookEvent.TOOL_CALL, lambda name, args: print(f"Calling {name}"))
```

Hooks dispatch concurrently via `asyncio.gather` with `return_exceptions=True`. Both sync and async callbacks are supported.

Remove a hook with `remove_hook()`:

```python
agent.remove_hook(HookEvent.RUN_START, my_callback)
```

All registration methods return `self` for fluent chaining:

```python
agent.on(HookEvent.RUN_START, on_start).on(HookEvent.RUN_END, on_end)
```

Read-only accessors return copies of internal state:

```python
agent.hooks       # dict[HookEvent, list[Callable]] — copy
agent.middleware   # list[BaseMiddleware] — copy
agent.tools        # dict[str, ToolDef] — copy
agent.events       # dict[str, EventType] — copy
```

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

Middleware executes in the order it is registered via `agent.use()`. Remove with `agent.remove_middleware(mw)`.

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

## Virtual Shell

The `HasShell` mixin provides an in-memory virtual filesystem and shell interpreter. Mount context as files and let the agent explore with standard Unix commands. The shell supports 30 built-in commands, control flow (`if/elif/else`, `for`, `while`, `case/esac`), logical operators (`&&`, `||`), variable assignment, command substitution `$(...)`, arithmetic `$((...))`, parameter expansion (`${var:-default}`, `${#var}`, etc.), `test`/`[`/`[[`, and `printf`.

### Standalone usage

```python
from virtual_fs import VirtualFS
from shell import Shell

fs = VirtualFS()
fs.write("/data/users.json", json.dumps(users))
shell = Shell(fs)
result = shell.exec("cat /data/users.json | jq '.[].name' | sort")
print(result.stdout)
```

### With an agent

```python
class MyAgent(BaseAgent, UsesTools, HasShell):
    pass

agent = MyAgent(model="anthropic/claude-sonnet-4-20250514")
agent.fs.write("/data/schema.yaml", schema_content)
response = await agent.run("What tables reference user_id?")
```

### Shell registry

```python
from shell import ShellRegistry, Shell
from virtual_fs import VirtualFS

ShellRegistry.register("data-explorer", Shell(
    fs=VirtualFS({"/schema/users.yaml": schema}),
    allowed_commands={"cat", "grep", "find", "ls", "jq", "head", "tail", "wc"},
))

# Each agent gets its own clone
agent = MyAgent(model="...", shell="data-explorer")
agent.fs.write("/data/results.json", results)  # only this agent sees this
```

### Custom commands

Register domain-specific commands that work like built-ins — composable with pipes, redirects, and control flow:

```python
from shell import Shell, ExecResult
from virtual_fs import VirtualFS

shell = Shell(fs=VirtualFS())

def deploy(args: list[str], stdin: str) -> ExecResult:
    return ExecResult(stdout=f"Deployed {args[0]} to {args[1] if len(args) > 1 else 'production'}\n")

shell.register_command("deploy", deploy)
shell.exec("deploy my-app staging")

# With an agent — delegates to the underlying shell
agent.register_command("validate", lambda args, stdin: ExecResult(
    stdout="ok\n" if is_valid(stdin) else "invalid\n",
    exit_code=0 if is_valid(stdin) else 1,
))

# Unregister when no longer needed
shell.unregister_command("deploy")

# Built-ins cannot be unregistered
shell.unregister_command("echo")  # raises ValueError
```

Custom commands survive `clone()` and `ShellRegistry.get()`, so registry templates can include domain commands.

### Shell hooks

When `HasHooks` is also composed, shell operations emit lifecycle hooks:

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.SHELL_CALL, lambda cmd: print(f"Executing: {cmd}"))
agent.on(HookEvent.SHELL_NOT_FOUND, lambda name: print(f"Unknown: {name}"))
agent.on(HookEvent.SHELL_CWD, lambda old, new: print(f"cd {old} -> {new}"))
```

Python's VirtualFS supports `str | bytes` content, so binary files (images, protobuf) can be stored directly. See [ADR 0012](../adr/0012-virtual-shell-architecture.md) and [ADR 0021](../adr/0021-custom-command-registration.md) for architecture details.

## Skills

The `HasSkills` mixin enables mountable capability bundles that combine tools, instructions, middleware, hooks, and lifecycle management into a single unit.

### Defining a skill

```python
from src.python.has_skills import Skill

class WebBrowsingSkill(Skill):
    name = "web_browsing"
    description = "Browse the web and extract content"
    version = "1.0.0"
    instructions = "You can browse the web using the fetch_page tool."

    async def setup(self, ctx):
        ctx.session = aiohttp.ClientSession()

    async def teardown(self, ctx):
        await ctx.session.close()

    def tools(self):
        return [fetch_page_tool]

    def middleware(self):
        return []

    def hooks(self):
        return {}

    def commands(self):
        return {}
```

### Mounting skills

```python
agent.mount(WebBrowsingSkill())
```

Mounting a skill resolves dependencies transitively, runs `setup()`, and registers all tools, middleware, hooks, and commands.

### Unmounting skills

```python
agent.unmount("web_browsing")
```

Unmounting runs `teardown()` and removes all tools, middleware, hooks, and commands associated with the skill.

### SkillPromptMiddleware

Middleware that auto-injects mounted skill instructions into the system prompt:

```python
from src.python.skill_prompt_middleware import SkillPromptMiddleware

agent.use(SkillPromptMiddleware())
```

### Skill hooks

When `HasHooks` is also composed, skill operations emit lifecycle hooks:

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.SKILL_MOUNT, lambda skill: print(f"Mounted: {skill.name}"))
agent.on(HookEvent.SKILL_SETUP, lambda skill: print(f"Setting up: {skill.name}"))
```

See [ADR 0024](../adr/0024-has-skills-mixin.md) for design details.
