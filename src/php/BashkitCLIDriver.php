<?php

declare(strict_types=1);

namespace AgentHarness;

if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

class BashkitCLIDriver implements ShellDriverInterface
{
    private string $cwd;
    private array $env;
    private BuiltinFilesystemDriver $fsDriver;
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    /** @var (\Closure(string): array{stdout: string, stderr: string, exitCode: int})|null */
    private ?\Closure $execOverride;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        ?\Closure $execOverride = null,
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new BuiltinFilesystemDriver();
        $this->execOverride = $execOverride;
    }

    public function fs(): FilesystemDriver { return $this->fsDriver; }
    public function cwd(): string { return $this->cwd; }
    public function env(): array { return $this->env; }

    public function exec(string $command): ExecResult
    {
        if ($this->execOverride !== null) {
            $result = ($this->execOverride)($command);
            return new ExecResult(
                stdout: $result['stdout'] ?? '',
                stderr: $result['stderr'] ?? '',
                exitCode: $result['exitCode'] ?? 0,
            );
        }

        $escaped = escapeshellarg($command);
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];
        $proc = proc_open("bashkit -c {$escaped}", $descriptors, $pipes);
        if (!is_resource($proc)) {
            return new ExecResult(stderr: 'Failed to spawn bashkit', exitCode: 1);
        }

        fclose($pipes[0]);
        $stdout = stream_get_contents($pipes[1]);
        $stderr = stream_get_contents($pipes[2]);
        fclose($pipes[1]);
        fclose($pipes[2]);
        $exitCode = proc_close($proc);

        return new ExecResult(
            stdout: $stdout ?: '',
            stderr: $stderr ?: '',
            exitCode: $exitCode,
        );
    }

    public function registerCommand(string $name, \Closure $handler): void
    {
        $this->commands[$name] = $handler;
    }

    public function unregisterCommand(string $name): void
    {
        unset($this->commands[$name]);
    }

    public function hasCommand(string $name): bool
    {
        return isset($this->commands[$name]);
    }

    public function cloneDriver(): ShellDriverInterface
    {
        $new = new self(
            cwd: $this->cwd,
            env: $this->env,
            execOverride: $this->execOverride,
        );
        $new->fsDriver = new BuiltinFilesystemDriver($this->fsDriver->vfs()->cloneFs());
        $new->commands = $this->commands;
        $new->onNotFound = $this->onNotFound;
        return $new;
    }

    public function setOnNotFound(?\Closure $callback): void
    {
        $this->onNotFound = $callback;
    }

    public function getOnNotFound(): ?\Closure
    {
        return $this->onNotFound;
    }
}
