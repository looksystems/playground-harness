<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class CreateSandboxRequest
{
    public function __construct(
        public string $name = '',
        public ?SandboxSpec $spec = null,
    ) {
        $this->spec ??= new SandboxSpec();
    }
}
