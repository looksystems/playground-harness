# Skills

Skills are mountable capability bundles. A single skill can contribute tools, middleware, hook handlers, shell commands, and system-prompt instructions to an agent, and can run setup and teardown logic around a session. Mount a skill once; it wires up every contribution in one step.

This guide covers the concepts that hold across all four implementations and shows the idiomatic API in each language. For the full language API reference, see the per-language guides ([Python](python.md), [TypeScript](typescript.md), [PHP](php.md), [Go](go.md)).

## Why skills

Skills give agents composable, reusable chunks of behaviour. Instead of hand-wiring a web-browsing tool, a scraping middleware, an on-error hook, and a prompt paragraph for every agent that needs them, you package the whole set as `WebBrowsingSkill` and mount it in one call.

Skills subsume slash commands: any `/foo` behaviour is just a skill that contributes a single tool. They also give you lifecycle — a skill can open an HTTP session on `setup` and close it on `teardown`, so per-agent resources clean up correctly.

## Anatomy of a skill

A skill declares some identity and optionally contributes resources:

| Part | Purpose |
|------|---------|
| **Name** | Unique identifier; used for mount/unmount and for the `SkillPromptMiddleware` heading. Auto-derived from the class/type name if not set. |
| **Description** | Short human-readable description for logs and UI. |
| **Version** | Semver-ish string. Defaults to `"0.1.0"` when not set. |
| **Instructions** | Text injected into the system prompt via `SkillPromptMiddleware`. This is how the LLM learns the skill exists. |
| **`setup()`** | Called once at mount time. Receives a `SkillContext` carrying the skill, the agent, and per-mount config. Open connections here. |
| **`teardown()`** | Called at unmount time and during shutdown. Close connections here. |
| **`tools()`** | Returns tool definitions to register on the agent. |
| **`middleware()`** | Returns middleware to append to the agent's chain. |
| **`hooks()`** | Returns hook handlers keyed by hook event. |
| **`commands()`** | Returns custom shell commands (only wired if the agent has a shell). |
| **`dependencies`** | Other skills that must be mounted first. |

Only `Name` and `Instructions` are typically required to make a skill useful. Everything else is optional; contribute only what you need.

## A minimum skill

Each language ships a base class (or interface + base struct) that provides no-op defaults. Override only what matters.

**Python:**

```python
from src.python.has_skills import Skill

class GreeterSkill(Skill):
    @property
    def instructions(self):
        return "Greet the user warmly when they say hello."
```

**TypeScript:**

```typescript
import { Skill } from "agent-harness-ts";

class GreeterSkill extends Skill {
  instructions = "Greet the user warmly when they say hello.";
}
```

**PHP:**

```php
use AgentHarness\Skills\Skill;

class GreeterSkill extends Skill {
    protected string $instructions = "Greet the user warmly when they say hello.";
}
```

**Go:**

```go
import "agent-harness/go/skills"

type GreeterSkill struct{ skills.Base }

func (GreeterSkill) Instructions() string {
    return "Greet the user warmly when they say hello."
}
```

In Go, `Name()` is auto-derived from the struct type via `skills.AutoName` (trailing `Skill` is stripped, then snake-cased). `GreeterSkill` becomes `"greeter"`. Override `Name()` to pin it explicitly.

## Contributing resources

Skills contribute four resource kinds to an agent: tools, middleware, hooks, and shell commands.

### Tools

A skill that adds an LLM-visible tool:

**Python:**

```python
from src.python.uses_tools import tool

@tool
def fetch_page(url: str) -> str:
    """Fetch a web page and return its text."""
    return requests.get(url).text

class WebBrowsingSkill(Skill):
    @property
    def name(self): return "web_browsing"
    @property
    def instructions(self): return "Use fetch_page to browse the web."
    def tools(self): return [fetch_page]
```

**Go:**

```go
type FetchArgs struct {
    URL string `json:"url" desc:"URL to fetch"`
}

fetchPageTool := tools.Tool(func(ctx context.Context, a FetchArgs) (string, error) {
    resp, err := http.Get(a.URL)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    return string(body), nil
}, tools.Description("Fetch a web page and return its text."))

type WebBrowsingSkill struct{ skills.Base }
func (WebBrowsingSkill) Instructions() string { return "Use fetch_page to browse the web." }
func (WebBrowsingSkill) Tools() []tools.Def   { return []tools.Def{fetchPageTool} }
```

### Middleware

Middleware the skill wants wrapped around every LLM call:

```python
def middleware(self):
    return [RetryMiddleware(max_attempts=3)]
```

### Hooks

Handlers for lifecycle events:

```python
def hooks(self):
    return {
        HookEvent.TOOL_CALL: [lambda name, args: log.info(f"Tool: {name}")],
    }
```

### Shell commands

Custom shell commands the skill wants registered on the agent's shell (only wired if the agent actually has a shell):

```python
def commands(self):
    return {
        "deploy": deploy_handler,
        "validate": validate_handler,
    }
```

## Instructions and the system prompt

A skill's `instructions` field is injected into the system prompt at run time via `SkillPromptMiddleware`. The middleware builds a section like:

```
---
**Available Skills:**

## web_browsing
Use fetch_page to browse the web.

## greeter
Greet the user warmly when they say hello.
```

…and appends it to the existing system message (or prepends a new one if none).

Install the middleware once; it reads the currently-mounted skills on every LLM call, so later `mount()` / `unmount()` calls take effect without reinstalling.

**Python:**

```python
from src.python.has_skills import SkillPromptMiddleware
agent.use(SkillPromptMiddleware(agent.skills))
```

**Go:** the fluent builder installs it automatically the first time `.Skill(...)` is called.

## Lifecycle: setup and teardown

Skills that hold resources (HTTP sessions, DB connections, open files) use `setup`/`teardown`:

**Python:**

```python
async def setup(self, ctx):
    ctx.session = aiohttp.ClientSession()

async def teardown(self, ctx):
    await ctx.session.close()
```

**Go:**

```go
func (s *WebBrowsingSkill) Setup(ctx context.Context, sctx *skills.SkillContext) error {
    s.client = &http.Client{Timeout: 30 * time.Second}
    return nil
}

func (s *WebBrowsingSkill) Teardown(ctx context.Context, sctx *skills.SkillContext) error {
    s.client.CloseIdleConnections()
    return nil
}
```

`SkillContext` carries the skill itself, a handle to the agent, and per-mount configuration. Store connection state on the skill receiver (Go: pointer receiver), not on the context — the context is a transient parameter.

Setup runs once, synchronously during mount. If it returns an error, the mount is aborted and no contributions are registered. Teardown runs at unmount time and during shutdown; errors are logged but do not abort the rest of shutdown.

## Dependencies

Skills can declare other skills they depend on. The manager mounts dependencies first and rejects cycles.

**Python** (class references):

```python
class ResearchSkill(Skill):
    dependencies = (WebBrowsingSkill, FileIndexSkill)
```

**Go** (instances — a deliberate divergence from Python; Go can't instantiate by type):

```go
func (s ResearchSkill) Dependencies() []skills.Skill {
    return []skills.Skill{&WebBrowsingSkill{}, &FileIndexSkill{}}
}
```

**TypeScript** (class references):

```typescript
class ResearchSkill extends Skill {
    dependencies = [WebBrowsingSkill, FileIndexSkill];
}
```

**PHP** (class strings):

```php
protected array $dependencies = [WebBrowsingSkill::class, FileIndexSkill::class];
```

Transitive deps are resolved breadth-first with a seen-set; a skill already mounted under its name is a no-op. Cycles (`A → B → A`) return a clear error.

## Mounting and unmounting

**Python:**

```python
await agent.mount(WebBrowsingSkill(), config={"timeout": 10})
agent.unmount("web_browsing")
```

**Go:**

```go
// Via the builder (queued, mounted at Build time):
agent, _ := agent.NewBuilder("gpt-4").
    Client(client).
    Skill(&WebBrowsingSkill{}, map[string]any{"timeout": 10}).
    Build(ctx)

// Or directly on an already-built agent:
_ = agent.Skills.Mount(ctx, &WebBrowsingSkill{}, nil)
_ = agent.Skills.Unmount(ctx, "web_browsing")
```

Mounting is idempotent: mounting a skill whose name is already registered is a no-op. Unmounting a skill that isn't mounted returns nil (matches every language).

### Per-mount configuration

The second argument to `mount` is a per-invocation config map. It's passed to `setup` via the `SkillContext` and is independent per mount:

```python
async def setup(self, ctx):
    timeout = ctx.config.get("timeout", 30)
    self.session = aiohttp.ClientSession(timeout=timeout)
```

## Shutdown

Tearing down all skills in reverse mount order:

**Python:**

```python
await agent.shutdown_skills()
```

**Go:**

```go
_ = agent.Skills.Shutdown(ctx)
```

Shutdown logs errors from individual teardown calls but does not short-circuit — every skill gets a chance to clean up.

## Skill lifecycle hooks

When the `HasHooks` capability is also composed, the manager emits lifecycle hooks:

- `SKILL_MOUNT(name)` / `SKILL_UNMOUNT(name)`
- `SKILL_SETUP(name)` / `SKILL_TEARDOWN(name)`

Subscribe to them to observe skill lifecycle:

**Python:**

```python
from src.python.has_hooks import HookEvent
agent.on(HookEvent.SKILL_MOUNT, lambda name: print(f"mounted {name}"))
```

**Go:**

```go
agent.Hub.On(hooks.SkillMount, func(ctx context.Context, args ...any) {
    fmt.Printf("mounted %s\n", args[0])
})
```

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|-----|
| Base class / embed | `Skill` ABC | `Skill` class | `Skill` class | `skills.Base` struct + `Skill` interface |
| Auto-name | from class name, strips `Skill` | from class name, strips `Skill` | from class name, strips `Skill` | from struct type via `AutoName`, strips `Skill` |
| `setup` / `teardown` | `async def` | `async` | synchronous | synchronous `Setup(ctx, *SkillContext) error` |
| Dependencies | class refs | class refs | class strings | instances (Go can't instantiate by type) |
| Prompt middleware | `SkillPromptMiddleware` (manual) | ditto | ditto | auto-installed by the fluent builder |
| Contributions | `tools/middleware/hooks/commands()` methods | same | same | narrow optional capability interfaces (see below) |

### The Go capability-interface model

Python, TypeScript, and PHP define one abstract class with stub methods (`tools()`, `middleware()`, `hooks()`, `commands()`) that return empty collections by default. Override the ones you need.

Go splits those into **narrow optional interfaces**; a skill opts in by implementing the interface:

```go
type ToolsContributor      interface { Tools() []tools.Def }
type MiddlewareContributor interface { Middleware() []middleware.Middleware }
type HooksContributor      interface { Hooks() map[hooks.Event][]hooks.Handler }
type CommandsContributor   interface { Commands() map[string]shell.CmdHandler }
type Setuppable            interface { Setup(ctx context.Context, sctx *SkillContext) error }
type Teardown              interface { Teardown(ctx context.Context, sctx *SkillContext) error }
type Dependencies          interface { Dependencies() []Skill }
```

The manager type-asserts each one at mount time — the direct Go analogue of the `hasattr` probes in the Python manager. This keeps the required interface tiny (four methods) and makes every skill's actual capability set visible at the type level.

## Known limitations

Shared across all languages:

- **Middleware and hook unmount is best-effort.** Middleware chains and hook handlers are not easily "un-added" — function identity isn't comparable in most of these languages, and the existing registries don't track provenance. `unmount` removes tools and commands cleanly; middleware and hooks remain until agent shutdown. Don't unmount-and-remount skills that contribute them.
- **No parallel mount.** `mount` serialises through the manager's mutex. This is deliberate; setup and dependency resolution aren't commutative.

Go-specific:

- **Dependencies return instances, not types.** Python's `dependencies = (FooSkill, BarSkill)` pattern can't translate — Go has no zero-arg-constructor-by-type primitive. Return concrete instances; the manager deduplicates by name.
- **`Setup` / `Teardown` are synchronous.** They return `error` rather than being `async`. Long-running setup should use goroutines and a channel back to the handler.

## Example: a complete skill

A web-research skill that:

- Depends on a generic HTTP-client skill.
- Opens an `http.Client` on setup, closes it on teardown.
- Contributes a `fetch_page` tool and a `search_index` middleware.
- Registers a `research` shell command.
- Subscribes to `TOOL_ERROR` to log failures.

**Go:**

```go
type ResearchSkill struct {
    skills.Base
    client *http.Client
}

func (ResearchSkill) Name() string         { return "research" }
func (ResearchSkill) Instructions() string { return "Use fetch_page to browse; use /research in the shell." }
func (ResearchSkill) Dependencies() []skills.Skill {
    return []skills.Skill{&HTTPClientSkill{}}
}

func (s *ResearchSkill) Setup(ctx context.Context, sctx *skills.SkillContext) error {
    s.client = &http.Client{Timeout: 30 * time.Second}
    return nil
}

func (s *ResearchSkill) Teardown(ctx context.Context, sctx *skills.SkillContext) error {
    s.client.CloseIdleConnections()
    return nil
}

func (s *ResearchSkill) Tools() []tools.Def {
    return []tools.Def{fetchPageTool(s.client)}
}

func (s *ResearchSkill) Middleware() []middleware.Middleware {
    return []middleware.Middleware{&searchIndexMiddleware{client: s.client}}
}

func (s *ResearchSkill) Hooks() map[hooks.Event][]hooks.Handler {
    return map[hooks.Event][]hooks.Handler{
        hooks.ToolError: {func(ctx context.Context, args ...any) {
            log.Printf("tool error: %v", args)
        }},
    }
}

func (s *ResearchSkill) Commands() map[string]shell.CmdHandler {
    return map[string]shell.CmdHandler{
        "research": func(args []string, stdin string) shell.ExecResult {
            return shell.ExecResult{Stdout: "researching " + strings.Join(args, " ") + "\n"}
        },
    }
}
```

Mount it and the agent now has an extra tool, middleware, hook handler, and shell command — plus a system-prompt blurb — all from one call.

## See also

- [ADR 0024](../adr/0024-has-skills-mixin.md) — the `HasSkills` design
- [ADR 0031](../adr/0031-go-struct-embedding-composition.md) — Go-specific composition (why the capability interfaces)
- [Python guide: Skills](python.md#skills) · [TypeScript guide: Skills](typescript.md#skills) · [PHP guide: Skills](php.md#skills) · [Go guide: Skills](go.md#skills)
