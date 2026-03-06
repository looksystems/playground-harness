<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use PHPUnit\Framework\TestCase;
use AgentHarness\StandardAgent;
use AgentHarness\HookEvent;

class OffTest extends TestCase
{
    public function testOffRemovesCallback(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $count = 0;
        $cb = function () use (&$count) {
            $count++;
        };

        $agent->on(HookEvent::RunStart, $cb);
        $agent->emit(HookEvent::RunStart);
        $this->assertSame(1, $count);

        $agent->off(HookEvent::RunStart, $cb);
        $agent->emit(HookEvent::RunStart);
        $this->assertSame(1, $count, 'Callback should not fire after off()');
    }

    public function testOffReturnsSelf(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $cb = fn() => null;
        $agent->on(HookEvent::RunStart, $cb);
        $result = $agent->off(HookEvent::RunStart, $cb);
        $this->assertSame($agent, $result);
    }

    public function testOffNonexistentIsNoop(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $result = $agent->off(HookEvent::RunStart, fn() => null);
        $this->assertSame($agent, $result);
    }
}
