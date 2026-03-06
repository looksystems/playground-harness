<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasSkills
{
    private ?SkillManager $skillManager = null;
    private bool $hasSkillsInitialized = false;

    private function ensureHasSkills(): void
    {
        if (!$this->hasSkillsInitialized) {
            $this->skillManager = new SkillManager($this);
            $this->hasSkillsInitialized = true;
        }
    }

    public function mount(Skill $skill, array $config = []): void
    {
        $this->ensureHasSkills();

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SkillSetup, $skill);
        }

        $this->skillManager->mount($skill, $config);

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SkillMount, $skill);
        }
    }

    public function unmount(string $name): void
    {
        $this->ensureHasSkills();

        $skills = $this->skillManager->skills();
        $skill = $skills[$name] ?? null;

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SkillTeardown, $skill ?? $name);
        }

        $this->skillManager->unmount($name);

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SkillUnmount, $skill ?? $name);
        }
    }

    public function shutdown(): void
    {
        $this->ensureHasSkills();
        $this->skillManager->shutdown();
    }

    /**
     * @return array<string, Skill>
     */
    public function skills(): array
    {
        $this->ensureHasSkills();
        return $this->skillManager->skills();
    }
}
