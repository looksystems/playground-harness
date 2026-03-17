<?php

declare(strict_types=1);

namespace AgentHarness;

if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

class BashkitCLIDriver implements ShellDriverInterface
{
    use RemoteSyncTrait;

    private string $cwd;
    private array $env;
    private DirtyTrackingFS $fsDriver;
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
        $this->fsDriver = new DirtyTrackingFS(new BuiltinFilesystemDriver());
        $this->execOverride = $execOverride;
    }

    public function fs(): FilesystemDriver { return $this->fsDriver; }
    public function cwd(): string { return $this->cwd; }
    public function env(): array { return $this->env; }

    /**
     * @return array{stdout: string, stderr: string, exitCode: int}
     */
    private function rawExec(string $command): array
    {
        if ($this->execOverride !== null) {
            return ($this->execOverride)($command);
        }

        $escaped = escapeshellarg($command);
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];
        $proc = proc_open("bashkit -c {$escaped}", $descriptors, $pipes);
        if (!is_resource($proc)) {
            return ['stdout' => '', 'stderr' => 'Failed to spawn bashkit', 'exitCode' => 1];
        }

        fclose($pipes[0]);
        $stdout = stream_get_contents($pipes[1]);
        $stderr = stream_get_contents($pipes[2]);
        fclose($pipes[1]);
        fclose($pipes[2]);
        $exitCode = proc_close($proc);

        return [
            'stdout' => $stdout ?: '',
            'stderr' => $stderr ?: '',
            'exitCode' => $exitCode,
        ];
    }

    public function exec(string $command): ExecResult
    {
        $preamble = $this->buildSyncPreamble();
        $marker = '__HARNESS_FS_SYNC_' . (int)(microtime(true) * 1000) . '__';
        $epilogue = $this->buildSyncEpilogue($marker);

        $fullCommand = $preamble !== ''
            ? "{$preamble} && {$command}{$epilogue}"
            : "{$command}{$epilogue}";

        $raw = $this->rawExec($fullCommand);
        $parsed = $this->parseSyncOutput($raw['stdout'], $marker);

        if ($parsed['files'] !== null) {
            $this->applySyncBack($parsed['files']);
        }

        return new ExecResult(
            stdout: $parsed['stdout'],
            stderr: $raw['stderr'],
            exitCode: $raw['exitCode'],
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

    public function capabilities(): array
    {
        return ['custom_commands', 'remote'];
    }

    public function cloneDriver(): ShellDriverInterface
    {
        $new = new self(
            cwd: $this->cwd,
            env: $this->env,
            execOverride: $this->execOverride,
        );
        $new->fsDriver = $this->fsDriver->cloneFs();
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
