# LLM Providers

Every harness agent needs an LLM. The four implementations diverge meaningfully in *how* they reach one — the choice was deliberate (each language picked the most idiomatic option available at the time) but the result is real asymmetry that matters when you're picking which language to build in. This guide describes what each implementation supports today, how to configure it, and the cross-language differences worth knowing.

The agent loop itself (turns, tool dispatch, message history) is identical across languages — only the LLM call site differs. Hooks (`TOOL_CALL` / `TOOL_RESULT`), middleware, events, and skills are completely provider-agnostic.

## Provider matrix

| Language | Implementation | Native providers | OpenAI-compatible endpoints | Streaming |
|----------|---------------|------------------|------------------------------|-----------|
| **Python** | `litellm` SDK | ~100+ providers via prefix routing (Anthropic, OpenAI, Bedrock, Azure, Gemini, Mistral, Ollama, Together, Groq, …) | Yes (any OpenAI-compatible URL via `api_base` kwarg) | Yes (litellm async iterator) |
| **TypeScript** | Pluggable `LlmClient` interface; ships native `OpenAIClient` (`openai` SDK) and `AnthropicClient` (`@anthropic-ai/sdk`) | OpenAI Chat Completions + Anthropic Messages API | Yes (configurable `baseURL` on either adapter) | Yes (SSE via SDK; consumer accumulates content + tool-call deltas) |
| **PHP** | Pluggable `ClientInterface`; ships native `OpenAIClient` ([`openai-php/client`](https://github.com/openai-php/client)) and `AnthropicClient` ([`anthropic-ai/sdk`](https://github.com/anthropics/anthropic-sdk-php)) | OpenAI Chat Completions + Anthropic Messages API | Yes (configurable `baseUrl` on either adapter) | Yes (SDK iterates SSE chunks; consumer accumulates content + tool-call deltas) |
| **Go** | Pluggable `llm.Client` interface | Anthropic Messages API + OpenAI Chat Completions, in `src/go/llm/{anthropic,openai}/` subpackages | Use the OpenAI adapter with `WithBaseURL(...)` | Yes (channel-based) |

All four implementations now reach Anthropic's native Messages API (extended thinking, prompt-caching breakpoints, native tool-use blocks). TypeScript and PHP gained first-class adapters in 2026-04 (see [ADR 0033 revision history](../adr/0033-llm-provider-abstraction.md)); previously they could only reach Anthropic through OpenAI-compatible proxies.

## Model identifiers

Model strings are passed verbatim to the underlying SDK and use whichever convention that SDK expects:

| Language | Model string format | Examples |
|----------|--------------------|----------|
| Python (litellm) | `provider/model-id` (provider prefix routes the call) | `anthropic/claude-sonnet-4-6`, `openai/gpt-4o`, `bedrock/anthropic.claude-3-5-sonnet-v2`, `gemini/gemini-2.5-pro`, `ollama/llama3` |
| TypeScript | Whatever the chosen `LlmClient` expects | `gpt-4o` (OpenAIClient default), `claude-sonnet-4-6` (AnthropicClient via `provider: "anthropic"`) |
| PHP | Whatever the chosen `ClientInterface` expects | `gpt-4o` (OpenAIClient default), `claude-sonnet-4-6` (AnthropicClient via `->provider('anthropic')`) |
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

The TS harness uses a pluggable `LlmClient` interface. Two implementations ship in-tree: `OpenAIClient` (backed by `openai`) and `AnthropicClient` (backed by `@anthropic-ai/sdk`). Pick one with `provider:` (default `"openai"`), or inject a pre-built client via `client:`:

```typescript
import { StandardAgent } from "./standard-agent.js";

// OpenAI default — apiKey from env if not specified
const agent = new StandardAgent({
  model: "gpt-4o",
  apiKey: process.env.OPENAI_API_KEY,
});

// Anthropic native (uses @anthropic-ai/sdk) — apiKey from env if not specified
const agent = new StandardAgent({
  model: "claude-sonnet-4-6",
  provider: "anthropic",
  apiKey: process.env.ANTHROPIC_API_KEY,
  // pass-through extras (temperature, max_tokens, …) flow through extraOptions
  temperature: 0.2,
});

// Inject a pre-built client (e.g. with a custom fetch for tests)
import { AnthropicClient } from "./llm/anthropic.js";
const agent = new StandardAgent({
  model: "claude-sonnet-4-6",
  client: new AnthropicClient({ apiKey: "...", fetch: myFetch }),
});

// OpenAI-compatible proxy (Anthropic shim, OpenRouter, litellm-proxy) still works via baseURL
const agent = new StandardAgent({
  model: "claude-sonnet-4-6",
  baseURL: "https://api.anthropic.com/v1/",
  apiKey: process.env.ANTHROPIC_API_KEY,
});
```

### PHP

The PHP harness uses a pluggable `ClientInterface` (in `AgentHarness\Llm\`). Two implementations ship in-tree: `OpenAIClient` (backed by [`openai-php/client`](https://github.com/openai-php/client)) and `AnthropicClient` (backed by [`anthropic-ai/sdk`](https://github.com/anthropics/anthropic-sdk-php)). Pick one with `->provider()` (default `'openai'`), or inject a pre-built client via `->llmClient()`:

```php
use AgentHarness\AgentBuilder;
use AgentHarness\Llm\AnthropicClient;

// OpenAI default — apiKey from env if not specified
$agent = (new AgentBuilder('gpt-4o'))->create();

// Anthropic native (uses anthropic-ai/sdk)
$agent = (new AgentBuilder('claude-sonnet-4-6'))
    ->provider('anthropic')
    ->apiKey(getenv('ANTHROPIC_API_KEY'))
    ->completionParams(['temperature' => 0.2, 'maxTokens' => 2048])
    ->create();

// Inject a pre-built client (e.g. with a fake SDK transport for tests)
$agent = (new AgentBuilder('claude-sonnet-4-6'))
    ->llmClient(new AnthropicClient(apiKey: 'sk-ant-...'))
    ->create();

// OpenAI-compatible proxy (Anthropic shim, OpenRouter, litellm-proxy) still works via baseUrl
$agent = (new AgentBuilder('claude-sonnet-4-6'))
    ->baseUrl('https://api.anthropic.com/v1')
    ->apiKey(getenv('ANTHROPIC_API_KEY'))
    ->create();
```

The `model`, `messages`, `tools` keys are managed by the harness and merged on top of `completionParams`. Tests can inject either a `OpenAI\Testing\ClientFake` (via the legacy `client:` constructor parameter, internally wrapped in OpenAIClient) or any `ClientInterface` (via the new `llmClient:` parameter or builder method).

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
| PHP | `foreach ($stream as $chunk)` over `StreamResponse<CreateStreamedResponse>` (openai-php SDK) | On |
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
| Implementation | litellm SDK | `LlmClient` interface + openai/anthropic SDK adapters | `ClientInterface` + openai-php/anthropic-ai SDK adapters | `llm.Client` interface + openai/anthropic adapters |
| Native Anthropic | ✓ (litellm) | ✓ (`AnthropicClient` using `@anthropic-ai/sdk`) | ✓ (`AnthropicClient` using `anthropic-ai/sdk`) | ✓ (`llm/anthropic`) |
| Native OpenAI | ✓ (litellm) | ✓ (`OpenAIClient` using `openai`) | ✓ (`OpenAIClient` using `openai-php/client`) | ✓ (`llm/openai`) |
| Other native providers | ✓ (~100 via litellm) | Implement `LlmClient` | Implement `ClientInterface` | Implement `llm.Client` |
| Streaming | Async iterator | Async iterator (per adapter) | Iterator (`StreamResponse` / SDK SSE stream) | Channel |
| API key source | Env var (litellm convention) | `apiKey` option per adapter (env var fallback) | `apiKey` constructor arg per adapter (env var fallback) | `WithAPIKey` option (env var fallback) |
| Custom base URL | `api_base` kwarg | `baseURL` option per adapter | `baseUrl` constructor arg per adapter | `WithBaseURL` option |
| Extra request params | `**litellm_kwargs` | Constructor rest params → `extraOptions` | `completionParams` constructor arg | `Request.Extra` map |
| Pluggable client | No (litellm is the abstraction) | Yes — implement `LlmClient` or pass `client:` to BaseAgent | Yes — implement `ClientInterface` or pass `llmClient:` to BaseAgent | Yes — implement `llm.Client` |

## Known limitations

- **Tool-call shape on the wire is OpenAI-flavoured.** All four implementations build OpenAI-format `tools` payloads as the harness's lingua franca. Adapters targeting non-OpenAI APIs (Anthropic in TS/PHP/Go, anything in Python via litellm) translate transparently. Providers added via custom `LlmClient`/`ClientInterface`/`llm.Client` implementations must do the same translation.
- **No retry for in-stream errors** in Python / TS / PHP. The retry loop covers the initial connection only; once chunks start flowing, a mid-stream failure surfaces as an exception and is not retried. Go's terminal `Chunk{Done: true, Err: err}` is delivered to the consumer, who can choose to retry; the decorator does not auto-restart streams.

## See also

- [Tools guide](tools.md) — how the tool schemas you register get serialised onto LLM requests
- [Exec Tool guide](exec-tool.md) — the always-present shell tool
- [File Tools guide](file-tools.md) — the typed file toolset
- [Middleware guide](middleware.md) — Pre/Post hooks around every LLM call
- [ADR 0007 — Language-Idiomatic Implementations](../adr/0007-language-idiomatic-implementations.md) — why the four languages diverge by design
- [Python guide: LLM Providers](python.md#llm-providers) · [TypeScript guide: LLM Providers](typescript.md#llm-providers) · [PHP guide: LLM Providers](php.md#llm-providers) · [Go guide: LLM Providers](go.md#llm-providers)
