# 7. Language-Idiomatic Implementations

Date: 2026-03-05 (revised 2026-04-28 to add Go and the LLM-provider consequences)

## Status

Accepted

## Context

The framework is implemented in Python, TypeScript, PHP, and Go. When porting a
design across multiple languages, there are two broad strategies:

- **Mechanical port** -- replicate the same structure, naming, and patterns in
  every language regardless of how natural they feel.
- **Idiomatic per-language** -- share the conceptual architecture but let each
  implementation follow the conventions and strengths of its language.

A mechanical port is easier to keep in sync but often produces code that feels
foreign to native developers of a given language. An idiomatic approach requires
more design effort per language but results in code that is easier to read,
maintain, and extend by specialists.

## Decision

Each language implementation follows its own idioms and conventions. The
conceptual architecture (agents, hooks, middleware, tools, event streaming,
LLM-call loop) is shared across all four languages, but the concrete
implementations differ along several dimensions:

| Dimension | Python | TypeScript | PHP | Go |
|-----------|--------|------------|-----|----|
| **Async** | `async`/`await` throughout (`asyncio`) | `async`/`await` (Promises) | Synchronous (no async runtime) | Goroutines + channels; `context.Context` for cancellation |
| **Composition** | Multiple inheritance over mixin classes | Function-based mixins (class-extending higher-order functions) | Native `trait`s | Anonymous struct embedding (see ADR 0031) |
| **LLM client** | [`litellm`](https://docs.litellm.ai/) — universal SDK; provider routed by model-prefix string (`anthropic/...`, `openai/...`, `bedrock/...`, …) | [`openai`](https://github.com/openai/openai-node) SDK; OpenAI native, OpenAI-compatible endpoints via `baseURL` | [`openai-php/client`](https://github.com/openai-php/client) SDK; OpenAI native, OpenAI-compatible endpoints via `baseUrl` | Typed `llm.Client` interface with native adapters in `src/go/llm/{anthropic,openai}/` |
| **Streaming consumer** | `async for chunk in stream` | `for await (const chunk of stream)` | `foreach ($stream as $chunk)` over `StreamResponse` | `<-chan llm.Chunk` (producer-owned, terminal-error chunk) |
| **Inline-event streaming** | `asyncio.Queue` → `AsyncIterator` | `createChannel()` → `AsyncIterable` | `Generator` (pull-based; see ADR 0010) | `<-chan` (same channel as LLM streaming) |

## LLM-provider consequences (revised 2026-04-28)

The LLM-client choice has the largest reach of any decision in this ADR. The
four implementations diverge meaningfully in what they can talk to out of the
box, and the asymmetry is a deliberate consequence of choosing the most
idiomatic option per language at the time of writing:

- **Python** reaches ~100+ providers natively because litellm is the
  multi-provider abstraction. There is no in-tree "provider adapter" concept —
  litellm *is* the abstraction layer.
- **TypeScript** and **PHP** are OpenAI-native by default and reach other
  providers (Anthropic, OpenRouter, litellm-proxy, …) through OpenAI-compatible
  endpoints by setting `baseURL` / `baseUrl`. There is no native Anthropic
  Messages API adapter in either language; using one means losing access to
  Messages-specific features (extended thinking, prompt-caching breakpoints,
  native tool-use blocks).
- **Go** has a typed `llm.Client` interface (`Stream` + `Complete`) with
  native adapters for both OpenAI Chat Completions *and* Anthropic Messages.
  The Anthropic adapter translates the OpenAI-shaped tool definitions the
  harness emits into Anthropic's native tool-use blocks transparently, so
  swapping providers requires no agent-level changes.

The cross-language guide [`docs/guides/llm-providers.md`](../guides/llm-providers.md)
documents this matrix in detail and notes which features are available where.
ADR 0033 captures the underlying provider-abstraction strategy.

## Consequences

**Positive**

- Code feels natural to developers of each language.
- Each implementation leverages the strengths of its runtime and ecosystem.
- Easier to maintain by language specialists who do not need to understand
  cross-language constraints.
- The provider-abstraction asymmetry is documented honestly rather than papered
  over with a lowest-common-denominator surface.

**Negative**

- Differences between implementations require separate documentation and
  testing strategies for each language.
- Behavioural parity must be verified through integration tests rather than
  structural comparison.
- Native Anthropic support is only available in Python and Go today;
  TypeScript and PHP need an OpenAI-compatible Anthropic proxy. Closing this
  gap would require writing native Anthropic adapters in both languages — a
  larger undertaking deliberately deferred.
