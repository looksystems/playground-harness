<?php

declare(strict_types=1);

namespace AgentHarness;

class BashkitDriver
{
    public static function resolve(array $opts = []): ShellDriverInterface
    {
        $execOverride = $opts['execOverride'] ?? null;
        $cliPath = $opts['cliPath'] ?? 'auto';
        // Only forward params that BashkitCLIDriver accepts
        $driverOpts = array_intersect_key($opts, array_flip(['cwd', 'env', 'execOverride']));
        if ($execOverride !== null) {
            return new BashkitCLIDriver(...$driverOpts);
        }
        $available = $cliPath === 'auto' ? self::checkCli() : ($cliPath !== null);
        if ($available) {
            return new BashkitCLIDriver(...$driverOpts);
        }
        throw new \RuntimeException('bashkit not found — install with: cargo install bashkit-cli');
    }

    public static function register(): void
    {
        ShellDriverFactory::register('bashkit', function (array $opts = []): ShellDriverInterface {
            return self::resolve($opts);
        });
    }

    private static function checkCli(): bool
    {
        $path = trim((string) shell_exec('which bashkit 2>/dev/null'));
        return $path !== '';
    }
}
