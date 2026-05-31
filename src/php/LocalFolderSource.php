<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Mirrors a host directory as a read-only mount source.
 *
 * Path-escape-safe: the real absolute root is resolved once, and every subpath
 * is joined, resolved (following symlinks), and asserted to live at or under
 * the root -- defeating '../' and symlink escapes. Absolute subpaths are
 * rejected; out-of-root paths are treated as not-found.
 */
class LocalFolderSource implements MountSource
{
    private string $root;

    public function __construct(string $root)
    {
        $real = realpath($root);
        $this->root = $real !== false ? $real : $root;
    }

    private function resolve(string $subpath): ?string
    {
        // Reject absolute subpaths (POSIX '/...' or Windows 'C:\...').
        if ($subpath !== '' && ($subpath[0] === '/' || preg_match('#^[A-Za-z]:[\\\\/]#', $subpath) === 1)) {
            return null;
        }
        $joined = $this->root . DIRECTORY_SEPARATOR . $subpath;
        $real = realpath($joined);
        if ($real === false) {
            return null; // not found
        }
        if ($real !== $this->root && !str_starts_with($real, $this->root . DIRECTORY_SEPARATOR)) {
            return null; // escaped the root
        }
        return $real;
    }

    public function stat(string $subpath): ?MountStat
    {
        $resolved = $this->resolve($subpath);
        if ($resolved === null) {
            return null;
        }
        return new MountStat(
            isDir: is_dir($resolved),
            size: is_file($resolved) ? (int) filesize($resolved) : 0,
            mtime: (int) filemtime($resolved),
        );
    }

    /** @return list<string> */
    public function list(string $subpath): array
    {
        $resolved = $this->resolve($subpath);
        if ($resolved === null) {
            throw new \RuntimeException("{$subpath}: No such file");
        }
        $entries = scandir($resolved);
        if ($entries === false) {
            throw new \RuntimeException("{$subpath}: cannot list");
        }
        $names = array_values(array_filter($entries, fn(string $n) => $n !== '.' && $n !== '..'));
        sort($names);
        return $names;
    }

    public function read(string $subpath): string
    {
        $resolved = $this->resolve($subpath);
        if ($resolved === null) {
            throw new \RuntimeException("{$subpath}: No such file");
        }
        $content = file_get_contents($resolved);
        if ($content === false) {
            throw new \RuntimeException("{$subpath}: cannot read");
        }
        return $content;
    }
}
