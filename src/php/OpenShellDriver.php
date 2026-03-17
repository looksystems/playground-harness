<?php

declare(strict_types=1);

namespace AgentHarness;

class OpenShellDriver
{
    public static function resolve(
        ?\Closure $execOverride = null,
        ?string $endpoint = 'localhost:50051',
    ): ShellDriverInterface {
        if ($execOverride !== null) {
            return new OpenShellGrpcDriver(execOverride: $execOverride, endpoint: $endpoint);
        }
        // Check if grpc extension is loaded
        if (!extension_loaded('grpc')) {
            throw new \RuntimeException(
                'grpc extension not found — install with: pecl install grpc'
            );
        }
        return new OpenShellGrpcDriver(endpoint: $endpoint);
    }

    public static function register(): void
    {
        ShellDriverFactory::register('openshell', function (array $opts = []): ShellDriverInterface {
            return self::resolve();
        });
    }
}
