<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class ExecSandboxRequest
{
    public function __construct(
        public string $sandboxId = '',
        public array $command = [],
        public array $env = [],
        public string $workingDir = '',
    ) {}
}
