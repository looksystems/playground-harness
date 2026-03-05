<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\HasHooks;
use AgentHarness\HookEvent;
use PHPUnit\Framework\TestCase;

class HookUser
{
    use HasHooks;
}

class HasHooksTest extends TestCase
{
    public function testSubscribeAndEmit(): void
    {
        $obj = new HookUser();
        $received = [];
        $obj->on(HookEvent::RunStart, function () use (&$received) {
            $received[] = 'started';
        });
        $obj->emit(HookEvent::RunStart);
        $this->assertSame(['started'], $received);
    }

    public function testEmitWithArgs(): void
    {
        $obj = new HookUser();
        $received = [];
        $obj->on(HookEvent::ToolCall, function (string $name, array $args) use (&$received) {
            $received[] = [$name, $args];
        });
        $obj->emit(HookEvent::ToolCall, 'add', ['a' => 1]);
        $this->assertSame([['add', ['a' => 1]]], $received);
    }

    public function testMultipleHooksSameEvent(): void
    {
        $obj = new HookUser();
        $received = [];
        $obj->on(HookEvent::RunEnd, function () use (&$received) {
            $received[] = 'a';
        });
        $obj->on(HookEvent::RunEnd, function () use (&$received) {
            $received[] = 'b';
        });
        $obj->emit(HookEvent::RunEnd);
        $this->assertEqualsCanonicalizing(['a', 'b'], $received);
    }

    public function testHookErrorDoesNotPropagate(): void
    {
        $obj = new HookUser();
        $received = [];
        $obj->on(HookEvent::RunStart, function () {
            throw new \RuntimeException('boom');
        });
        $obj->on(HookEvent::RunStart, function () use (&$received) {
            $received[] = 'ok';
        });
        $obj->emit(HookEvent::RunStart);
        $this->assertSame(['ok'], $received);
    }

    public function testNoHooksRegistered(): void
    {
        $obj = new HookUser();
        // Should not throw
        $obj->emit(HookEvent::RunStart);
        $this->assertTrue(true);
    }

    public function testDifferentEventsAreIsolated(): void
    {
        $obj = new HookUser();
        $received = [];
        $obj->on(HookEvent::RunStart, function () use (&$received) {
            $received[] = 'start';
        });
        $obj->emit(HookEvent::RunEnd);
        $this->assertSame([], $received);
    }
}
