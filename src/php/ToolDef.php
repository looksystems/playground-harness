<?php

declare(strict_types=1);

namespace AgentHarness;

class ToolDef
{
    public function __construct(
        public readonly string $name,
        public readonly string $description,
        public readonly array $parameters,
        private readonly \Closure $execute,
    ) {
    }

    public static function make(
        string $name,
        string $description,
        array $parameters,
        callable $execute,
    ): self {
        return new self(
            name: $name,
            description: $description,
            parameters: $parameters,
            execute: $execute(...),
        );
    }

    public function call(array $args): mixed
    {
        return ($this->execute)($args);
    }
}
