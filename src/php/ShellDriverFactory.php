<?php

declare(strict_types=1);

namespace AgentHarness;

class ShellDriverFactory
{
    public static string $default = 'builtin';
    /** @var array<string, \Closure> */
    private static array $registry = [];

    public static function register(string $name, \Closure $factory): void
    {
        self::$registry[$name] = $factory;
    }

    public static function create(?string $name = null, array $opts = []): ShellDriverInterface
    {
        $name = $name ?? self::$default;
        if ($name === 'builtin') {
            return new BuiltinShellDriver(...$opts);
        }
        if (!isset(self::$registry[$name])) {
            throw new \RuntimeException("Shell driver '{$name}' not registered");
        }
        return (self::$registry[$name])($opts);
    }

    public static function reset(): void
    {
        self::$registry = [];
        self::$default = 'builtin';
    }
}
