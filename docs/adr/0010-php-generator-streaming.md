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
