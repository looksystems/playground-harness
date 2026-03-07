<?php

declare(strict_types=1);

namespace AgentHarness;

// ExecResult is defined in Shell.php; ensure it is loaded.
if (!class_exists(ExecResult::class, false)) {
    class_exists(Shell::class);
}

/**
 * ShellDriver that calls bashkit via FFI through a shared C library.
 */
class BashkitNativeDriver implements ShellDriverInterface
{
    private string $cwd;
    /** @var array<string, string> */
    private array $env;
    private BuiltinFilesystemDriver $fsDriver;
    /** @var array<string, \Closure> */
    private array $commands = [];
    private ?\Closure $onNotFound = null;
    /** @var mixed FFI instance or mock library */
    private mixed $lib;
    /** @var object Context handle from bashkit_create */
    private object $ctx;

    private const C_HEADER = <<<'CDEF'
    void* bashkit_create(const char* config_json);
    void  bashkit_destroy(void* ctx);
    char* bashkit_exec(void* ctx, const char* request_json);
    void  bashkit_register_command(void* ctx, const char* name, void (*callback)(const char*, void*), void* userdata);
    void  bashkit_unregister_command(void* ctx, const char* name);
    void  bashkit_free_string(char* s);
    CDEF;

    public function __construct(
        string $cwd = '/',
        array $env = [],
        mixed $libOverride = null,
    ) {
        $this->cwd = $cwd;
        $this->env = $env;
        $this->fsDriver = new BuiltinFilesystemDriver();

        $this->lib = $libOverride ?? $this->loadLibrary();

        $config = json_encode(['cwd' => $this->cwd, 'env' => $this->env], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        $this->ctx = $this->lib->bashkit_create($config);
    }

    /**
     * Load the bashkit shared library via PHP FFI.
     */
    private function loadLibrary(): \FFI
    {
        $path = static::findLibrary();
        if ($path === null) {
            throw new \RuntimeException(
                'bashkit shared library not found. Set BASHKIT_LIB_PATH or '
                . 'install libashkit to a standard library path.'
            );
        }
        return \FFI::cdef(self::C_HEADER, $path);
    }

    /**
     * Discover the bashkit shared library path.
     *
     * Search order:
     * 1. BASHKIT_LIB_PATH env var (exact path)
     * 2. Platform library search paths (DYLD_LIBRARY_PATH/LD_LIBRARY_PATH)
     * 3. Standard system paths (/usr/local/lib, /usr/lib)
     */
    public static function findLibrary(): ?string
    {
        // 1. Explicit env var
        $envPath = getenv('BASHKIT_LIB_PATH');
        if ($envPath !== false && $envPath !== '') {
            return is_file($envPath) ? $envPath : null;
        }

        // 2. Platform-specific library name
        if (PHP_OS_FAMILY === 'Darwin') {
            $libName = 'libashkit.dylib';
            $pathVar = 'DYLD_LIBRARY_PATH';
        } elseif (PHP_OS_FAMILY === 'Windows') {
            $libName = 'bashkit.dll';
            $pathVar = 'PATH';
        } else {
            $libName = 'libashkit.so';
            $pathVar = 'LD_LIBRARY_PATH';
        }

        // Search dirs from env var + standard paths
        $searchDirs = [];
        $envDirs = getenv($pathVar);
        if ($envDirs !== false && $envDirs !== '') {
            $searchDirs = array_merge($searchDirs, explode(PATH_SEPARATOR, $envDirs));
        }
        $searchDirs[] = '/usr/local/lib';
        $searchDirs[] = '/usr/lib';

        foreach ($searchDirs as $dir) {
            $candidate = $dir . DIRECTORY_SEPARATOR . $libName;
            if (is_file($candidate)) {
                return $candidate;
            }
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

    /**
     * Wrap a PHP handler into a closure suitable for the C callback.
     */
    private function wrapCallback(string $name, \Closure $handler): \Closure
    {
        return function (string $argsJson) use ($handler): string {
            try {
                $request = json_decode($argsJson, true);
                $args = $request['args'] ?? [];
                $stdin = $request['stdin'] ?? '';

                $result = $handler($args, $stdin);

                // Support both ExecResult and raw string returns
                if ($result instanceof ExecResult) {
                    return $result->stdout;
                }
                return (string) $result;
            } catch (\Throwable $e) {
                return json_encode(['error' => $e->getMessage()]);
            }
        };
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
        $snapshot = $this->snapshotFs();

        $request = json_encode([
            'cmd' => $command,
            'cwd' => $this->cwd,
            'env' => $this->env ?: new \stdClass(),
            'fs' => $snapshot ?: new \stdClass(),
        ], JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

        $responseJson = $this->lib->bashkit_exec($this->ctx, $request);
        $response = json_decode($responseJson, true);

        // Error response
        if (isset($response['error'])) {
            $error = $response['error'];
            $msg = is_array($error) ? ($error['message'] ?? 'Unknown error') : (string) $error;
            return new ExecResult(stderr: $msg, exitCode: 1);
        }

        // Apply filesystem changes
        if (isset($response['fs_changes'])) {
            $this->applyFsChanges($response['fs_changes']);
        }

        return new ExecResult(
            stdout: $response['stdout'] ?? '',
            stderr: $response['stderr'] ?? '',
            exitCode: $response['exitCode'] ?? 0,
        );
    }

    public function registerCommand(string $name, \Closure $handler): void
    {
        $this->commands[$name] = $handler;
        $wrapped = $this->wrapCallback($name, $handler);
        $this->lib->bashkit_register_command($this->ctx, $name, $wrapped, null);
    }

    public function unregisterCommand(string $name): void
    {
        unset($this->commands[$name]);
        $this->lib->bashkit_unregister_command($this->ctx, $name);
    }

    public function hasCommand(string $name): bool
    {
        return isset($this->commands[$name]);
    }

    public function cloneDriver(mixed $libOverride = null): ShellDriverInterface
    {
        $newDriver = new self(
            cwd: $this->cwd,
            env: $this->env,
            libOverride: $libOverride ?? $this->lib,
        );

        // Clone the filesystem
        $newDriver->fsDriver = new BuiltinFilesystemDriver($this->fsDriver->vfs()->cloneFs());

        // Copy registered commands and re-register with the new context
        $newDriver->commands = $this->commands;
        $newDriver->onNotFound = $this->onNotFound;

        foreach ($this->commands as $name => $handler) {
            $wrapped = $newDriver->wrapCallback($name, $handler);
            $newDriver->lib->bashkit_register_command($newDriver->ctx, $name, $wrapped, null);
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
        if (isset($this->lib) && isset($this->ctx)) {
            try {
                $this->lib->bashkit_destroy($this->ctx);
            } catch (\Throwable) {
                // Ignore cleanup errors
            }
        }
    }
}
