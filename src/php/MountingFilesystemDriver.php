<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Overlays read-only MountSources onto an inner writable FilesystemDriver
 * using copy-up semantics: writes shadow the source in the inner layer (the
 * source is never mutated), and deleting a source-only path throws (read-only,
 * no whiteout/tombstone in v1).
 *
 * With an empty mount table every method is a transparent passthrough to inner.
 *
 * See docs/adr/0034-programmatic-mount-sources.md.
 */
class MountingFilesystemDriver implements FilesystemDriver, Mountable
{
    /** @var list<array{point: string, source: MountSource}> Ordered mount table. */
    private array $mounts = [];

    /**
     * @param list<array{point: string, source: MountSource}> $mounts
     */
    public function __construct(
        private FilesystemDriver $inner,
        array $mounts = [],
    ) {
        $this->mounts = $mounts;
    }

    private static function norm(string $path): string
    {
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

    // --- Mountable -----------------------------------------------------------

    public function mount(string $mountPoint, MountSource $source): void
    {
        $mountPoint = self::norm($mountPoint);
        $this->unmount($mountPoint);
        $this->mounts[] = ['point' => $mountPoint, 'source' => $source];
    }

    public function unmount(string $mountPoint): void
    {
        $mountPoint = self::norm($mountPoint);
        $this->mounts = array_values(array_filter(
            $this->mounts,
            fn(array $e) => $e['point'] !== $mountPoint,
        ));
    }

    /** @return list<string> */
    public function mounts(): array
    {
        $points = array_map(fn(array $e) => $e['point'], $this->mounts);
        sort($points);
        return $points;
    }

    // --- routing helpers -----------------------------------------------------

    /** @return array{0: MountSource, 1: string}|null */
    private function resolveMount(string $path): ?array
    {
        $path = self::norm($path);
        $best = null;
        $bestPoint = '';
        $bestSub = '';
        foreach ($this->mounts as $e) {
            $point = $e['point'];
            if ($path === $point) {
                $sub = '';
            } elseif ($point === '/') {
                $sub = substr($path, 1);
            } elseif (str_starts_with($path, $point . '/')) {
                $sub = substr($path, strlen($point) + 1);
            } else {
                continue;
            }
            if ($best === null || strlen($point) > strlen($bestPoint)) {
                $best = $e['source'];
                $bestPoint = $point;
                $bestSub = $sub;
            }
        }
        return $best === null ? null : [$best, $bestSub];
    }

    private function isMountPointOrAncestor(string $path): bool
    {
        $path = self::norm($path);
        $prefix = $path === '/' ? '/' : $path . '/';
        foreach ($this->mounts as $e) {
            if ($e['point'] === $path || str_starts_with($e['point'], $prefix)) {
                return true;
            }
        }
        return false;
    }

    // --- FilesystemDriver ----------------------------------------------------

    public function write(string $path, string $content): void
    {
        $this->inner->write($path, $content);
    }

    public function writeLazy(string $path, \Closure $provider): void
    {
        $this->inner->writeLazy($path, $provider);
    }

    public function read(string $path): string
    {
        if ($this->inner->exists($path) && !$this->inner->isDir($path)) {
            return $this->inner->read($path);
        }
        $resolved = $this->resolveMount($path);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            $st = $source->stat($sub);
            if ($st !== null && !$st->isDir) {
                return $source->read($sub);
            }
        }
        return $this->inner->read($path); // throws
    }

    public function readText(string $path): string
    {
        return $this->read($path);
    }

    public function exists(string $path): bool
    {
        if ($this->inner->exists($path)) {
            return true;
        }
        if ($this->isMountPointOrAncestor($path)) {
            return true;
        }
        $resolved = $this->resolveMount($path);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            return $source->stat($sub) !== null;
        }
        return false;
    }

    public function remove(string $path): void
    {
        if ($this->inner->exists($path)) {
            $this->inner->remove($path);
            return;
        }
        $resolved = $this->resolveMount($path);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            if ($source->stat($sub) !== null) {
                throw new \RuntimeException(self::norm($path) . ': read-only mount source (cannot delete)');
            }
        }
        throw new \RuntimeException(self::norm($path) . ': No such file');
    }

    public function isDir(string $path): bool
    {
        if ($this->inner->isDir($path)) {
            return true;
        }
        if ($this->isMountPointOrAncestor($path)) {
            return true;
        }
        $resolved = $this->resolveMount($path);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            $st = $source->stat($sub);
            return $st !== null && $st->isDir;
        }
        return false;
    }

    /** @return list<string> */
    public function listdir(string $path = '/'): array
    {
        $path = self::norm($path);
        $entries = [];
        foreach ($this->inner->listdir($path) as $name) {
            $entries[$name] = true;
        }
        $resolved = $this->resolveMount($path);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            $st = $source->stat($sub);
            if ($st !== null && $st->isDir) {
                foreach ($source->list($sub) as $name) {
                    $entries[$name] = true;
                }
            }
        }
        $prefix = $path === '/' ? '/' : $path . '/';
        foreach ($this->mounts as $e) {
            if (str_starts_with($e['point'], $prefix)) {
                $seg = explode('/', substr($e['point'], strlen($prefix)))[0];
                if ($seg !== '') {
                    $entries[$seg] = true;
                }
            }
        }
        $result = array_keys($entries);
        sort($result);
        return $result;
    }

    /** @return list<string> */
    public function find(string $root = '/', string $pattern = '*'): array
    {
        $root = self::norm($root);
        $results = [];
        $walk = function (string $dir) use (&$walk, &$results, $pattern): void {
            foreach ($this->listdir($dir) as $name) {
                $full = $dir === '/' ? '/' . $name : $dir . '/' . $name;
                $isDir = $this->isDir($full);
                if (!$isDir && fnmatch($pattern, $name)) {
                    $results[] = $full;
                }
                if ($isDir) {
                    $walk($full);
                }
            }
        };
        if ($this->isDir($root)) {
            $walk($root);
        }
        sort($results);
        return $results;
    }

    /** @return array<string, mixed> */
    public function stat(string $path): array
    {
        if ($this->inner->exists($path)) {
            return $this->inner->stat($path);
        }
        $np = self::norm($path);
        if ($this->isMountPointOrAncestor($np)) {
            return ['path' => $np, 'type' => 'directory'];
        }
        $resolved = $this->resolveMount($np);
        if ($resolved !== null) {
            [$source, $sub] = $resolved;
            $st = $source->stat($sub);
            if ($st !== null) {
                if ($st->isDir) {
                    return ['path' => $np, 'type' => 'directory'];
                }
                return ['path' => $np, 'type' => 'file', 'size' => $st->size, 'mtime' => $st->mtime];
            }
        }
        return $this->inner->stat($path); // throws
    }

    public function cloneFs(): FilesystemDriver
    {
        // Sources are read-only and shareable; copy the table by reference.
        return new self($this->inner->cloneFs(), $this->mounts);
    }
}
