<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Lightweight stat for a path inside a MountSource.
 */
final class MountStat
{
    public function __construct(
        public bool $isDir,
        public int $size = 0,
        public int $mtime = 0,
    ) {
    }
}
