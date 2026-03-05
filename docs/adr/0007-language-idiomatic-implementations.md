# 7. Language-Idiomatic Implementations

Date: 2026-03-05

## Status

Accepted

## Context

The framework is implemented in Python, TypeScript, and PHP. When porting a
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
conceptual architecture (agents, hooks, middleware, tools, event streaming) is
shared across all three languages, but the concrete implementations differ:

- **Python** -- async/await, litellm for LLM access, multiple inheritance for
  mixin composition.
- **TypeScript** -- OpenAI SDK for LLM access, function-based mixins for
  composition.
- **PHP** -- Guzzle for HTTP, native traits for composition, synchronous
  execution model.

## Consequences

**Positive**

- Code feels natural to developers of each language.
- Each implementation leverages the strengths of its runtime and ecosystem.
- Easier to maintain by language specialists who do not need to understand
  cross-language constraints.

**Negative**

- Differences between implementations require separate documentation and
  testing strategies for each language.
- Behavioral parity must be verified through integration tests rather than
  structural comparison.
