# Slash Commands

"Slash commands" in this framework is a design pattern, not a built-in parser. There is no pre-LLM `/command` intercept. Commands reach the agent as natural-language prompts; the LLM reads them and dispatches to the appropriate tools or shell commands. Two idiomatic patterns cover every use case:

- **Pattern A — skill-with-single-tool**: a skill named `deploy` contributes one tool also named `deploy`. The LLM sees `deploy` in its tool list and calls it when the user's intent matches.
- **Pattern B — custom shell command**: a handler registered via `register_command("deploy", handler)` is invocable through the `exec` tool, identical to any built-in shell command.

This guide covers both patterns in all four languages, when to choose each, and how to combine them.

## Why there is no `/command` parser

The agent's entry point is a text prompt. The LLM reads it and decides what to call. Adding a special pre-LLM interception layer for `/foo` syntax would:

- bypass the LLM's understanding of the request (arguments, context, intent);
- require exact syntax matching where the LLM handles natural variation for free;
- create a second dispatch surface to maintain alongside the tool and shell surfaces.

Per [ADR 0024](../adr/0024-has-skills-mixin.md), the `HasCommands` mixin and its `SlashCommandMiddleware` were retired in favour of `HasSkills`. Any `/foo` behaviour is now just a skill with a single tool — the LLM is the parser. The four `SLASH_COMMAND_*` hook events were renamed `SKILL_*` at the same time.

## Pattern A: skill-with-single-tool

A skill contributes a named tool to the agent's tool list. The skill's `instructions` field tells the LLM when to call it. The user can type "deploy staging to prod", `/deploy staging`, or any natural phrasing; the LLM maps intent to the `deploy` tool and supplies the arguments.

### Python

```python
from src.python.has_skills import Skill, skill
from src.python.uses_tools import tool

# Option 1: @skill decorator (quickest for simple cases)
@skill(
    description="Deploy an environment to a target.",
    instructions="When the user says /deploy or asks to deploy, call the deploy tool.",
)
def deploy(env: str, target: str) -> str:
    """Deploy *env* to *target* and return a status string."""
    return f"Deploying {env} to {target}…"

agent.register_skill_function(deploy)


# Option 2: full Skill subclass (preferred when you need lifecycle)
@tool
def deploy_tool(env: str, target: str) -> str:
    """Deploy *env* to *target*."""
    return f"Deploying {env} to {target}…"

class DeploySkill(Skill):
    @property
    def instructions(self):
        return "When the user says /deploy or asks to deploy, call the deploy tool."

    def tools(self):
        return [deploy_tool]

await agent.mount(DeploySkill())
```

### TypeScript

```typescript
import { Skill } from "agent-harness-ts";
import { tool } from "agent-harness-ts/tools";

const deployTool = tool(
    async ({ env, target }: { env: string; target: string }) =>
        `Deploying ${env} to ${target}…`,
    { name: "deploy", description: "Deploy env to target." }
);

class DeploySkill extends Skill {
    instructions =
        "When the user says /deploy or asks to deploy, call the deploy tool.";

    tools() {
        return [deployTool];
    }
}

await agent.mount(new DeploySkill());
```

### PHP

```php
use AgentHarness\Skills\Skill;

class DeploySkill extends Skill {
    protected string $instructions =
        "When the user says /deploy or asks to deploy, call the deploy tool.";

    public function tools(): array {
        return [
            [
                'name'        => 'deploy',
                'description' => 'Deploy env to target.',
                'parameters'  => [
                    'type'       => 'object',
                    'properties' => [
                        'env'    => ['type' => 'string'],
                        'target' => ['type' => 'string'],
                    ],
                    'required'   => ['env', 'target'],
                ],
                'handler' => function (array $args): string {
                    return "Deploying {$args['env']} to {$args['target']}…";
                },
            ],
        ];
    }
}

$agent->mount(new DeploySkill());
```

### Go

```go
import (
    "context"
    "fmt"
    "agent-harness/go/skills"
    "agent-harness/go/tools"
)

type deployArgs struct {
    Env    string `json:"env"    desc:"Environment name"`
    Target string `json:"target" desc:"Deployment target"`
}

var deployTool = tools.Tool(
    func(_ context.Context, a deployArgs) (string, error) {
        return fmt.Sprintf("Deploying %s to %s…", a.Env, a.Target), nil
    },
    tools.Name("deploy"),
    tools.Description("Deploy env to target."),
)

type DeploySkill struct{ skills.Base }

func (DeploySkill) Instructions() string {
    return "When the user says /deploy or asks to deploy, call the deploy tool."
}

func (DeploySkill) Tools() []tools.Def { return []tools.Def{deployTool} }

// Mount via the fluent builder (SkillPromptMiddleware installed automatically):
agent, _ := agent.NewBuilder("gpt-4").
    Client(client).
    Skill(&DeploySkill{}, nil).
    Build(ctx)
```

The `instructions` text is injected into the system prompt by `SkillPromptMiddleware` before every LLM call. Write it as a directive to the LLM: "When the user says X, call the Y tool."

## Pattern B: custom shell command

A custom shell command is a function registered on the agent's shell. The LLM must still invoke it through the `exec` tool (`exec("deploy staging")`), but execution is deterministic — the LLM does not interpret arguments, your handler does.

This pattern suits:

- scripted pipelines where arguments are positional and fixed;
- commands that compose with pipes (`deploy staging | tee deploy.log`);
- operations that produce machine-readable output for further shell processing.

Pipe composition works fully in the builtin shell driver and in bashkit (which supports the complete POSIX pipeline model). The OpenShell driver and Go's bashkit CLI driver intercept only the first word of a command line, so pipelines that mix custom commands with built-ins may behave differently there; see the [OpenShell guide](openshell.md) and the [Bashkit guide](bashkit.md) for details.

### Python — direct registration

```python
from src.python.shell import ExecResult

def deploy_handler(args: list[str], stdin: str) -> ExecResult:
    # args = ["staging"]  when invoked as: exec("deploy staging")
    env = args[0] if args else "default"
    return ExecResult(stdout=f"Deploying {env}…\n", exit_code=0)

agent.register_command("deploy", deploy_handler)
```

### Python — via skill `commands()`

```python
class DeploySkill(Skill):
    @property
    def instructions(self):
        return "Run /deploy <env> in the shell to deploy an environment."

    def commands(self):
        return {"deploy": deploy_handler}
```

The skill manager calls `agent.register_command` for each entry during mount, and `agent.unregister_command` during unmount.

### Go

```go
import "agent-harness/go/shell"

agent.Shell.RegisterCommand("deploy", func(args []string, stdin string) shell.ExecResult {
    env := "default"
    if len(args) > 0 {
        env = args[0]
    }
    return shell.ExecResult{Stdout: fmt.Sprintf("Deploying %s…\n", env)}
})
```

Or via a skill's `Commands()` contribution (preferred — commands are unregistered on unmount):

```go
func (s *DeploySkill) Commands() map[string]shell.CmdHandler {
    return map[string]shell.CmdHandler{
        "deploy": func(args []string, stdin string) shell.ExecResult {
            env := "default"
            if len(args) > 0 {
                env = args[0]
            }
            return shell.ExecResult{Stdout: fmt.Sprintf("Deploying %s…\n", env)}
        },
    }
}
```

### Handler signature

The `CmdHandler` signature is identical across all four languages:

| Language | Type |
|----------|------|
| Python | `(args: list[str], stdin: str) -> ExecResult` |
| TypeScript | `(args: string[], stdin: string) => ExecResult` |
| PHP | `callable(array $args, string $stdin): ExecResult` |
| Go | `func(args []string, stdin string) shell.ExecResult` |

`args` is the tokenised argument list (not including the command name). `stdin` is the piped input string, empty when not piped. `ExecResult` carries `stdout`, `stderr`, and `exit_code`.

## When to use which

| Consideration | Pattern A (tool) | Pattern B (shell command) |
|---------------|-----------------|--------------------------|
| Arguments need LLM interpretation | Yes — LLM infers from natural language | No — positional, caller-controlled |
| Composable with pipes and redirects | No | Yes |
| Stateless / scriptable | Not typically | Yes |
| Multiple optional parameters | Natural | Awkward (positional only) |
| Invoked from shell scripts | No | Yes (`deploy staging && notify`) |
| Needs lifecycle (connections, cleanup) | Yes (via `setup`/`teardown`) | Via the owning skill |

Use Pattern A when the LLM needs to understand the request. Use Pattern B when the invocation is deterministic and the LLM just needs to run a command string.

## Hybrid: tool and shell command together

A skill can contribute both a tool and a shell command for the same operation. The LLM picks whichever fits its current context — the tool when the user prompt needs interpretation, the shell command when composing a pipeline.

```python
class DeploySkill(Skill):
    @property
    def instructions(self):
        return (
            "To deploy interactively, call the deploy tool with env and target. "
            "To deploy from a script or pipe the result, run: exec('deploy <env>')"
        )

    def tools(self):
        return [deploy_tool]        # LLM-callable; accepts structured args

    def commands(self):
        return {"deploy": deploy_handler}   # shell-callable; accepts positional args
```

## Example: a minimal `/status` skill

A skill that responds to `/status` or "what's the status" in all four languages.

### Python

```python
from src.python.has_skills import Skill
from src.python.uses_tools import tool

@tool
def status() -> str:
    """Return current system status."""
    return "All systems operational."

class StatusSkill(Skill):
    @property
    def instructions(self):
        return (
            "When the user says /status or asks for system status, "
            "call the status tool with no arguments."
        )

    def tools(self):
        return [status]

await agent.mount(StatusSkill())
```

### TypeScript

```typescript
class StatusSkill extends Skill {
    instructions =
        "When the user says /status or asks for system status, " +
        "call the status tool with no arguments.";

    tools() {
        return [
            tool(async () => "All systems operational.", {
                name: "status",
                description: "Return current system status.",
            }),
        ];
    }
}
```

### PHP

```php
class StatusSkill extends Skill {
    protected string $instructions =
        "When the user says /status or asks for system status, " .
        "call the status tool with no arguments.";

    public function tools(): array {
        return [[
            'name'        => 'status',
            'description' => 'Return current system status.',
            'parameters'  => ['type' => 'object', 'properties' => []],
            'handler'     => fn() => 'All systems operational.',
        ]];
    }
}
```

### Go

```go
type StatusSkill struct{ skills.Base }

func (StatusSkill) Instructions() string {
    return "When the user says /status or asks for system status, " +
        "call the status tool with no arguments."
}

func (StatusSkill) Tools() []tools.Def {
    return []tools.Def{
        tools.Tool(
            func(_ context.Context, _ struct{}) (string, error) {
                return "All systems operational.", nil
            },
            tools.Name("status"),
            tools.Description("Return current system status."),
        ),
    }
}
```

## Example: a shell-style `deploy` command (Pattern B)

### Python

```python
from src.python.shell import ExecResult

def deploy_handler(args: list[str], stdin: str) -> ExecResult:
    if not args:
        return ExecResult(stderr="usage: deploy <env>\n", exit_code=1)
    env = args[0]
    target = args[1] if len(args) > 1 else "production"
    output = f"Deploying {env} to {target}…\ndone.\n"
    return ExecResult(stdout=output, exit_code=0)

agent.register_command("deploy", deploy_handler)

# Now the LLM can run:  exec("deploy staging")
# Or as part of a pipeline:  exec("deploy staging | tee /tmp/deploy.log")
```

### Go

```go
agent.Shell.RegisterCommand("deploy", func(args []string, stdin string) shell.ExecResult {
    if len(args) == 0 {
        return shell.ExecResult{Stderr: "usage: deploy <env>\n", ExitCode: 1}
    }
    env := args[0]
    target := "production"
    if len(args) > 1 {
        target = args[1]
    }
    return shell.ExecResult{
        Stdout:   fmt.Sprintf("Deploying %s to %s…\ndone.\n", env, target),
        ExitCode: 0,
    }
})
```

## Lifecycle hooks

ADR 0024 renamed the four hook events introduced by the now-superseded `HasCommands` mixin. The old names are not supported; use the skill events:

| Old event (HasCommands) | New event (HasSkills) | Fires when |
|---|---|---|
| `slash_command_register` | `skill_mount` | Skill mounted |
| `slash_command_unregister` | `skill_unmount` | Skill unmounted |
| `slash_command_call` | `skill_setup` | Before skill setup executes |
| `slash_command_result` | `skill_teardown` | After skill teardown completes |

Subscribe via the standard hook API:

**Python:**

```python
from src.python.has_hooks import HookEvent
agent.on(HookEvent.SKILL_MOUNT, lambda name: print(f"mounted skill: {name}"))
```

**Go:**

```go
agent.Hub.On(hooks.SkillMount, func(ctx context.Context, args ...any) {
    fmt.Printf("mounted skill: %s\n", args[0])
})
```

Custom shell command registration also emits hooks:

- `COMMAND_REGISTER(name)` — fired when `register_command` is called.
- `COMMAND_UNREGISTER(name)` — fired when `unregister_command` is called.

## Cross-language surface

| Feature | Python | TypeScript | PHP | Go |
|---------|--------|------------|-----|----|
| **Pattern A** — Skill base | `Skill` ABC | `Skill` class | `Skill` class | `skills.Base` + `Skill` interface |
| **Pattern A** — single-tool shortcut | `@skill` decorator + `register_skill_function()` | `tool()` helper inline in `tools()` | inline handler map in `tools()` | `tools.Tool()` func in `Tools()` |
| **Pattern A** — prompt injection | `SkillPromptMiddleware` (manual) | `SkillPromptMiddleware` (manual) | `SkillPromptMiddleware` (manual) | auto-installed by the fluent builder |
| **Pattern B** — direct registration | `agent.register_command(name, fn)` | `agent.registerCommand(name, fn)` | `$agent->registerCommand($name, $fn)` | `agent.Shell.RegisterCommand(name, fn)` |
| **Pattern B** — via skill | `commands()` dict | `commands()` map | `commands()` array | `Commands() map[string]CmdHandler` |
| **Pattern B** — handler type | `(list[str], str) -> ExecResult` | `(string[], string) => ExecResult` | `callable(array, string): ExecResult` | `func([]string, string) ExecResult` |
| Hook events | `HookEvent.SKILL_MOUNT` etc. | `HookEvent.SKILL_MOUNT` etc. | `HookEvent::SKILL_MOUNT` etc. | `hooks.SkillMount` etc. |

## See also

- [ADR 0024](../adr/0024-has-skills-mixin.md) — why `HasCommands` was replaced by `HasSkills`, and the hook event rename
- [ADR 0021](../adr/0021-custom-command-registration.md) — custom command registration design for the virtual shell
- [Skills guide](skills.md) — full Skill contract, lifecycle, dependencies, and mounting
- [Virtual Bash reference](virtual-bash-reference.md) — pipe composition, redirects, and control flow available to shell commands
- [Bashkit guide](bashkit.md) — POSIX shell driver; custom command support in the Commands section
- [OpenShell guide](openshell.md) — sandboxed execution; first-word interception limitation for custom commands
- Per-language guides: [Python](python.md#skills) · [TypeScript](typescript.md#skills) · [PHP](php.md#skills) · [Go](go.md#skills)
