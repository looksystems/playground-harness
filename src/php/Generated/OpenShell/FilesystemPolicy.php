<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class FilesystemPolicy
{
    public function __construct(
        public array $readOnly = [],
        public array $readWrite = [],
    ) {}
}
