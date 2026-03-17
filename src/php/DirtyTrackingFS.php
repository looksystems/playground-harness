<?php

declare(strict_types=1);

namespace AgentHarness;

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

    public function cloneFs(): self
    {
        return new self($this->inner->cloneFs());
    }
}
