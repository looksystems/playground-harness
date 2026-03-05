<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasHooks
{
    private array $hooks = [];

    public function on(HookEvent $event, callable $callback): void
    {
        $this->hooks[$event->value][] = $callback;
    }

    public function emit(HookEvent $event, mixed ...$args): void
    {
        foreach ($this->hooks[$event->value] ?? [] as $cb) {
            try {
                $cb(...$args);
            } catch (\Throwable $e) {
                // Swallow hook errors so they don't propagate
            }
        }
    }
}
