<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use PHPUnit\Framework\TestCase;
use AgentHarness\StandardAgent;
use AgentHarness\HookEvent;

class RemoveHookTest extends TestCase
{
    public function testRemoveHookRemovesCallback(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $count = 0;
        $cb = function () use (&$count) {
            $count++;
        };

        $agent->on(HookEvent::RunStart, $cb);
        $agent->emit(HookEvent::RunStart);
        $this->assertSame(1, $count);

        $agent->removeHook(HookEvent::RunStart, $cb);
        $agent->emit(HookEvent::RunStart);
        $this->assertSame(1, $count, 'Callback should not fire after removeHook()');
    }

    public function testRemoveHookReturnsSelf(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $cb = fn() => null;
        $agent->on(HookEvent::RunStart, $cb);
        $result = $agent->removeHook(HookEvent::RunStart, $cb);
        $this->assertSame($agent, $result);
    }

    public function testRemoveHookNonexistentIsNoop(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $result = $agent->removeHook(HookEvent::RunStart, fn() => null);
        $this->assertSame($agent, $result);
    }
}
