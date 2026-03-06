<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasMiddleware
{
    private array $middlewareStack = [];

    public function use(Middleware $mw): static
    {
        $this->middlewareStack[] = $mw;
        return $this;
    }

    public function removeMiddleware(Middleware $mw): static
    {
        $idx = array_search($mw, $this->middlewareStack, true);
        if ($idx !== false) {
            array_splice($this->middlewareStack, $idx, 1);
        }
        return $this;
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
