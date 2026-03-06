<?php

declare(strict_types=1);

namespace AgentHarness;

abstract class Skill
{
    public readonly string $name;
    public string $description = '';
    public string $version = '0.1.0';
    public string $instructions = '';

    /** @var array<int, class-string<Skill>> */
    public array $dependencies = [];

    public ?SkillContext $context = null;

    public function __construct()
    {
        $this->name = $this->deriveName();
    }

    private function deriveName(): string
    {
        $class = (new \ReflectionClass($this))->getShortName();

        // Remove "Skill" suffix if present
        if (str_ends_with($class, 'Skill')) {
            $class = substr($class, 0, -5);
        }

        // CamelCase to snake_case
        $snake = preg_replace('/([a-z])([A-Z])/', '$1_$2', $class);
        $snake = preg_replace('/([A-Z]+)([A-Z][a-z])/', '$1_$2', $snake);

        return strtolower($snake);
    }

    public function setup(SkillContext $ctx): void
    {
    }

    public function teardown(SkillContext $ctx): void
    {
    }

    /**
     * @return array<int, ToolDef>
     */
    public function tools(): array
    {
        return [];
    }

    /**
     * @return array<int, Middleware>
     */
    public function middleware(): array
    {
        return [];
    }

    /**
     * @return array<value-of<HookEvent>, list<callable>>
     */
    public function hooks(): array
    {
        return [];
    }

    /**
     * @return array<string, callable>
     */
    public function commands(): array
    {
        return [];
    }
}
