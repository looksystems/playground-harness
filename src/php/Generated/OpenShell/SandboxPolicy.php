<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class SandboxPolicy
{
    public function __construct(
        public ?FilesystemPolicy $filesystem = null,
        public array $networkPolicies = [],
        public ?InferencePolicy $inference = null,
    ) {
        $this->filesystem ??= new FilesystemPolicy();
        $this->inference ??= new InferencePolicy();
    }
}
