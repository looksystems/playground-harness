<?php

declare(strict_types=1);

namespace AgentHarness;

class OpenShellDriver
{
    private const DRIVER_PARAMS = [
        'cwd', 'env', 'execOverride', 'endpoint', 'sandboxId',
        'policy', 'sshHost', 'sshPort', 'sshUser', 'workspace',
    ];

    public static function resolve(array $opts = []): ShellDriverInterface
    {
        $execOverride = $opts['execOverride'] ?? null;
        $driverOpts = array_intersect_key($opts, array_flip(self::DRIVER_PARAMS));
        if ($execOverride !== null) {
            return new OpenShellGrpcDriver(...$driverOpts);
        }
        $sshPath = trim((string) shell_exec('which ssh 2>/dev/null'));
        if ($sshPath === '') {
            throw new \RuntimeException(
                'ssh not found — OpenShell driver requires SSH for execution'
            );
        }
        return new OpenShellGrpcDriver(...$driverOpts);
    }

    public static function register(): void
    {
        ShellDriverFactory::register('openshell', function (array $opts = []): ShellDriverInterface {
            return self::resolve($opts);
        });
    }
}
