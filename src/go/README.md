# Agent Harness — Go

Go port of the Agent Harness. For the full guide (setup, examples, driver
configuration) see [`docs/guides/go.md`](../../docs/guides/go.md). For
architecture decisions see [`docs/adr/`](../../docs/adr/).

## Module

```
module agent-harness/go
```

## Packages

| Package | Description |
|---------|-------------|
| `agent` | Composed entry point: `Agent` struct, `Builder`, and the `Run` loop (port of Python `StandardAgent` + `AgentBuilder`). |
| `hooks` | Hook registry and concurrent dispatch (`Hub`). Lifecycle events: `RunStart`, `RunEnd`, `LLMRequest`, `LLMResponse`, `ToolCall`, `ToolResult`, `ToolError`. |
| `tools` | Tool registration, JSON-schema generation from struct tags, and dispatch (`Registry`). |
| `middleware` | Pre/post pipeline (`Chain`), shared `Message` and `ToolCall` types. |
| `llm` | `Client` interface, `Chunk`/`Request`/`Response` types, retry wrapper. |
| `llm/openai` | OpenAI-compatible streaming client. |
| `llm/anthropic` | Anthropic streaming client. |
| `events` | Inline-YAML event parsing, message bus, and per-run event registry (`Host`). |
| `shell` | `Driver` interface, `Host`, `DefaultFactory`, and shell-related hook events. |
| `shell/builtin` | Built-in virtual shell driver (30-command emulator + `VirtualFS`). |
| `shell/bashkit` | Bashkit CLI subprocess driver. |
| `shell/openshell` | OpenShell SSH-based remote driver (native gRPC transport). |
| `shell/vfs` | `FilesystemDriver` interface and `InMemoryFS` implementation. |
| `skills` | Skill lifecycle manager (`Manager`), `Skill` interface, and capability interfaces (`ToolsContributor`, `Setuppable`, …). |
| `internal/remotesync` | Internal: remote file-sync helpers for OpenShell. |
| `internal/util` | Internal: shared utilities. |
| `cmd/harness-example` | Runnable example wiring all subsystems together. |

## Quick start

```go
import (
    "agent-harness/go/agent"
    openaiprov "agent-harness/go/llm/openai"
    "agent-harness/go/middleware"
)

client := openaiprov.NewClient(os.Getenv("OPENAI_API_KEY"))
a := agent.NewBuilder("gpt-4o", client).Build()

reply, err := a.Run(ctx, []middleware.Message{
    {Role: "user", Content: "Hello!"},
})
```

## Tests

```
go test ./...
go test -race ./...
```

43 test files, ~15 000 lines of test code across all packages.
