<?php

declare(strict_types=1);

namespace AgentHarness\Tests\Llm;

use AgentHarness\Llm\AnthropicClient;
use PHPUnit\Framework\TestCase;

final class AnthropicClientTest extends TestCase
{
    // -----------------------------------------------------------------------
    // splitSystemAndMessages
    // -----------------------------------------------------------------------

    public function testExtractsSystemMessages(): void
    {
        $split = AnthropicClient::splitSystemAndMessages([
            ['role' => 'system', 'content' => 'You are helpful.'],
            ['role' => 'user', 'content' => 'Hi'],
        ]);
        $this->assertSame([['type' => 'text', 'text' => 'You are helpful.']], $split['system']);
        $this->assertCount(1, $split['messages']);
        $this->assertSame('user', $split['messages'][0]['role']);
    }

    public function testConcatenatesMultipleSystemMessages(): void
    {
        $split = AnthropicClient::splitSystemAndMessages([
            ['role' => 'system', 'content' => 'First.'],
            ['role' => 'system', 'content' => 'Second.'],
            ['role' => 'user', 'content' => 'Hi'],
        ]);
        $this->assertSame([
            ['type' => 'text', 'text' => 'First.'],
            ['type' => 'text', 'text' => 'Second.'],
        ], $split['system']);
    }

    public function testTranslatesAssistantToolCallsToToolUseBlocks(): void
    {
        $split = AnthropicClient::splitSystemAndMessages([
            ['role' => 'user', 'content' => 'add 3 and 4'],
            [
                'role' => 'assistant',
                'content' => null,
                'tool_calls' => [[
                    'id' => 'call_abc',
                    'type' => 'function',
                    'function' => ['name' => 'add', 'arguments' => '{"a":3,"b":4}'],
                ]],
            ],
        ]);
        $this->assertCount(2, $split['messages']);
        $this->assertSame('assistant', $split['messages'][1]['role']);
        $this->assertCount(1, $split['messages'][1]['content']);
        $block = $split['messages'][1]['content'][0];
        $this->assertSame('tool_use', $block['type']);
        $this->assertSame('call_abc', $block['id']);
        $this->assertSame('add', $block['name']);
        $this->assertSame(['a' => 3, 'b' => 4], $block['input']);
    }

    public function testCoalescesConsecutiveToolResults(): void
    {
        $split = AnthropicClient::splitSystemAndMessages([
            ['role' => 'user', 'content' => 'compute'],
            [
                'role' => 'assistant',
                'content' => null,
                'tool_calls' => [
                    ['id' => 'c1', 'type' => 'function', 'function' => ['name' => 'add', 'arguments' => '{}']],
                    ['id' => 'c2', 'type' => 'function', 'function' => ['name' => 'mul', 'arguments' => '{}']],
                ],
            ],
            ['role' => 'tool', 'content' => '7', 'tool_call_id' => 'c1'],
            ['role' => 'tool', 'content' => '12', 'tool_call_id' => 'c2'],
        ]);
        // user / assistant / user (with two tool_result blocks coalesced)
        $this->assertCount(3, $split['messages']);
        $this->assertSame('user', $split['messages'][2]['role']);
        $this->assertCount(2, $split['messages'][2]['content']);
        $this->assertSame('tool_result', $split['messages'][2]['content'][0]['type']);
        $this->assertSame('c1', $split['messages'][2]['content'][0]['tool_use_id']);
        $this->assertSame('7', $split['messages'][2]['content'][0]['content']);
        $this->assertSame('c2', $split['messages'][2]['content'][1]['tool_use_id']);
        $this->assertSame('12', $split['messages'][2]['content'][1]['content']);
    }

    public function testThrowsOnToolMessageWithoutToolCallId(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessageMatches('/tool_call_id/');
        AnthropicClient::splitSystemAndMessages([
            ['role' => 'tool', 'content' => 'x'],
        ]);
    }

    public function testFallsBackToEmptyObjectOnInvalidArgumentsJson(): void
    {
        $split = AnthropicClient::splitSystemAndMessages([
            [
                'role' => 'assistant',
                'content' => null,
                'tool_calls' => [[
                    'id' => 'c1',
                    'type' => 'function',
                    'function' => ['name' => 'x', 'arguments' => 'not-json'],
                ]],
            ],
        ]);
        $this->assertSame('tool_use', $split['messages'][0]['content'][0]['type']);
        // Empty stdClass equals the input fallback for non-JSON
        $this->assertEquals(new \stdClass(), $split['messages'][0]['content'][0]['input']);
    }

    // -----------------------------------------------------------------------
    // translateToolSchema
    // -----------------------------------------------------------------------

    public function testTranslateToolSchemaStripsOpenAIWrapper(): void
    {
        $out = AnthropicClient::translateToolSchema([
            'type' => 'function',
            'function' => [
                'name' => 'add',
                'description' => 'Add two integers',
                'parameters' => [
                    'type' => 'object',
                    'properties' => ['a' => ['type' => 'integer'], 'b' => ['type' => 'integer']],
                    'required' => ['a', 'b'],
                ],
            ],
        ]);
        $this->assertSame('add', $out['name']);
        $this->assertSame('Add two integers', $out['description']);
        $this->assertSame([
            'type' => 'object',
            'properties' => ['a' => ['type' => 'integer'], 'b' => ['type' => 'integer']],
            'required' => ['a', 'b'],
        ], $out['input_schema']);
    }

    public function testTranslateToolSchemaOmitsDescriptionWhenAbsent(): void
    {
        $out = AnthropicClient::translateToolSchema([
            'type' => 'function',
            'function' => ['name' => 'noop', 'parameters' => ['type' => 'object', 'properties' => []]],
        ]);
        $this->assertArrayNotHasKey('description', $out);
    }
}
