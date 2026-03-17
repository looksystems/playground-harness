<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class NetworkPolicy
{
    public const ALLOW = 0;
    public const DENY = 1;

    public function __construct(
        public string $cidr = '',
        public int $action = self::ALLOW,
    ) {}
}
