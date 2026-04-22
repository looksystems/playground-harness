# Tools

Tools are LLM-callable functions with auto-generated JSON schemas. When an agent runs, it serialises its registered tools into the `tools` array of the LLM request; the model emits `tool_call` blocks naming a tool and supplying JSON arguments; the harness dispatches those calls to the matching handler, serialises the result, and feeds it back as a `tool` role message. This loop continues until the model produces a plain text reply or the turn limit is reached. Tools are the primary mechanism for giving an agent real-world capabilities — filesystem access, HTTP calls, database queries, arithmetic, anything that can be expressed as a typed function.

This guide covers the concepts that hold across all four implementations and shows the idiomatic API in each language. For the full language API reference, see the per-language guides ([Python](python.md), [TypeScript](typescript.md), [PHP](php.md), [Go](go.md)).

## Anatomy of a tool

Every tool has four parts:

| Part | Purpose |
|------|---------|
| **Name** | Snake-case string passed to the LLM. Must be unique within a registry. Auto-derived from the function or struct name where the language supports it. |
| **Description** | Short human-readable sentence. The LLM reads this to decide when to call the tool — write it like a docstring. |
| **Parameters** | A JSON Schema object (`type: object`, `properties`, `required`). The LLM fills in these fields when it emits a `tool_call`. |
| **Handler** | The function that executes when the tool is called. Returns a JSON-serialisable value on success, or an error. |

The return value is serialised to JSON and sent back to the LLM as the tool result. Errors are caught by the execution layer and wrapped in an error envelope (`{"error": "..."}`) rather than crashing the agent loop — this lets the model self-correct rather than halting.

## Defining a tool

Use the two example tools below across all four languages: `add(a, b)` and `fetch_page(url)`.

### Python

The `@tool` decorator attaches metadata to the function. JSON schema is auto-generated from type hints at registration time — no schema literal needed.

```python
from src.python.uses_tools import tool

@tool(description="Add two integers and return the sum.")
async def add(a: int, b: int) -> int:
    return a + b

@tool(description="Fetch a URL and return the response body as text.")
async def fetch_page(url: str) -> str:
    import httpx
    async with httpx.AsyncClient() as client:
        resp = await client.get(url)
        return resp.text

agent.register_tool(add)
agent.register_tool(fetch_page)
```

Both sync and async functions are accepted. `@tool` also accepts `name=` to override the derived name and `schema_override=` to supply an explicit schema.

### TypeScript

TypeScript types are erased at runtime, so the schema must be supplied as a plain JSON Schema literal inside the `defineTool()` call.

```typescript
import { defineTool } from "./uses-tools.js";

const addTool = defineTool({
  name: "add",
  description: "Add two integers and return the sum.",
  parameters: {
    type: "object",
    properties: {
      a: { type: "number", description: "First operand" },
      b: { type: "number", description: "Second operand" },
    },
    required: ["a", "b"],
  },
  execute: ({ a, b }: { a: number; b: number }) => a + b,
});

const fetchPageTool = defineTool({
  name: "fetch_page",
  description: "Fetch a URL and return the response body as text.",
  parameters: {
    type: "object",
    properties: { url: { type: "string", description: "URL to fetch" } },
    required: ["url"],
  },
  execute: async ({ url }: { url: string }) => {
    const resp = await fetch(url);
    return resp.text();
  },
});

agent.registerTool(addTool);
agent.registerTool(fetchPageTool);
```

The `execute` function can be sync or async; the framework wraps the return value in `Promise.resolve`.

### PHP

PHP reflection does not expose reliable parameter-level type information for schema generation, so the schema is supplied explicitly via `ToolDef::make()`.

```php
use AgentHarness\ToolDef;

$addTool = ToolDef::make(
    name: 'add',
    description: 'Add two integers and return the sum.',
    parameters: [
        'type' => 'object',
        'properties' => [
            'a' => ['type' => 'number', 'description' => 'First operand'],
            'b' => ['type' => 'number', 'description' => 'Second operand'],
        ],
        'required' => ['a', 'b'],
    ],
    fn: fn(array $args) => $args['a'] + $args['b'],
);

$fetchPageTool = ToolDef::make(
    name: 'fetch_page',
    description: 'Fetch a URL and return the response body as text.',
    parameters: [
        'type' => 'object',
        'properties' => ['url' => ['type' => 'string', 'description' => 'URL to fetch']],
        'required' => ['url'],
    ],
    fn: fn(array $args) => file_get_contents($args['url']),
);

$agent->registerTool($addTool);
$agent->registerTool($fetchPageTool);
```

PHP tools are always synchronous. Errors thrown inside `fn` are caught and wrapped as `{"error": "..."}`.

### Go

`tools.Tool()` accepts a typed function and derives the schema from the args struct's field tags. No schema literal is required.

```go
import "agent-harness/go/tools"

type AddArgs struct {
    A int `json:"a" desc:"First operand"`
    B int `json:"b" desc:"Second operand"`
}

func addFn(_ context.Context, args AddArgs) (int, error) {
    return args.A + args.B, nil
}

type FetchArgs struct {
    URL string `json:"url" desc:"URL to fetch"`
}

func fetchFn(_ context.Context, args FetchArgs) (string, error) {
    resp, err := http.Get(args.URL)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    return string(body), nil
}

addTool    := tools.Tool(addFn,   tools.Description("Add two integers and return the sum."))
fetchTool  := tools.Tool(fetchFn, tools.Description("Fetch a URL and return the response body as text."))

a.Registry.Register(addTool)
a.Registry.Register(fetchTool)
```

The tool name is derived automatically: `addFn` → `add_fn`; override with `tools.Name("add")`. Programmer errors (wrong function signature, unsupported field type) panic at construction time rather than at call time.

## Schema generation

Each language has a different strategy for turning a tool definition into the JSON Schema object the LLM receives.

### Python

`_build_param_schema` calls `inspect.signature` and `typing.get_type_hints` on the function at registration time. It walks every parameter (skipping `self`/`cls`), maps Python types to JSON Schema types via a fixed table (`str → "string"`, `int → "integer"`, `float → "number"`, `bool → "boolean"`, `list → "array"`, `dict → "object"`), and marks parameters with no default as required. Pass `schema_override=` to `@tool` to supply an explicit schema instead.

```python
import json
from src.python.uses_tools import _build_param_schema

print(json.dumps(_build_param_schema(add.__wrapped__ if hasattr(add, '__wrapped__') else add), indent=2))
# {
#   "type": "object",
#   "properties": { "a": {"type": "integer"}, "b": {"type": "integer"} },
#   "required": ["a", "b"]
# }
```

### TypeScript

TypeScript has no runtime type information. The `defineTool()` helper accepts a `parameters` literal that is passed through verbatim. This is the universal path — any JSON Schema expression is valid.

### PHP

`ToolDef::make()` accepts a `parameters` array that maps directly to the JSON Schema sent to the LLM. No automatic inference occurs.

### Go

`tools.Schema(reflect.TypeOf(args))` walks the struct's exported fields using reflection. Field tags drive the output:

- `json:"name"` — JSON property name (falls back to lowercased Go field name).
- `json:",omitempty"` — marks the field optional (omitted from `required`).
- `desc:"..."` — sets the property `description`.

Nested structs are recursed into, slices produce `{"type": "array", "items": ...}`, and `map[string]any` produces `{"type": "object"}`. Unsupported kinds (`chan`, `func`, `interface`) panic at construction time. Cyclic types are detected with a per-call `seen` set; the cycle point emits `{"type": "object"}` so generation terminates rather than stack-overflowing (see [The Go reflection model](#the-go-reflection-model)).

```go
import (
    "encoding/json"
    "reflect"
    "agent-harness/go/tools"
)

schema := tools.Schema(reflect.TypeOf(AddArgs{}))
out, _ := json.MarshalIndent(schema, "", "  ")
fmt.Println(string(out))
// {
//   "type": "object",
//   "properties": {
//     "a": { "type": "integer", "description": "First operand" },
//     "b": { "type": "integer", "description": "Second operand" }
//   },
//   "required": ["a", "b"]
// }
```

`tools.Registry.Schemas()` returns the full OpenAI-format `tools` array for all registered tools, sorted by name.

## Registering and executing

### Python

```python
# Register
agent.register_tool(add)        # @tool-decorated function
agent.register_tool(some_def)   # ToolDef dataclass

# Inspect
agent.tools  # dict[str, ToolDef] — read-only copy

# Execute (called internally by the run loop; also callable directly)
result_json = await agent._execute_tool("add", {"a": 3, "b": 4})
# '7'
```

`register_tool` returns `self` for chaining. `unregister_tool(name)` removes a tool and fires `TOOL_UNREGISTER`.

### TypeScript

```typescript
// Register
agent.registerTool(addTool);

// Inspect
agent.tools;  // read-only Map<string, ToolDef>

// Execute (called internally; also callable directly)
const result = await agent.executeTool("add", { a: 3, b: 4 });
// 7
```

Multiple tool calls in a single LLM turn execute concurrently via `Promise.all`.

### PHP

```php
// Register
$agent->registerTool($addTool);

// Inspect
$agent->getTools();  // array<string, ToolDef>

// Execute
$result = $agent->executeTool('add', ['a' => 3, 'b' => 4]);
// 7
```

PHP executes tool calls sequentially; there is no concurrent dispatch.

### Go

`*tools.Registry` is embedded anonymously on `*Agent`, so its methods are promoted:

```go
// Register via builder
a, _ := agent.NewBuilder("gpt-4o").
    Client(client).
    Tool(addTool).
    Tool(fetchTool).
    Build(ctx)

// Register after Build
a.Registry.Register(addTool)

// List / inspect
defs := a.Registry.List()
schemas := a.Registry.Schemas()  // OpenAI-format tools array

// Execute
result, err := a.Registry.Execute(ctx, "add", []byte(`{"a":3,"b":4}`))
// result = 7
```

`Registry.Execute` returns `tools.ErrNotFound` if the name is not registered; the agent run loop handles this by wrapping the error in the error envelope.

## Tool errors and the error envelope

Tool execution errors do not abort the agent run. Instead, they are caught by the execution layer, serialised as `{"error": "message string"}`, and returned to the LLM as the tool result. The model can inspect the error and retry with corrected arguments.

**Unknown tools** (the LLM calls a name that is not registered) follow the same path: the harness returns `{"error": "Unknown tool: <name>"}` and continues the loop rather than raising.

### Python

`_execute_tool` wraps the call in a try/except. Unknown tool produces `{"error": "Unknown tool: name"}` via `td is None` check. Any exception from the handler is caught and serialised.

### TypeScript

`executeTool` wraps in a try/catch and awaits `Promise.resolve(result)`. Unknown tool and handler exceptions both produce the `{"error": "..."}` JSON string.

### PHP

`executeTool` wraps in a try/catch. Unknown tool and exceptions produce `["error" => "..."]` serialised to JSON.

### Go

Go handlers return `(any, error)` rather than throwing. The agent run loop checks the error return from `Registry.Execute`:

- `tools.ErrNotFound` → envelope `{"error": "Unknown tool: name"}` + continue.
- Any other error returned by the handler → envelope `{"error": "error string"}` + continue.
- Successful return → JSON-marshal the `any` result.

The envelope construction lives in the run loop, not in `Registry.Execute` itself, which returns the raw Go error to the caller. This keeps the registry reusable outside of agent contexts.

## Lifecycle hooks

When `HasHooks` (Python/TS/PHP) or `hooks.Hub` (Go) is present, the tools subsystem emits the following events:

| Event | Fired when |
|-------|-----------|
| `TOOL_REGISTER` | `register_tool` / `registerTool` / `a.Registry.Register` is called |
| `TOOL_UNREGISTER` | `unregister_tool` / `unregisterTool` / `a.Registry.Unregister` is called |
| `TOOL_CALL` | The LLM emits a `tool_call` block and the harness is about to dispatch it |
| `TOOL_RESULT` | The handler returned successfully and the result is about to be sent back |
| `TOOL_ERROR` | The handler returned an error (or the tool was unknown) |

Subscribe to observe tool activity:

**Python:**

```python
from src.python.has_hooks import HookEvent

agent.on(HookEvent.TOOL_CALL,   lambda name, args: print(f"calling {name}({args})"))
agent.on(HookEvent.TOOL_RESULT, lambda name, result: print(f"{name} → {result}"))
agent.on(HookEvent.TOOL_ERROR,  lambda name, err: print(f"{name} failed: {err}"))
```

**Go:**

```go
a.Hub.On(hooks.ToolCall,   func(ctx context.Context, args ...any) {
    fmt.Printf("calling %s\n", args[0])
})
a.Hub.On(hooks.ToolError,  func(ctx context.Context, args ...any) {
    fmt.Printf("tool error: %v\n", args)
})
```

There is no middleware hook on tool execution itself — use `TOOL_CALL` and `TOOL_RESULT`/`TOOL_ERROR` to observe and log. The builder's `.On(hooks.ToolCall, ...)` registers handlers before the first run.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Definition helper | `@tool` decorator | `defineTool()` | `ToolDef::make()` | `tools.Tool(fn, opts...)` |
| Schema source | `get_type_hints` + `inspect.signature` auto-inference | Explicit JSON Schema literal | Explicit JSON Schema literal | Reflection on ArgsStruct with `json:` / `desc:` tags |
| Async/sync | Both (async preferred) | Both (`async execute` supported) | Synchronous only | Synchronous (`func(ctx, args) (result, error)`) |
| Parallel dispatch | `asyncio.gather` (concurrent) | `Promise.all` (concurrent) | Sequential | Goroutines via `sync.WaitGroup` (concurrent) |
| Error shape returned to LLM | `{"error": "..."}` JSON string | `{"error": "..."}` JSON string | `{"error": "..."}` JSON string | `{"error": "..."}` JSON string (constructed in run loop) |
| Unknown-tool handling | Envelope + continue | Envelope + continue | Envelope + continue | `ErrNotFound` → envelope + continue |
| Register method | `agent.register_tool(fn_or_def)` | `agent.registerTool(def)` | `$agent->registerTool($def)` | `a.Registry.Register(def)` |
| Unregister method | `agent.unregister_tool(name)` | `agent.unregisterTool(name)` | `$agent->unregisterTool($name)` | `a.Registry.Unregister(name)` |
| Reflection scope | Function signature (parameters only; return type ignored for schema) | None | None | Exported struct fields, recursively |

## The Go reflection model

Go's tool schema is driven entirely by struct field tags on the args type. `tools.Schema(reflect.TypeOf(ArgsStruct{}))` walks exported fields and calls `kindToSchema` recursively:

- **Primitives** (`string`, `bool`, `int*`, `uint*`, `float*`) map to the corresponding JSON Schema type.
- **Slices** produce `{"type": "array", "items": <element schema>}`.
- **Maps** produce `{"type": "object"}` (generic; key/value types are not inspected).
- **Nested structs** recurse into `schemaInternal` with the same `seen` set.
- **Pointers** are dereferenced one level before dispatch.

**Cycle detection** is implemented via a `map[reflect.Type]bool` passed through the recursion. Before generating a struct's schema, the type is added to `seen`; it is removed on exit via `defer delete(seen, t)`. If `seen[t]` is already true when `schemaInternal` is entered, a generic `{"type": "object"}` is returned immediately and the recursion terminates. This was introduced in milestone M1.9 to handle self-referential or mutually-recursive args types without stack overflow. See [ADR 0031](../adr/0031-go-struct-embedding-composition.md) for the broader Go composition design.

Programmer errors — wrong function signature, unsupported field kind — are panics at `tools.Tool(...)` call time, not at run time. This mirrors Python's `TypeError` on decorator misuse and keeps runtime failure modes limited to actual handler logic.

## Known limitations

**All languages:**

- **No middleware hook on execution.** There is no before/after hook that can intercept and mutate tool arguments or results mid-flight. Use `TOOL_CALL` and `TOOL_RESULT` hooks to observe, but they cannot modify the payload. This is a deliberate scope decision; a `ToolMiddleware` abstraction is not currently planned.
- **Tool unregistration from skill unmount.** When a skill is unmounted, its contributed tools are cleanly unregistered. However, if a tool was registered by both a skill and directly on the agent, the direct registration is also removed. Register tools either through a skill or directly — not both.

**Go-specific:**

- **Cycle detection was not present before M1.9.** If you pin to a pre-M1.9 build and your args structs contain cycles (or you pass a recursive type by mistake), `tools.Schema` will overflow the stack. Upgrade to M1.9 or later.
- **Anonymous functions require `tools.Name(...)`.**  Go's `runtime.FuncForPC` returns an empty or `funcN` name for closures. `tools.Tool` panics at construction time if the name cannot be derived and `tools.Name(...)` is not supplied.

**Python/TypeScript/PHP:**

- **`unregister_tool` from skill unmount is not propagated to middleware or hooks.** If your middleware or hook handler caches a reference to a tool name and that tool is later unregistered by skill unmount, you may see stale references. Subscribe to `TOOL_UNREGISTER` to stay consistent.

## See also

- [ADR 0016](../adr/0016-tool-registration.md) — Tool registration and schema generation design
- [ADR 0031](../adr/0031-go-struct-embedding-composition.md) — Go composition model and capability interfaces
- [Python guide: Tools](python.md#tools) · [TypeScript guide: Tools](typescript.md#tools) · [PHP guide: Tools](php.md#tools) · [Go guide: Tools](go.md#tools)
- [Skills guide](skills.md) — How skills contribute tools to an agent; the `ToolsContributor` interface
