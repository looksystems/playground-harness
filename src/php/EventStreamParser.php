<?php

declare(strict_types=1);

namespace AgentHarness;

class EventStreamParser
{
    private const EVENT_START_DELIMITER = '---event';
    private const EVENT_END_DELIMITER = '---';

    /** @var array<string, EventType> */
    private array $eventTypes = [];

    /** @var list<callable> */
    private array $callbacks = [];

    /**
     * @param list<EventType> $eventTypes
     */
    public function __construct(array $eventTypes)
    {
        foreach ($eventTypes as $et) {
            $this->eventTypes[$et->name] = $et;
        }
    }

    public function onEvent(callable $callback): void
    {
        $this->callbacks[] = $callback;
    }

    private function fireEvent(ParsedEvent $event): void
    {
        foreach ($this->callbacks as $cb) {
            try {
                $cb($event);
            } catch (\Throwable $e) {
                // swallow
            }
        }
    }

    /**
     * Wrap a token iterable, extracting events and yielding clean text chunks.
     *
     * @param iterable<string> $tokenStream
     * @return \Generator<string>
     */
    public function wrap(iterable $tokenStream): \Generator
    {
        $state = 'TEXT';
        $lineBuffer = '';
        $eventLines = [];
        $streamBuffer = [];
        $streamingActive = false;

        foreach ($tokenStream as $token) {
            $lineBuffer .= $token;

            while (str_contains($lineBuffer, "\n")) {
                $pos = strpos($lineBuffer, "\n");
                $line = substr($lineBuffer, 0, $pos);
                $lineBuffer = substr($lineBuffer, $pos + 1);

                if ($state === 'TEXT') {
                    if (trim($line) === self::EVENT_START_DELIMITER) {
                        $state = 'EVENT_BODY';
                        $eventLines = [];
                    } else {
                        yield $line . "\n";
                    }
                } elseif ($state === 'EVENT_BODY') {
                    if (trim($line) === self::EVENT_END_DELIMITER) {
                        $handled = $this->finalizeEvent($eventLines);
                        if (!$handled) {
                            // Unrecognized or malformed - pass through as text
                            yield self::EVENT_START_DELIMITER . "\n";
                            foreach ($eventLines as $el) {
                                yield $el . "\n";
                            }
                            yield self::EVENT_END_DELIMITER . "\n";
                        }
                        $state = 'TEXT';
                        $eventLines = [];
                    } else {
                        $eventLines[] = $line;
                        $streaming = $this->tryDetectStreaming($eventLines);
                        if ($streaming !== null) {
                            $streamBuffer = [];
                            $streamingActive = true;
                            $state = 'STREAMING';
                        }
                    }
                } elseif ($state === 'STREAMING') {
                    if (trim($line) === self::EVENT_END_DELIMITER) {
                        $streamingActive = false;
                        $state = 'TEXT';
                    } else {
                        $streamBuffer[] = $line . "\n";
                    }
                }
            }
        }

        // Handle remaining content at end of stream
        if ($lineBuffer !== '') {
            if ($state === 'TEXT') {
                yield $lineBuffer;
            } elseif ($state === 'EVENT_BODY') {
                // Incomplete event -- dump as text
                yield self::EVENT_START_DELIMITER . "\n";
                yield implode("\n", $eventLines) . "\n";
                if (trim($lineBuffer) !== '') {
                    yield $lineBuffer;
                }
            }
        } elseif ($state === 'EVENT_BODY') {
            // Incomplete event with no trailing content
            yield self::EVENT_START_DELIMITER . "\n";
            yield implode("\n", $eventLines);
        }
    }

    /**
     * Try to detect a streaming event from accumulated lines.
     * If detected, fires the event immediately with a generator for the stream.
     *
     * @return array{string, array}|null
     */
    private function tryDetectStreaming(array $lines): ?array
    {
        $raw = implode("\n", $lines);
        $data = $this->parseSimpleYaml($raw);
        if ($data === null || !isset($data['type'])) {
            return null;
        }

        $eventName = $data['type'];
        $et = $this->eventTypes[$eventName] ?? null;
        if ($et === null || $et->streaming->mode !== 'streaming') {
            return null;
        }

        // Check if streaming field is present
        foreach ($et->streaming->streamFields as $sf) {
            $parts = explode('.', $sf);
            $obj = $data;
            foreach (array_slice($parts, 0, -1) as $part) {
                if (is_array($obj) && isset($obj[$part])) {
                    $obj = $obj[$part];
                } else {
                    return null;
                }
            }
            $lastKey = end($parts);
            if (is_array($obj) && isset($obj[$lastKey])) {
                $initialValue = (string)$obj[$lastKey];

                // For PHP, we create a generator that yields the initial value
                // and any additional buffered content. Since PHP is synchronous,
                // the streaming is conceptual - we collect content, then yield.
                $streamGen = (function () use ($initialValue) {
                    yield $initialValue;
                })();

                $event = new ParsedEvent(
                    type: $eventName,
                    data: $data,
                    stream: $streamGen,
                );
                $this->fireEvent($event);
                return [$eventName, $data];
            }
        }

        return null;
    }

    /**
     * Parse a complete buffered event and fire it.
     *
     * @return bool True if handled
     */
    private function finalizeEvent(array $lines): bool
    {
        $raw = implode("\n", $lines);
        $data = $this->parseSimpleYaml($raw);

        if ($data === null || !isset($data['type'])) {
            return false;
        }

        $eventName = $data['type'];
        if (!isset($this->eventTypes[$eventName])) {
            return false;
        }

        $event = new ParsedEvent(type: $eventName, data: $data, raw: $raw);
        $this->fireEvent($event);
        return true;
    }

    /**
     * Simple YAML parser that handles our specific event format:
     * - `key: value` on a line
     * - `key:` followed by indented `sub_key: value` lines
     *
     * @return array|null
     */
    private function parseSimpleYaml(string $raw): ?array
    {
        $result = [];
        $lines = explode("\n", $raw);
        $currentKey = null;
        $currentSubMap = [];

        foreach ($lines as $line) {
            // Skip empty lines
            if (trim($line) === '') {
                continue;
            }

            // Check for indented line (sub-key)
            if (preg_match('/^  (\w[\w\-]*)\s*:\s*(.*)$/', $line, $matches)) {
                if ($currentKey !== null) {
                    $value = trim($matches[2]);
                    $currentSubMap[$matches[1]] = $this->castYamlValue($value);
                }
                continue;
            }

            // Top-level key
            if (preg_match('/^(\w[\w\-]*)\s*:\s*(.*)$/', $line, $matches)) {
                // Save previous sub-map if any
                if ($currentKey !== null && !empty($currentSubMap)) {
                    $result[$currentKey] = $currentSubMap;
                }

                $key = $matches[1];
                $value = trim($matches[2]);

                if ($value === '') {
                    // Start of a sub-map
                    $currentKey = $key;
                    $currentSubMap = [];
                } else {
                    $currentKey = null;
                    $currentSubMap = [];
                    $result[$key] = $this->castYamlValue($value);
                }
                continue;
            }

            // If we hit a line we can't parse, return null (malformed)
            if (trim($line) !== '' && !str_starts_with(trim($line), '#')) {
                return null;
            }
        }

        // Save final sub-map
        if ($currentKey !== null && !empty($currentSubMap)) {
            $result[$currentKey] = $currentSubMap;
        }

        return !empty($result) ? $result : null;
    }

    private function castYamlValue(string $value): string|int|float|bool|null
    {
        if ($value === 'true') {
            return true;
        }
        if ($value === 'false') {
            return false;
        }
        if ($value === 'null' || $value === '~') {
            return null;
        }
        if (is_numeric($value) && !str_contains($value, '.')) {
            return (int)$value;
        }
        if (is_numeric($value)) {
            return (float)$value;
        }
        return $value;
    }
}
