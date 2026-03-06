# PHP Developer Guide

## Overview

The PHP implementation of the agent harness framework uses native traits for
capability composition, Guzzle HTTP for LLM calls, and a synchronous execution
model. Streaming is handled through Generators.

## Installation

Dependencies are declared in `src/php/composer.json`:

- **Runtime:** `guzzlehttp/guzzle`
- **Dev:** `phpunit/phpunit`
- **Autoloading:** PSR-4 under the `AgentHarness` namespace

```bash
cd src/php
composer install
```

## Creating an Agent

### Using StandardAgent (all capabilities)

`StandardAgent` bundles every available trait:

```php
use AgentHarness\StandardAgent;

$agent = new StandardAgent(
    model: 'gpt-4',
    system: 'You are a helpful assistant.',
    apiKey: getenv('OPENAI_API_KEY'),
);
$result = $agent->run([['role' => 'user', 'content' => 'Hello']]);
```

Under the hood, `StandardAgent` composes all six traits:

```php
class StandardAgent extends BaseAgent
{
    use HasHooks;
    use HasMiddleware;
    use UsesTools;
    use EmitsEvents;
    use HasShell;
    use HasSkills;
}
```

### Using the Builder (declarative setup)

`StandardAgent::build()` provides a fluent interface for configuring an agent declaratively:

```php
use AgentHarness\StandardAgent;
use AgentHarness\HookEvent;

$agent = StandardAgent::build('gpt-4')
    ->system('You are a helpful assistant.')
    ->maxTurns(10)
    ->tools($searchTool, $calcTool)
    ->middleware(new LoggingMiddleware())
    ->on(HookEvent::RunStart, fn() => print("started\n"))
    ->skill(new WebBrowsingSkill())
    ->shell()
    ->create();
```

All methods return the builder for chaining. `create()` is synchronous in PHP.

### Custom composition

Include only the capabilities you need:

```php
class MyAgent extends BaseAgent
{
    use HasHooks;
    use UsesTools;
}
```

## Lifecycle Hooks

`HookEvent` is a string-backed enum with 22 cases. Because PHP is synchronous,
dispatch is always sequential.

```php
use AgentHarness\HookEvent;

$agent->on(HookEvent::RUN_START, function () {
    echo "Run started\n";
});
$agent->on(HookEvent::TOOL_CALL, function (string $name, array $args) {
    echo "Calling {$name}\n";
});
```

Remove a hook with `removeHook()`:

```php
$agent->removeHook(HookEvent::RunStart, $callback);
```

All registration methods return `$this` for fluent chaining. Read-only accessors: `$agent->getHooks()`, `$agent->getMiddleware()`, `$agent->getTools()`, `$agent->getEvents()`.

## Middleware

Implement the `Middleware` interface or extend `BaseMiddleware`. The pipeline
executes sequentially.

```php
use AgentHarness\BaseMiddleware;

class LoggingMiddleware extends BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        echo "Sending " . count($messages) . " messages\n";
        return $messages;
    }

    public function post(array $message, mixed $context): array
    {
        echo "Got: " . substr($message['content'] ?? '', 0, 50) . "\n";
        return $message;
    }
}

$agent->use(new LoggingMiddleware());
```

Remove with `$agent->removeMiddleware($mw)`.

## Tools

Use the `ToolDef::make()` static factory to define tools:

```php
use AgentHarness\ToolDef;

$addTool = ToolDef::make(
    name: 'add',
    description: 'Add two numbers',
    parameters: [
        'type' => 'object',
        'properties' => [
            'a' => ['type' => 'number'],
            'b' => ['type' => 'number'],
        ],
        'required' => ['a', 'b'],
    ],
    fn: fn(array $args) => $args['a'] + $args['b'],
);
$agent->registerTool($addTool);
```

## Events

Register custom event types to structure agent output:

```php
use AgentHarness\EventType;
use AgentHarness\StreamConfig;

$progressEvent = new EventType(
    name: 'progress',
    description: 'Report task progress',
    schema: ['percent' => 'integer', 'message' => 'string'],
);
$agent->registerEvent($progressEvent);
$agent->defaultEvents = ['progress'];
```

## Streaming Events

For events that deliver content incrementally, attach a `StreamConfig`:

```php
$codeEvent = new EventType(
    name: 'code_output',
    description: 'Stream generated code',
    schema: ['language' => 'string', 'code' => 'string'],
    streaming: new StreamConfig(mode: 'streaming', streamFields: ['code']),
);
```

Streaming uses PHP Generators. The parser's `wrap()` method returns a
`Generator` that yields clean text while extracting events. For streaming
events, the event's `stream` property is also a `Generator` that yields content
as it becomes available.

## Message Bus

Subscribe to events by name, or use `*` as a wildcard:

```php
use AgentHarness\MessageBus;

$agent->getBus()->subscribe('progress', function ($event, $bus) {
    echo "Progress: {$event->data['percent']}%\n";
});
$agent->getBus()->subscribe('*', function ($event, $bus) {
    echo "Any event: {$event->type}\n";
});
```

Dispatch is synchronous and sequential. Cycle detection uses a depth counter
with a default maximum of 10 (configurable).

## Key Patterns

- **Native traits.** PHP's `trait` keyword provides built-in composition -- no
  workarounds needed.
- **Synchronous execution.** No async runtime; everything runs sequentially.
  Hook dispatch is sequential, not concurrent like the Python or TypeScript
  implementations.
- **Generator-based streaming.** `Generator` is PHP's ecosystem-standard
  pattern for lazy iteration, consistent with openai-php/client, Laravel AI
  SDK, and Prism PHP.
- **Custom YAML parser.** `parseSimpleYaml()` handles the specific YAML subset
  needed (key-value pairs, one level of nesting) without requiring the pecl
  yaml extension or the `symfony/yaml` dependency.
- **String-backed enum.** `HookEvent` is `enum HookEvent: string` -- PHP's
  native typed enumeration.

## Virtual Shell

The `HasShell` trait provides an in-memory virtual filesystem and shell interpreter. Mount context as files and let the agent explore with standard Unix commands. The shell supports 30 built-in commands, control flow (`if/elif/else`, `for`, `while`, `case/esac`), logical operators (`&&`, `||`), variable assignment, command substitution `$(...)`, arithmetic `$((...))`, parameter expansion (`${var:-default}`, `${#var}`, etc.), `test`/`[`/`[[`, and `printf`.

### Standalone usage

```php
use AgentHarness\VirtualFS;
use AgentHarness\Shell;

$fs = new VirtualFS();
$fs->write('/data/users.json', json_encode($users));
$shell = new Shell($fs);
$result = $shell->exec('cat /data/users.json | jq ".[].name" | sort');
echo $result->stdout;
```

### With an agent

```php
class MyAgent extends BaseAgent {
    use UsesTools;
    use HasShell;
}

$agent = new MyAgent(model: 'gpt-4o');
$agent->fs()->write('/data/schema.yaml', $schemaContent);
$result = $agent->run('What tables reference user_id?');
```

### Shell registry

```php
use AgentHarness\ShellRegistry;
use AgentHarness\Shell;
use AgentHarness\VirtualFS;

ShellRegistry::register('data-explorer', new Shell(
    fs: new VirtualFS(['/schema/users.yaml' => $schema]),
    allowedCommands: ['cat', 'grep', 'find', 'ls', 'jq', 'head', 'tail', 'wc'],
));

$agent = new MyAgent(model: 'gpt-4o', shell: 'data-explorer');
$agent->fs()->write('/data/results.json', $results);  // only this agent sees this
```

### Custom commands

Register domain-specific commands that work like built-ins — composable with pipes, redirects, and control flow:

```php
use AgentHarness\Shell;
use AgentHarness\ExecResult;
use AgentHarness\VirtualFS;

$shell = new Shell(fs: new VirtualFS());

$shell->registerCommand('deploy', function (array $args, string $stdin): ExecResult {
    return new ExecResult(stdout: "Deployed {$args[0]} to " . ($args[1] ?? 'production') . "\n");
});

$shell->exec('deploy my-app staging');

// With an agent — delegates to the underlying shell
$agent->registerCommand('validate', function (array $args, string $stdin): ExecResult {
    $valid = isValid($stdin);
    return new ExecResult(stdout: $valid ? "ok\n" : "invalid\n", exitCode: $valid ? 0 : 1);
});

// Unregister when no longer needed
$shell->unregisterCommand('deploy');

// Built-ins cannot be unregistered
$shell->unregisterCommand('echo'); // throws RuntimeException
```

Custom commands survive `cloneShell()` and `ShellRegistry::get()`, so registry templates can include domain commands.

### Shell hooks

When `HasHooks` is also composed, shell operations emit lifecycle hooks:

```php
use AgentHarness\HookEvent;

$agent->on(HookEvent::ShellCall, function (string $cmd) {
    echo "Executing: {$cmd}\n";
});
$agent->on(HookEvent::ShellNotFound, function (string $name) {
    echo "Unknown: {$name}\n";
});
$agent->on(HookEvent::ShellCwd, function (string $old, string $new) {
    echo "cd {$old} -> {$new}\n";
});
```

See [ADR 0012](../adr/0012-virtual-shell-architecture.md) and [ADR 0021](../adr/0021-custom-command-registration.md) for architecture details.

## Skills

The `HasSkills` trait enables mountable capability bundles that combine tools, instructions, middleware, hooks, and lifecycle management into a single unit.

### Defining a skill

```php
use AgentHarness\Skill;
use AgentHarness\SkillContext;

class WebBrowsingSkill extends Skill
{
    public string $name = 'web_browsing';
    public string $description = 'Browse the web and extract content';
    public string $version = '1.0.0';
    public string $instructions = 'You can browse the web using the fetch_page tool.';

    public function setup(SkillContext $ctx): void
    {
        $ctx->client = new \GuzzleHttp\Client();
    }

    public function teardown(SkillContext $ctx): void
    {
        // cleanup
    }

    public function tools(): array { return [$this->fetchPageTool()]; }
    public function middleware(): array { return []; }
    public function hooks(): array { return []; }
    public function commands(): array { return []; }
}
```

### Mounting skills

```php
$agent->mount(new WebBrowsingSkill());
```

Mounting a skill resolves dependencies transitively, runs `setup()`, and registers all tools, middleware, hooks, and commands.

### Unmounting skills

```php
$agent->unmount('web_browsing');
```

Unmounting runs `teardown()` and removes all tools, middleware, and hooks associated with the skill.

### SkillPromptMiddleware

Middleware that auto-injects mounted skill instructions into the system prompt:

```php
use AgentHarness\SkillPromptMiddleware;

$agent->use(new SkillPromptMiddleware());
```

### Skill hooks

When `HasHooks` is also composed, skill operations emit lifecycle hooks:

```php
use AgentHarness\HookEvent;

$agent->on(HookEvent::SkillMount, function (Skill $skill) {
    echo "Mounted: {$skill->name}\n";
});
$agent->on(HookEvent::SkillSetup, function (Skill $skill) {
    echo "Setting up: {$skill->name}\n";
});
```

See [ADR 0024](../adr/0024-has-skills-mixin.md) for design details.
