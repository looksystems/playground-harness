<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\MessageBus;
use AgentHarness\ParsedEvent;
use PHPUnit\Framework\TestCase;

class MessageBusTest extends TestCase
{
    public function testSubscribeAndPublish(): void
    {
        $bus = new MessageBus();
        $received = [];
        $bus->subscribe('greeting', function (ParsedEvent $event, MessageBus $bus) use (&$received) {
            $received[] = $event->type;
        });
        $event = new ParsedEvent(type: 'greeting', data: ['msg' => 'hi']);
        $bus->publish($event);
        $this->assertSame(['greeting'], $received);
    }

    public function testWildcardSubscriber(): void
    {
        $bus = new MessageBus();
        $received = [];
        $bus->subscribe('*', function (ParsedEvent $event, MessageBus $bus) use (&$received) {
            $received[] = $event->type;
        });
        $bus->publish(new ParsedEvent(type: 'a', data: []));
        $bus->publish(new ParsedEvent(type: 'b', data: []));
        $this->assertSame(['a', 'b'], $received);
    }

    public function testMultipleHandlers(): void
    {
        $bus = new MessageBus();
        $received = [];
        $bus->subscribe('test', function () use (&$received) {
            $received[] = 'h1';
        });
        $bus->subscribe('test', function () use (&$received) {
            $received[] = 'h2';
        });
        $bus->publish(new ParsedEvent(type: 'test', data: []));
        $this->assertEqualsCanonicalizing(['h1', 'h2'], $received);
    }

    public function testHandlerCanPublish(): void
    {
        $bus = new MessageBus();
        $received = [];
        $bus->subscribe('first', function (ParsedEvent $event, MessageBus $bus) use (&$received) {
            $received[] = $event->type;
            if ($event->type === 'first') {
                $bus->publish(new ParsedEvent(type: 'second', data: []));
            }
        });
        $bus->subscribe('second', function (ParsedEvent $event, MessageBus $bus) use (&$received) {
            $received[] = $event->type;
        });
        $bus->publish(new ParsedEvent(type: 'first', data: []));
        $this->assertSame(['first', 'second'], $received);
    }

    public function testCycleDetection(): void
    {
        $bus = new MessageBus(maxDepth: 3);
        $callCount = 0;
        $bus->subscribe('loop', function (ParsedEvent $event, MessageBus $bus) use (&$callCount) {
            $callCount++;
            $bus->publish(new ParsedEvent(type: 'loop', data: []));
        });
        $bus->publish(new ParsedEvent(type: 'loop', data: []));
        $this->assertLessThanOrEqual(3, $callCount);
    }

    public function testHandlerErrorDoesNotPropagate(): void
    {
        $bus = new MessageBus();
        $received = [];
        $bus->subscribe('test', function () {
            throw new \RuntimeException('boom');
        });
        $bus->subscribe('test', function () use (&$received) {
            $received[] = 'ok';
        });
        $bus->publish(new ParsedEvent(type: 'test', data: []));
        $this->assertSame(['ok'], $received);
    }

    public function testNoSubscribers(): void
    {
        $bus = new MessageBus();
        // Should not throw
        $bus->publish(new ParsedEvent(type: 'orphan', data: []));
        $this->assertTrue(true);
    }
}
