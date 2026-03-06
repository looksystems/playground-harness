<?php

declare(strict_types=1);

namespace AgentHarness;

interface FilesystemDriver
{
    public function write(string $path, string $content): void;
    public function writeLazy(string $path, \Closure $provider): void;
    public function read(string $path): string;
    public function readText(string $path): string;
    public function exists(string $path): bool;
    public function remove(string $path): void;
    public function isDir(string $path): bool;
    /** @return list<string> */
    public function listdir(string $path = '/'): array;
    /** @return list<string> */
    public function find(string $root = '/', string $pattern = '*'): array;
    /** @return array<string, mixed> */
    public function stat(string $path): array;
    public function cloneFs(): FilesystemDriver;
}
