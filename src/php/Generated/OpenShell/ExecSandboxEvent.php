<?php

declare(strict_types=1);

namespace AgentHarness\Generated\OpenShell;

class ExecSandboxEvent
{
    public function __construct(
        public ?StdoutChunk $stdout = null,
        public ?StderrChunk $stderr = null,
        public ?ExitStatus $exit = null,
    ) {}
}
