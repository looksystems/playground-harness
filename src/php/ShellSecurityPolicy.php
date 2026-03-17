<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Composable security policy for shell drivers.
 *
 * Formalizes security constraints that were previously scattered across
 * constructor parameters. Inspired by OpenShell's multi-layer policy model.
 */
class ShellSecurityPolicy
{
    /** @var list<string>|null */
    public ?array $allowedCommands;

    /** @var list<string>|null */
    public ?array $writablePaths;

    public int $maxOutput;
    public int $maxIterations;
    public bool $readOnly;

    public function __construct(
        ?array $allowedCommands = null,
        ?array $writablePaths = null,
        int $maxOutput = 16_000,
        int $maxIterations = 10_000,
        bool $readOnly = false,
    ) {
        $this->allowedCommands = $allowedCommands;
        $this->writablePaths = $writablePaths;
        $this->maxOutput = $maxOutput;
        $this->maxIterations = $maxIterations;
        $this->readOnly = $readOnly;
    }
}
