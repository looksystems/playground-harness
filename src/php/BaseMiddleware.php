<?php

declare(strict_types=1);

namespace AgentHarness;

abstract class BaseMiddleware implements Middleware
{
    public function pre(array $messages, mixed $context): array
    {
        return $messages;
    }

    public function post(array $message, mixed $context): array
    {
        return $message;
    }
}
