<?php

declare(strict_types=1);

namespace AgentHarness;

class MessageBus
{
    /** @var array<string, list<callable>> */
    private array $handlers = [];
    private int $depth = 0;

    public function __construct(
        private readonly int $maxDepth = 10,
    ) {
    }

    public function subscribe(string $eventType, callable $handler): void
    {
        $this->handlers[$eventType][] = $handler;
    }

    public function publish(ParsedEvent $event): void
    {
        if ($this->depth >= $this->maxDepth) {
            return;
        }

        $handlers = $this->handlers[$event->type] ?? [];
        $wildcardHandlers = $this->handlers['*'] ?? [];
        $allHandlers = array_merge($handlers, $wildcardHandlers);

        if (empty($allHandlers)) {
            return;
        }

        $this->depth++;
        try {
            foreach ($allHandlers as $handler) {
                try {
                    $handler($event, $this);
                } catch (\Throwable $e) {
                    // Swallow handler errors so they don't propagate
                }
            }
        } finally {
            $this->depth--;
        }
    }
}
