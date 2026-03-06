<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\CommandDef;
use AgentHarness\HasCommands;
use AgentHarness\HasHooks;
use AgentHarness\HookEvent;
use AgentHarness\SlashCommandMiddleware;
use AgentHarness\UsesTools;
use AgentHarness\ToolDef;
use PHPUnit\Framework\TestCase;

class CommandsOnly
{
    use HasCommands;
}

class CommandsWithTools
{
    use HasCommands;
    use UsesTools;
}

class HookCommandsAgent
{
    use HasCommands;
    use UsesTools;
    use HasHooks;
}

class HasCommandsTest extends TestCase
{
    // ── Standalone ──────────────────────────────────────────────

    public function testRegisterCommandDef(): void
    {
        $obj = new CommandsOnly();
        $def = CommandDef::make(
            name: 'help',
            description: 'Show help',
            parameters: [],
            execute: fn(array $args) => 'help text',
        );
        $obj->registerSlashCommand($def);
        $this->assertArrayHasKey('help', $obj->commands());
        $this->assertSame($def, $obj->commands()['help']);
    }

    public function testUnregisterCommand(): void
    {
        $obj = new CommandsOnly();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Show help',
            parameters: [],
            execute: fn(array $args) => 'help text',
        ));
        $this->assertArrayHasKey('help', $obj->commands());
        $obj->unregisterSlashCommand('help');
        $this->assertArrayNotHasKey('help', $obj->commands());
    }

    public function testExecuteCommand(): void
    {
        $obj = new CommandsOnly();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'greet',
            description: 'Greet',
            parameters: [],
            execute: fn(array $args) => 'Hello ' . ($args['name'] ?? 'World'),
        ));
        $result = $obj->executeSlashCommand('greet', ['name' => 'Alice']);
        $this->assertSame('Hello Alice', $result);
    }

    public function testExecuteUnknownCommand(): void
    {
        $obj = new CommandsOnly();
        $result = $obj->executeSlashCommand('nope', []);
        $this->assertStringContainsString('Unknown', $result);
    }

    public function testInterceptSlashCommand(): void
    {
        $obj = new CommandsOnly();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Help',
            parameters: [],
            execute: fn(array $args) => 'ok',
        ));
        $result = $obj->interceptSlashCommand('/help some args');
        $this->assertIsArray($result);
        $this->assertSame('help', $result[0]);
        $this->assertSame(['input' => 'some args'], $result[1]);
    }

    public function testInterceptNonSlash(): void
    {
        $obj = new CommandsOnly();
        $result = $obj->interceptSlashCommand('hello');
        $this->assertNull($result);
    }

    public function testInterceptWithSchema(): void
    {
        $obj = new CommandsOnly();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'greet',
            description: 'Greet someone',
            parameters: [
                'type' => 'object',
                'properties' => [
                    'name' => ['type' => 'string'],
                ],
            ],
            execute: fn(array $args) => 'Hi ' . ($args['name'] ?? ''),
        ));
        $result = $obj->interceptSlashCommand('/greet name=World');
        $this->assertIsArray($result);
        $this->assertSame('greet', $result[0]);
        $this->assertSame(['name' => 'World'], $result[1]);
    }

    public function testCommandsProperty(): void
    {
        $obj = new CommandsOnly();
        $this->assertIsArray($obj->commands());
        $this->assertEmpty($obj->commands());
    }

    public function testLazyInit(): void
    {
        $obj = new CommandsOnly();
        // Calling commands() without explicit init should work
        $cmds = $obj->commands();
        $this->assertIsArray($cmds);
    }

    // ── With UsesTools ─────────────────────────────────────────

    public function testAutoRegistersTool(): void
    {
        $obj = new CommandsWithTools();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Show help',
            parameters: ['type' => 'object', 'properties' => ['topic' => ['type' => 'string']]],
            execute: fn(array $args) => 'help text',
            llmVisible: true,
        ));
        $schema = $obj->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertContains('slash_help', $names);
    }

    public function testNoToolWhenNotVisible(): void
    {
        $obj = new CommandsWithTools();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'secret',
            description: 'Secret cmd',
            parameters: [],
            execute: fn(array $args) => 'shh',
            llmVisible: false,
        ));
        $schema = $obj->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertNotContains('slash_secret', $names);
    }

    public function testAgentLevelDisable(): void
    {
        $obj = new CommandsWithTools();
        $obj->initHasCommands(llmCommandsEnabled: false);
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Show help',
            parameters: [],
            execute: fn(array $args) => 'help',
            llmVisible: true,
        ));
        $schema = $obj->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertNotContains('slash_help', $names);
    }

    public function testUnregisterRemovesTool(): void
    {
        $obj = new CommandsWithTools();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Show help',
            parameters: [],
            execute: fn(array $args) => 'help',
        ));
        $schema = $obj->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertContains('slash_help', $names);

        $obj->unregisterSlashCommand('help');
        $schema = $obj->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertNotContains('slash_help', $names);
    }

    public function testToolExecutesCommand(): void
    {
        $obj = new CommandsWithTools();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'ping',
            description: 'Ping',
            parameters: ['type' => 'object', 'properties' => new \stdClass()],
            execute: fn(array $args) => 'pong',
        ));
        $result = $obj->executeTool('slash_ping', []);
        $decoded = json_decode($result, true);
        $this->assertSame('pong', $decoded);
    }

    // ── With HasHooks ──────────────────────────────────────────

    public function testRegisterHook(): void
    {
        $obj = new HookCommandsAgent();
        $fired = [];
        $obj->on(HookEvent::SlashCommandRegister, function (CommandDef $def) use (&$fired) {
            $fired[] = $def->name;
        });
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Help',
            parameters: [],
            execute: fn(array $args) => 'ok',
        ));
        $this->assertSame(['help'], $fired);
    }

    public function testUnregisterHook(): void
    {
        $obj = new HookCommandsAgent();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Help',
            parameters: [],
            execute: fn(array $args) => 'ok',
        ));
        $fired = [];
        $obj->on(HookEvent::SlashCommandUnregister, function (string $name) use (&$fired) {
            $fired[] = $name;
        });
        $obj->unregisterSlashCommand('help');
        $this->assertSame(['help'], $fired);
    }

    public function testCallHook(): void
    {
        $obj = new HookCommandsAgent();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'test',
            description: 'Test',
            parameters: [],
            execute: fn(array $args) => 'done',
        ));
        $fired = [];
        $obj->on(HookEvent::SlashCommandCall, function (string $name, array $args) use (&$fired) {
            $fired[] = ['name' => $name, 'args' => $args];
        });
        $obj->executeSlashCommand('test', ['key' => 'val']);
        $this->assertCount(1, $fired);
        $this->assertSame('test', $fired[0]['name']);
        $this->assertSame(['key' => 'val'], $fired[0]['args']);
    }

    public function testResultHook(): void
    {
        $obj = new HookCommandsAgent();
        $obj->registerSlashCommand(CommandDef::make(
            name: 'test',
            description: 'Test',
            parameters: [],
            execute: fn(array $args) => 'result-value',
        ));
        $fired = [];
        $obj->on(HookEvent::SlashCommandResult, function (string $name, string $result) use (&$fired) {
            $fired[] = ['name' => $name, 'result' => $result];
        });
        $obj->executeSlashCommand('test', []);
        $this->assertCount(1, $fired);
        $this->assertSame('test', $fired[0]['name']);
        $this->assertSame('result-value', $fired[0]['result']);
    }

    // ── Middleware ──────────────────────────────────────────────

    public function testInterceptsSlashMessage(): void
    {
        $agent = new HookCommandsAgent();
        $agent->registerSlashCommand(CommandDef::make(
            name: 'help',
            description: 'Help',
            parameters: [],
            execute: fn(array $args) => 'help output',
        ));

        $middleware = new SlashCommandMiddleware();
        $messages = [
            ['role' => 'user', 'content' => '/help'],
        ];
        $result = $middleware->pre($messages, $agent);
        $this->assertStringContainsString('help output', $result[0]['content']);
        $this->assertStringContainsString('/help', $result[0]['content']);
    }

    public function testPassesThroughRegular(): void
    {
        $agent = new HookCommandsAgent();
        $middleware = new SlashCommandMiddleware();
        $messages = [
            ['role' => 'user', 'content' => 'Hello world'],
        ];
        $result = $middleware->pre($messages, $agent);
        $this->assertSame($messages, $result);
    }
}
