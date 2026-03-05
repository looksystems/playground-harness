# 4. Last-Field Convention for Streaming

**Status:** Accepted

## Context

For streaming events, the parser needs to know when structured fields end and
streaming content begins. Several approaches were considered:

- An explicit marker separating structured data from streamed content.
- Schema-based detection using type annotations.
- A field ordering convention within the YAML block.

The approach needed to work without additional syntax while keeping the parser
logic straightforward.

## Decision

Streaming fields must be the last field in the YAML event block. The parser
detects the streaming field (declared in `StreamConfig.stream_fields`), fires
the event immediately with all preceding structured data populated, and pipes
the remaining lines into the async iterator for that field.

## Consequences

**Positive:**

- No special syntax or markers needed beyond standard YAML.
- The parser can reliably detect the transition point from structured data to
  streamed content.
- Structured data is always fully available before the stream starts, so
  handlers can make routing decisions based on metadata.

**Negative:**

- Constrains event schema ordering -- the streaming field cannot appear before
  other fields.
- Only one streaming field per event is supported (a practical limitation,
  though sufficient for current use cases).
- Event authors must be aware of the convention when designing event schemas.
