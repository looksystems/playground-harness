<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class SandboxSpec
{
    public function __construct(
        public string $image = '',
        public ?SandboxPolicy $policy = null,
        public array $env = [],
        public string $workspace = '',
    ) {
        $this->policy ??= new SandboxPolicy();
    }
}
