<?php

declare(strict_types=1);

namespace AgentHarness;

class StructuredEvent
{
    public function __construct(
        public readonly string $name,
        public readonly string $description,
        public readonly array $schema,
        public readonly ?string $instructions = null,
        public readonly StreamConfig $streaming = new StreamConfig(),
    ) {
    }
}
