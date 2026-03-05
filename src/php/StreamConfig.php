<?php

declare(strict_types=1);

namespace AgentHarness;

class StreamConfig
{
    public function __construct(
        public readonly string $mode = 'buffered',
        public readonly array $streamFields = [],
    ) {
    }
}
