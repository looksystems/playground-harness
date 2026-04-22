# Design Principles

This document describes the core design principles behind the harness framework -- a lightweight, composable foundation for building LLM agent loops.

---

## 1. Composition Over Inheritance

The framework uses trait/mixin composition rather than deep class hierarchies. Each capability -- hooks, middleware, tools, events -- is an independent mixin that can be applied to any base class. Only `BaseAgent` uses traditional inheritance to define the core agent loop; everything else is layered on through composition.

This keeps individual mixins small and testable. A class that needs hooks but not middleware simply includes the hooks mixin and nothing else. There is no god-object that forces every feature onto every consumer.

How this is expressed varies by language:

- **Python:** Multiple inheritance mixins with a lazy-init pattern. Each mixin defines an `__init_has_<capability>__()` method that is called on first access via `hasattr` checks, avoiding MRO `__init__` conflicts entirely. No cooperative `super().__init__()` chains required.
- **TypeScript:** Function-based mixins of the form `HasHooks<TBase extends Constructor>(Base: TBase)` that return anonymous classes extending the base. This is the idiomatic TypeScript approach to mixins and plays well with the type system.
- **PHP:** Native `trait` keyword -- the language's built-in composition mechanism. No workarounds needed; PHP traits are purpose-built for this.
- **Go:** Pointer struct embedding (`*hooks.Hub`, `*tools.Registry`, `*middleware.Chain`) with method promotion. Subsystems whose public method names collide (`events.Host`, `skills.Manager`) are named fields with thin forwarders. Narrow capability interfaces (`ToolsHost`, `HooksHost`, `ShellHost`, ...) let cross-cutting code like the skill manager detect features at runtime via type assertion -- the Go analogue of Python's `hasattr` probe. Eager initialisation in `NewAgent`; no lazy-init. See ADR 0031.

## 2. Opt-in Complexity

Defaults are simple. Advanced features require explicit opt-in rather than configuration to turn them off.

Events are buffered by default; streaming requires an explicit `StreamConfig(mode="streaming", stream_fields=[...])`. `StandardAgent` gives you everything composed together, but you can build a minimal agent from only the mixins you need. The `MessageBus` defaults to a max depth of 10 for cycle detection -- a sensible default that works without configuration but remains adjustable.

The goal is that the simplest use case requires the least code, and complexity scales linearly with the sophistication of what you are building.

## 3. Language-Idiomatic Implementations

Each language implementation follows that language's conventions rather than being a mechanical port from a reference implementation. The APIs feel native to their ecosystem.

- **Python:** async/await throughout, litellm for multi-provider LLM support, dataclasses for data structures, `Protocol` for interfaces.
- **TypeScript:** OpenAI SDK for LLM calls, interfaces for contracts, function-based mixins (idiomatic TS pattern), `Promise.allSettled` for concurrent hook dispatch.
- **PHP:** Guzzle HTTP for LLM calls, native traits for composition, string-backed enums, synchronous execution model, Generators for streaming.
- **Go:** Thin in-tree LLM provider interface with OpenAI and Anthropic implementations (ADR 0033), channels for streaming with producer-owned lifecycle and terminal-event errors (ADR 0032), `context.Context` first-arg on every public I/O boundary, `sync.RWMutex` on every registry, goroutines + `sync.WaitGroup` for concurrent hook dispatch, `gopkg.in/yaml.v3` for event parsing.

Shared concepts (the agent loop, hook lifecycle, middleware pipeline, event system) are consistent across languages, but the expression of those concepts respects each language's idioms and ecosystem.

## 4. Minimal Core, Extensible Surface

`BaseAgent` is deliberately thin. It contains only the agent loop: call the LLM, handle the response, manage turns. Everything else -- tool registration, hook dispatch, middleware pipelines, event emission -- is added through mixins.

Extension points allow customization without modifying the core. `_build_system_prompt` controls what the LLM sees. `_handle_response` intercepts model output. `_on_run_start` and `_on_run_end` bracket the agent's execution. These are override points, not configuration options -- subclass and replace the behavior you need.

## 5. Convention-Based Streaming (Last-Field)

Streaming events follow a structural convention: the streaming field must be the last field in the YAML event block. This allows the parser to detect which field is being streamed, fire the event immediately with initial data from the preceding fields, and then pipe subsequent lines directly into an async iterator.

No special syntax, markers, or schema annotations are needed. Field ordering alone signals intent. This keeps the event definition format clean and makes streaming a property of how you structure your event rather than a separate system to configure.

## 6. Filesystem as Interface

Instead of building a tool for every query pattern, mount context as files and let the model explore with standard Unix commands. This principle — inspired by Vercel's just-bash — leverages the fact that LLMs already understand `grep`, `cat`, `find`, and `jq` deeply.

The virtual shell provides a single `exec` tool backed by an in-memory filesystem. The model decides how to navigate and extract information using shell commands it already knows. This approach produces better results than many specialized tools because it gives the model a familiar, composable interface rather than a fixed set of query patterns.

The virtual filesystem and shell interpreter are pure emulation — no real shell or filesystem is ever accessed. Every command is a function in the host language operating on in-memory data structures. This provides the full power of shell-based exploration without any security risk.

When the built-in commands aren't enough, `registerCommand()` lets you add domain-specific operations (deploy, validate, query) that compose naturally with pipes, redirects, and control flow — keeping the LLM's tool surface to a single `exec` entry point rather than proliferating specialized tools.

## 7. Cycle-Safe Event Propagation

The `MessageBus` uses a depth counter to prevent infinite recursion when event handlers publish new events. Each nested publish increments the counter; when it exceeds the configured maximum (default: 10), the event is silently dropped and a warning is logged.

This makes it safe to build reactive systems where handlers respond to events by emitting further events, without risking unbounded recursion. The depth limit is configurable for cases where deeper chains are legitimate, but the default is deliberately conservative.
