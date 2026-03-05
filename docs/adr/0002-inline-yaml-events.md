# 2. Inline YAML Events

**Status:** Accepted

## Context

We needed a way for LLMs to emit structured data (events) alongside natural
language output. Several options were considered:

- A separate API channel for structured data.
- JSON blocks embedded in text output.
- A custom DSL for event encoding.
- YAML blocks embedded in text output.

The chosen approach needed to work across LLM providers without requiring
special API support, and be easy for both LLMs and humans to read and produce.

## Decision

Use YAML blocks delimited by `---event` / `---` inline in the LLM's text
stream. The `type` field within the YAML block identifies the event. The
EventStreamParser extracts these blocks from the raw text and yields clean
text with the event markup removed.

## Consequences

**Positive:**

- Works with any LLM that produces text -- no special API support needed.
- Human-readable format that is easy to inspect and debug.
- LLMs produce YAML reliably; it appears frequently in training data.
- No dependency on provider-specific structured output features.

**Negative:**

- Requires a parser in the response path to detect and extract event blocks.
- The LLM must be prompted to emit the correct delimited format.
- Malformed YAML from the LLM can cause parse failures that need handling.
