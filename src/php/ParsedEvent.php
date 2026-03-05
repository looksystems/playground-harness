<?php

declare(strict_types=1);

namespace AgentHarness;

class ParsedEvent
{
    public function __construct(
        public readonly string $type,
        public readonly array $data,
        public readonly ?\Generator $stream = null,
        public readonly ?string $raw = null,
    ) {
    }
}
