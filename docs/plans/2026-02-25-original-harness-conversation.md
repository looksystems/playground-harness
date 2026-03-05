# Original Harness Design Conversation

> Captured from the initial design conversation (25 Feb - 4 Mar 2026) that produced the agent harness, skill system, and virtual shell skill.

---

## What is involved in building out a lightweight LLM/agent harness?

*25 Feb*

Building a lightweight LLM/agent harness typically involves a few core layers:

### The basics

At minimum you need a message loop — something that takes user input, sends it to an LLM API, gets a response, and optionally loops back if the model wants to take an action. The simplest version is ~100 lines of code: a while loop, an API call, and some output parsing.

### Tool/function calling

This is where it gets interesting. You define a set of tools (functions with names, descriptions, and parameter schemas), pass them to the model, and then handle the model's tool-use requests by dispatching to actual implementations. The core pattern is:

1. Send messages + tool definitions to the LLM
2. If the response contains a tool call, execute it
3. Append the tool result to the conversation
4. Send again (back to step 1)
5. If no tool call, return the final text response

### Conversation/context management

You need to manage the message list — appending user messages, assistant messages, and tool results in the right format. For longer interactions, you'll hit context limits, so you may need truncation, summarization, or sliding-window strategies.

### Common additions (still "lightweight")

- System prompt management — templating and injecting instructions
- Retry/error handling — rate limits, malformed tool calls, timeouts
- Streaming — forwarding token-by-token output for better UX
- Guardrails/validation — checking tool call arguments before execution, sanitizing outputs
- Logging/observability — recording each turn for debugging (surprisingly important early on)
- Multi-model support — abstracting the API layer so you can swap between providers

### Things people often over-engineer early

- Complex agent orchestration frameworks (multi-agent, planning trees) — usually unnecessary at first
- Persistent memory/RAG — add it when you actually need it
- Elaborate plugin systems — start with hardcoded tools

### Things people under-invest in early

- Good tool descriptions — the quality of your tool schemas and descriptions matters enormously for reliability
- Structured error messages back to the model — when a tool fails, telling the model *why* lets it self-correct
- Eval/testing — even a small set of test cases saves a lot of grief

### Tech choices

Most people build this in Python or TypeScript. You can do it with raw HTTP calls to the API, or use an SDK (like Anthropic's Python/TS SDK) that handles message formatting and streaming. Frameworks like LangChain or LlamaIndex exist but add significant complexity — many teams find they're better off with a thin custom harness they fully understand.

The whole thing can reasonably be a single file under 500 lines for a capable agent, which is a good sign you're keeping it lightweight.

---

## Single-file implementation with litellm, middleware, hooks, and tool registration

*25 Feb*

A ~560-line single-file harness with the following contracts:

**Tool registration** — decorate any function with `@tool(description="...")` and call `agent.register_tool(fn)`. Parameter schemas are auto-derived from type hints, or you can pass `schema_override` for full control.

```python
@tool(description="Add two numbers")
def add(a: int, b: int) -> int:
    return a + b

agent.register_tool(add)
```

**Middleware** — extend `BaseMiddleware` and override `pre` (transform messages before each LLM call) and/or `post` (transform the assistant response after). Middleware runs in FIFO order.

```python
class MyMiddleware(BaseMiddleware):
    def pre(self, messages, context):
        return messages
    def post(self, message, context):
        return message

agent.use(MyMiddleware())
```

**Hooks** — subscribe to lifecycle events with `agent.on(HookEvent.X, callback)`. Events include `RUN_START`, `LLM_REQUEST`, `LLM_RESPONSE`, `TOOL_CALL`, `TOOL_RESULT`, `TOOL_ERROR`, `TOKEN_STREAM`, `RETRY`, `ERROR`, and `RUN_END`.

```python
agent.on(HookEvent.TOOL_CALL, lambda name, args: print(f"Calling {name}"))
```

Included batteries: `TruncationMiddleware` (sliding window over messages), `LoggingMiddleware` (debug logging).

The only external dependency is litellm, so it works with Anthropic, OpenAI, Ollama, Bedrock, Vertex, etc. out of the box just by changing the model string.

> Output: `examples/agent_harness.py` (sync version)

---

## Async-native rewrite

*25 Feb*

Key changes from the sync version (~637 lines):

**Core async changes:**
- `agent.run()` is now `await agent.run()` — the entire loop is async
- Uses `litellm.acompletion()` instead of `litellm.completion()`
- Streaming uses `async for chunk in resp` instead of sync iteration
- Retry backoff uses `await asyncio.sleep()` with exponential delay

**Parallel tool execution:**
New `parallel_tool_calls=True` flag (default). When the model returns multiple tool calls in one turn, they run concurrently via `asyncio.gather()`.

**Sync/async interop everywhere:**
Tools, middleware, and hooks can all be sync or async — the harness handles both transparently via `_call_fn()`.

Hooks fire concurrently — all subscribers for an event run via `asyncio.gather()`, so a slow logging hook doesn't block a metrics hook.

Middleware remains sequential (FIFO) since each step's output feeds the next — intentional pipeline semantics.

> Output: `examples/agent_harness.py` (async version)

---

## TypeScript and PHP ports

*25 Feb*

Contracts are identical across all three languages — same hook events, same middleware pre/post pipeline, same tool registration pattern.

### TypeScript (~578 lines)

- Uses the official `openai` SDK directly (works with any OpenAI-compatible endpoint, including a litellm proxy via `baseURL`)
- `defineTool()` helper replaces the Python `@tool` decorator
- `Promise.all()` for parallel tool calls, `Promise.allSettled()` for concurrent hook dispatch
- `structuredClone()` for deep copies
- `for await...of` for streaming chunks

### PHP (~697 lines)

- Uses `guzzlehttp/guzzle` for HTTP, hitting the raw OpenAI-compatible REST API
- `ToolDef::make()` named-argument factory for ergonomic tool registration
- PHP enums (`enum HookEvent: string`) for hook events
- Streaming parses SSE manually from the Guzzle response stream
- `BaseMiddleware` abstract class with `pre()`/`post()`
- Fluent `$agent->registerTool()->use()->on()` chaining
- PHP 8.1+ required (enums, readonly properties, named arguments, fibers)

**Key design decision across all three:** the middleware pipeline is sequential (each step's output feeds the next), while hooks and parallel tool calls are concurrent.

> Output: `examples/agent_harness.ts`, `examples/agent_harness.php`

---

## Skill system across all three languages

*25 Feb*

A skill is a composable bundle of tools, prompt fragments, middleware, and hooks that represents a coherent capability.

### The Skill contract

| Concern | Method | Purpose |
|---------|--------|---------|
| Identity | `name`, `description`, `version` | Metadata (name auto-derives from class) |
| Prompt | `instructions` | Injected into system prompt via middleware |
| Tools | `tools()` | Returns tool definitions the LLM can call |
| Middleware | `middleware()` | Returns pre/post message transforms |
| Hooks | `hooks()` | Returns event -> callback mappings |
| Resources | `setup(ctx)` / `teardown(ctx)` | Lifecycle for connections, sessions, etc. |
| Dependencies | `dependencies` | Other skill classes, resolved transitively |

The `SkillManager` handles dependency resolution (topological sort), lifecycle ordering (setup in dependency order, teardown in reverse), and prompt injection.

### Language-specific patterns

**Python** — Skills are mounted directly on the agent via monkey-patched `agent.mount()` / `agent.shutdown()` convenience methods. The `@tool` decorator works inside `tools()` methods with closures over `self.context.state`. Lifecycle is async.

**TypeScript** — `SkillManager` is a separate class (cleaner than patching). `defineTool()` replaces decorators. Closures capture `this.context!.state` for tool implementations. Same async lifecycle with Promise.

**PHP** — `SkillManager` is also a separate class. Tools use `ToolDef::make()` with closures via `use ($ctx)` to capture the skill context. Anonymous classes for inline middleware. Lifecycle is synchronous.

### Example skills included

All three languages include the same four example skills:
- **MathSkill** — pure tools, no state
- **MemorySkill** — stateful tools via setup
- **GuardrailSkill** — middleware + hooks, no tools
- **WebBrowsingSkill** — async resource lifecycle

> Output: `examples/skills.py`, `examples/skills.ts`, `examples/skills.php`

---

## Virtual shell skill (inspired by vercel-labs/just-bash)

*4 Mar*

### The core insight

Vercel's d0 agent went from 80% to 100% success rate by deleting most of their specialized tools and replacing them with a single bash tool over their semantic layer files. The approach leverages existing shell semantics to let models navigate and extract structured information efficiently.

**The mental shift:** don't build a tool for every query pattern — mount your context as files and let the model use `grep`, `cat`, `find`, `jq` to answer its own questions.

### Three-layer stack

| Layer | File | Lines | Purpose |
|-------|------|-------|---------|
| Harness | `agent_harness.*` | ~640 | Async agent loop, middleware, hooks, tool registration |
| Skills | `skills.*` | ~530 | Composable bundles with lifecycle, deps, prompt injection |
| Shell | `shell_skill.*` | ~860 | VirtualFS + shell interpreter -> single `exec` tool |

### What the ShellSkill provides

**VirtualFS** — an in-memory filesystem with lazy file loading (providers called on first read, then cached):

```python
skill = ShellSkill()
skill.write("/data/schema.yaml", schema_content)
skill.mount_dir("/docs", {"api.md": "...", "guide.md": "..."})
skill.mount_json("/data/users.json", user_list)
skill.write_lazy("/data/big_report.csv", lambda: fetch_from_s3())
```

**Shell** — a lightweight interpreter supporting 23 commands: `cat`, `echo`, `ls`, `find`, `grep`, `head`, `tail`, `wc`, `sort`, `uniq`, `cut`, `tr`, `sed`, `jq`, `tree`, `tee`, `touch`, `mkdir`, `cp`, `rm`, `stat`, `cd`, `pwd`. Pipes, redirects, and command chaining all work.

**Single tool** — the entire shell surfaces as one `exec` tool. Instead of the model choosing between `search_schema`, `get_config`, `lookup_user`, `count_records`... it just runs shell commands.

---

## Security model: pure emulation

*4 Mar*

All three implementations are pure emulation — no real shell is ever invoked. Every command is a function in the host language operating on the in-memory VirtualFS. There is no `subprocess.run`, no `child_process.exec`, no `proc_open` anywhere.

### Security boundaries

- **No process spawning** — no subprocess, child_process, exec(), proc_open anywhere
- **No real filesystem access** — all reads/writes go to an in-memory dict/Map
- **No network access** — no HTTP calls, sockets, or fetch
- **No code execution** — no eval, awk, or function definition support
- **Output truncation** — `MAX_OUTPUT = 16,000` chars prevents context flooding
- **Command allowlisting** — restrict which commands are available:

```python
skill = ShellSkill(allowed_commands={"cat", "grep", "find", "ls", "head", "tail", "wc", "jq", "tree"})
```

---

## Virtual filesystem comparison across languages

*4 Mar*

### Storage model

All three are flat key-value stores where the key is an absolute normalized path and the value is the file content. Directories are inferred by prefix scanning — no actual directory objects.

### Where they diverge

- **Content types** — Python supports `str | bytes` for binary content. TypeScript and PHP are string-only.
- **Lazy file loading** — all three support it. TypeScript's `read()` is async because lazy providers can return `Promise<string>`. Python and PHP lazy providers are synchronous.
- **Path normalization** — Python uses `os.path.normpath`. TypeScript and PHP use manual split/resolve loops.

### Intentional limitations (all three)

- No permissions (chmod, uid/gid, rwx)
- No symlinks
- No file descriptors or locking
- No timestamps (stat returns path/type/size only)
- No sparse files or streams
- No glob expansion at the VFS level

### Suggested hardening

- **Read-only mode** — `readonly: bool` flag that makes `write()` throw
- **Size limits** — cap total VFS size to prevent memory exhaustion
- **Path jailing** — restrict writes to specific prefixes (e.g., `/tmp`, `/workspace`) while keeping everything else read-only
