# Cross-Language Comparison

The Python, TypeScript, PHP, and Go implementations of the agent harness share the same conceptual architecture — mixins/embedding for composition, hooks for extensibility, middleware for pipeline processing, streaming for LLM output, and YAML-based event parsing. They differ in language-specific idioms and runtime constraints. For the shared architecture itself, see [architecture.md](architecture.md).

## Comparison Table

| Aspect | Python | TypeScript | PHP | Go |
|--------|--------|------------|-----|----|
| **Trait/Mixin Mechanism** | Multiple inheritance mixins | Function-based mixins (`HasX<TBase>(Base)`) | Native `trait` keyword | Anonymous struct embedding |
| **Mixin Init Strategy** | Lazy init via `hasattr` + `__init_has_X__()` | Inline field initialization in anonymous class | PHP trait properties initialize on first use | Eager init in `NewAgent`; no init collision possible |
| **Agent Composition** | `class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents, HasShell, HasSkills): pass` | `const StandardAgent = HasSkills(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))))` | `class StandardAgent extends BaseAgent { use HasHooks; use HasMiddleware; use UsesTools; use EmitsEvents; use HasShell; use HasSkills; }` | `Agent` embeds `*hooks.Hub`, `*tools.Registry`, `*middleware.Chain`; named fields for `*shell.Host`, `*events.Host`, `*skills.Manager` |
| **Async Model** | `async`/`await` throughout, `asyncio` | `async`/`await`, Promises | Synchronous (no async runtime) | Goroutines + channels; `context.Context` for cancellation |
| **LLM Client** | litellm (multi-provider) | OpenAI SDK | Guzzle HTTP (raw API calls) | OpenAI + Anthropic in-tree; pluggable `llm.Client` interface |
| **YAML Parsing** | PyYAML (`yaml.safe_load`) | `yaml` npm package (`YAML.parse`) | Custom `parseSimpleYaml()` (zero dependencies) | `gopkg.in/yaml.v3` |
| **Hook Dispatch** | Concurrent (`asyncio.gather` + `return_exceptions=True`) | Concurrent (`Promise.allSettled`) | Sequential (synchronous `foreach`) | Concurrent (goroutines + `sync.WaitGroup`; panics recovered per handler) |
| **Streaming Primitive** | `asyncio.Queue` → `AsyncIterator` | `createChannel()` → `AsyncIterable` | `Generator` (pull-based) | `<-chan llm.Chunk` (producer-owned, closed exactly once) |
| **Tool Schema** | Auto-generated from type hints via `_build_param_schema()` | Explicit via `ToolDef.parameters` (manual JSON schema) | Explicit via `ToolDef::make()` parameters array | Auto-generated from struct tags via `tools.Schema()` at construction time |
| **Interface/Protocol** | `Protocol` (runtime_checkable) | `interface` | `interface` | `interface` + narrow capability interfaces (type-asserted at mount) |
| **Type System** | Type hints (optional, not enforced at runtime) | Static types (enforced at compile time) | Typed properties + `declare(strict_types=1)` | Static types (enforced at compile time); no generics needed for current surface |
| **Test Framework** | pytest + pytest-asyncio | vitest | PHPUnit | stdlib `testing` + `testify/require` |
| **Test Count** | 360 | 364 | 328 | 842 |
| **Shell Driver** | `ShellDriver` (ABC) + `BuiltinShellDriver` | `ShellDriver` (interface) + `BuiltinShellDriver` | `ShellDriverInterface` + `BuiltinShellDriver` | `shell.Driver` (interface) + `builtin.BuiltinShellDriver` |
| **Bashkit Driver** | `BashkitPythonDriver` (PyO3 in-process) | `BashkitCLIDriver` (CLI subprocess) | `BashkitCLIDriver` (CLI subprocess) | `bashkit.Driver` (CLI subprocess) |
| **Bashkit Resolver** | `BashkitDriver.resolve()` → `import bashkit` | `BashkitDriver.resolve()` → `which bashkit` | `BashkitDriver::resolve()` → `which bashkit` | `bashkit.NewDriver()` → `which bashkit` at Exec time |
| **FS Driver** | `FilesystemDriver` (ABC) + `BuiltinFilesystemDriver` | `FilesystemDriver` (interface) + `BuiltinFilesystemDriver` | `FilesystemDriver` (interface) + `BuiltinFilesystemDriver` | `vfs.FilesystemDriver` (interface) + `vfs.InMemoryFS` |
| **Driver Factory** | `ShellDriverFactory` (class methods) | `ShellDriverFactory` (static methods) | `ShellDriverFactory` (static methods) | `shell.DefaultFactory` (package-level var) |
| **Driver Selection** | Global default + per-agent via builder `.driver()` | Global default + per-agent via builder `.driver()` | Global default + per-agent via builder `->driver()` | Per-agent via `agent.NewBuilder(...).Shell(driver)` |
| **VFS Content Types** | `str \| bytes` | `string` only | `string` only | `string` or `[]byte` |
| **Lazy File Providers** | Synchronous callables | Async (returns `Promise<string>`) | Synchronous closures | Synchronous functions |
| **Shell Registry** | Global singleton (module-level) | Global singleton (module-level) | Global singleton (static class) | No global shell registry; drivers constructed explicitly |

## Notable Differences

### 1. Mixin Composition

Python uses multiple inheritance mixins with a lazy-init pattern: each mixin defines an `__init_has_X__()` method, and the first call to any mixin method checks via `hasattr` whether initialization has run. This pattern exists because Python's MRO makes cooperative `__init__` fragile when mixins are independent and don't know about each other. Each mixin must be safe to use regardless of whether `__init__` was called in the right order.

TypeScript uses function-based mixins — each mixin is a function that takes a base class and returns an anonymous subclass with the new behavior. Composition reads right-to-left: in `EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))`, `HasHooks` wraps `BaseAgent` first, then `HasMiddleware` wraps that, and so on. Field initialization happens inline in the anonymous class body.

PHP uses native `trait` declarations, which are the cleanest syntactically. Traits are declared with `use HasHooks;` inside the class body, and the language handles method and property merging. Conflict resolution (when two traits define the same method) is handled with `insteadof` and `as` operators, though the harness avoids conflicts by design.

### 2. Async vs Synchronous

Python and TypeScript are fully async. Every agent method, hook handler, middleware layer, and streaming consumer uses `async`/`await`. This enables concurrent hook dispatch and non-blocking streaming.

PHP is fully synchronous — there is no event loop and no promises. This is a deliberate choice, not a limitation; the PHP ecosystem overwhelmingly favors synchronous, request-scoped execution. The consequence is that hook dispatch is sequential (listeners run one after another in a `foreach` loop) and streaming is pull-based (the consumer drives iteration via `Generator`).

### 3. Streaming Implementation

Python uses `asyncio.Queue` as the bridge between producer and consumer. The YAML parser pushes parsed lines onto the queue, and the consumer pulls them via an `AsyncIterator` adapter. Backpressure is implicit in the queue's capacity.

TypeScript uses a custom `createChannel()` utility that returns a linked `writer`/`reader` pair. The writer produces values, the reader consumes them as an `AsyncIterable`. Backpressure is implemented via promises — the writer awaits until the reader has consumed the previous value.

PHP uses `Generator`, which is the ecosystem-standard pattern for streaming (used by openai-php/client, Laravel AI SDK, Prism PHP, among others). The generator is pull-based: the consumer calls `foreach` or `next()`, which resumes the generator to produce the next value. No queue or channel abstraction is needed.

### 4. YAML Parsing

Python uses PyYAML (`yaml.safe_load`), the standard YAML library in the Python ecosystem. TypeScript uses the `yaml` npm package (`YAML.parse`).

PHP uses a custom subset parser (`parseSimpleYaml`) to avoid adding a Composer dependency for the simple key-value format that events use. This custom parser handles top-level key-value pairs, one level of nesting (for structured event data), and type casting for booleans, null, integers, and floats. It does not attempt to handle the full YAML specification — only the subset the harness actually produces.

### 5. LLM Client

Python uses litellm, which provides a unified interface across multiple LLM providers (OpenAI, Anthropic, Cohere, and others). A single `acompletion()` call works regardless of the underlying provider, with litellm handling the API translation.

TypeScript uses the OpenAI SDK directly. This gives idiomatic access to OpenAI's API (including streaming via `stream: true`) but ties the implementation to OpenAI-compatible endpoints.

PHP uses Guzzle HTTP for raw API calls, constructing request bodies and parsing responses manually. This gives full control over the HTTP layer and avoids depending on a provider-specific SDK, but requires the implementation to handle request construction, error mapping, and response parsing itself.

### 6. Virtual Shell

The virtual shell implementation is nearly identical across all three languages — the same 30 commands, same pipe/redirect/chaining logic, same VirtualFS storage model, and the same `registerCommand()`/`unregisterCommand()` API for custom commands. The differences are minor:

Python's VirtualFS supports `str | bytes` content, allowing binary files (images, protobuf). TypeScript and PHP are string-only; binary content would need to be base64-encoded.

TypeScript's lazy file providers are async (returning `Promise<string>`) because the VFS `read()` method is async. This allows lazy providers to fetch from APIs or databases. Python and PHP lazy providers are synchronous callables.

The `HasShell` mixin follows the same language-specific patterns as other mixins: Python multiple inheritance, TypeScript function-based mixin, PHP native trait. In all three, it auto-registers the `exec` tool when `UsesTools` is also composed, and works independently for programmatic use when it isn't.

### 7. Shell Driver Architecture

All three languages implement the same driver/adapter pattern for the shell and filesystem backends. Two open contracts (`FilesystemDriver` and `ShellDriver`) allow the backend to be swapped without changing agent code. The built-in driver wraps the existing `VirtualFS` and `Shell` classes as the default, requiring no external dependencies.

Python defines the contracts as ABCs (`FilesystemDriver(ABC)`, `ShellDriver(ABC)`). TypeScript uses interfaces. PHP uses interfaces, with the shell contract named `ShellDriverInterface` to avoid a class name conflict with the existing `Shell` class.

`ShellDriverFactory` is consistent across all three: a class-level registry with `register(name, factory)`, `create(name?, opts?)`, a `default` property, and `reset()`. Custom drivers can be registered and selected globally or per-agent via the builder's `.driver()` method.

The `on_not_found` callback (invoked when a command is not found) is a property on the Python and TypeScript drivers, and a `setOnNotFound()` method on the PHP driver (matching PHP's convention of explicit setter methods for callbacks).

### 8. Skills

The `HasSkills` mixin is consistent across all three languages. Skills are mounted via `mount(skill)` and unmounted via `unmount(name)`. Each skill bundles tools, instructions, middleware, hooks, and lifecycle management into a single mountable unit. Dependencies are resolved transitively via topological sort. Four `skill_*` hook events (mount, unmount, setup, teardown) are emitted when `HasHooks` is composed.

The `SkillPromptMiddleware` in all three languages auto-injects mounted skill instructions into the system prompt, ensuring the LLM is aware of all active skill capabilities.

### 9. Go: Struct Embedding and Capability Interfaces

Go replaces class inheritance and function-based mixins with anonymous struct embedding. `*Agent` embeds `*hooks.Hub`, `*tools.Registry`, and `*middleware.Chain` so their methods are promoted directly onto the agent. The embedding approach requires type names to be deliberately distinct to avoid promotion collisions — this is why `Hub`, `Registry`, and `Chain` do not share a common prefix.

Subsystems that would cause ambiguity (`*shell.Host`, `*events.Host`, `*skills.Manager`) are named fields rather than anonymous embeddings.

Skills in Go use narrow capability interfaces (`skills.ToolsContributor`, `skills.Setuppable`, etc.) rather than a monolithic interface. The skill manager type-asserts each capability at mount time, giving the same composable opt-in behaviour as Python's duck-typed mixins without requiring a deep inheritance hierarchy.

Setup and teardown are synchronous in Go (`return error`), unlike Python's `async def setup`. This is idiomatic for Go — synchronous code can still spin goroutines internally, and the `context.Context` passed to `Setup` provides cancellation support.

`Dependencies()` returns `[]Skill` instances (live values) rather than type references, because Go cannot instantiate an arbitrary type by reflection without additional runtime support. Callers supply configured instances directly.

### 10. Go: Concurrency Model

Go is concurrent throughout but without an async/await syntax layer. The agent run loop, hook dispatch, and streaming all use goroutines and channels. `context.Context` propagates cancellation, so a cancelled context aborts the run loop, prevents new hook handlers from starting, and signals stream consumers.

Hook dispatch spins one goroutine per registered handler and waits with `sync.WaitGroup` — equivalent to Python's `asyncio.gather` and TypeScript's `Promise.allSettled`. Panics inside handlers are recovered per-goroutine and logged, preventing one failing hook from blocking the others.

The streaming channel contract — producer-owned, closed exactly once, terminal `Done` chunk — allows goroutines that produce and consume stream events to coordinate without shared mutable state. This is documented in ADR 0032.
