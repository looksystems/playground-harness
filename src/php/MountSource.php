<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Read-only provider of a directory tree, addressed by relative subpaths
 * ('' is the mount root). stat() is called constantly for existence checks and
 * must return null for missing paths (no exceptions for control flow);
 * list()/read() are only called after stat() confirms existence and may throw
 * on real I/O faults.
 */
interface MountSource
{
    public function stat(string $subpath): ?MountStat;

    /** @return list<string> Immediate child names of a directory subpath. */
    public function list(string $subpath): array;

    public function read(string $subpath): string;
}
