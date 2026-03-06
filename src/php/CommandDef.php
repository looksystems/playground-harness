<?php

declare(strict_types=1);

namespace AgentHarness;

class CommandDef
{
    public function __construct(
        public readonly string $name,
        public readonly string $description,
        public readonly array $parameters,
        private readonly \Closure $execute,
        public readonly bool $llmVisible = true,
    ) {
    }

    public static function make(
        string $name,
        string $description,
        array $parameters,
        callable $execute,
        bool $llmVisible = true,
    ): self {
        return new self(
            name: $name,
            description: $description,
            parameters: $parameters,
            execute: $execute(...),
            llmVisible: $llmVisible,
        );
    }

    public function call(array $args): string
    {
        $result = ($this->execute)($args);
        return is_string($result) ? $result : (string) $result;
    }
}
