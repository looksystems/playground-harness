<?php

declare(strict_types=1);

namespace AgentHarness;

class RunContext
{
    public function __construct(
        public readonly object $agent,
        public int $turn = 0,
        public array $metadata = [],
    ) {
    }
}
