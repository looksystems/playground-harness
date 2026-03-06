# Cross-Language Comparison

The Python, TypeScript, and PHP implementations of the agent harness share the same conceptual architecture — mixins for composition, hooks for extensibility, middleware for pipeline processing, streaming for LLM output, and YAML-based event parsing. They differ in language-specific idioms and runtime constraints. For the shared architecture itself, see [architecture.md](architecture.md).

## Comparison Table

| Aspect | Python | TypeScript | PHP |
|--------|--------|------------|-----|
| **Trait/Mixin Mechanism** | Multiple inheritance mixins | Function-based mixins (`HasX<TBase>(Base)`) | Native `trait` keyword |
| **Mixin Init Strategy** | Lazy init via `hasattr` + `__init_has_X__()` | Inline field initialization in anonymous class | PHP trait properties initialize on first use |
| **Agent Composition** | `class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents): pass` | `const StandardAgent = EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))` | `class StandardAgent extends BaseAgent { use HasHooks; use HasMiddleware; use UsesTools; use EmitsEvents; }` |
| **Async Model** | `async`/`await` throughout, `asyncio` | `async`/`await`, Promises | Synchronous (no async runtime) |
| **LLM Client** | litellm (multi-provider) | OpenAI SDK | Guzzle HTTP (raw API calls) |
| **YAML Parsing** | PyYAML (`yaml.safe_load`) | `yaml` npm package (`YAML.parse`) | Custom `parseSimpleYaml()` (zero dependencies) |
| **Hook Dispatch** | Concurrent (`asyncio.gather` + `return_exceptions=True`) | Concurrent (`Promise.allSettled`) | Sequential (synchronous `foreach`) |
| **Streaming Primitive** | `asyncio.Queue` → `AsyncIterator` | `createChannel()` → `AsyncIterable` | `Generator` (pull-based) |
| **Tool Schema** | Auto-generated from type hints via `_build_param_schema()` | Explicit via `ToolDef.parameters` (manual JSON schema) | Explicit via `ToolDef::make()` parameters array |
| **Interface/Protocol** | `Protocol` (runtime_checkable) | `interface` | `interface` |
| **Type System** | Type hints (optional, not enforced at runtime) | Static types (enforced at compile time) | Typed properties + `declare(strict_types=1)` |
| **Test Framework** | pytest + pytest-asyncio | vitest | PHPUnit |
| **Test Count** | 47 | 44 | 44 (82 assertions) |
| **VFS Content Types** | `str \| bytes` | `string` only | `string` only |
| **Lazy File Providers** | Synchronous callables | Async (returns `Promise<string>`) | Synchronous closures |
| **Shell Registry** | Global singleton (module-level) | Global singleton (module-level) | Global singleton (static class) |

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
