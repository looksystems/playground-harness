<?php

/**
 * skills.php — Skill system for agent_harness.php
 *
 * A Skill is a composable unit that bundles:
 *   - Tools (functions the LLM can call)
 *   - System prompt fragments (instructions injected into the prompt)
 *   - Middleware (pre/post message transforms)
 *   - Hooks (lifecycle event listeners)
 *   - Lifecycle methods (setup/teardown for resources)
 *
 * Skills can declare dependencies on other skills, and the manager resolves
 * the full dependency graph when mounting.
 *
 * Usage:
 *   require_once 'agent_harness.php';
 *   require_once 'skills.php';
 *
 *   use AgentHarness\Agent;
 *   use AgentHarness\Skills\{Skill, SkillManager, SkillContext};
 *
 *   class WebBrowsingSkill extends Skill {
 *       public string $name = 'web_browsing';
 *       public string $instructions = 'Use fetch_page to get web content.';
 *
 *       public function setup(SkillContext $ctx): void {
 *           $ctx->state['client'] = new GuzzleHttp\Client();
 *       }
 *
 *       public function tools(): array {
 *           $ctx = $this->context;
 *           return [ToolDef::make(
 *               name: 'fetch_page',
 *               description: 'Fetch a web page',
 *               parameters: [...],
 *               execute: fn($args) => $ctx->state['client']->get($args['url'])->getBody()->getContents(),
 *           )];
 *       }
 *   }
 *
 *   $agent = new Agent(model: 'gpt-4o');
 *   $manager = new SkillManager($agent);
 *   $manager->mount(new WebBrowsingSkill());
 *   $result = $agent->run('Summarize https://example.com');
 *   $manager->shutdown();
 */

declare(strict_types=1);

namespace AgentHarness\Skills;

use AgentHarness\{
    Agent,
    BaseMiddleware,
    HookEvent,
    Middleware,
    RunContext,
    ToolDef,
};

// ---------------------------------------------------------------------------
// Skill context — per-skill state bag
// ---------------------------------------------------------------------------

class SkillContext
{
    public function __construct(
        public readonly Skill $skill,
        public readonly Agent $agent,
        public array          $config = [],
        public array          $state = [],
    ) {}
}

// ---------------------------------------------------------------------------
// Skill base class
// ---------------------------------------------------------------------------

abstract class Skill
{
    public string $name = '';
    public string $description = '';
    public string $version = '0.1.0';

    /** Instructions injected into the system prompt. */
    public string $instructions = '';

    /**
     * Skill class names this skill depends on.
     * @var list<class-string<Skill>>
     */
    public array $dependencies = [];

    /** Set by the SkillManager when mounted. */
    public ?SkillContext $context = null;

    public function __construct()
    {
        // Auto-derive name from class: WebBrowsingSkill -> web_browsing
        if ($this->name === '') {
            $short = (new \ReflectionClass($this))->getShortName();
            $short = preg_replace('/Skill$/', '', $short);
            // CamelCase to snake_case
            $this->name = strtolower(ltrim(
                preg_replace('/([A-Z])/', '_$1', $short),
                '_'
            ));
        }
    }

    // -- Lifecycle ----------------------------------------------------------

    /** Called once when the skill is mounted. Acquire resources here. */
    public function setup(SkillContext $ctx): void {}

    /** Called on shutdown. Release resources here. */
    public function teardown(SkillContext $ctx): void {}

    // -- Composition -------------------------------------------------------

    /** @return list<ToolDef> Tools this skill provides. */
    public function tools(): array
    {
        return [];
    }

    /** @return list<Middleware> Middleware this skill provides. */
    public function middleware(): array
    {
        return [];
    }

    /**
     * Hooks this skill provides.
     * @return array<string, list<callable>> Map of HookEvent->value to callbacks.
     */
    public function hooks(): array
    {
        return [];
    }
}

// ---------------------------------------------------------------------------
// Skill-aware prompt middleware
// ---------------------------------------------------------------------------

class SkillPromptMiddleware extends BaseMiddleware
{
    /** @param list<Skill> $skills */
    public function __construct(private readonly array $skills) {}

    public function pre(array $messages, RunContext $context): array
    {
        $fragments = [];
        foreach ($this->skills as $skill) {
            if ($skill->instructions !== '') {
                $fragments[] = "## {$skill->name}\n{$skill->instructions}";
            }
        }

        if (empty($fragments)) {
            return $messages;
        }

        $block = "\n\n---\n**Available Skills:**\n\n" . implode("\n\n", $fragments);

        // Append to existing system message or create one
        foreach ($messages as &$m) {
            if ($m['role'] === 'system') {
                $m['content'] .= $block;
                return $messages;
            }
        }
        unset($m);

        array_unshift($messages, ['role' => 'system', 'content' => trim($block)]);
        return $messages;
    }
}

// ---------------------------------------------------------------------------
// Skill manager
// ---------------------------------------------------------------------------

class SkillManager
{
    /** @var array<string, Skill> */
    private array $skills = [];

    /** @var list<string> */
    private array $mountOrder = [];

    private ?SkillPromptMiddleware $promptMw = null;

    public function __construct(private readonly Agent $agent) {}

    /** @return array<string, Skill> */
    public function mounted(): array
    {
        return $this->skills;
    }

    /**
     * Mount a skill onto the agent.
     * Resolves dependencies, calls setup(), registers tools/middleware/hooks.
     */
    public function mount(Skill $skill, array $config = []): self
    {
        $toMount = $this->resolveDeps($skill);

        foreach ($toMount as $s) {
            if (isset($this->skills[$s->name])) {
                continue;
            }

            // Create context
            $ctx = new SkillContext(
                skill: $s,
                agent: $this->agent,
                config: $config,
            );
            $s->context = $ctx;

            // Lifecycle setup
            $s->setup($ctx);

            // Register tools
            foreach ($s->tools() as $tool) {
                $this->agent->registerTool($tool);
            }

            // Register middleware
            foreach ($s->middleware() as $mw) {
                $this->agent->use($mw);
            }

            // Register hooks
            foreach ($s->hooks() as $eventValue => $callbacks) {
                $event = HookEvent::from($eventValue);
                foreach ($callbacks as $cb) {
                    $this->agent->on($event, $cb);
                }
            }

            $this->skills[$s->name] = $s;
            $this->mountOrder[] = $s->name;
            error_log("Mounted skill: {$s->name} (v{$s->version})");
        }

        $this->rebuildPromptMiddleware();
        return $this;
    }

    /** Teardown and remove a skill. */
    public function unmount(string $skillName): self
    {
        $skill = $this->skills[$skillName] ?? null;
        if ($skill === null) {
            throw new \RuntimeException("Skill not mounted: {$skillName}");
        }

        if ($skill->context !== null) {
            $skill->teardown($skill->context);
        }

        unset($this->skills[$skillName]);
        $this->mountOrder = array_values(
            array_filter($this->mountOrder, fn($n) => $n !== $skillName)
        );
        $this->rebuildPromptMiddleware();
        error_log("Unmounted skill: {$skillName}");
        return $this;
    }

    /** Teardown all skills in reverse mount order. */
    public function shutdown(): void
    {
        foreach (array_reverse($this->mountOrder) as $name) {
            $skill = $this->skills[$name] ?? null;
            if ($skill?->context !== null) {
                try {
                    $skill->teardown($skill->context);
                    error_log("Torn down skill: {$name}");
                } catch (\Throwable $e) {
                    error_log("Teardown error for {$name}: {$e->getMessage()}");
                }
            }
        }
        $this->skills = [];
        $this->mountOrder = [];
    }

    // -- Dependency resolution ---------------------------------------------

    /**
     * @return list<Skill>
     */
    private function resolveDeps(Skill $skill): array
    {
        $visited = [];
        $order = [];

        $visit = function (Skill $s) use (&$visited, &$order, &$visit): void {
            if (isset($visited[$s->name])) {
                return;
            }
            $visited[$s->name] = true;

            foreach ($s->dependencies as $depClass) {
                /** @var Skill $dep */
                $dep = new $depClass();
                $visit($dep);
            }
            $order[] = $s;
        };

        $visit($skill);
        return $order;
    }

    private function rebuildPromptMiddleware(): void
    {
        $withInstructions = array_filter(
            $this->skills,
            fn(Skill $s) => $s->instructions !== ''
        );

        if (empty($withInstructions)) {
            return;
        }

        // The agent doesn't expose middleware removal directly,
        // so we track and replace via reference.
        // In production you'd add removeMiddleware() to Agent.
        $this->promptMw = new SkillPromptMiddleware(array_values($withInstructions));
        $this->agent->use($this->promptMw);
    }
}

// ===========================================================================
// Example skills
// ===========================================================================

class MathSkill extends Skill
{
    public string $name = 'math';
    public string $description = 'Perform arithmetic operations';
    public string $instructions =
        'You have access to math tools for precise calculations. '
        . 'Always use these tools instead of doing math in your head.';

    public function tools(): array
    {
        return [
            ToolDef::make(
                name: 'add',
                description: 'Add two numbers',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'a' => ['type' => 'number'],
                        'b' => ['type' => 'number'],
                    ],
                    'required' => ['a', 'b'],
                ],
                execute: fn(array $args) => $args['a'] + $args['b'],
            ),
            ToolDef::make(
                name: 'subtract',
                description: 'Subtract b from a',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'a' => ['type' => 'number'],
                        'b' => ['type' => 'number'],
                    ],
                    'required' => ['a', 'b'],
                ],
                execute: fn(array $args) => $args['a'] - $args['b'],
            ),
            ToolDef::make(
                name: 'multiply',
                description: 'Multiply two numbers',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'a' => ['type' => 'number'],
                        'b' => ['type' => 'number'],
                    ],
                    'required' => ['a', 'b'],
                ],
                execute: fn(array $args) => $args['a'] * $args['b'],
            ),
            ToolDef::make(
                name: 'divide',
                description: 'Divide a by b',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'a' => ['type' => 'number'],
                        'b' => ['type' => 'number'],
                    ],
                    'required' => ['a', 'b'],
                ],
                execute: function (array $args) {
                    if ($args['b'] == 0) {
                        throw new \InvalidArgumentException('Division by zero');
                    }
                    return $args['a'] / $args['b'];
                },
            ),
        ];
    }
}

class MemorySkill extends Skill
{
    public string $name = 'memory';
    public string $description = 'Remember and recall information across turns';
    public string $instructions =
        'You can store and retrieve facts using the memory tools. '
        . 'Use remember() to save important information and recall() to retrieve it.';

    public function setup(SkillContext $ctx): void
    {
        $ctx->state['store'] = [];
    }

    public function tools(): array
    {
        $ctx = $this->context;

        return [
            ToolDef::make(
                name: 'remember',
                description: 'Store a value with a key',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'key'   => ['type' => 'string'],
                        'value' => ['type' => 'string'],
                    ],
                    'required' => ['key', 'value'],
                ],
                execute: function (array $args) use ($ctx): string {
                    $ctx->state['store'][$args['key']] = $args['value'];
                    return "Stored '{$args['key']}'";
                },
            ),
            ToolDef::make(
                name: 'recall',
                description: 'Retrieve a value by key',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'key' => ['type' => 'string'],
                    ],
                    'required' => ['key'],
                ],
                execute: function (array $args) use ($ctx): string {
                    return $ctx->state['store'][$args['key']]
                        ?? "No memory found for '{$args['key']}'";
                },
            ),
            ToolDef::make(
                name: 'list_memories',
                description: 'List all stored memory keys',
                parameters: [
                    'type' => 'object',
                    'properties' => (object) [],
                ],
                execute: function (array $args) use ($ctx): array {
                    return array_keys($ctx->state['store']);
                },
            ),
        ];
    }
}

class GuardrailSkill extends Skill
{
    public string $name = 'guardrails';
    public string $description = 'Content safety guardrails';

    /** @param list<string> $blockedPatterns */
    public function __construct(private readonly array $blockedPatterns = [])
    {
        parent::__construct();
    }

    public function middleware(): array
    {
        $blocked = $this->blockedPatterns;

        return [
            new class($blocked) extends BaseMiddleware {
                /** @param list<string> $blocked */
                public function __construct(private readonly array $blocked) {}

                public function post(array $message, RunContext $context): array
                {
                    $content = strtolower($message['content'] ?? '');
                    foreach ($this->blocked as $pattern) {
                        if (str_contains($content, strtolower($pattern))) {
                            error_log("Guardrail triggered: blocked pattern '{$pattern}'");
                            $message['content'] =
                                "I'm unable to provide that information. "
                                . "Please rephrase your request.";
                            break;
                        }
                    }
                    return $message;
                }
            },
        ];
    }

    public function hooks(): array
    {
        return [
            HookEvent::RunStart->value => [
                function (array $msgs): void {
                    $count = count($msgs);
                    error_log("Guardrail active for {$count} messages");
                },
            ],
        ];
    }
}

// ===========================================================================
// Demo
// ===========================================================================

if (php_sapi_name() === 'cli' && realpath($argv[0] ?? '') === realpath(__FILE__)) {

    require_once __DIR__ . '/vendor/autoload.php';
    require_once __DIR__ . '/agent_harness.php';

    $agent = new Agent(
        model: getenv('AGENT_MODEL') ?: 'gpt-4o',
        system: 'You are a helpful assistant.',
        maxTurns: 10,
        completionParams: ['temperature' => 0.3],
    );

    $manager = new SkillManager($agent);

    $manager->mount(new MathSkill());
    $manager->mount(new MemorySkill());
    $manager->mount(new GuardrailSkill(['social security']));

    echo "Mounted skills: " . implode(', ', array_keys($manager->mounted())) . "\n";

    try {
        $result = $agent->run(
            "Remember that my favorite number is 42, then compute 42 * 17."
        );
        echo "\n---\n{$result}\n";
    } catch (\Throwable $e) {
        echo "Error: {$e->getMessage()}\n";
    }

    $manager->shutdown();
}
