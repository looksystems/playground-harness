<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasHooks
{
    private array $hooks = [];

    public function on(HookEvent $event, callable $callback): \Closure
    {
        $this->hooks[$event->value][] = $callback;
        return function () use ($event, $callback): void {
            $key = $event->value;
            if (isset($this->hooks[$key])) {
                $idx = array_search($callback, $this->hooks[$key], true);
                if ($idx !== false) {
                    array_splice($this->hooks[$key], $idx, 1);
                }
            }
        };
    }

    public function emit(HookEvent $event, mixed ...$args): void
    {
        foreach ($this->hooks[$event->value] ?? [] as $cb) {
            try {
                $cb(...$args);
            } catch (\Throwable $e) {
                if ($event !== HookEvent::HookError) {
                    $this->emit(HookEvent::HookError, $event, $e);
                }
            }
        }
    }
}
