<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use PHPUnit\Framework\TestCase;
use AgentHarness\StandardAgent;
use AgentHarness\StructuredEvent;

class UnregisterEventTest extends TestCase
{
    public function testUnregisterEvent(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $ev = new StructuredEvent(
            name: 'my_event',
            description: 'desc',
            schema: [],
        );

        $agent->registerEvent($ev);
        $this->assertArrayHasKey('my_event', $agent->getEvents());

        $agent->unregisterEvent('my_event');
        $this->assertArrayNotHasKey('my_event', $agent->getEvents());
    }

    public function testUnregisterEventReturnsSelf(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $result = $agent->unregisterEvent('nonexistent');
        $this->assertSame($agent, $result);
    }
}
