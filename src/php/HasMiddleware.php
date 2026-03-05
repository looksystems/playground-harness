<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasMiddleware
{
    private array $middlewareStack = [];

    public function use(Middleware $mw): void
    {
        $this->middlewareStack[] = $mw;
    }

    public function runPre(array $messages, mixed $context): array
    {
        foreach ($this->middlewareStack as $mw) {
            $messages = $mw->pre($messages, $context);
        }
        return $messages;
    }

    public function runPost(array $message, mixed $context): array
    {
        foreach ($this->middlewareStack as $mw) {
            $message = $mw->post($message, $context);
        }
        return $message;
    }
}
