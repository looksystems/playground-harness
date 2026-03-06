<?php

declare(strict_types=1);

namespace AgentHarness;

class SkillManager
{
    /** @var array<string, Skill> */
    private array $skills = [];

    /** @var array<int, string> */
    private array $mountOrder = [];

    /** @var array<string, list<string>> */
    private array $skillTools = [];

    private ?SkillPromptMiddleware $promptMw = null;

    public function __construct(
        private readonly mixed $agent,
    ) {
    }

    public function mount(Skill $skill, array $config = []): void
    {
        if (isset($this->skills[$skill->name])) {
            return;
        }

        // Resolve and mount dependencies first
        $this->resolveDeps($skill, $config);

        // Create context
        $ctx = new SkillContext(
            skill: $skill,
            agent: $this->agent,
            config: $config,
        );
        $skill->context = $ctx;

        // Call setup
        $skill->setup($ctx);

        // Register tools
        $toolNames = [];
        if (method_exists($this->agent, 'registerTool')) {
            foreach ($skill->tools() as $tool) {
                $this->agent->registerTool($tool);
                $toolNames[] = $tool->name;
            }
        }
        $this->skillTools[$skill->name] = $toolNames;

        // Register middleware
        if (method_exists($this->agent, 'use')) {
            foreach ($skill->middleware() as $mw) {
                $this->agent->use($mw);
            }
        }

        // Register hooks
        if (method_exists($this->agent, 'on')) {
            foreach ($skill->hooks() as $eventValue => $callbacks) {
                $event = HookEvent::from($eventValue);
                foreach ($callbacks as $callback) {
                    $this->agent->on($event, $callback);
                }
            }
        }

        $this->skills[$skill->name] = $skill;
        $this->mountOrder[] = $skill->name;

        $this->rebuildPromptMiddleware();
    }

    public function unmount(string $name): void
    {
        if (!isset($this->skills[$name])) {
            return;
        }

        $skill = $this->skills[$name];

        // Teardown
        if ($skill->context !== null) {
            $skill->teardown($skill->context);
        }

        // Remove tracked tools
        if (method_exists($this->agent, 'unregisterTool') && isset($this->skillTools[$name])) {
            foreach ($this->skillTools[$name] as $toolName) {
                $this->agent->unregisterTool($toolName);
            }
        }
        unset($this->skillTools[$name]);

        unset($this->skills[$name]);
        $this->mountOrder = array_values(array_filter(
            $this->mountOrder,
            fn(string $n) => $n !== $name,
        ));

        $this->rebuildPromptMiddleware();
    }

    public function shutdown(): void
    {
        // Reverse-order teardown
        $reversed = array_reverse($this->mountOrder);
        foreach ($reversed as $name) {
            if (isset($this->skills[$name])) {
                $skill = $this->skills[$name];
                if ($skill->context !== null) {
                    $skill->teardown($skill->context);
                }
            }
        }

        $this->skills = [];
        $this->mountOrder = [];
        $this->skillTools = [];
    }

    /**
     * @return array<string, Skill>
     */
    public function skills(): array
    {
        return $this->skills;
    }

    private function resolveDeps(Skill $skill, array $config): void
    {
        $resolved = [];
        $this->topoSort($skill, $resolved, []);

        // Mount each dependency (excluding the skill itself, which is last)
        array_pop($resolved); // remove the skill itself
        foreach ($resolved as $depClass) {
            if (!$this->isClassMounted($depClass)) {
                /** @var Skill $dep */
                $dep = new $depClass();
                $this->mount($dep, $config);
            }
        }
    }

    /**
     * @param Skill $skill
     * @param array<int, class-string<Skill>> $resolved
     * @param array<int, class-string<Skill>> $seen
     */
    private function topoSort(Skill $skill, array &$resolved, array $seen): void
    {
        $class = $skill::class;

        if (in_array($class, $seen, true)) {
            throw new \RuntimeException("Circular skill dependency detected: {$class}");
        }

        if (in_array($class, $resolved, true)) {
            return;
        }

        $seen[] = $class;

        foreach ($skill->dependencies as $depClass) {
            /** @var Skill $dep */
            $dep = new $depClass();
            $this->topoSort($dep, $resolved, $seen);
        }

        $resolved[] = $class;
    }

    /**
     * @param class-string<Skill> $class
     */
    private function isClassMounted(string $class): bool
    {
        foreach ($this->skills as $skill) {
            if ($skill instanceof $class) {
                return true;
            }
        }
        return false;
    }

    private function rebuildPromptMiddleware(): void
    {
        if (!method_exists($this->agent, 'use')) {
            return;
        }

        // Remove existing prompt middleware if present
        if ($this->promptMw !== null && method_exists($this->agent, 'removeMiddleware')) {
            $this->agent->removeMiddleware($this->promptMw);
        }

        $activeSkills = array_values($this->skills);

        if (count($activeSkills) === 0) {
            $this->promptMw = null;
            return;
        }

        $this->promptMw = new SkillPromptMiddleware($activeSkills);
        $this->agent->use($this->promptMw);
    }
}
