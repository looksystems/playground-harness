<?php

declare(strict_types=1);

namespace AgentHarness;

class BuiltinFilesystemDriver implements FilesystemDriver
{
    private VirtualFS $vfs;

    public function __construct(?VirtualFS $vfs = null)
    {
        $this->vfs = $vfs ?? new VirtualFS();
    }

    public function write(string $path, string $content): void { $this->vfs->write($path, $content); }
    public function writeLazy(string $path, \Closure $provider): void { $this->vfs->writeLazy($path, $provider); }
    public function read(string $path): string { return $this->vfs->read($path); }
    public function readText(string $path): string { return $this->vfs->readText($path); }
    public function exists(string $path): bool { return $this->vfs->exists($path); }
    public function remove(string $path): void { $this->vfs->remove($path); }
    public function isDir(string $path): bool { return $this->vfs->isDir($path); }
    public function listdir(string $path = '/'): array { return $this->vfs->listdir($path); }
    public function find(string $root = '/', string $pattern = '*'): array { return $this->vfs->find($root, $pattern); }
    public function stat(string $path): array { return $this->vfs->stat($path); }
    public function cloneFs(): FilesystemDriver { return new self($this->vfs->cloneFs()); }
    public function vfs(): VirtualFS { return $this->vfs; }
}
