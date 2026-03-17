<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class Sandbox
{
    public function __construct(
        public string $name = '',
        public string $sandboxId = '',
        public ?SandboxSpec $spec = null,
        public ?SandboxStatus $status = null,
    ) {}
}
