# Go Developer Guide

## Overview

The Go implementation of the agent harness uses struct embedding for composition, goroutines for concurrency, and Go channels for streaming. It ships OpenAI and Anthropic providers in-tree and a pluggable `llm.Client` interface for anything else. The shell backend is swappable: builtin (pure Go, no dependencies), bashkit (CLI subprocess), or OpenShell (gRPC, streaming-capable).

Go 1.22 or later is required. The module path is `agent-harness/go`.

## Installation

```bash
go get agent-harness/go@latest
```

Dependencies (from `go.mod`):

- `github.com/openai/openai-go` — OpenAI client
- `github.com/anthropics/anthropic-sdk-go` — Anthropic client
- `google.golang.org/grpc` — OpenShell gRPC transport (optional)
- `gopkg.in/yaml.v3` — event YAML parsing
- `github.com/stretchr/testify` — test assertions (dev)

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "os"

    "agent-harness/go/agent"
    "agent-harness/go/llm/openai"
    "agent-harness/go/middleware"
)

func main() {
    client := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))

    a, err := agent.NewBuilder("gpt-4o-mini").
        System("You are a helpful assistant.").
        Client(client).
        Build(context.Background())
    if err != nil {
        panic(err)
    }

    result, err := a.Run(context.Background(), []middleware.Message{
        {Role: "user", Content: "Hello, world!"},
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result)
}
```

## Core Concepts

### Agent and Builder

`Agent` is the composed entry point. It embeds `*hooks.Hub`, `*tools.Registry`, and `*middleware.Chain` anonymously so their methods are promoted onto `*Agent`. The shell, events, and skills subsystems are named fields to avoid method-name collisions.

`NewBuilder(model)` returns a fluent `*Builder`:

```go
a, err := agent.NewBuilder("gpt-4o").
    System("You are helpful.").
    Client(client).
    MaxTurns(10).
    Tool(myTool).
    Use(loggingMW).
    On(hooks.RunStart, onStart).
    Shell(nil).       // nil = use builtin driver
    Event(progressEvent).
    Skill(mySkill{}, nil).
    Build(ctx)
```

All builder methods return `*Builder`. `Build` validates required fields (`Client`, `Model`) and returns an error rather than panicking. A built `*Agent` is safe for concurrent `Run` calls — all subsystems are goroutine-safe and `Run` keeps no shared per-run state on the agent.

To bypass the builder:

```go
a := agent.NewAgent("gpt-4o-mini", client)     // no shell
a := agent.NewAgentWithShell("gpt-4o-mini", client, driver) // with shell
```

### Hooks

Hook events are typed `string` constants in the `hooks` package:

```go
hooks.RunStart, hooks.RunEnd, hooks.LLMRequest, hooks.LLMResponse,
hooks.ToolCall, hooks.ToolResult, hooks.ToolError,
hooks.ShellCall, hooks.ShellResult, hooks.ShellNotFound, hooks.ShellCwd,
hooks.ShellStdoutChunk, hooks.ShellStderrChunk,
hooks.SkillMount, hooks.SkillUnmount, hooks.SkillSetup, hooks.SkillTeardown,
hooks.ToolRegister, hooks.ToolUnregister, hooks.CommandRegister, hooks.CommandUnregister,
hooks.Retry, hooks.TokenStream, hooks.Error
```

Register handlers via `Hub.On` (returns `*Hub` for chaining):

```go
// Via builder (preferred at construction time)
agent.NewBuilder("gpt-4o").
    On(hooks.RunStart, func(ctx context.Context, args ...any) {
        fmt.Println("run started")
    }).
    On(hooks.ToolCall, func(ctx context.Context, args ...any) {
        name, _ := args[0].(string)
        fmt.Printf("calling tool: %s\n", name)
    })

// After Build, on the embedded Hub directly
a.Hub.On(hooks.RunEnd, myHandler)
```

Handlers receive a `context.Context` and variadic `...any` arguments — each event documents what it provides. `Hub.Emit` dispatches all handlers for an event concurrently via goroutines and `sync.WaitGroup`, mirroring Python's `asyncio.gather` semantics. Panics inside handlers are recovered and logged without preventing other handlers from running.

`Hub.EmitAsync` is fire-and-forget (goroutine, no wait). `Hub.Off(event)` removes all handlers for an event.

### Middleware

`Middleware` is an interface with two methods:

```go
type Middleware interface {
    Pre(ctx context.Context, messages []Message, runCtx any) ([]Message, error)
    Post(ctx context.Context, message Message, runCtx any) (Message, error)
}
```

Embed `middleware.Base` and override only what you need:

```go
type LoggingMiddleware struct{ middleware.Base }

func (m LoggingMiddleware) Pre(ctx context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
    fmt.Printf("sending %d messages\n", len(msgs))
    return msgs, nil
}
```

Register with `Builder.Use(mw)` or `a.Chain.Use(mw)`. Execution is sequential in registration order for both Pre and Post — the same direction for both, matching Python's `_run_pre/_run_post`.

`middleware.MiddlewareFunc` adapts two plain functions into a `Middleware` without defining a new type:

```go
mw := middleware.NewMiddlewareFunc(
    func(ctx context.Context, msgs []middleware.Message, _ any) ([]middleware.Message, error) {
        // pre
        return msgs, nil
    },
    nil, // post: no-op
)
```

### Tools

`tools.Tool` wraps a typed function into a `tools.Def` with auto-generated JSON schema. The function signature must be `func(context.Context, ArgsStruct) (Result, error)`. Schema is derived from struct field tags at construction time:

```go
type searchArgs struct {
    Query  string `json:"query"  desc:"search query"`
    Limit  int    `json:"limit,omitempty" desc:"max results"`
}

func searchFn(ctx context.Context, args searchArgs) ([]string, error) {
    // ...
    return results, nil
}

searchTool := tools.Tool(searchFn, tools.Description("Search the web."))
```

- `json:"name"` controls the JSON field name (defaults to lowercase Go name).
- `json:",omitempty"` marks the field as optional (not in `"required"`).
- `desc:"..."` sets the field description in the schema.
- `tools.Name("override")` overrides the derived snake_case tool name.

Programmer errors (wrong function shape, unsupported types) panic at construction time. Anonymous functions require `tools.Name(...)`.

Register via `Builder.Tool(d)` or `a.Registry.Register(d)` after Build.

### Events

Events are YAML blocks the LLM emits inline in its response. Define an `events.EventType` and register it on the builder:

```go
progressEvent := events.EventType{
    Name:        "progress",
    Description: "Report task progress",
    Schema:      map[string]any{"step": "string", "percent": "integer"},
    Instructions: "Emit one progress event per major step.",
}

a, _ := agent.NewBuilder("gpt-4o").
    Client(client).
    Event(progressEvent).
    Build(ctx)
```

The builder automatically installs prompt-injection middleware when any event type is registered, so the LLM receives formatting instructions. When streaming is enabled and event types are active, the events parser intercepts inline YAML blocks, publishes `events.ParsedEvent` values to the message bus, and passes clean text (events stripped) to the assistant message.

Subscribe to events via the bus:

```go
a.EventBus().Subscribe("progress", func(ctx context.Context, ev events.ParsedEvent, bus *events.MessageBus) error {
    fmt.Printf("progress: %v%%\n", ev.Data["percent"])
    return nil
})
```

Use `"*"` to receive all event types. `ParsedEvent.Stream` is non-nil for streaming events and delivers the streaming field token-by-token via a `<-chan string`. Consumers must drain the channel or the parser goroutine blocks.

### Skills

Skills are mountable capability bundles. The minimum interface is:

```go
type Skill interface {
    Name() string         // "" triggers AutoName from struct type
    Description() string
    Version() string
    Instructions() string // injected into system prompt
}
```

Implement optional capability interfaces to contribute resources:

| Interface | Method |
|-----------|--------|
| `skills.Setuppable` | `Setup(ctx, *SkillContext) error` |
| `skills.Teardown` | `Teardown(ctx, *SkillContext) error` |
| `skills.ToolsContributor` | `Tools() []tools.Def` |
| `skills.MiddlewareContributor` | `Middleware() []middleware.Middleware` |
| `skills.HooksContributor` | `Hooks() map[hooks.Event][]hooks.Handler` |
| `skills.CommandsContributor` | `Commands() map[string]shell.CmdHandler` |
| `skills.Dependencies` | `Dependencies() []Skill` |

Embed `skills.Base` for default no-op implementations:

```go
type WebBrowsingSkill struct{ skills.Base }

// Name is auto-derived: "web_browsing"

func (s WebBrowsingSkill) Instructions() string {
    return "You can browse the web using the fetch_page tool."
}

func (s WebBrowsingSkill) Tools() []tools.Def {
    return []tools.Def{fetchPageTool}
}

func (s WebBrowsingSkill) Setup(ctx context.Context, sctx *skills.SkillContext) error {
    // initialise per-skill state; sctx.Config carries builder-supplied config
    return nil
}
```

Mount via builder or after Build:

```go
// Via builder
agent.NewBuilder("gpt-4o").Skill(WebBrowsingSkill{}, nil)

// After Build
a.Skills.Mount(ctx, WebBrowsingSkill{}, nil)
a.Skills.Unmount(ctx, "web_browsing")
```

`skills.NewPromptMiddleware(manager)` auto-injects instructions from all mounted skills into the system prompt — the builder installs it automatically when skills are queued.

### Shell

The shell subsystem wraps any `shell.Driver` and auto-registers an `exec` tool on the agent. When the builder has `Shell(driver)`, the LLM can call `exec` to run commands:

```go
import "agent-harness/go/shell/builtin"

driver := builtin.NewBuiltinShellDriver()
driver.FS().WriteString("/data/readme.txt", "hello")

a, _ := agent.NewBuilder("gpt-4o").
    Client(client).
    Shell(driver).
    Build(ctx)
```

Pass `nil` to `Shell()` to use the default factory (the builtin driver). Register custom commands via the builder:

```go
agent.NewBuilder("gpt-4o").
    Command("greet", func(args []string, stdin string) shell.ExecResult {
        return shell.ExecResult{Stdout: "Hello, " + args[0] + "\n"}
    })
```

## Composition Model

Go uses struct embedding rather than class inheritance or function-based mixins. Three subsystems are embedded anonymously on `*Agent` so their methods are promoted: `*hooks.Hub`, `*tools.Registry`, `*middleware.Chain`. Their unqualified type names are deliberately distinct to prevent promotion collisions.

The shell, events, and skills subsystems are named fields (`Host`, `Events`, `Skills`) because they either conflict with another promoted name (`Register` is already on `*tools.Registry`) or need explicit addressing.

Capability interfaces (e.g. `skills.ToolsContributor`) replace Python's duck-typed mixin methods: the skill manager type-asserts each capability at mount time rather than requiring one monolithic interface. This gives composable opt-in behaviour without a deep inheritance chain.

See [ADR 0031](../adr/0031-go-port-scope-and-composition.md) for the full design rationale.

## Streaming Model

`llm.Client.Stream` returns a `<-chan llm.Chunk`. The channel is producer-owned and closed exactly once when the stream ends. Consumers must never close it. A terminal `Chunk{Done: true}` signals end-of-stream; `Chunk{Done: true, Err: err}` signals an in-stream error.

```go
ch, err := client.Stream(ctx, req)
if err != nil {
    // failed to open stream
}
for chunk := range ch {
    if chunk.Err != nil { /* in-stream error */ break }
    if chunk.Done { break }
    fmt.Print(chunk.Content)
}
```

`Agent.Run` handles streaming internally: when `a.Stream == true` (the default), it calls `client.Stream` and feeds content through the events parser if event types are registered, or through a plain aggregator otherwise. The produced `middleware.Message` is identical either way — callers do not see the difference.

See [ADR 0032](../adr/0032-go-streaming-model.md) for the channel contract and producer-owns-close invariant.

## LLM Providers

`llm.Client` is the provider contract:

```go
type Client interface {
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
    Complete(ctx context.Context, req Request) (Response, error)
}
```

Two providers ship in-tree:

```go
import "agent-harness/go/llm/openai"
import "agent-harness/go/llm/anthropic"

c1 := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))
c2 := anthropic.New(anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
```

Wrap with `llm.WithRetry` for automatic retry on transient errors:

```go
import "agent-harness/go/llm"

retrying := llm.WithRetry(client, llm.RetryConfig{MaxAttempts: 3})
```

Any type satisfying `llm.Client` works — implement the interface to add providers, mock clients in tests, or wrap with observability middleware.

See [ADR 0033](../adr/0033-go-llm-provider-design.md) for the provider design.

## Shell Drivers

Three drivers are available:

| Driver | Package | Streaming | Notes |
|--------|---------|-----------|-------|
| **Builtin** | `shell/builtin` | No (emulated) | Pure Go, zero dependencies. Default. |
| **Bashkit** | `shell/bashkit` | No | Spawns `bashkit -c` subprocess. Stateless per call. |
| **OpenShell** | `shell/openshell` | Yes (gRPC) | Remote sandbox, policy enforcement, real-time events. |

Switch driver at construction time:

```go
import "agent-harness/go/shell/builtin"
import "agent-harness/go/shell/bashkit"
import "agent-harness/go/shell/openshell"

// Builtin (default)
driver := builtin.NewBuiltinShellDriver()

// Bashkit CLI subprocess
driver := bashkit.NewDriver()

// OpenShell — SSH transport (default)
driver, err := openshell.NewDriver(openshell.WithSSH("localhost", 2222, "sandbox"))

// OpenShell — gRPC transport with streaming
driver, err := openshell.NewDriver(
    openshell.WithGRPC("localhost:50051"),
    openshell.WithPolicy(&shell.SecurityPolicy{
        FilesystemAllow: []string{"/data", "/tmp"},
    }),
)
```

The builtin driver is the only one that supports `ExecStream` returning real events. Bashkit and OpenShell SSH emulate streaming by collecting all output and emitting it as a single chunk. The OpenShell gRPC driver genuinely streams — use it when you need real-time stdout/stderr delivery.

See [docs/guides/bashkit.md](bashkit.md) and [docs/guides/openshell.md](openshell.md) for driver-specific setup.

## Example

`cmd/harness-example/main.go` is a runnable demo that exercises tools, shell, events, skills, and hooks together. Run it with:

```bash
# Dry run — no API key needed
cd src/go && go run ./cmd/harness-example --dry-run --verbose

# Real LLM
OPENAI_API_KEY=sk-... go run ./cmd/harness-example --model gpt-4o-mini --verbose
```

Key snippet (abridged):

```go
type addArgs struct {
    A int `json:"a" desc:"first number"`
    B int `json:"b" desc:"second number"`
}

func addFn(_ context.Context, args addArgs) (int, error) {
    return args.A + args.B, nil
}

type helloSkill struct{ skills.Base }

func (helloSkill) Instructions() string {
    return "When the user greets you, respond with a friendly greeting."
}

func run(ctx context.Context, stdout io.Writer, args []string) error {
    client := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))
    addTool := tools.Tool(addFn, tools.Description("Add two integers."))

    driver := builtin.NewBuiltinShellDriver()
    driver.FS().WriteString("/data/numbers.txt", "1\n2\n3\n42\n5\n")

    a, err := agent.NewBuilder("gpt-4o-mini").
        System("You are helpful. Use exec to inspect files.").
        Client(client).
        Tool(addTool).
        Shell(driver).
        Event(events.EventType{
            Name:   "progress",
            Schema: map[string]any{"step": "string", "percent": "integer"},
        }).
        Skill(helloSkill{}, nil).
        On(hooks.RunStart, func(_ context.Context, _ ...any) {
            fmt.Fprintln(os.Stderr, "[hook] run started")
        }).
        Build(ctx)
    if err != nil {
        return err
    }

    result, err := a.Run(ctx, []middleware.Message{
        {Role: "user", Content: "What is in /data/numbers.txt?"},
    })
    fmt.Fprintln(stdout, result)
    return err
}
```

## Deliberate Divergences from Python

These are intentional scope reductions documented in ADR 0031 and the M2–M5 implementation reports.

**Shell interpreter:**
- No tilde expansion (`~/foo` is not expanded; use absolute paths).
- No IFS-based word-splitting; words are split on whitespace only.
- No positional parameters (`$1`, `$@`, `$*` inside scripts are not supported).
- No hex or octal arithmetic literals (`0xff`, `077`); decimal only.
- No `~user` expansion.

**Skills and lifecycle:**
- `Setup` and `Teardown` are synchronous (`return error`) rather than async — Go's goroutine model makes this cleaner.
- `Dependencies()` returns `[]Skill` instances rather than type references; Go cannot instantiate by type, so callers supply live instances.
- No middleware or hook unregistration on skill unmount (matches Python's current behaviour — unregistration is not yet implemented).

**Agent:**
- `Setup` on skills runs synchronously inside `Build` or `Skills.Mount` — there is no async context to defer to.
- The builder auto-installs `events.PromptMiddleware` and `skills.PromptMiddleware` when event types or skills are registered. Python requires explicit `SkillPromptMiddleware` wiring.

**Middleware:**
- `Base` uses method receivers, not Python's ABC abstract methods. Embedding `middleware.Base` gives you safe no-ops for the half you don't need.

## Testing

Tests use the stdlib `testing` package and `github.com/stretchr/testify/require` for assertions.

```bash
cd src/go

# All tests
go test ./...

# With race detector (recommended for streaming code)
go test -race ./...

# Specific package
go test -race ./agent/...

# Verbose output
go test -v -race ./...
```

The `-race` flag is especially important for code that uses channels, goroutines, or the hooks hub. All shipping tests pass with `-race`.

Fake LLM clients are used throughout the test suite — `llm.Client` is an interface, so tests substitute a scripted `fakeClient` with no network dependency. The `cmd/harness-example` binary also accepts `--dry-run` for CI-safe end-to-end smoke tests.

## Known Gaps

- **Streaming on builtin and bashkit drivers**: `ExecStream` on these drivers emulates streaming by collecting all output synchronously and emitting a single stdout chunk. Only the OpenShell gRPC driver (`shell/openshell` with `WithGRPC`) delivers genuine real-time chunks.
- **Middleware and hook unregistration on skill unmount**: unmounting a skill runs `Teardown` and removes contributed tools and commands, but does not remove contributed middleware or hooks from the agent. This matches Python's current behaviour and is tracked as a known gap in both implementations.
- **No persistent shell state on bashkit**: each `Exec` spawns a fresh subprocess. Variables, functions, and aliases do not persist between calls. VFS files do persist via the preamble/epilogue sync protocol.
