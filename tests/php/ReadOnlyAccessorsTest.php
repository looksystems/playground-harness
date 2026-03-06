<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use PHPUnit\Framework\TestCase;
use AgentHarness\StandardAgent;
use AgentHarness\HookEvent;
use AgentHarness\StructuredEvent;
use AgentHarness\ToolDef;
use AgentHarness\BaseMiddleware;

class ReadOnlyAccessorNoopMiddleware extends BaseMiddleware
{
}

class ReadOnlyAccessorsTest extends TestCase
{
    public function testGetToolsReturnsCopy(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $tool = ToolDef::make(
            name: 'ping',
            description: 'ping',
            parameters: ['type' => 'object', 'properties' => []],
            execute: fn(array $a) => 'pong',
        );
        $agent->registerTool($tool);

        $tools = $agent->getTools();
        $tools['ping'] = null;
        $this->assertNotNull($agent->getTools()['ping']);
    }

    public function testGetHooksReturnsCopy(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $cb = fn() => null;
        $agent->on(HookEvent::RunStart, $cb);

        $hooks = $agent->getHooks();
        $hooks[HookEvent::RunStart->value][] = fn() => null;

        $this->assertCount(1, $agent->getHooks()[HookEvent::RunStart->value]);
    }

    public function testGetMiddlewareReturnsCopy(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $mw = new ReadOnlyAccessorNoopMiddleware();
        $agent->use($mw);

        $stack = $agent->getMiddleware();
        $stack[] = new ReadOnlyAccessorNoopMiddleware();

        $this->assertCount(1, $agent->getMiddleware());
    }

    public function testGetEventsReturnsCopy(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $ev = new StructuredEvent(
            name: 'test',
            description: 'desc',
            schema: [],
        );
        $agent->registerEvent($ev);

        $events = $agent->getEvents();
        $events['test'] = null;

        $this->assertNotNull($agent->getEvents()['test']);
    }
}
