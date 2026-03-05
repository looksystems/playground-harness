# 3. Buffered-by-Default Streaming

**Status:** Accepted

## Context

Events can be small (metadata, status updates) or large (generated code, long
text). Some consumers want the full event payload before acting; others want
to process content as it streams in, for example to display generated code
incrementally.

A single strategy does not fit both cases well.

## Decision

Events are buffered by default -- the entire YAML block is collected and
parsed before the event is fired. Streaming is opt-in per event type via
`StreamConfig(mode="streaming", stream_fields=[...])`.

Only the fields declared in `stream_fields` are exposed as async iterators.
All other fields in the event are available immediately when the event fires,
before the streaming field has finished arriving.

## Consequences

**Positive:**

- Simple default behavior -- most events just work without extra configuration.
- Streaming is only introduced where it is actually needed.
- Structured fields (metadata, type, etc.) are always available upfront, even
  for streaming events.

**Negative:**

- Streaming fields must follow the last-field convention (see ADR-0004).
- Consumers of streaming events need to handle async iteration, adding
  complexity compared to the buffered path.
