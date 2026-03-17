<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class InferencePolicy
{
    public function __construct(
        public bool $routingEnabled = false,
        public string $provider = '',
    ) {}
}
