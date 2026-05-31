<?php

declare(strict_types=1);

namespace AgentHarness;

interface FilesystemDriver extends FilesystemLike
{
    // Covariant narrowing: a driver clone is itself a driver.
    public function cloneFs(): FilesystemDriver;
}
