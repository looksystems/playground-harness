<?php

declare(strict_types=1);

namespace AgentHarness\Tests\Llm;

use AgentHarness\Llm\OpenAIClient;
use OpenAI\Responses\Chat\CreateResponse;
use OpenAI\Responses\Chat\CreateStreamedResponse;
use OpenAI\Testing\ClientFake;
use PHPUnit\Framework\TestCase;

final class OpenAIClientTest extends TestCase
{
    public function testNonStreamingReturnsAssistantMessageShape(): void
    {
        $sdk = new ClientFake([
            CreateResponse::fake([
                'choices' => [[
                    'index' => 0,
                    'message' => [
                        'role' => 'assistant',
                        'content' => 'pong',
                        'function_call' => null,
                        'tool_calls' => [],
                    ],
                    'logprobs' => null,
                    'finish_reason' => 'stop',
                ]],
            ]),
        ]);

        $client = new OpenAIClient(client: $sdk);
        $out = $client->callLlm([
            'model' => 'gpt-4',
            'messages' => [['role' => 'user', 'content' => 'ping']],
            'stream' => false,
        ]);
        $this->assertSame('assistant', $out['role']);
        $this->assertSame('pong', $out['content']);
        $this->assertNull($out['tool_calls']);
    }

    public function testStreamingAccumulatesContent(): void
    {
        $sse = $this->buildSseStream([
            ['delta' => ['role' => 'assistant'], 'finish_reason' => null],
            ['delta' => ['content' => 'Hello'], 'finish_reason' => null],
            ['delta' => ['content' => ', '],    'finish_reason' => null],
            ['delta' => ['content' => 'world'], 'finish_reason' => 'stop'],
        ]);

        $sdk = new ClientFake([CreateStreamedResponse::fake($sse)]);
        $client = new OpenAIClient(client: $sdk);
        $out = $client->callLlm([
            'model' => 'gpt-4',
            'messages' => [['role' => 'user', 'content' => 'hi']],
            'stream' => true,
        ]);
        $this->assertSame('Hello, world', $out['content']);
        $this->assertNull($out['tool_calls']);
    }

    public function testStreamingAccumulatesToolCallsAcrossChunks(): void
    {
        $sse = $this->buildSseStream([
            ['delta' => ['role' => 'assistant'], 'finish_reason' => null],
            ['delta' => [
                'tool_calls' => [[
                    'index' => 0,
                    'id' => 'call_abc',
                    'type' => 'function',
                    'function' => ['name' => 'add', 'arguments' => ''],
                ]],
            ], 'finish_reason' => null],
            ['delta' => [
                'tool_calls' => [[
                    'index' => 0,
                    'function' => ['arguments' => '{"a":'],
                ]],
            ], 'finish_reason' => null],
            ['delta' => [
                'tool_calls' => [[
                    'index' => 0,
                    'function' => ['arguments' => '3,"b":4}'],
                ]],
            ], 'finish_reason' => 'tool_calls'],
        ]);

        $sdk = new ClientFake([CreateStreamedResponse::fake($sse)]);
        $client = new OpenAIClient(client: $sdk);
        $out = $client->callLlm([
            'model' => 'gpt-4',
            'messages' => [['role' => 'user', 'content' => 'add 3 and 4']],
            'stream' => true,
        ]);
        $this->assertNotNull($out['tool_calls']);
        $this->assertCount(1, $out['tool_calls']);
        $tc = $out['tool_calls'][0];
        $this->assertSame('call_abc', $tc['id']);
        $this->assertSame('add', $tc['function']['name']);
        $this->assertSame('{"a":3,"b":4}', $tc['function']['arguments']);
    }

    /**
     * @param list<array{delta: array<string, mixed>, finish_reason: ?string}> $chunks
     * @return resource
     */
    private function buildSseStream(array $chunks)
    {
        $resource = fopen('php://temp', 'w+');
        foreach ($chunks as $chunk) {
            $payload = [
                'id' => 'chatcmpl-test',
                'object' => 'chat.completion.chunk',
                'created' => 0,
                'model' => 'gpt-4',
                'choices' => [array_merge(['index' => 0], $chunk)],
            ];
            fwrite($resource, 'data: ' . json_encode($payload) . "\n\n");
        }
        fwrite($resource, "data: [DONE]\n\n");
        rewind($resource);
        return $resource;
    }
}
