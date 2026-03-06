<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\EmitsEvents;
use AgentHarness\StructuredEvent;
use AgentHarness\MessageBus;
use AgentHarness\StreamConfig;
use PHPUnit\Framework\TestCase;

class EventEmitter
{
    use EmitsEvents;
}

class EmitsEventsTest extends TestCase
{
    public function testRegisterEvent(): void
    {
        $obj = new EventEmitter();
        $et = new StructuredEvent(name: 'test', description: 'a test', schema: []);
        $obj->registerEvent($et);
        // Verify via resolveActiveEvents
        $obj->defaultEvents = ['test'];
        $active = $obj->resolveActiveEvents();
        $this->assertCount(1, $active);
        $this->assertSame('test', $active[0]->name);
    }

    public function testDefaultEvents(): void
    {
        $obj = new EventEmitter();
        $obj->defaultEvents = ['test'];
        $et = new StructuredEvent(name: 'test', description: 'a test', schema: []);
        $obj->registerEvent($et);
        $active = $obj->resolveActiveEvents();
        $this->assertCount(1, $active);
        $this->assertSame('test', $active[0]->name);
    }

    public function testOverrideEventsPerRun(): void
    {
        $obj = new EventEmitter();
        $et1 = new StructuredEvent(name: 'a', description: '', schema: []);
        $et2 = new StructuredEvent(name: 'b', description: '', schema: []);
        $obj->registerEvent($et1);
        $obj->registerEvent($et2);
        $obj->defaultEvents = ['a', 'b'];
        $active = $obj->resolveActiveEvents(events: ['a']);
        $this->assertCount(1, $active);
        $this->assertSame('a', $active[0]->name);
    }

    public function testAdhocEvent(): void
    {
        $obj = new EventEmitter();
        $obj->defaultEvents = [];
        $adhoc = new StructuredEvent(name: 'adhoc', description: 'inline', schema: []);
        $active = $obj->resolveActiveEvents(events: [$adhoc]);
        $this->assertCount(1, $active);
        $this->assertSame('adhoc', $active[0]->name);
    }

    public function testMixedRegisteredAndAdhoc(): void
    {
        $obj = new EventEmitter();
        $registered = new StructuredEvent(name: 'reg', description: '', schema: []);
        $obj->registerEvent($registered);
        $adhoc = new StructuredEvent(name: 'adhoc', description: '', schema: []);
        $active = $obj->resolveActiveEvents(events: ['reg', $adhoc]);
        $this->assertCount(2, $active);
    }

    public function testBusExists(): void
    {
        $obj = new EventEmitter();
        $this->assertNotNull($obj->getBus());
        $this->assertInstanceOf(MessageBus::class, $obj->getBus());
    }

    public function testBuildEventPrompt(): void
    {
        $obj = new EventEmitter();
        $et = new StructuredEvent(
            name: 'user_response',
            description: 'Send a message to the user',
            schema: ['data' => ['message' => 'string']],
            instructions: 'Always use this for replies.',
        );
        $obj->registerEvent($et);
        $prompt = $obj->buildEventPrompt([$et]);
        $this->assertStringContainsString('user_response', $prompt);
        $this->assertStringContainsString('---event', $prompt);
        $this->assertStringContainsString('Always use this for replies.', $prompt);
    }
}
