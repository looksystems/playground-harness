<?php

declare(strict_types=1);

namespace AgentHarness;

class BashkitDriver
{
    public static function resolve(
        ?\Closure $execOverride = null,
        ?string $cliPath = 'auto',
    ): ShellDriverInterface {
        if ($execOverride !== null) {
            return new BashkitCLIDriver(execOverride: $execOverride);
        }
        $available = $cliPath === 'auto' ? self::checkCli() : ($cliPath !== null);
        if ($available) {
            return new BashkitCLIDriver();
        }
        throw new \RuntimeException('bashkit not found — install with: cargo install bashkit-cli');
    }

    public static function register(): void
    {
        ShellDriverFactory::register('bashkit', function (array $opts = []): ShellDriverInterface {
            return self::resolve();
        });
    }

    private static function checkCli(): bool
    {
        $path = trim((string) shell_exec('which bashkit 2>/dev/null'));
        return $path !== '';
    }
}
