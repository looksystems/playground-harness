<?php

declare(strict_types=1);

namespace AgentHarness;

interface Middleware
{
    public function pre(array $messages, mixed $context): array;
    public function post(array $message, mixed $context): array;
}
