<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class SandboxStatus
{
    public const UNKNOWN = 0;
    public const PENDING = 1;
    public const RUNNING = 2;
    public const STOPPED = 3;
    public const FAILED = 4;

    public function __construct(
        public int $phase = self::UNKNOWN,
        public string $message = '',
    ) {}
}
