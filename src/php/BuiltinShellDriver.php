<?php

declare(strict_types=1);

namespace AgentHarness;

class BuiltinShellDriver implements ShellDriverInterface
{
    private Shell $shell;
    private BuiltinFilesystemDriver $fsDriver;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        ?array $allowedCommands = null,
        int $maxOutput = 16_000,
        int $maxIterations = 10_000,
    ) {
        $this->shell = new Shell(
            fs: new VirtualFS(),
            cwd: $cwd,
            env: $env,
            allowedCommands: $allowedCommands,
            maxOutput: $maxOutput,
            maxIterations: $maxIterations,
        );
        $this->fsDriver = new BuiltinFilesystemDriver($this->shell->fs);
    }

    public function fs(): FilesystemDriver { return $this->fsDriver; }
    public function cwd(): string { return $this->shell->cwd; }
    public function env(): array { return $this->shell->env; }
    public function exec(string $command): ExecResult { return $this->shell->exec($command); }
    public function registerCommand(string $name, \Closure $handler): void { $this->shell->registerCommand($name, $handler); }
    public function unregisterCommand(string $name): void { $this->shell->unregisterCommand($name); }

    public function cloneDriver(): ShellDriverInterface
    {
        $cloned = $this->shell->cloneShell();
        return self::fromShell($cloned);
    }

    public function capabilities(): array { return ['custom_commands', 'stateful']; }

    public function setOnNotFound(?\Closure $callback): void { $this->shell->onNotFound = $callback; }
    public function getOnNotFound(): ?\Closure { return $this->shell->onNotFound; }

    public function shell(): Shell { return $this->shell; }

    public static function fromShell(Shell $shell): self
    {
        $driver = new self();
        $driver->shell = $shell;
        $driver->fsDriver = new BuiltinFilesystemDriver($shell->fs);
        return $driver;
    }
}
