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

Under the hood, `StandardAgent` composes all four traits:

```php
class StandardAgent extends BaseAgent
{
    use HasHooks;
    use HasMiddleware;
    use UsesTools;
    use EmitsEvents;
}
```

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

`HookEvent` is a string-backed enum with 10 cases. Because PHP is synchronous,
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

See [ADR 0012](../adr/0012-virtual-shell-architecture.md) for architecture details.
