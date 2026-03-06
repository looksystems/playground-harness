<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasSkills
{
    private ?SkillManager $skillManager = null;
    private bool $hasSkillsInitialized = false;

    private function tryEmitFromSkills(HookEvent $event, mixed ...$args): void
    {
        if (method_exists($this, 'emit')) {
            $this->emit($event, ...$args);
        }
    }

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
        $this->tryEmitFromSkills(HookEvent::SkillSetup, $skill);
        $this->skillManager->mount($skill, $config);
        $this->tryEmitFromSkills(HookEvent::SkillMount, $skill);
        return $this;
    }

    public function unmount(string $name): void
    {
        $this->ensureHasSkills();

        $skills = $this->skillManager->skills();
        $skill = $skills[$name] ?? null;

        $this->tryEmitFromSkills(HookEvent::SkillTeardown, $skill ?? $name);
        $this->skillManager->unmount($name);
        $this->tryEmitFromSkills(HookEvent::SkillUnmount, $skill ?? $name);
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
