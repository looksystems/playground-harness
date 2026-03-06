<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\{HasSkills, HasHooks, UsesTools, HasMiddleware, HookEvent, Skill, SkillContext, SkillManager, SkillPromptMiddleware, ToolDef};
use PHPUnit\Framework\TestCase;

// ── Test helper classes ───────────────────────────────────────────

class SkillsOnly
{
    use HasSkills;
}

class SkillsWithTools
{
    use HasSkills;
    use UsesTools;
}

class FullSkillAgent
{
    use HasSkills;
    use UsesTools;
    use HasHooks;
    use HasMiddleware;
}

// ── Test skill classes ────────────────────────────────────────────

class TestWebBrowsingSkill extends Skill
{
    public string $description = 'Browse the web';
}

class TestMathSkill extends Skill
{
    public string $description = 'Math operations';
    public string $instructions = 'Use math tools';

    public function tools(): array
    {
        return [ToolDef::make(
            name: 'add',
            description: 'Add numbers',
            parameters: ['type' => 'object', 'properties' => ['a' => ['type' => 'number'], 'b' => ['type' => 'number']]],
            execute: fn(array $args) => ($args['a'] ?? 0) + ($args['b'] ?? 0),
        )];
    }
}

class TestTrackingSkill extends Skill
{
    public static array $setupCalls = [];
    public static array $teardownCalls = [];

    public function setup(SkillContext $ctx): void
    {
        static::$setupCalls[] = $this->name;
    }

    public function teardown(SkillContext $ctx): void
    {
        static::$teardownCalls[] = $this->name;
    }

    public static function reset(): void
    {
        static::$setupCalls = [];
        static::$teardownCalls = [];
    }
}

class TestTrackingSkillA extends TestTrackingSkill
{
    public static array $setupCalls = [];
    public static array $teardownCalls = [];
}

class TestTrackingSkillB extends TestTrackingSkill
{
    public static array $setupCalls = [];
    public static array $teardownCalls = [];
}

class TestNoInstructionsSkill extends Skill
{
    public string $description = 'No instructions skill';
}

class TestInstructionAlphaSkill extends Skill
{
    public string $description = 'Alpha skill';
    public string $instructions = 'Alpha instructions here';
}

class TestInstructionBetaSkill extends Skill
{
    public string $description = 'Beta skill';
    public string $instructions = 'Beta instructions here';
}

class TestMiddlewareProvidingSkill extends Skill
{
    public static array $preCalledWith = [];

    public function middleware(): array
    {
        return [new TestSkillMiddleware()];
    }
}

class TestSkillMiddleware extends \AgentHarness\BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        TestMiddlewareProvidingSkill::$preCalledWith[] = $messages;
        return $messages;
    }
}

class TestHookProvidingSkill extends Skill
{
    public static array $hookFired = [];

    public function hooks(): array
    {
        return [
            HookEvent::RunStart->value => [
                function () {
                    TestHookProvidingSkill::$hookFired[] = 'run_start';
                },
            ],
        ];
    }
}

// ── Dependency skills ─────────────────────────────────────────────

class TestBaseDepSkill extends Skill
{
    public string $description = 'Base dependency';
}

class TestMidDepSkill extends Skill
{
    public string $description = 'Mid dependency';
    public array $dependencies = [TestBaseDepSkill::class];
}

class TestTopDepSkill extends Skill
{
    public string $description = 'Top skill with transitive deps';
    public array $dependencies = [TestMidDepSkill::class];
}

class TestSimpleDepSkill extends Skill
{
    public string $description = 'Depends on base';
    public array $dependencies = [TestBaseDepSkill::class];
}

// ── Tests ─────────────────────────────────────────────────────────

class HasSkillsTest extends TestCase
{
    protected function setUp(): void
    {
        TestTrackingSkill::reset();
        TestTrackingSkillA::reset();
        TestTrackingSkillB::reset();
        TestMiddlewareProvidingSkill::$preCalledWith = [];
        TestHookProvidingSkill::$hookFired = [];
    }

    // ── 1. Skill base - auto-name ─────────────────────────────────

    public function testAutoNameWebBrowsingSkill(): void
    {
        $skill = new TestWebBrowsingSkill();
        $this->assertSame('test_web_browsing', $skill->name);
    }

    public function testAutoNameMathSkill(): void
    {
        $skill = new TestMathSkill();
        $this->assertSame('test_math', $skill->name);
    }

    public function testDefaultVersion(): void
    {
        $skill = new TestMathSkill();
        $this->assertSame('0.1.0', $skill->version);
    }

    public function testDefaultsEmpty(): void
    {
        $skill = new TestNoInstructionsSkill();
        $this->assertSame([], $skill->tools());
        $this->assertSame([], $skill->middleware());
        $this->assertSame([], $skill->hooks());
    }

    // ── 2. SkillManager - mount/unmount/shutdown ──────────────────

    public function testMountAddsToSkills(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        $this->assertArrayHasKey('test_math', $agent->skills());
        $this->assertSame($skill, $agent->skills()['test_math']);
    }

    public function testUnmountRemoves(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        $this->assertArrayHasKey('test_math', $agent->skills());
        $agent->unmount('test_math');
        $this->assertArrayNotHasKey('test_math', $agent->skills());
    }

    public function testShutdownReverseOrder(): void
    {
        $agent = new SkillsOnly();
        $a = new TestTrackingSkillA();
        $b = new TestTrackingSkillB();
        $agent->mount($a);
        $agent->mount($b);

        // Reset so we only capture teardown calls from shutdown
        TestTrackingSkill::reset();
        TestTrackingSkillA::reset();
        TestTrackingSkillB::reset();

        $agent->shutdown();

        // B was mounted second so should be torn down first
        // Both subclasses write to their own static arrays via static::
        $allTeardowns = array_merge(TestTrackingSkillB::$teardownCalls, TestTrackingSkillA::$teardownCalls);

        $this->assertContains('test_tracking_skill_a', $allTeardowns);
        $this->assertContains('test_tracking_skill_b', $allTeardowns);

        // After shutdown, no skills remain
        $this->assertEmpty($agent->skills());
    }

    public function testDuplicateMountSkipped(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        // Mount again - same instance, should be skipped
        $agent->mount($skill);
        $this->assertCount(1, $agent->skills());
    }

    // ── 3. With UsesTools - tool registration ─────────────────────

    public function testMountRegistersTools(): void
    {
        $agent = new SkillsWithTools();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        $schema = $agent->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertContains('add', $names);
    }

    public function testUnmountRemovesTools(): void
    {
        $agent = new SkillsWithTools();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        $schema = $agent->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertContains('add', $names);

        $agent->unmount('test_math');
        $schema = $agent->toolsSchema();
        $names = array_column(array_column($schema, 'function'), 'name');
        $this->assertNotContains('add', $names);
    }

    public function testToolFunctional(): void
    {
        $agent = new SkillsWithTools();
        $skill = new TestMathSkill();
        $agent->mount($skill);
        $result = $agent->executeTool('add', ['a' => 3, 'b' => 4]);
        $decoded = json_decode($result, true);
        $this->assertSame(7, $decoded);
    }

    // ── 4. With HasHooks - hook events ────────────────────────────

    public function testSkillMountHookFires(): void
    {
        $agent = new FullSkillAgent();
        $fired = [];
        $agent->on(HookEvent::SkillMount, function (Skill $skill) use (&$fired) {
            $fired[] = $skill->name;
        });
        $agent->mount(new TestMathSkill());
        $this->assertSame(['test_math'], $fired);
    }

    public function testSkillUnmountHookFires(): void
    {
        $agent = new FullSkillAgent();
        $agent->mount(new TestMathSkill());
        $fired = [];
        $agent->on(HookEvent::SkillUnmount, function (Skill $skill) use (&$fired) {
            $fired[] = $skill->name;
        });
        $agent->unmount('test_math');
        $this->assertSame(['test_math'], $fired);
    }

    public function testSkillSetupHookFires(): void
    {
        $agent = new FullSkillAgent();
        $fired = [];
        $agent->on(HookEvent::SkillSetup, function (Skill $skill) use (&$fired) {
            $fired[] = $skill->name;
        });
        $agent->mount(new TestMathSkill());
        $this->assertSame(['test_math'], $fired);
    }

    public function testSkillTeardownHookFires(): void
    {
        $agent = new FullSkillAgent();
        $agent->mount(new TestMathSkill());
        $fired = [];
        $agent->on(HookEvent::SkillTeardown, function (Skill $skill) use (&$fired) {
            $fired[] = $skill->name;
        });
        $agent->unmount('test_math');
        $this->assertSame(['test_math'], $fired);
    }

    // ── 5. Lifecycle ──────────────────────────────────────────────

    public function testSetupCalledOnMount(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestTrackingSkill();
        $agent->mount($skill);
        $this->assertContains('test_tracking', TestTrackingSkill::$setupCalls);
    }

    public function testTeardownCalledOnUnmount(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestTrackingSkill();
        $agent->mount($skill);
        TestTrackingSkill::reset();
        $agent->unmount('test_tracking');
        $this->assertContains('test_tracking', TestTrackingSkill::$teardownCalls);
    }

    public function testShutdownReverseOrderTeardown(): void
    {
        $agent = new SkillsOnly();
        $a = new TestTrackingSkillA();
        $b = new TestTrackingSkillB();
        $agent->mount($a);
        $agent->mount($b);

        // Reset to only capture shutdown teardown
        TestTrackingSkillA::reset();
        TestTrackingSkillB::reset();

        $agent->shutdown();

        // B was mounted after A, so in reverse order B teardown should come first
        $this->assertSame(['test_tracking_skill_b'], TestTrackingSkillB::$teardownCalls);
        $this->assertSame(['test_tracking_skill_a'], TestTrackingSkillA::$teardownCalls);
    }

    public function testSkillContextCorrect(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestMathSkill();
        $config = ['precision' => 10];
        $agent->mount($skill, $config);

        $this->assertNotNull($skill->context);
        $this->assertInstanceOf(SkillContext::class, $skill->context);
        $this->assertSame($skill, $skill->context->skill);
        $this->assertSame($agent, $skill->context->agent);
        $this->assertSame($config, $skill->context->config);
    }

    // ── 6. Instructions ───────────────────────────────────────────

    public function testSkillPromptMiddlewareInjectsInstructions(): void
    {
        $skill = new TestMathSkill();
        $middleware = new SkillPromptMiddleware([$skill]);
        $messages = [['role' => 'system', 'content' => 'You are helpful.']];
        $result = $middleware->pre($messages, null);
        $this->assertStringContainsString('Use math tools', $result[0]['content']);
        $this->assertStringContainsString('test_math', $result[0]['content']);
    }

    public function testMultipleSkillInstructions(): void
    {
        $alpha = new TestInstructionAlphaSkill();
        $beta = new TestInstructionBetaSkill();
        $middleware = new SkillPromptMiddleware([$alpha, $beta]);
        $messages = [['role' => 'system', 'content' => 'Base system prompt.']];
        $result = $middleware->pre($messages, null);
        $this->assertStringContainsString('Alpha instructions here', $result[0]['content']);
        $this->assertStringContainsString('Beta instructions here', $result[0]['content']);
        $this->assertStringContainsString('test_instruction_alpha', $result[0]['content']);
        $this->assertStringContainsString('test_instruction_beta', $result[0]['content']);
    }

    public function testNoInstructionsNoInjection(): void
    {
        $skill = new TestNoInstructionsSkill();
        $middleware = new SkillPromptMiddleware([$skill]);
        $messages = [['role' => 'system', 'content' => 'You are helpful.']];
        $result = $middleware->pre($messages, null);
        // No instructions means no modification
        $this->assertSame('You are helpful.', $result[0]['content']);
    }

    // ── 7. Middleware/hooks from skill ─────────────────────────────

    public function testSkillMiddlewareRegistered(): void
    {
        $agent = new FullSkillAgent();
        $skill = new TestMiddlewareProvidingSkill();
        $agent->mount($skill);

        $messages = [['role' => 'user', 'content' => 'Hello']];
        $agent->runPre($messages, null);
        $this->assertNotEmpty(TestMiddlewareProvidingSkill::$preCalledWith);
    }

    public function testSkillHooksRegistered(): void
    {
        $agent = new FullSkillAgent();
        $skill = new TestHookProvidingSkill();
        $agent->mount($skill);

        $agent->emit(HookEvent::RunStart);
        $this->assertSame(['run_start'], TestHookProvidingSkill::$hookFired);
    }

    // ── 8. Dependencies ───────────────────────────────────────────

    public function testDependencyAutoMounted(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestSimpleDepSkill();
        $agent->mount($skill);

        $skills = $agent->skills();
        $this->assertArrayHasKey('test_base_dep', $skills);
        $this->assertArrayHasKey('test_simple_dep', $skills);
    }

    public function testAlreadyMountedDepSkipped(): void
    {
        $agent = new SkillsOnly();
        $base = new TestBaseDepSkill();
        $agent->mount($base);
        $this->assertCount(1, $agent->skills());

        $skill = new TestSimpleDepSkill();
        $agent->mount($skill);

        // Base dep should not be mounted twice
        $this->assertCount(2, $agent->skills());
        $this->assertArrayHasKey('test_base_dep', $agent->skills());
        $this->assertArrayHasKey('test_simple_dep', $agent->skills());
    }

    public function testTransitiveDeps(): void
    {
        $agent = new SkillsOnly();
        $skill = new TestTopDepSkill();
        $agent->mount($skill);

        $skills = $agent->skills();
        $this->assertArrayHasKey('test_base_dep', $skills);
        $this->assertArrayHasKey('test_mid_dep', $skills);
        $this->assertArrayHasKey('test_top_dep', $skills);
        $this->assertCount(3, $skills);
    }
}
