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

Each language uses the most idiomatic option available in its ecosystem at the
time of writing:

- **Python — litellm.** When this work began, litellm was already the
  ecosystem-standard multi-provider abstraction in Python. Re-implementing
  provider routing per-language would have meant re-doing what litellm already
  does well. Python therefore has no in-tree "provider adapter" concept —
  litellm *is* the abstraction.
- **TypeScript — `openai` SDK.** The official OpenAI Node SDK is the
  ecosystem default for OpenAI-compatible work. There is no equivalent of
  litellm in the TypeScript ecosystem, so multi-provider reach is limited to
  whatever OpenAI-compatible endpoint the user configures via `baseURL`.
- **PHP — `openai-php/client`.** Same logic as TypeScript: openai-php is the
  community-standard OpenAI client in PHP. The harness initially used a
  hand-rolled Guzzle implementation but was migrated to openai-php (2026-04)
  to fix broken streaming and to gain `OpenAI\Testing\ClientFake` for
  in-memory test injection.
- **Go — typed `llm.Client` interface + native adapters.** Go has no
  litellm-equivalent and no openai-php-equivalent that's clearly the
  community standard. The harness defines its own provider contract
  (`Stream(ctx, Request) (<-chan Chunk, error)`,
  `Complete(ctx, Request) (Response, error)`) so that other providers can be
  added by implementing the interface. Native Anthropic and OpenAI adapters
  ship in-tree.

### Asymmetry as a deliberate consequence

Not all four implementations reach the same set of providers, and the harness
does not pretend otherwise:

- Native **Anthropic Messages API** support: only Python (via litellm) and Go
  (via the in-tree adapter).
- Native **OpenAI Chat Completions**: all four.
- **Other providers** (Bedrock, Gemini, Mistral, Together, …): only Python via
  litellm. TypeScript and PHP can reach them only through an OpenAI-compatible
  proxy.

This asymmetry could be closed by writing native Anthropic adapters in
TypeScript and PHP — a real but bounded piece of work. We deliberately defer
it until the asymmetry causes concrete user pain that an OpenAI-compatible
proxy cannot address (e.g. needing extended-thinking blocks, prompt-caching
breakpoints, or native multipart tool-use blocks).

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

- Each language uses the most idiomatic LLM client available in its
  ecosystem; users can lean on whichever testing helpers and provider
  documentation they already know.
- The Go interface gives a clean extension point for new providers without
  imposing the same shape on Python (litellm already does this) or TS/PHP
  (where the OpenAI SDK is the abstraction).
- The asymmetry is documented in
  [`docs/guides/llm-providers.md`](../guides/llm-providers.md) and
  [ADR 0007](0007-language-idiomatic-implementations.md), not hidden.
- The boundary between LLM-call streaming and inline event-stream parsing is
  now explicit, which removes the ambiguity that made earlier doc references
  to "streaming in PHP" confusing.

### Negative

- Native Anthropic support is uneven. TypeScript and PHP must use an
  OpenAI-compatible proxy to reach Anthropic; this loses access to
  Messages-specific features (extended thinking, prompt-caching breakpoints,
  native tool-use blocks). Closing this gap would require writing native
  Anthropic adapters in both languages.
- New providers added to Python (via litellm) appear automatically; for the
  other three languages, supporting a new provider requires either (a) a
  proxy, (b) a new in-tree adapter (Go), or (c) an OpenAI-compatible
  endpoint.
- Tests can not be shared cross-language. Each implementation has its own
  mocking approach, and verifying behavioural parity requires running the
  full per-language test suite rather than a single shared one.
- The Go `llm.Client` interface is the only first-class extension point. If a
  new shared abstraction is ever needed for TS or PHP, it will have to be
  retro-fitted.

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
