<?php

declare(strict_types=1);

namespace AgentHarness;

final class Helpers
{
    public static function tryEmit(object $target, HookEvent $event, mixed ...$args): void
    {
        if (method_exists($target, 'emit')) {
            $target->emit($event, ...$args);
        }
    }
}
