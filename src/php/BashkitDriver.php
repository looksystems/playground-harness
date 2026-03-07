<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * BashkitDriver: auto-resolves native (future Phase 3) vs IPC driver.
 */
class BashkitDriver
{
    /**
     * Return a bashkit ShellDriver, preferring native over IPC.
     *
     * @param mixed       $nativeLib        Pass 'auto' to detect, an object to force native,
     *                                      or null to skip native.
     * @param string|null $cliPath          Pass 'auto' to detect, a path string to force available,
     *                                      or null to force unavailable.
     * @param mixed       $processOverride  Fake process for testing.
     */
    public static function resolve(
        mixed $nativeLib = 'auto',
        ?string $cliPath = 'auto',
        mixed $processOverride = null,
    ): ShellDriverInterface {
        // 1. Try native FFI
        if ($nativeLib !== null && $nativeLib !== 'auto') {
            return new BashkitNativeDriver(libOverride: $nativeLib);
        }
        if ($nativeLib === 'auto' && BashkitNativeDriver::findLibrary() !== null) {
            return new BashkitNativeDriver();
        }

        // 2. Fall back to IPC
        $cliAvailable = $cliPath === 'auto' ? self::checkCli() : ($cliPath !== null);
        if ($cliAvailable) {
            return new BashkitIPCDriver(processOverride: $processOverride);
        }

        throw new \RuntimeException('bashkit not found — install libashkit, bashkit-cli, or the native extension');
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
