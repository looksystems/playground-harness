# LLM Providers

Every harness agent needs an LLM. The four implementations diverge meaningfully in *how* they reach one — the choice was deliberate (each language picked the most idiomatic option available at the time) but the result is real asymmetry that matters when you're picking which language to build in. This guide describes what each implementation supports today, how to configure it, and the cross-language differences worth knowing.

The agent loop itself (turns, tool dispatch, message history) is identical across languages — only the LLM call site differs. Hooks (`TOOL_CALL` / `TOOL_RESULT`), middleware, events, and skills are completely provider-agnostic.

## Provider matrix

| Language | Implementation | Native providers | OpenAI-compatible endpoints | Streaming |
|----------|---------------|------------------|------------------------------|-----------|
| **Python** | `litellm` SDK | ~100+ providers via prefix routing (Anthropic, OpenAI, Bedrock, Azure, Gemini, Mistral, Ollama, Together, Groq, …) | Yes (any OpenAI-compatible URL via `api_base` kwarg) | Yes (litellm async iterator) |
| **TypeScript** | `openai` SDK | OpenAI native | Yes (any OpenAI-compatible URL via the SDK's `baseURL` option) | Yes (SSE via SDK) |
| **PHP** | Hand-rolled HTTP via Guzzle | OpenAI Chat Completions wire format | Yes (configurable `baseUrl`) | Limited — `stream: true` is sent but the response is parsed as JSON; works against non-streaming endpoints |
| **Go** | Native `llm.Client` interface | Anthropic Messages API + OpenAI Chat Completions, in `src/go/llm/{anthropic,openai}/` subpackages | Use the OpenAI adapter with `WithBaseURL(...)` | Yes (channel-based) |

**Key asymmetry:** only Python and Go can talk to Anthropic's native Messages API. TypeScript and PHP can reach Anthropic only through an OpenAI-compatible proxy (Anthropic's own [OpenAI-compatible endpoint](https://docs.anthropic.com/en/api/openai-sdk), litellm-proxy, OpenRouter, etc.). If you want first-class Anthropic support without a proxy, use the Python or Go harness.

## Model identifiers

Model strings are passed verbatim to the underlying SDK and use whichever convention that SDK expects:

| Language | Model string format | Examples |
|----------|--------------------|----------|
| Python (litellm) | `provider/model-id` (provider prefix routes the call) | `anthropic/claude-sonnet-4-6`, `openai/gpt-4o`, `bedrock/anthropic.claude-3-5-sonnet-v2`, `gemini/gemini-2.5-pro`, `ollama/llama3` |
| TypeScript (OpenAI SDK) | OpenAI model ID (or whatever the configured base URL accepts) | `gpt-4o`, `gpt-4o-mini`, `claude-sonnet-4-6` (when `baseURL` points at an Anthropic-compatible endpoint) |
| PHP | OpenAI model ID (or whatever the configured baseUrl accepts) | `gpt-4o`, `claude-sonnet-4-6` (when `baseUrl` points at an Anthropic-compatible endpoint) |
| Go | Whatever the chosen provider adapter expects | `claude-sonnet-4-6` (Anthropic adapter), `gpt-4o` (OpenAI adapter) — adapter is selected at agent construction, not via the model string |

## Configuring the provider

### Python

Configuration flows through litellm. API keys are read from environment variables by convention (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AWS_REGION_NAME`, etc. — see [litellm provider docs](https://docs.litellm.ai/docs/providers)). Pass any other litellm option as a constructor kwarg; they reach `litellm.acompletion()` unchanged via `**self.litellm_kwargs`.

```python
from src.python.standard_agent import StandardAgent

# API key from env var
agent = StandardAgent.build("anthropic/claude-sonnet-4-6").build()

# Explicit options as litellm kwargs
agent = StandardAgent("openai/gpt-4o",
                     api_key="sk-...",
                     api_base="https://my-proxy.example.com/v1",
                     temperature=0.2,
                     max_tokens=2048)
```

The `model`, `messages`, `tools`, `stream` keys are constructed by the harness and override anything passed in kwargs.

### TypeScript

Configuration flows through the OpenAI SDK. The `apiKey` option is exposed directly; other SDK options pass through via the agent constructor's rest params. To target a non-OpenAI endpoint, supply `baseURL` (an OpenAI SDK option) as a constructor option:

```typescript
import { StandardAgent } from "./standard-agent.js";

// OpenAI default
const agent = new StandardAgent({
  model: "gpt-4o",
  apiKey: process.env.OPENAI_API_KEY,
});

// OpenAI-compatible endpoint (e.g. Anthropic, OpenRouter, litellm-proxy)
const agent = new StandardAgent({
  model: "claude-sonnet-4-6",
  apiKey: process.env.ANTHROPIC_API_KEY,
  baseURL: "https://api.anthropic.com/v1/",
  // any other OpenAI SDK options pass through (temperature, top_p, ...)
  temperature: 0.2,
});
```

### PHP

Configuration is on the `BaseAgent` constructor (or via `AgentBuilder`'s fluent setters). `baseUrl` defaults to `https://api.openai.com/v1`; `apiKey` is sent as a `Bearer` token. Extra completion parameters go through `completionParams`.

```php
use AgentHarness\AgentBuilder;

// OpenAI default (apiKey from getenv if you wire it yourself)
$agent = (new AgentBuilder('gpt-4o'))
    ->apiKey(getenv('OPENAI_API_KEY'))
    ->build();

// OpenAI-compatible endpoint (e.g. Anthropic, OpenRouter)
$agent = (new AgentBuilder('claude-sonnet-4-6'))
    ->baseUrl('https://api.anthropic.com/v1')
    ->apiKey(getenv('ANTHROPIC_API_KEY'))
    ->completionParams(['temperature' => 0.2, 'max_tokens' => 2048])
    ->build();
```

The `model`, `messages`, `tools`, `stream` keys in the request body are managed by the harness and merged on top of `completionParams`.

### Go

Provider adapters live in `src/go/llm/anthropic` and `src/go/llm/openai`. Each exposes a `New(opts ...Option) *Client` constructor with `WithAPIKey`, `WithBaseURL`, `WithHTTPClient`, etc. Construct the client, hand it to `agent.NewBuilder(...).Client(c).Build(ctx)`, and you're set.

```go
import (
    anthropicllm "agent-harness/go/llm/anthropic"
    openaillm    "agent-harness/go/llm/openai"
    "agent-harness/go/agent"
)

// Anthropic native
client := anthropicllm.New(
    anthropicllm.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    anthropicllm.WithMaxTokens(4096),
)
a, _ := agent.NewBuilder("claude-sonnet-4-6").Client(client).Build(ctx)

// OpenAI (or any OpenAI-compatible endpoint)
client := openaillm.New(
    openaillm.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    openaillm.WithBaseURL("https://api.openai.com/v1/"),
)
a, _ := agent.NewBuilder("gpt-4o").Client(client).Build(ctx)
```

If neither `WithAPIKey` nor the SDK's default env-var lookup finds a key, the SDK errors at first request. The Anthropic adapter requires `max_tokens`; if the request and the option both leave it zero, it falls back to the package default of 4096.

## Streaming

Streaming is enabled by default in Python, TypeScript, and Go. PHP currently sets the `stream: true` body parameter but parses the response as JSON, so it works correctly only against endpoints that ignore the flag (or against streaming-disabled providers); set `stream: false` at construction time to be explicit.

| Language | API surface | Default |
|----------|-------------|---------|
| Python | `async for chunk in stream` (litellm async iterator) | On |
| TypeScript | `for await (const chunk of stream)` (OpenAI SDK) | On |
| PHP | (no streaming consumer wired) | On flag, but parsed non-streamed |
| Go | `<-chan llm.Chunk`, terminal `Chunk{Done: true, Err: err}` | Decided per provider, both adapters support it |

Disable per agent:

```python
StandardAgent("openai/gpt-4o", stream=False)
```

```typescript
new StandardAgent({ model: "gpt-4o", stream: false })
```

```php
new StandardAgent('gpt-4o', stream: false)
```

```go
agent.NewBuilder("gpt-4o").Streaming(false).Client(c).Build(ctx)
```

## Tool calling

All four implementations send tools in OpenAI's `{"type": "function", "function": {...}}` shape and expect tool calls back as `{"id", "name", "arguments"}` objects.

The Go Anthropic adapter translates internally — it accepts the same `tools.Def` you'd give the OpenAI adapter, builds Anthropic's native `ToolUseBlock` / `ToolResultBlock` content blocks for the wire, and surfaces tool calls back through the same `llm.Chunk{ToolCallID, ToolName, ToolArgs}` shape. From the agent's perspective, swapping providers does not change tool-call handling code.

Python (litellm) does the same translation transparently; pass tools in OpenAI shape and litellm handles the rest.

For TS and PHP targeting Anthropic via an OpenAI-compatible proxy, tool-calling support depends on the proxy. Anthropic's own OpenAI-compatible endpoint supports it; OpenRouter does; rolling your own proxy may not.

## Retries

All four implementations retry transient failures with exponential back-off (`min(2^attempt, 10)` seconds), defaulting to two retries (three total attempts).

| Language | Configuration |
|----------|---------------|
| Python | `max_retries=N` constructor arg or builder method |
| TypeScript | `maxRetries: N` option |
| PHP | `maxRetries: N` constructor arg |
| Go | `llm.MaxRetries(N)` option on the retry decorator: `llm.WithRetry(client, llm.MaxRetries(3))` |

Go's retry is a separate decorator wrapping any `llm.Client`, so you can stack it with custom logging or metric clients without touching the provider adapter.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| Implementation | litellm SDK | openai SDK | Guzzle HTTP | `llm.Client` interface + adapters |
| Native Anthropic | ✓ (litellm) | ✗ (use OpenAI-compatible proxy) | ✗ (use OpenAI-compatible proxy) | ✓ (`llm/anthropic`) |
| Native OpenAI | ✓ (litellm) | ✓ | ✓ | ✓ (`llm/openai`) |
| Other native providers | ✓ (~100 via litellm) | ✗ | ✗ | Implement the `llm.Client` interface |
| Streaming | Async iterator | Async iterator | Limited (see above) | Channel |
| API key source | Env var (litellm convention) | `apiKey` option | `apiKey` constructor arg | `WithAPIKey` option (or env var fallback) |
| Custom base URL | `api_base` kwarg | OpenAI SDK `baseURL` | `baseUrl` constructor arg | `WithBaseURL` option |
| Extra request params | `**litellm_kwargs` | Constructor rest params | `completionParams` | `Request.Extra` map |
| Pluggable client | No (litellm is the abstraction) | No (openai SDK is the abstraction) | No (HTTP is the abstraction) | Yes — implement `llm.Client` |

## Known limitations

- **PHP streaming is not actually streamed.** The `stream: true` body parameter is sent but the response body is read in one shot and JSON-parsed. To use streaming you would need to add an SSE consumer; until then, prefer `stream: false` for non-streaming-tolerant endpoints.
- **TypeScript and PHP have no native Anthropic adapter.** The OpenAI-compatible path works for most use cases but loses access to features the Messages API exposes that the OpenAI surface does not (e.g. extended thinking, prompt caching breakpoints).
- **Tool-call shape on the wire is OpenAI-flavoured.** All four implementations build OpenAI-format `tools` payloads. Providers that don't speak that shape need a translation layer (the Go Anthropic adapter does it for you; litellm does it transparently for Python; TS and PHP rely on the proxy).
- **No retry for in-stream errors** in Python / TS / PHP. The retry loop covers the initial connection only; once chunks start flowing, a mid-stream failure surfaces as an exception and is not retried. Go's terminal `Chunk{Done: true, Err: err}` is delivered to the consumer, who can choose to retry; the decorator does not auto-restart streams.

## See also

- [Tools guide](tools.md) — how the tool schemas you register get serialised onto LLM requests
- [Exec Tool guide](exec-tool.md) — the always-present shell tool
- [File Tools guide](file-tools.md) — the typed file toolset
- [Middleware guide](middleware.md) — Pre/Post hooks around every LLM call
- [ADR 0007 — Language-Idiomatic Implementations](../adr/0007-language-idiomatic-implementations.md) — why the four languages diverge by design
- [Python guide: LLM Providers](python.md#llm-providers) · [TypeScript guide: LLM Providers](typescript.md#llm-providers) · [PHP guide: LLM Providers](php.md#llm-providers) · [Go guide: LLM Providers](go.md#llm-providers)
