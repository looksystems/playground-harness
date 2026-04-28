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
| **LLM client** | [`litellm`](https://docs.litellm.ai/) — universal SDK; provider routed by model-prefix string (`anthropic/...`, `openai/...`, `bedrock/...`, …) | Pluggable `LlmClient` interface with in-tree `OpenAIClient` ([`openai`](https://github.com/openai/openai-node)) and `AnthropicClient` ([`@anthropic-ai/sdk`](https://github.com/anthropics/anthropic-sdk-typescript)) | Pluggable `ClientInterface` with in-tree `OpenAIClient` ([`openai-php/client`](https://github.com/openai-php/client)) and `AnthropicClient` ([`anthropic-ai/sdk`](https://github.com/anthropics/anthropic-sdk-php)) | Typed `llm.Client` interface with native adapters in `src/go/llm/{anthropic,openai}/` |
| **Streaming consumer** | `async for chunk in stream` | `for await (const chunk of stream)` | `foreach ($stream as $chunk)` over `StreamResponse` | `<-chan llm.Chunk` (producer-owned, terminal-error chunk) |
| **Inline-event streaming** | `asyncio.Queue` → `AsyncIterator` | `createChannel()` → `AsyncIterable` | `Generator` (pull-based; see ADR 0010) | `<-chan` (same channel as LLM streaming) |

## LLM-provider consequences (revised 2026-04-28)

The LLM-client choice has the largest reach of any decision in this ADR. As
of 2026-04-28, all four implementations reach Anthropic's native Messages
API and OpenAI's Chat Completions API; the per-language differences are
about *how* the provider abstraction is shaped, not *which* providers are
reachable:

- **Python** uses litellm as the multi-provider abstraction layer; there is
  no in-tree "provider adapter" concept and adding a new provider means
  using whichever model-prefix litellm already supports (~100+).
- **TypeScript** and **PHP** each define a small pluggable interface
  (`LlmClient` and `ClientInterface` respectively) with two in-tree
  adapters: an OpenAI one (using the official ecosystem SDK) and an
  Anthropic one (using the official first-party SDK). Each Anthropic
  adapter handles the OpenAI ↔ Messages-API translation transparently
  (system extraction, role mapping, tool-call delta accumulation).
  Adding a third provider means implementing the interface.
- **Go** has the same shape — a typed `llm.Client` interface
  (`Stream` + `Complete`) with native OpenAI and Anthropic adapters.
  This was the prototype the TS / PHP interface designs were lifted
  from.

Provider selection in TS / PHP is explicit (`provider: "openai" |
"anthropic"`, default openai), preserving backward compatibility with
agents that don't specify a provider. Auto-routing by model prefix
(litellm-style) is deliberately not done — see ADR 0033 for the
rationale.

The cross-language guide
[`docs/guides/llm-providers.md`](../guides/llm-providers.md) documents
the matrix in detail. ADR 0033 captures the provider-abstraction
strategy and revision history.

## Consequences

**Positive**

- Code feels natural to developers of each language.
- Each implementation leverages the strengths of its runtime and ecosystem.
- Easier to maintain by language specialists who do not need to understand
  cross-language constraints.
- The provider-abstraction shape is captured per-language rather than papered
  over with a lowest-common-denominator surface.

**Negative**

- Differences between implementations require separate documentation and
  testing strategies for each language.
- Behavioural parity must be verified through integration tests rather than
  structural comparison.
- The Anthropic translation logic now lives in three places (Go, TS, PHP);
  drift is mitigated by parity tests but not eliminated.
