<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Narrow filesystem shape shared by VirtualFS and MountingFilesystemDriver.
 *
 * The shell builtins and Shell::$fs depend on this structural contract, so the
 * fs can be re-seated to a mount-aware filesystem without a logic change.
 * FilesystemDriver extends this with the same surface; VirtualFS implements it
 * directly (it is not itself a driver).
 */
interface FilesystemLike
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
    public function cloneFs(): FilesystemLike;
}
