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

    /** @var array<string, list<Middleware>> */
    private array $skillMiddleware = [];

    /** @var array<string, list<array{HookEvent, callable}>> */
    private array $skillHooks = [];

    /** @var array<string, list<string>> */
    private array $skillCommands = [];

    private ?SkillPromptMiddleware $promptMw = null;

    public function __construct(
        private readonly object $agent,
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
        $mwList = $skill->middleware();
        if (method_exists($this->agent, 'use')) {
            foreach ($mwList as $mw) {
                $this->agent->use($mw);
            }
        }
        $this->skillMiddleware[$skill->name] = $mwList;

        // Register hooks
        $hookPairs = [];
        if (method_exists($this->agent, 'on')) {
            foreach ($skill->hooks() as $eventValue => $callbacks) {
                $event = $eventValue instanceof HookEvent ? $eventValue : HookEvent::from($eventValue);
                foreach ($callbacks as $callback) {
                    $this->agent->on($event, $callback);
                    $hookPairs[] = [$event, $callback];
                }
            }
        }
        $this->skillHooks[$skill->name] = $hookPairs;

        // Register commands
        $cmdNames = [];
        if (method_exists($this->agent, 'registerCommand')) {
            foreach ($skill->commands() as $cmdName => $handler) {
                $this->agent->registerCommand($cmdName, $handler);
                $cmdNames[] = $cmdName;
            }
        }
        $this->skillCommands[$skill->name] = $cmdNames;

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

        // Remove middleware
        if (method_exists($this->agent, 'removeMiddleware') && isset($this->skillMiddleware[$name])) {
            foreach ($this->skillMiddleware[$name] as $mw) {
                $this->agent->removeMiddleware($mw);
            }
        }
        unset($this->skillMiddleware[$name]);

        // Remove hooks
        if (method_exists($this->agent, 'off') && isset($this->skillHooks[$name])) {
            foreach ($this->skillHooks[$name] as [$event, $cb]) {
                $this->agent->off($event, $cb);
            }
        }
        unset($this->skillHooks[$name]);

        // Remove commands
        if (method_exists($this->agent, 'unregisterCommand') && isset($this->skillCommands[$name])) {
            foreach ($this->skillCommands[$name] as $cmdName) {
                $this->agent->unregisterCommand($cmdName);
            }
        }
        unset($this->skillCommands[$name]);

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
        $this->skillMiddleware = [];
        $this->skillHooks = [];
        $this->skillCommands = [];
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
