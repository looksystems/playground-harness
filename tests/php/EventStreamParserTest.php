<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\EventStreamParser;
use AgentHarness\EventType;
use AgentHarness\ParsedEvent;
use AgentHarness\StreamConfig;
use PHPUnit\Framework\TestCase;

class EventStreamParserTest extends TestCase
{
    /**
     * Create a character-by-character token stream from a string.
     *
     * @return \Generator<string>
     */
    private function tokenStream(string $text): \Generator
    {
        for ($i = 0; $i < strlen($text); $i++) {
            yield $text[$i];
        }
    }

    /**
     * Collect all text chunks from the parser's wrap generator.
     */
    private function collectText(EventStreamParser $parser, iterable $stream): string
    {
        $chunks = [];
        foreach ($parser->wrap($stream) as $chunk) {
            $chunks[] = $chunk;
        }
        return implode('', $chunks);
    }

    public function testPlainTextPassesThrough(): void
    {
        $parser = new EventStreamParser(eventTypes: []);
        $text = "Hello world, no events here.";
        $result = $this->collectText($parser, $this->tokenStream($text));
        $this->assertSame($text, $result);
    }

    public function testBufferedEventExtraction(): void
    {
        $eventType = new EventType(
            name: 'log_entry',
            description: 'A log entry',
            schema: ['data' => ['level' => 'string', 'message' => 'string']],
        );
        $parser = new EventStreamParser(eventTypes: [$eventType]);
        $events = [];
        $parser->onEvent(function (ParsedEvent $e) use (&$events) {
            $events[] = $e;
        });

        $text = "Before.\n---event\ntype: log_entry\ndata:\n  level: info\n  message: something happened\n---\nAfter.";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertStringContainsString('Before.', $result);
        $this->assertStringContainsString('After.', $result);
        $this->assertStringNotContainsString('---event', $result);
        $this->assertCount(1, $events);
        $this->assertSame('log_entry', $events[0]->type);
        $this->assertSame('info', $events[0]->data['data']['level']);
    }

    public function testStreamingEvent(): void
    {
        $eventType = new EventType(
            name: 'user_response',
            description: 'Response to user',
            schema: ['data' => ['message' => 'string']],
            streaming: new StreamConfig(mode: 'streaming', streamFields: ['data.message']),
        );
        $parser = new EventStreamParser(eventTypes: [$eventType]);
        $events = [];
        $parser->onEvent(function (ParsedEvent $e) use (&$events) {
            $events[] = $e;
        });

        $text = "Hi.\n---event\ntype: user_response\ndata:\n  message: Hello there friend\n---\nDone.";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertStringContainsString('Hi.', $result);
        $this->assertStringContainsString('Done.', $result);
        $this->assertCount(1, $events);
        $this->assertNotNull($events[0]->stream);

        // Collect the stream
        $streamed = '';
        foreach ($events[0]->stream as $chunk) {
            $streamed .= $chunk;
        }
        $this->assertStringContainsString('Hello there friend', $streamed);
    }

    public function testUnrecognizedEventPassesAsText(): void
    {
        $parser = new EventStreamParser(eventTypes: []);
        $text = "Before.\n---event\ntype: unknown_thing\ndata:\n  x: 1\n---\nAfter.";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertStringContainsString('---event', $result);
        $this->assertStringContainsString('unknown_thing', $result);
    }

    public function testMalformedYamlPassesAsText(): void
    {
        $eventType = new EventType(name: 'test', description: 'test', schema: []);
        $parser = new EventStreamParser(eventTypes: [$eventType]);
        $text = "Before.\n---event\n: this is not valid yaml [\n---\nAfter.";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertStringContainsString('Before.', $result);
        $this->assertStringContainsString('After.', $result);
    }

    public function testIncompleteEventAtEndOfStream(): void
    {
        $eventType = new EventType(name: 'test', description: 'test', schema: []);
        $parser = new EventStreamParser(eventTypes: [$eventType]);
        $text = "Before.\n---event\ntype: test\ndata:\n  x: 1";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertStringContainsString('Before.', $result);
        $this->assertStringContainsString('---event', $result);
    }

    public function testMultipleEvents(): void
    {
        $eventType = new EventType(
            name: 'log',
            description: 'A log',
            schema: ['data' => ['msg' => 'string']],
        );
        $parser = new EventStreamParser(eventTypes: [$eventType]);
        $events = [];
        $parser->onEvent(function (ParsedEvent $e) use (&$events) {
            $events[] = $e;
        });

        $text = "A\n---event\ntype: log\ndata:\n  msg: first\n---\nB\n---event\ntype: log\ndata:\n  msg: second\n---\nC";
        $result = $this->collectText($parser, $this->tokenStream($text));

        $this->assertCount(2, $events);
        $this->assertSame('first', $events[0]->data['data']['msg']);
        $this->assertSame('second', $events[1]->data['data']['msg']);
    }
}
