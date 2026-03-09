<?php

declare(strict_types=1);

namespace AgentHarness;

if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

class DirtyTrackingFS implements FilesystemDriver
{
    private FilesystemDriver $inner;
    /** @var array<string, true> */
    private array $dirty = [];

    public function __construct(FilesystemDriver $inner)
    {
        $this->inner = $inner;
    }

    public function inner(): FilesystemDriver { return $this->inner; }

    /** @return list<string> */
    public function getDirty(): array { return array_keys($this->dirty); }

    public function clearDirty(): void { $this->dirty = []; }

    public function write(string $path, string $content): void
    {
        $this->inner->write($path, $content);
        $this->dirty[$path] = true;
    }

    public function writeLazy(string $path, \Closure $provider): void
    {
        $this->inner->writeLazy($path, $provider);
        $this->dirty[$path] = true;
    }

    public function read(string $path): string { return $this->inner->read($path); }
    public function readText(string $path): string { return $this->inner->readText($path); }
    public function exists(string $path): bool { return $this->inner->exists($path); }

    public function remove(string $path): void
    {
        $this->inner->remove($path);
        $this->dirty[$path] = true;
    }

    public function isDir(string $path): bool { return $this->inner->isDir($path); }
    public function listdir(string $path = '/'): array { return $this->inner->listdir($path); }
    public function find(string $root = '/', string $pattern = '*'): array { return $this->inner->find($root, $pattern); }
    public function stat(string $path): array { return $this->inner->stat($path); }

    public function cloneFs(): FilesystemDriver
    {
        return new self($this->inner->cloneFs());
    }
}

class BashkitCLIDriver implements ShellDriverInterface
{
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

    private function buildSyncPreamble(): string
    {
        $commands = [];
        foreach ($this->fsDriver->getDirty() as $path) {
            if ($this->fsDriver->exists($path) && !$this->fsDriver->isDir($path)) {
                $content = $this->fsDriver->readText($path);
                $encoded = base64_encode($content);
                $commands[] = "mkdir -p \$(dirname '{$path}') && printf '%s' '{$encoded}' | base64 -d > '{$path}'";
            } elseif (!$this->fsDriver->exists($path)) {
                $commands[] = "rm -f '{$path}'";
            }
        }
        $this->fsDriver->clearDirty();
        return count($commands) > 0 ? implode(' && ', $commands) : '';
    }

    private function buildSyncEpilogue(string $marker): string
    {
        return "; __exit=\$?; printf '\\n" . $marker . "\\n';"
            . ' find / -type f 2>/dev/null -exec sh -c'
            . " 'for f; do printf \"===FILE:%s===\\n\" \"\$f\"; base64 \"\$f\"; done' _ {} +;"
            . ' exit $__exit';
    }

    /**
     * @return array{stdout: string, files: array<string, string>|null}
     */
    private function parseSyncOutput(string $raw, string $marker): array
    {
        $markerPos = strpos($raw, "\n{$marker}\n");
        if ($markerPos === false) {
            return ['stdout' => $raw, 'files' => null];
        }
        $files = [];
        $stdout = substr($raw, 0, $markerPos);
        $syncData = substr($raw, $markerPos + strlen($marker) + 2);

        $fileMarker = '===FILE:';
        $endMarker = '===';
        $currentPath = null;
        $contentLines = [];

        foreach (explode("\n", $syncData) as $line) {
            if (str_starts_with($line, $fileMarker) && str_ends_with($line, $endMarker) && strlen($line) > strlen($fileMarker) + strlen($endMarker)) {
                if ($currentPath !== null) {
                    $encoded = implode('', $contentLines);
                    $decoded = base64_decode($encoded, true);
                    $files[$currentPath] = $decoded !== false ? $decoded : $encoded;
                }
                $currentPath = substr($line, strlen($fileMarker), -(strlen($endMarker)));
                $contentLines = [];
            } elseif ($currentPath !== null) {
                $contentLines[] = $line;
            }
        }
        if ($currentPath !== null) {
            $encoded = implode('', $contentLines);
            $decoded = base64_decode($encoded, true);
            $files[$currentPath] = $decoded !== false ? $decoded : $encoded;
        }

        return ['stdout' => $stdout, 'files' => $files];
    }

    /**
     * @param array<string, string> $files
     */
    private function applySyncBack(array $files): void
    {
        $vfsFiles = [];
        foreach ($this->fsDriver->find('/', '*') as $path) {
            if (!$this->fsDriver->isDir($path)) {
                $vfsFiles[$path] = true;
            }
        }

        foreach ($files as $path => $content) {
            if (!isset($vfsFiles[$path])) {
                $this->fsDriver->inner()->write($path, $content);
            } else {
                $existing = $this->fsDriver->readText($path);
                if ($existing !== $content) {
                    $this->fsDriver->inner()->write($path, $content);
                }
            }
        }

        foreach (array_keys($vfsFiles) as $path) {
            if (!isset($files[$path]) && $this->fsDriver->exists($path)) {
                $this->fsDriver->inner()->remove($path);
            }
        }
    }

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
