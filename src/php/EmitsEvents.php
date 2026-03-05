<?php

declare(strict_types=1);

namespace AgentHarness;

trait EmitsEvents
{
    /** @var array<string, EventType> */
    private array $eventRegistry = [];

    /** @var list<string> */
    public array $defaultEvents = [];

    private ?MessageBus $messageBus = null;

    public function registerEvent(EventType $eventType): void
    {
        $this->eventRegistry[$eventType->name] = $eventType;
    }

    /**
     * Resolve which events are active for a run.
     *
     * @param list<string|EventType>|null $events
     * @return list<EventType>
     */
    public function resolveActiveEvents(?array $events = null): array
    {
        if ($events === null) {
            $result = [];
            foreach ($this->defaultEvents as $name) {
                if (isset($this->eventRegistry[$name])) {
                    $result[] = $this->eventRegistry[$name];
                }
            }
            return $result;
        }

        $result = [];
        foreach ($events as $item) {
            if (is_string($item)) {
                if (isset($this->eventRegistry[$item])) {
                    $result[] = $this->eventRegistry[$item];
                }
            } elseif ($item instanceof EventType) {
                $result[] = $item;
            }
        }
        return $result;
    }

    /**
     * Build the prompt section describing available events.
     *
     * @param list<EventType> $eventTypes
     */
    public function buildEventPrompt(array $eventTypes): string
    {
        if (empty($eventTypes)) {
            return '';
        }

        $sections = [];
        $sections[] = '# Event Emission';
        $sections[] = '';
        $sections[] = 'You can emit structured events inline in your response using the following format:';
        $sections[] = '';

        foreach ($eventTypes as $et) {
            $sections[] = "## Event: {$et->name}";
            $sections[] = "Description: {$et->description}";
            $sections[] = 'Format:';
            $sections[] = '```';
            $sections[] = '---event';
            $sections[] = "type: {$et->name}";
            if (!empty($et->schema)) {
                foreach ($et->schema as $key => $val) {
                    if (is_array($val)) {
                        $sections[] = "{$key}:";
                        foreach ($val as $k => $v) {
                            $sections[] = "  {$k}: <{$v}>";
                        }
                    } else {
                        $sections[] = "{$key}: <{$val}>";
                    }
                }
            }
            $sections[] = '---';
            $sections[] = '```';
            if ($et->instructions !== null) {
                $sections[] = $et->instructions;
            }
            $sections[] = '';
        }

        return implode("\n", $sections);
    }

    public function getBus(): MessageBus
    {
        $this->messageBus ??= new MessageBus();
        return $this->messageBus;
    }
}
