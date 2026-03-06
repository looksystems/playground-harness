# 16. Tool Registration and Schema Generation

Date: 2026-03-05

## Status

Accepted

## Context

LLM tool/function calling requires passing tool definitions (name, description, JSON schema for parameters) alongside messages. We needed a tool registration system that is declarative, type-safe where possible, and consistent across three languages with very different type systems.

## Decision

The `UsesTools` mixin provides tool registration with two paths:

### 1. Decorated functions (Python only)

```python
@tool(description="Add two numbers")
async def add(a: int, b: int) -> int:
    return a + b

agent.register_tool(add)
```

The `@tool` decorator attaches metadata to the function. On registration, `_build_param_schema` auto-generates JSON schema from Python type hints. This works because Python has runtime-accessible type annotations.

### 2. Explicit ToolDef (all languages)

```python
ToolDef(name="add", description="Add", function=fn, parameters={...})
```

TypeScript uses `defineTool()`, PHP uses `ToolDef::make()`. The JSON schema is provided explicitly since neither TypeScript nor PHP has reliable runtime type introspection for function parameters.

### Key design choices

- **Auto-schema from type hints** is Python-only. We don't attempt to replicate this in TypeScript (types erased at runtime) or PHP (limited reflection). Explicit schema is the universal path.
- **Sync/async interop** — tool functions can be sync or async in Python and TypeScript. The framework wraps execution in `await Promise.resolve(result)` or checks `inspect.isawaitable`. PHP tools are always synchronous.
- **Parallel execution** — when the LLM returns multiple tool calls in one turn, they execute concurrently via `asyncio.gather` (Python) / `Promise.all` (TypeScript). PHP executes sequentially.
- **Error handling** — tool errors are caught, serialized as JSON `{"error": "..."}`, and returned to the LLM as tool results. This lets the model self-correct rather than crashing the agent loop.
- **`register_tool` accepts both paths** — in Python, it inspects whether the argument is a `ToolDef` or a `@tool`-decorated function and handles both. TypeScript and PHP only accept `ToolDef`.

## Consequences

- Python gets the most ergonomic experience (decorator + auto-schema), which is appropriate since it's the primary language for AI/ML work
- TypeScript and PHP require explicit schemas, which is more verbose but unambiguous
- The `ToolDef` dataclass/interface is the universal interchange format — any system that produces a `ToolDef` can register tools (skills, plugins, auto-generated from OpenAPI specs, etc.)
- Error serialization back to the LLM is a key reliability feature — the model can retry with corrected arguments rather than the conversation dying on a tool failure
- `unregister_tool(name)` was later added to complement `register_tool`, enabling clean removal of auto-registered tools (e.g., by `HasSkills` when unmounting a skill). It fires the `TOOL_UNREGISTER` hook event.
