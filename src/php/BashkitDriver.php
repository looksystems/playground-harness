<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * BashkitDriver: auto-resolves native (future Phase 3) vs IPC driver.
 */
class BashkitDriver
{
    /**
     * Return a bashkit ShellDriver, preferring native (Phase 3) over IPC.
     *
     * @param string|null $cliPath  Pass 'auto' to detect, a path string to force available,
     *                              or null to force unavailable.
     * @param mixed       $processOverride  Fake process for testing.
     */
    public static function resolve(?string $cliPath = 'auto', mixed $processOverride = null): ShellDriverInterface
    {
        $cliAvailable = $cliPath === 'auto' ? self::checkCli() : ($cliPath !== null);

        if ($cliAvailable) {
            return new BashkitIPCDriver(processOverride: $processOverride);
        }

        throw new \RuntimeException('bashkit not found — install bashkit-cli or the native extension');
    }

    /**
     * Register the "bashkit" driver with ShellDriverFactory.
     */
    public static function register(): void
    {
        ShellDriverFactory::register('bashkit', function (array $opts = []): ShellDriverInterface {
            return self::resolve();
        });
    }

    /**
     * Check if bashkit-cli is on PATH.
     */
    private static function checkCli(): bool
    {
        $path = trim((string) shell_exec('which bashkit-cli 2>/dev/null'));
        return $path !== '';
    }
}
