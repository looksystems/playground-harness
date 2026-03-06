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

    public function mount(Skill $skill, array $config = []): static
    {
        $this->ensureHasSkills();
        Helpers::tryEmit($this,HookEvent::SkillSetup, $skill);
        $this->skillManager->mount($skill, $config);
        Helpers::tryEmit($this,HookEvent::SkillMount, $skill);
        return $this;
    }

    public function unmount(string $name): static
    {
        $this->ensureHasSkills();
        $skills = $this->skillManager->skills();
        $skill = $skills[$name] ?? null;
        Helpers::tryEmit($this, HookEvent::SkillTeardown, $skill ?? $name);
        $this->skillManager->unmount($name);
        Helpers::tryEmit($this, HookEvent::SkillUnmount, $skill ?? $name);
        return $this;
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
