<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BaseMiddleware as AgentBaseMiddleware;
use AgentHarness\EventType;
use AgentHarness\HookEvent;
use AgentHarness\StandardAgent;
use AgentHarness\ToolDef;
use PHPUnit\Framework\TestCase;

class IntegrationTest extends TestCase
{
    public function testStandardAgentWithEvents(): void
    {
        $agent = new StandardAgent(model: 'gpt-4');

        $agent->registerEvent(new EventType(
            name: 'user_response',
            description: 'Respond to user',
            schema: ['data' => ['message' => 'string']],
        ));
        $agent->defaultEvents = ['user_response'];

        // Hook
        $hookLog = [];
        $agent->on(HookEvent::RunStart, function () use (&$hookLog) {
            $hookLog[] = 'start';
        });

        // Tool
        $addTool = ToolDef::make('add', 'Add numbers', [
            'type' => 'object',
            'properties' => [
                'a' => ['type' => 'integer'],
                'b' => ['type' => 'integer'],
            ],
        ], fn(array $args) => $args['a'] + $args['b']);
        $agent->registerTool($addTool);

        // Verify tool registered
        $this->assertCount(1, $agent->toolsSchema());

        // Verify bus
        $busEvents = [];
        $agent->getBus()->subscribe('user_response', function ($e, $b) use (&$busEvents) {
            $busEvents[] = $e;
        });

        // Verify active events
        $active = $agent->resolveActiveEvents();
        $this->assertCount(1, $active);

        // Verify event prompt
        $prompt = $agent->buildEventPrompt($active);
        $this->assertStringContainsString('user_response', $prompt);

        // Verify hooks fire
        $agent->emit(HookEvent::RunStart);
        $this->assertSame(['start'], $hookLog);
    }

    public function testStandardAgentWithShell(): void
    {
        $agent = new StandardAgent(model: 'gpt-4');

        // Shell is available via lazy init
        $this->assertNotNull($agent->shell());
        $this->assertNotNull($agent->fs());

        // Can write files and exec commands
        $agent->fs()->write('/data/test.txt', "hello world\n");
        $result = $agent->execCommand('cat /data/test.txt');
        $this->assertSame("hello world\n", $result->stdout);

        // exec tool is auto-registered
        $schema = $agent->toolsSchema();
        $toolNames = array_column(array_column($schema, 'function'), 'name');
        $this->assertContains('exec', $toolNames);

        // Pipes work
        $agent->fs()->write('/data/nums.txt', "3\n1\n2\n");
        $result2 = $agent->execCommand('cat /data/nums.txt | sort');
        $this->assertSame("1\n2\n3\n", $result2->stdout);
    }
}
