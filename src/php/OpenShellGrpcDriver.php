<?php

declare(strict_types=1);

namespace AgentHarness;

if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

class OpenShellGrpcDriver implements ShellDriverInterface
{
    use RemoteSyncTrait;

    private string $cwd;
    private array $env;
    private DirtyTrackingFS $fsDriver;
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    /** @var (\Closure(string): array{stdout: string, stderr: string, exitCode: int})|null */
    private ?\Closure $execOverride;
    private string $endpoint;
    private ?string $sandboxId;
    /** @var array{filesystemAllow?: list<string>, networkRules?: list<array>, inferenceRouting?: bool} */
    private array $policy;
    private string $sshHost;
    private int $sshPort;
    private string $sshUser;
    private string $workspace;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        ?\Closure $execOverride = null,
        string $endpoint = 'localhost:50051',
        ?string $sandboxId = null,
        array $policy = [],
        string $sshHost = 'localhost',
        int $sshPort = 2222,
        string $sshUser = 'sandbox',
        string $workspace = '/home/sandbox/workspace',
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new DirtyTrackingFS(new BuiltinFilesystemDriver());
        $this->execOverride = $execOverride;
        $this->endpoint = $endpoint;
        $this->sandboxId = $sandboxId;
        $this->policy = array_merge(['inferenceRouting' => true], $policy);
        $this->sshHost = $sshHost;
        $this->sshPort = $sshPort;
        $this->sshUser = $sshUser;
        $this->workspace = $workspace;
    }

    private function ensureSandbox(): void
    {
        if ($this->sandboxId !== null) {
            return;
        }

        if ($this->execOverride !== null) {
            $override = $this->execOverride;
            if (is_object($override) && method_exists($override, 'createSandbox')) {
                $result = $override->createSandbox($this->policy);
                $this->sandboxId = $result->sandboxId;
                return;
            }
        }

        $this->sandboxId = "{$this->sshUser}@{$this->sshHost}:{$this->sshPort}";
    }

    public function fs(): FilesystemDriver { return $this->fsDriver; }
    public function cwd(): string { return $this->cwd; }
    public function env(): array { return $this->env; }
    public function sandboxId(): ?string { return $this->sandboxId; }
    public function policy(): array { return $this->policy; }

    /**
     * @return array{stdout: string, stderr: string, exitCode: int}
     */
    private function rawExec(string $command): array
    {
        $this->ensureSandbox();

        if ($this->execOverride !== null) {
            return ($this->execOverride)($command);
        }

        // Real execution via SSH into the OpenShell sandbox
        $escaped = escapeshellarg($command);
        $sshCmd = sprintf(
            'ssh -p %d -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR %s@%s %s',
            $this->sshPort,
            escapeshellarg($this->sshUser),
            escapeshellarg($this->sshHost),
            $escaped,
        );
        $descriptors = [
            0 => ['pipe', 'r'],
            1 => ['pipe', 'w'],
            2 => ['pipe', 'w'],
        ];
        $mergedEnv = array_merge(getenv(), $this->env);
        $proc = proc_open($sshCmd, $descriptors, $pipes, null, $mergedEnv);
        if (!is_resource($proc)) {
            return ['stdout' => '', 'stderr' => 'Failed to spawn ssh', 'exitCode' => 1];
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

    private function remapPath(string $vfsPath): string
    {
        if ($this->execOverride !== null) {
            return $vfsPath;
        }
        return rtrim($this->workspace, '/') . '/' . ltrim($vfsPath, '/');
    }

    private function unmapPath(string $remotePath): string
    {
        if ($this->execOverride !== null) {
            return $remotePath;
        }
        $prefix = rtrim($this->workspace, '/') . '/';
        if (str_starts_with($remotePath, $prefix)) {
            return '/' . substr($remotePath, strlen($prefix));
        }
        return $remotePath;
    }

    public function exec(string $command): ExecResult
    {
        $marker = '__HARNESS_FS_SYNC_' . (int)(microtime(true) * 1000) . '__';

        if ($this->execOverride !== null) {
            // Mock mode: standard sync
            $preamble = $this->buildSyncPreamble();
            $epilogue = $this->buildSyncEpilogue($marker);
        } else {
            // Real mode: remap paths to workspace
            $parts = ["mkdir -p {$this->workspace}"];
            foreach ($this->fsDriver->getDirty() as $path) {
                $remote = $this->remapPath($path);
                if ($this->fsDriver->exists($path) && !$this->fsDriver->isDir($path)) {
                    $content = $this->fsDriver->readText($path);
                    $encoded = base64_encode($content);
                    $parts[] = "mkdir -p \$(dirname '{$remote}') && printf '%s' '{$encoded}' | base64 -d > '{$remote}'";
                } elseif (!$this->fsDriver->exists($path)) {
                    $parts[] = "rm -f '{$remote}'";
                }
            }
            $this->fsDriver->clearDirty();
            $preamble = implode(' && ', $parts);
            $epilogue = $this->buildSyncEpilogue($marker, $this->workspace);
        }

        $fullCommand = $preamble !== ''
            ? "{$preamble} && {$command}{$epilogue}"
            : "{$command}{$epilogue}";

        $raw = $this->rawExec($fullCommand);
        $parsed = $this->parseSyncOutput($raw['stdout'], $marker);

        if ($parsed['files'] !== null) {
            // Remap remote paths back to VFS paths
            $remapped = [];
            foreach ($parsed['files'] as $remotePath => $content) {
                $remapped[$this->unmapPath($remotePath)] = $content;
            }
            $this->applySyncBack($remapped);
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
        return ['custom_commands', 'remote', 'policies', 'streaming'];
    }

    public function close(): void
    {
        if ($this->sandboxId !== null) {
            if ($this->execOverride !== null && is_object($this->execOverride) && method_exists($this->execOverride, 'deleteSandbox')) {
                $this->execOverride->deleteSandbox($this->sandboxId);
            }
            $this->sandboxId = null;
        }
    }

    public function cloneDriver(): ShellDriverInterface
    {
        $new = new self(
            cwd: $this->cwd,
            env: $this->env,
            execOverride: $this->execOverride,
            endpoint: $this->endpoint,
            sandboxId: null, // New sandbox on first exec
            policy: $this->policy,
            sshHost: $this->sshHost,
            sshPort: $this->sshPort,
            sshUser: $this->sshUser,
            workspace: $this->workspace,
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
