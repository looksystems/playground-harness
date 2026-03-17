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
    /** @var 'ssh'|'grpc' */
    private string $transport;
    private mixed $grpcClient;

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
        string $transport = 'ssh',
        mixed $grpcClient = null,
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
        $this->transport = $transport;
        $this->grpcClient = $grpcClient;
    }

    private function getGrpcClient(): mixed
    {
        if ($this->grpcClient !== null) {
            return $this->grpcClient;
        }
        $clientClass = \AgentHarness\Generated\OpenShell\OpenShellClient::class;
        $this->grpcClient = new $clientClass($this->endpoint, [
            'credentials' => \Grpc\ChannelCredentials::createInsecure(),
        ]);
        return $this->grpcClient;
    }

    private function buildSandboxSpec(): \AgentHarness\Generated\OpenShell\SandboxSpec
    {
        $fsPolicy = new \AgentHarness\Generated\OpenShell\FilesystemPolicy(
            readWrite: $this->policy['filesystemAllow'] ?? [],
        );
        $netPolicies = [];
        foreach ($this->policy['networkRules'] ?? [] as $rule) {
            $deny = $rule['deny'] ?? null;
            $allow = $rule['allow'] ?? null;
            $netPolicies[] = new \AgentHarness\Generated\OpenShell\NetworkPolicy(
                cidr: $allow ?? $deny ?? '',
                action: $deny !== null
                    ? \AgentHarness\Generated\OpenShell\NetworkPolicy::DENY
                    : \AgentHarness\Generated\OpenShell\NetworkPolicy::ALLOW,
            );
        }
        $infPolicy = new \AgentHarness\Generated\OpenShell\InferencePolicy(
            routingEnabled: $this->policy['inferenceRouting'] ?? true,
        );
        $policy = new \AgentHarness\Generated\OpenShell\SandboxPolicy(
            filesystem: $fsPolicy,
            networkPolicies: $netPolicies,
            inference: $infPolicy,
        );
        return new \AgentHarness\Generated\OpenShell\SandboxSpec(
            policy: $policy,
            workspace: $this->workspace,
        );
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

        if ($this->transport === 'grpc') {
            $this->ensureSandboxGrpc();
            return;
        }

        $this->sandboxId = "{$this->sshUser}@{$this->sshHost}:{$this->sshPort}";
    }

    private function ensureSandboxGrpc(): void
    {
        $client = $this->getGrpcClient();
        $spec = $this->buildSandboxSpec();
        $request = new \AgentHarness\Generated\OpenShell\CreateSandboxRequest(
            name: 'harness-' . bin2hex(random_bytes(4)),
            spec: $spec,
        );
        $response = $client->CreateSandbox($request);
        $this->sandboxId = $response->sandbox->sandboxId ?? $response->sandbox->name;
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

        if ($this->transport === 'grpc') {
            return $this->rawExecGrpc($command);
        }
        return $this->rawExecSsh($command);
    }

    /**
     * @return array{stdout: string, stderr: string, exitCode: int}
     */
    private function rawExecSsh(string $command): array
    {
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

    /**
     * @return array{stdout: string, stderr: string, exitCode: int}
     */
    private function rawExecGrpc(string $command): array
    {
        $client = $this->getGrpcClient();
        $request = new \AgentHarness\Generated\OpenShell\ExecSandboxRequest(
            sandboxId: $this->sandboxId,
            command: ['bash', '-c', $command],
            env: $this->env,
        );
        $stdoutParts = [];
        $stderrParts = [];
        $exitCode = 0;
        foreach ($client->ExecSandbox($request) as $event) {
            if ($event->stdout !== null) {
                $stdoutParts[] = $event->stdout->data;
            } elseif ($event->stderr !== null) {
                $stderrParts[] = $event->stderr->data;
            } elseif ($event->exit !== null) {
                $exitCode = $event->exit->code;
            }
        }
        return [
            'stdout' => implode('', $stdoutParts),
            'stderr' => implode('', $stderrParts),
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

    private function tryCustomCommand(string $command): ?ExecResult
    {
        $parts = preg_split('/\s+/', trim($command), -1, PREG_SPLIT_NO_EMPTY);
        if (empty($parts)) {
            return null;
        }
        $handler = $this->commands[$parts[0]] ?? null;
        if ($handler === null) {
            return null;
        }
        return $handler(array_slice($parts, 1), '');
    }

    public function exec(string $command): ExecResult
    {
        $customResult = $this->tryCustomCommand($command);
        if ($customResult !== null) {
            return $customResult;
        }

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

    /**
     * Execute a command via gRPC streaming, yielding events.
     *
     * Requires transport='grpc'. VFS sync-back happens after the stream completes.
     *
     * @return \Generator<array{type: string, data?: string, exitCode?: int}>
     */
    public function execStream(string $command): \Generator
    {
        if ($this->transport !== 'grpc' && $this->execOverride === null) {
            throw new \RuntimeException("execStream requires transport='grpc'");
        }

        $customResult = $this->tryCustomCommand($command);
        if ($customResult !== null) {
            if ($customResult->stdout !== '') {
                yield ['type' => 'stdout', 'data' => $customResult->stdout];
            }
            if ($customResult->stderr !== '') {
                yield ['type' => 'stderr', 'data' => $customResult->stderr];
            }
            yield ['type' => 'exit', 'exitCode' => $customResult->exitCode];
            return;
        }

        $marker = '__HARNESS_FS_SYNC_' . (int)(microtime(true) * 1000) . '__';

        if ($this->execOverride !== null) {
            $preamble = $this->buildSyncPreamble();
            $epilogue = $this->buildSyncEpilogue($marker);
        } else {
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

        $this->ensureSandbox();

        if ($this->execOverride !== null) {
            $raw = ($this->execOverride)($fullCommand);
            $parsed = $this->parseSyncOutput($raw['stdout'], $marker);
            if ($parsed['stdout'] !== '') {
                yield ['type' => 'stdout', 'data' => $parsed['stdout']];
            }
            if ($raw['stderr'] !== '') {
                yield ['type' => 'stderr', 'data' => $raw['stderr']];
            }
            yield ['type' => 'exit', 'exitCode' => $raw['exitCode']];

            if ($parsed['files'] !== null) {
                $remapped = [];
                foreach ($parsed['files'] as $remotePath => $content) {
                    $remapped[$this->unmapPath($remotePath)] = $content;
                }
                $this->applySyncBack($remapped);
            }
            return;
        }

        $client = $this->getGrpcClient();
        $request = new \AgentHarness\Generated\OpenShell\ExecSandboxRequest(
            sandboxId: $this->sandboxId,
            command: ['bash', '-c', $fullCommand],
            env: $this->env,
        );

        $stdoutAccum = [];
        foreach ($client->ExecSandbox($request) as $event) {
            if ($event->stdout !== null) {
                $chunk = $event->stdout->data;
                $stdoutAccum[] = $chunk;
                yield ['type' => 'stdout', 'data' => $chunk];
            } elseif ($event->stderr !== null) {
                yield ['type' => 'stderr', 'data' => $event->stderr->data];
            } elseif ($event->exit !== null) {
                yield ['type' => 'exit', 'exitCode' => $event->exit->code];
            }
        }

        $rawStdout = implode('', $stdoutAccum);
        $parsed = $this->parseSyncOutput($rawStdout, $marker);
        if ($parsed['files'] !== null) {
            $remapped = [];
            foreach ($parsed['files'] as $remotePath => $content) {
                $remapped[$this->unmapPath($remotePath)] = $content;
            }
            $this->applySyncBack($remapped);
        }
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
        $caps = ['custom_commands', 'remote', 'policies'];
        if ($this->transport === 'grpc') {
            $caps[] = 'streaming';
        }
        return $caps;
    }

    public function close(): void
    {
        if ($this->sandboxId !== null) {
            if ($this->execOverride !== null && is_object($this->execOverride) && method_exists($this->execOverride, 'deleteSandbox')) {
                $this->execOverride->deleteSandbox($this->sandboxId);
            } elseif ($this->transport === 'grpc') {
                $client = $this->getGrpcClient();
                $request = new \AgentHarness\Generated\OpenShell\DeleteSandboxRequest(
                    name: $this->sandboxId,
                );
                try {
                    $client->DeleteSandbox($request);
                } catch (\Throwable) {
                    // Best-effort cleanup
                }
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
            sandboxId: null,
            policy: $this->policy,
            sshHost: $this->sshHost,
            sshPort: $this->sshPort,
            sshUser: $this->sshUser,
            workspace: $this->workspace,
            transport: $this->transport,
            grpcClient: $this->grpcClient,
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
