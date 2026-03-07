<?php

declare(strict_types=1);

namespace AgentHarness;

// ExecResult is defined in Shell.php; ensure it is loaded.
if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

/**
 * ShellDriver that communicates with bashkit-cli via JSON-RPC over stdio.
 */
class BashkitIPCDriver implements ShellDriverInterface
{
    private string $cwd;
    /** @var array<string, string> */
    private array $env;
    private BuiltinFilesystemDriver $fsDriver;
    /** @var array<string, \Closure> */
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    private int $requestId = 0;
    /** @var mixed Process override object or proc_open resource bundle */
    private mixed $process;
    /** @var resource|null */
    private mixed $procHandle = null;
    /** @var resource|null */
    private mixed $stdin = null;
    /** @var resource|null */
    private mixed $stdout = null;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        mixed $processOverride = null,
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new BuiltinFilesystemDriver();

        if ($processOverride !== null) {
            $this->process = $processOverride;
        } else {
            $this->spawnProcess();
        }
    }

    private function spawnProcess(): void
    {
        $descriptors = [
            0 => ['pipe', 'r'], // stdin
            1 => ['pipe', 'w'], // stdout
            2 => ['pipe', 'w'], // stderr
        ];
        $proc = proc_open(['bashkit-cli', '--jsonrpc'], $descriptors, $pipes);
        if (!is_resource($proc)) {
            throw new \RuntimeException('Failed to spawn bashkit-cli');
        }
        $this->procHandle = $proc;
        $this->stdin = $pipes[0];
        $this->stdout = $pipes[1];
        $this->process = null;
    }

    private function nextId(): int
    {
        return ++$this->requestId;
    }

    private function send(array $msg): void
    {
        $line = json_encode($msg, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE) . "\n";
        if ($this->process !== null) {
            $this->process->write($line);
        } elseif ($this->stdin !== null) {
            fwrite($this->stdin, $line);
            fflush($this->stdin);
        }
    }

    private function recv(): ?array
    {
        if ($this->process !== null) {
            $line = $this->process->readline();
            if ($line === null || $line === '') {
                return null;
            }
            return json_decode(trim($line), true);
        }

        if ($this->stdout !== null) {
            $line = fgets($this->stdout);
            if ($line === false || $line === '') {
                return null;
            }
            return json_decode(trim($line), true);
        }

        return null;
    }

    /**
     * Serialize all VFS files to a dict, resolving lazy providers.
     * @return array<string, string>
     */
    private function snapshotFs(): array
    {
        $snapshot = [];
        foreach ($this->fsDriver->find('/', '*') as $path) {
            if (!$this->fsDriver->isDir($path)) {
                $snapshot[$path] = $this->fsDriver->readText($path);
            }
        }
        return $snapshot;
    }

    /**
     * Apply created files and deleted paths back to the host FS.
     */
    private function applyFsChanges(array $changes): void
    {
        $created = $changes['created'] ?? [];
        $deleted = $changes['deleted'] ?? [];

        if (is_array($created)) {
            foreach ($created as $path => $content) {
                $this->fsDriver->write($path, $content);
            }
        }

        foreach ($deleted as $path) {
            if ($this->fsDriver->exists($path)) {
                $this->fsDriver->remove($path);
            }
        }
    }

    public function fs(): FilesystemDriver
    {
        return $this->fsDriver;
    }

    public function cwd(): string
    {
        return $this->cwd;
    }

    public function env(): array
    {
        return $this->env;
    }

    public function exec(string $command): ExecResult
    {
        $reqId = $this->nextId();
        $snapshot = $this->snapshotFs();

        $this->send([
            'id' => $reqId,
            'method' => 'exec',
            'params' => [
                'cmd' => $command,
                'cwd' => $this->cwd,
                'env' => $this->env ?: new \stdClass(),
                'fs' => $snapshot ?: new \stdClass(),
            ],
        ]);

        // Event loop: handle callbacks until we get the final result
        while (true) {
            $response = $this->recv();
            if ($response === null) {
                return new ExecResult(stderr: 'No response from bashkit-cli', exitCode: 1);
            }

            // Error response
            if (isset($response['error'])) {
                $error = $response['error'];
                $msg = is_array($error) ? ($error['message'] ?? 'Unknown error') : (string) $error;
                return new ExecResult(stderr: $msg, exitCode: 1);
            }

            // Callback: invoke_command from bashkit
            if (($response['method'] ?? '') === 'invoke_command') {
                $cbId = $response['id'] ?? null;
                $params = $response['params'] ?? [];
                $name = $params['name'] ?? '';
                $args = $params['args'] ?? [];
                $stdin = $params['stdin'] ?? '';

                if (is_string($args)) {
                    $args = $args !== '' ? explode(' ', $args) : [];
                }

                if (isset($this->commands[$name])) {
                    try {
                        $result = ($this->commands[$name])($args, $stdin);
                        $this->send(['id' => $cbId, 'result' => $result]);
                    } catch (\Throwable $e) {
                        $this->send(['id' => $cbId, 'error' => ['code' => -1, 'message' => $e->getMessage()]]);
                    }
                } else {
                    $this->send(['id' => $cbId, 'error' => ['code' => -1, 'message' => "Unknown command: {$name}"]]);
                }
                continue;
            }

            // Final result
            if (isset($response['result'])) {
                $r = $response['result'];
                if (isset($r['fs_changes'])) {
                    $this->applyFsChanges($r['fs_changes']);
                }
                return new ExecResult(
                    stdout: $r['stdout'] ?? '',
                    stderr: $r['stderr'] ?? '',
                    exitCode: $r['exitCode'] ?? 0,
                );
            }
        }
    }

    public function registerCommand(string $name, \Closure $handler): void
    {
        $this->commands[$name] = $handler;
        $this->send([
            'method' => 'register_command',
            'params' => ['name' => $name],
        ]);
    }

    public function unregisterCommand(string $name): void
    {
        unset($this->commands[$name]);
        $this->send([
            'method' => 'unregister_command',
            'params' => ['name' => $name],
        ]);
    }

    public function hasCommand(string $name): bool
    {
        return isset($this->commands[$name]);
    }

    public function cloneDriver(mixed $processOverride = null): ShellDriverInterface
    {
        $newDriver = new self(
            cwd: $this->cwd,
            env: $this->env,
            processOverride: $processOverride,
        );

        // Clone the filesystem
        $newDriver->fsDriver = new BuiltinFilesystemDriver($this->fsDriver->vfs()->cloneFs());

        // Copy registered commands and re-register with the new process
        $newDriver->commands = $this->commands;
        $newDriver->onNotFound = $this->onNotFound;

        foreach ($this->commands as $name => $handler) {
            $newDriver->send([
                'method' => 'register_command',
                'params' => ['name' => $name],
            ]);
        }

        return $newDriver;
    }

    public function setOnNotFound(?\Closure $callback): void
    {
        $this->onNotFound = $callback;
    }

    public function getOnNotFound(): ?\Closure
    {
        return $this->onNotFound;
    }

    public function __destruct()
    {
        if ($this->stdin !== null) {
            @fclose($this->stdin);
        }
        if ($this->stdout !== null) {
            @fclose($this->stdout);
        }
        if ($this->procHandle !== null && is_resource($this->procHandle)) {
            @proc_terminate($this->procHandle);
            @proc_close($this->procHandle);
        }
    }
}
