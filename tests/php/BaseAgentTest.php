<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BaseAgent;
use AgentHarness\RunContext;
use PHPUnit\Framework\TestCase;

class BaseAgentTest extends TestCase
{
    public function testInitDefaults(): void
    {
        $agent = new BaseAgent(model: 'gpt-4');
        $this->assertSame('gpt-4', $agent->model);
        $this->assertSame(20, $agent->maxTurns);
        $this->assertSame(2, $agent->maxRetries);
        $this->assertTrue($agent->stream);
    }

    public function testInitCustom(): void
    {
        $agent = new BaseAgent(
            model: 'claude-3-opus',
            system: 'You are helpful.',
            maxTurns: 5,
            maxRetries: 0,
            stream: false,
        );
        $this->assertSame('claude-3-opus', $agent->model);
        $this->assertSame('You are helpful.', $agent->system);
        $this->assertSame(5, $agent->maxTurns);
    }

    public function testBuildSystemPrompt(): void
    {
        $agent = new BaseAgent(model: 'gpt-4', system: 'Be helpful.');
        // Use reflection to call protected method
        $method = new \ReflectionMethod($agent, 'buildSystemPrompt');
        $result = $method->invoke($agent, 'Be helpful.', null);
        $this->assertSame('Be helpful.', $result);
    }

    public function testRunContextCreation(): void
    {
        $agent = new BaseAgent(model: 'gpt-4');
        $ctx = new RunContext(agent: $agent, turn: 0, metadata: []);
        $this->assertSame($agent, $ctx->agent);
        $this->assertSame(0, $ctx->turn);
    }
}
