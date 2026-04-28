# ADR 0033: LLM Provider Abstraction Strategy

Date: 2026-04-28

## Status

Accepted

## Context

Every harness agent needs to call an LLM. The four implementations diverge
meaningfully in *how* they reach one — and there has not been an ADR
documenting the per-language choices, the asymmetry that results, or the
deliberate boundary between "event-stream parsing" and "HTTP-level streaming".

The four current implementations:

- **Python** uses [`litellm`](https://docs.litellm.ai/), a third-party SDK
  that provides a single `acompletion()` call routing to ~100+ providers via
  model-prefix strings (`anthropic/...`, `openai/...`, `bedrock/...`).
- **TypeScript** uses the [`openai`](https://github.com/openai/openai-node)
  SDK directly. The SDK also supports the `baseURL` option, which gives
  OpenAI-compatible endpoints (Anthropic's compatibility shim, OpenRouter,
  litellm-proxy) for free.
- **PHP** uses the [`openai-php/client`](https://github.com/openai-php/client)
  SDK (the de-facto OpenAI PHP client). Same OpenAI-compatible-via-`baseUrl`
  story as TypeScript.
- **Go** defines its own `llm.Client` interface with `Stream` and `Complete`
  methods, and ships two in-tree adapters (`src/go/llm/openai`,
  `src/go/llm/anthropic`) plus a `WithRetry` decorator. The Anthropic adapter
  translates between the harness's OpenAI-shaped tool definitions and
  Anthropic's native tool-use blocks transparently.

This is real asymmetry. Two questions need answers:

1. *Why* did each language take its approach, and why is the asymmetry
   acceptable?
2. *Where is the boundary* between "parsing events out of the LLM's response
   stream" (covered by ADR 0010 for PHP) and "consuming an HTTP-level SSE
   stream from the provider" (a separate concern that lives in the LLM
   client)?

## Decision

### Per-language LLM-client implementation

Each language uses the most idiomatic option available in its ecosystem,
behind a thin pluggable interface where one is needed:

- **Python — litellm.** When this work began, litellm was already the
  ecosystem-standard multi-provider abstraction in Python. Re-implementing
  provider routing per-language would have meant re-doing what litellm
  already does well. Python therefore has no in-tree "provider adapter"
  concept — litellm *is* the abstraction.
- **TypeScript — `LlmClient` interface + native adapters.** A minimal
  interface in `src/typescript/llm/client.ts` (one method: `callLlm`)
  with two in-tree implementations: `OpenAIClient` (backed by the
  `openai` SDK) and `AnthropicClient` (backed by `@anthropic-ai/sdk`).
  The Anthropic adapter performs the OpenAI ↔ Messages-API translation
  transparently. Pluggable: users can implement `LlmClient` directly to
  add more providers without modifying BaseAgent.
- **PHP — `ClientInterface` + native adapters.** Same shape as
  TypeScript. Interface in `src/php/Llm/ClientInterface.php`, with
  in-tree `OpenAIClient` (backed by `openai-php/client`) and
  `AnthropicClient` (backed by `anthropic-ai/sdk`, the official
  first-party PHP SDK).
- **Go — typed `llm.Client` interface + native adapters.** The
  prototype this approach was lifted from. Interface in
  `src/go/llm/client.go` (`Stream` + `Complete`), with in-tree adapters
  in `src/go/llm/{openai,anthropic}/`.

### Provider selection in TS / PHP

Selection is explicit (no auto-routing by model prefix). The selection knob
is a `provider:` option (default `"openai"`) — for full control, callers
inject a pre-built `LlmClient` / `ClientInterface` directly. Existing
agents that supply only `model` and `apiKey` continue to default to
OpenAI without code changes; backward compatibility was a hard
requirement of the introduction.

### Anthropic translation responsibilities

Each Anthropic adapter (TS, PHP, Go) performs the same five translations
between the harness's OpenAI-shaped wire format and Anthropic's native
Messages API:

1. **System message extraction** — OpenAI puts `role:"system"` in the
   messages array; Anthropic uses a top-level `system` field. Multiple
   system messages concatenate.
2. **Role mapping** — Anthropic only knows `user` / `assistant`. The
   `role:"tool"` message becomes a `ToolResultBlock` inside a user-role
   turn; adjacent tool results coalesce.
3. **Tool definition translation** — strip the
   `{type:"function", function:{...}}` OpenAI wrapper; emit Anthropic's
   flatter `{name, description, input_schema}` shape.
4. **Tool-call response translation** — `ToolUseBlock` content blocks
   become OpenAI-shaped `tool_calls`; concatenated text blocks become
   `content`.
5. **Streaming event accumulation** — map Anthropic's SSE events
   (`content_block_start` for tool_use, `content_block_delta` with
   text_delta or input_json_delta) to the unified
   `{role, content, tool_calls}` shape.

The translation logic is now duplicated three times (once per language).
This is the cost of doing pluggable native adapters per-language; it was
weighed against the alternative (single shared transport) and rejected
because each language's idiomatic SDK shape differs enough that a shared
transport would be a lowest-common-denominator surface.

### Test injection

Three of the four SDKs expose first-class testing helpers:

- Python: `litellm.completion` is patchable; tests typically use
  `unittest.mock.patch` or the `respx` HTTP-mock library.
- TypeScript: the `openai` SDK accepts a custom `fetch`; tests typically use
  `vi.spyOn` or pass a fake `fetch`.
- PHP: `OpenAI\Testing\ClientFake` is a drop-in implementation of
  `OpenAI\Contracts\ClientContract` that takes a queue of pre-built
  `CreateResponse` / `StreamResponse` objects. Injected via
  `BaseAgent`'s `client:` constructor parameter.
- Go: tests construct a fake `llm.Client` directly (the interface has only two
  methods, both trivial to mock).

The harness adopts the SDK-native testing approach in each language rather
than imposing its own mock layer.

### Boundary: event-stream parsing vs HTTP SSE consumption

These are distinct concerns and have distinct ADRs:

- **HTTP-level SSE consumption** is *this* ADR (0033). The LLM provider sends
  Server-Sent Events; each language's LLM client reads them, decodes
  individual chunks, and accumulates them into a complete assistant message.
  This happens *inside* the LLM call, before the agent loop sees the message.
- **Inline event-stream parsing** is ADR 0010 (PHP) and the broader event
  system (ADRs 0002–0006). It operates on the *content* of the assistant
  message after the LLM call returns: parsing inline YAML event blocks out of
  the model's text output, dispatching them to subscribers, optionally
  streaming individual fields. This happens *after* the LLM call.

Both surfaces deal with "streaming" but at different layers of the stack. ADR
0010 was previously the only ADR mentioning streaming for PHP and was
sometimes read as covering the LLM call too — it does not. The PHP harness's
LLM-call streaming is now (post-2026-04 migration to openai-php) a real SSE
consumer that iterates `StreamResponse` and accumulates content + tool-call
deltas; the Generator-based event parsing in ADR 0010 sits on top of that.

## Consequences

### Positive

- All four implementations now reach Anthropic's native Messages API. The
  asymmetry that existed at the time of the original ADR (only Python and
  Go) is closed.
- TypeScript and PHP both have a small `LlmClient` / `ClientInterface`
  interface that mirrors Go's design. New providers (Bedrock, Cohere,
  on-prem fine-tunes) can be added by implementing the interface without
  touching BaseAgent.
- Each language uses the most idiomatic LLM client available in its
  ecosystem; users can lean on whichever testing helpers and provider
  documentation they already know (litellm patches for Python, custom
  fetch for TS, ClientFake for PHP, fake transports for Go).
- The boundary between LLM-call streaming and inline event-stream parsing
  is explicit (this ADR vs ADR 0010), which removes the ambiguity that
  made earlier doc references to "streaming in PHP" confusing.

### Negative

- The Anthropic translation logic is now duplicated three times (Go, TS,
  PHP). Drift is a real risk; mitigated by parity tests in each language
  that exercise the same fixture inputs (system + tool call + tool
  result + adjacent tool result) and assert the same outgoing Anthropic
  shape.
- New providers added to Python (via litellm) appear automatically; for
  the other three languages, supporting a new provider requires a new
  in-tree adapter or an OpenAI-compatible proxy.
- The Anthropic SDKs in TS and PHP are first-party but young
  (`@anthropic-ai/sdk` 0.91.x and `anthropic-ai/sdk` 0.17.x at time of
  writing). API surface may shift in minor releases; the adapter is the
  surface that absorbs the change.
- Tests can not be shared cross-language. Each implementation has its
  own mocking approach, and verifying behavioural parity requires
  running the full per-language test suite rather than a single shared
  one.

## Revisions

- **2026-04-28** — Added native Anthropic adapters to TypeScript (using
  `@anthropic-ai/sdk`) and PHP (using `anthropic-ai/sdk`). Closed the
  asymmetry that previously listed only Python and Go as having native
  Anthropic support. Introduced the `LlmClient` (TS) /
  `ClientInterface` (PHP) interface to keep the addition pluggable
  without re-wiring BaseAgent. Backward compatible: existing
  OpenAI-targeting agents work unchanged.

## Alternatives considered

- **A single typed interface in every language (port the Go shape to
  Python/TS/PHP).** Rejected: would mean re-implementing litellm in Python and
  building from-scratch SDKs in TS and PHP that compete with established
  community libraries. The maintenance cost and onboarding friction
  outweighed any cross-language symmetry benefit.
- **A unified HTTP layer in every language (port PHP's old hand-rolled Guzzle
  approach to TS and Python).** Rejected for the inverse reason: this would
  ignore mature SDKs and force the harness to handle request construction,
  error mapping, response parsing, and streaming protocol details in four
  places.
- **Use litellm-proxy as the universal endpoint everywhere** (point all four
  languages at a litellm-proxy instance and only ever speak OpenAI-compat).
  Considered but rejected as the *default* — it adds an operational
  dependency. Documented as a recommended pattern when an organisation needs
  cross-language provider parity without writing adapters.

## See also

- [LLM Providers guide](../guides/llm-providers.md) — the user-facing matrix
  and per-language configuration reference.
- [ADR 0007](0007-language-idiomatic-implementations.md) — the broader
  language-idiomatic decision this ADR specialises.
- [ADR 0010](0010-php-generator-streaming.md) — Generator-based streaming for
  the PHP **inline event** parser; distinct from the HTTP SSE consumption
  documented here.
- [ADR 0031](0031-go-struct-embedding-composition.md) — how the Go agent
  embeds `llm.Client` alongside the other subsystems.
