<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Simple in-memory filesystem. Paths are always absolute and normalized.
 */
class VirtualFS
{
    /** @var array<string, string> */
    private array $files = [];

    /** @var array<string, \Closure> */
    private array $lazy = [];

    /**
     * @param array<string, string>|null $files
     */
    public function __construct(?array $files = null)
    {
        if ($files !== null) {
            foreach ($files as $path => $content) {
                $this->write($path, $content);
            }
        }
    }

    private static function norm(string $path): string
    {
        // Ensure path starts with /
        if ($path === '' || $path[0] !== '/') {
            $path = '/' . $path;
        }

        $parts = explode('/', $path);
        $normalized = [];
        foreach ($parts as $part) {
            if ($part === '' || $part === '.') {
                continue;
            }
            if ($part === '..') {
                array_pop($normalized);
            } else {
                $normalized[] = $part;
            }
        }

        return '/' . implode('/', $normalized);
    }

    public function write(string $path, string $content): void
    {
        $path = self::norm($path);
        $this->files[$path] = $content;
    }

    /**
     * Register a lazy file -- provider called on first read, then cached.
     */
    public function writeLazy(string $path, \Closure $provider): void
    {
        $path = self::norm($path);
        $this->lazy[$path] = $provider;
    }

    public function read(string $path): string
    {
        $path = self::norm($path);

        // Resolve lazy providers
        if (isset($this->lazy[$path])) {
            $provider = $this->lazy[$path];
            unset($this->lazy[$path]);
            $this->files[$path] = $provider();
        }

        if (!isset($this->files[$path])) {
            throw new \RuntimeException("{$path}: No such file");
        }

        return $this->files[$path];
    }

    /**
     * Alias for read() since PHP version is string-only.
     */
    public function readText(string $path): string
    {
        return $this->read($path);
    }

    public function exists(string $path): bool
    {
        $path = self::norm($path);
        return isset($this->files[$path]) || isset($this->lazy[$path]) || $this->isDir($path);
    }

    public function remove(string $path): void
    {
        $path = self::norm($path);

        if (isset($this->files[$path])) {
            unset($this->files[$path]);
        } elseif (isset($this->lazy[$path])) {
            unset($this->lazy[$path]);
        } else {
            throw new \RuntimeException("{$path}: No such file");
        }
    }

    /**
     * @return array<string>
     */
    private function allPaths(): array
    {
        return array_unique(array_merge(array_keys($this->files), array_keys($this->lazy)));
    }

    public function isDir(string $path): bool
    {
        $path = self::norm($path);
        $prefix = rtrim($path, '/') . '/';
        foreach ($this->allPaths() as $p) {
            if (str_starts_with($p, $prefix)) {
                return true;
            }
        }
        return false;
    }

    /**
     * @return list<string>
     */
    public function listdir(string $path = '/'): array
    {
        $path = rtrim(self::norm($path), '/') . '/';
        if ($path === '//') {
            $path = '/';
        }

        $entries = [];
        foreach ($this->allPaths() as $p) {
            if (str_starts_with($p, $path) && $p !== $path) {
                $rest = substr($p, strlen($path));
                $entry = explode('/', $rest)[0];
                $entries[$entry] = true;
            }
        }

        $result = array_keys($entries);
        sort($result);
        return $result;
    }

    /**
     * @return list<string>
     */
    public function find(string $root = '/', string $pattern = '*'): array
    {
        $root = rtrim(self::norm($root), '/');
        $paths = $this->allPaths();
        sort($paths);

        $results = [];
        foreach ($paths as $p) {
            if (!str_starts_with($p, $root)) {
                continue;
            }
            $basename = basename($p);
            if (fnmatch($pattern, $basename)) {
                $results[] = $p;
            }
        }
        return $results;
    }

    /**
     * @return array<string, mixed>
     */
    public function stat(string $path): array
    {
        $path = self::norm($path);

        if ($this->isDir($path)) {
            return ['path' => $path, 'type' => 'directory'];
        }

        $content = $this->read($path);
        return ['path' => $path, 'type' => 'file', 'size' => strlen($content)];
    }

    /**
     * Create an independent copy of this filesystem.
     */
    public function cloneFs(): self
    {
        $new = new self();
        $new->files = $this->files; // strings are copy-on-write in PHP
        $new->lazy = $this->lazy;   // closures are immutable references
        return $new;
    }
}
