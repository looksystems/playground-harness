<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use PHPUnit\Framework\TestCase;
use AgentHarness\StandardAgent;
use AgentHarness\AgentBuilder;
use AgentHarness\HookEvent;
use AgentHarness\StructuredEvent;
use AgentHarness\Skill;
use AgentHarness\SkillContext;
use AgentHarness\ToolDef;
use AgentHarness\BaseMiddleware;
use AgentHarness\ExecResult;

// ---- Test doubles ----

class NoopMiddleware extends BaseMiddleware
{
}

class StubSkill extends Skill
{
    public string $description = 'A stub skill for testing';
}

// ---- AgentBuilderTest ----

class AgentBuilderTest extends TestCase
{
    private function dummyTool(string $name = 'echo'): ToolDef
    {
        return ToolDef::make(
            name: $name,
            description: 'echo tool',
            parameters: ['type' => 'object', 'properties' => []],
            execute: fn(array $args) => 'ok',
        );
    }

    public function testBuildReturnsBuilder(): void
    {
        $builder = StandardAgent::build('gpt-4');
        $this->assertInstanceOf(AgentBuilder::class, $builder);
    }

    public function testCreateReturnsAgent(): void
    {
        $agent = StandardAgent::build('gpt-4')->create();
        $this->assertInstanceOf(StandardAgent::class, $agent);
    }

    public function testSystemPrompt(): void
    {
        $agent = StandardAgent::build('gpt-4')
            ->system('You are helpful.')
            ->create();
        $this->assertSame('You are helpful.', $agent->system);
    }

    public function testMaxTurns(): void
    {
        $agent = StandardAgent::build('gpt-4')
            ->maxTurns(5)
            ->create();
        $this->assertSame(5, $agent->maxTurns);
    }

    public function testToolsRegistered(): void
    {
        $tool = $this->dummyTool();
        $agent = StandardAgent::build('gpt-4')
            ->tool($tool)
            ->create();

        $tools = $agent->getTools();
        $this->assertArrayHasKey('echo', $tools);
        $this->assertSame($tool, $tools['echo']);
    }

    public function testMiddlewareRegistered(): void
    {
        $mw = new NoopMiddleware();
        $agent = StandardAgent::build('gpt-4')
            ->middleware($mw)
            ->create();

        $stack = $agent->getMiddleware();
        $this->assertCount(1, $stack);
        $this->assertSame($mw, $stack[0]);
    }

    public function testHooksRegistered(): void
    {
        $called = false;
        $agent = StandardAgent::build('gpt-4')
            ->on(HookEvent::RunStart, function () use (&$called) {
                $called = true;
            })
            ->create();

        $hooks = $agent->getHooks();
        $this->assertArrayHasKey(HookEvent::RunStart->value, $hooks);
        $this->assertCount(1, $hooks[HookEvent::RunStart->value]);

        $agent->emit(HookEvent::RunStart);
        $this->assertTrue($called);
    }

    public function testEventsRegistered(): void
    {
        $ev = new StructuredEvent(
            name: 'test_event',
            description: 'A test event',
            schema: ['msg' => 'string'],
        );
        $agent = StandardAgent::build('gpt-4')
            ->event($ev)
            ->create();

        $events = $agent->getEvents();
        $this->assertArrayHasKey('test_event', $events);
        $this->assertSame($ev, $events['test_event']);
    }

    public function testSkillsMounted(): void
    {
        $skill = new StubSkill();
        $agent = StandardAgent::build('gpt-4')
            ->skill($skill)
            ->create();

        $skills = $agent->skills();
        $this->assertArrayHasKey('stub', $skills);
        $this->assertSame($skill, $skills['stub']);
    }

    public function testShellConfigured(): void
    {
        $agent = StandardAgent::build('gpt-4')
            ->shell()
            ->create();

        $shell = $agent->shell();
        $this->assertNotNull($shell);
    }

    public function testCommandRegistered(): void
    {
        $agent = StandardAgent::build('gpt-4')
            ->shell()
            ->command('hello', fn(array $args, string $stdin = '') => new ExecResult(stdout: 'world'))
            ->create();

        $result = $agent->execCommand('hello');
        $this->assertSame('world', $result->stdout);
    }

    public function testFluentChaining(): void
    {
        $builder = StandardAgent::build('gpt-4');

        $this->assertSame($builder, $builder->system('test'));
        $this->assertSame($builder, $builder->maxTurns(10));
        $this->assertSame($builder, $builder->maxRetries(3));
        $this->assertSame($builder, $builder->stream(false));
        $this->assertSame($builder, $builder->baseUrl('http://localhost'));
        $this->assertSame($builder, $builder->apiKey('sk-test'));
        $this->assertSame($builder, $builder->completionParams(['temperature' => 0.5]));
        $this->assertSame($builder, $builder->tool($this->dummyTool()));
        $this->assertSame($builder, $builder->tools($this->dummyTool('t1'), $this->dummyTool('t2')));
        $this->assertSame($builder, $builder->middleware(new NoopMiddleware()));
        $this->assertSame($builder, $builder->on(HookEvent::RunStart, fn() => null));
        $this->assertSame($builder, $builder->event(new StructuredEvent(
            name: 'e',
            description: 'd',
            schema: [],
        )));
        $this->assertSame($builder, $builder->events());
        $this->assertSame($builder, $builder->skill(new StubSkill()));
        $this->assertSame($builder, $builder->skills());
        $this->assertSame($builder, $builder->shell());
        $this->assertSame($builder, $builder->command('x', fn() => 'y'));
    }
}
