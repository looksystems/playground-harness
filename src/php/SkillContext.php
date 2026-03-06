<?php

declare(strict_types=1);

namespace AgentHarness;

class SkillContext
{
    public function __construct(
        public readonly Skill $skill,
        public readonly mixed $agent,
        public array $config = [],
        public array $state = [],
    ) {
    }
}
