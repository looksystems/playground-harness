# 10. PHP Generator Streaming

Date: 2026-03-05

## Status

Accepted

## Context

PHP lacks native async/await. The event streaming system needs to deliver event
content incrementally as it arrives from the LLM provider. Several approaches
were considered:

- **Callbacks** -- flexible but invert control flow and complicate error
  handling.
- **Promises (ReactPHP)** -- powerful but introduce a heavy async runtime
  dependency.
- **Generators** -- PHP's built-in mechanism for lazy, pull-based iteration.

Major PHP libraries in the AI ecosystem already use Generators for streaming:
openai-php/client, Laravel AI SDK, and Prism PHP all follow this pattern.

## Decision

Use PHP Generators for streaming events. The event stream parser yields content
via `Generator`, allowing consumers to iterate over events with a standard
`foreach` loop. This is consistent with the broader PHP ecosystem's approach to
lazy iteration and streaming.

## Consequences

**Positive**

- Idiomatic PHP -- developers immediately understand the consumption pattern.
- No async runtime dependency; works in any PHP environment.
- Pull-based model is simple to consume and reason about.
- Consistent with how major PHP AI libraries handle streaming.

**Negative**

- True concurrent streaming is not possible without an async runtime; content
  is buffered then yielded.
- If future requirements demand concurrent event processing, a more significant
  architectural change would be needed.

## Scope clarification (added 2026-04-28)

This ADR covers the **inline event-stream parser** — turning accumulated LLM
text into a sequence of typed event objects via Generators. It is **not** the
same thing as HTTP-level SSE consumption from the LLM provider, which lives in
the LLM client and is documented separately in [ADR 0033](0033-llm-provider-abstraction.md).

Concretely:

- The PHP `BaseAgent`'s `handleStream()` iterates the openai-php SDK's
  `StreamResponse` (HTTP SSE) and accumulates content + tool-call deltas into
  a complete assistant message — covered by ADR 0033.
- That assistant message's text content may contain inline YAML event blocks,
  which the event parser yields incrementally via a `Generator` — covered by
  this ADR.

Both layers can be active simultaneously. The Generator-based parser sits on
top of the LLM client; it does not replace it.
