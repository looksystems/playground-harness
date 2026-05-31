<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Capability interface for filesystems that support mounting read-only
 * sources. Implemented only by MountingFilesystemDriver. Kept out of the core
 * FilesystemDriver contract per ADR 0026.
 */
interface Mountable
{
    public function mount(string $mountPoint, MountSource $source): void;

    public function unmount(string $mountPoint): void;

    /** @return list<string> */
    public function mounts(): array;
}
