<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BaseAgent;
use AgentHarness\RunContext;
use OpenAI\Responses\Chat\CreateResponse;
use OpenAI\Responses\Chat\CreateStreamedResponse;
use OpenAI\Testing\ClientFake;
use PHPUnit\Framework\TestCase;

class BaseAgentTest extends TestCase
{
    public function testInitDefaults(): void
    {
        $agent = new BaseAgent(model: 'gpt-4');
        $this->assertSame('gpt-4', $agent->model);
        $this->assertSame(20, $agent->maxTurns);
        $this->assertSame(2, $agent->maxRetries);
        $this->assertTrue($agent->stream);
    }

    public function testInitCustom(): void
    {
        $agent = new BaseAgent(
            model: 'claude-3-opus',
            system: 'You are helpful.',
            maxTurns: 5,
            maxRetries: 0,
            stream: false,
        );
        $this->assertSame('claude-3-opus', $agent->model);
        $this->assertSame('You are helpful.', $agent->system);
        $this->assertSame(5, $agent->maxTurns);
    }

    public function testSystemPromptIsStored(): void
    {
        $agent = new BaseAgent(model: 'gpt-4', system: 'Be helpful.');
        $this->assertSame('Be helpful.', $agent->system);
    }

    public function testRunContextCreation(): void
    {
        $agent = new BaseAgent(model: 'gpt-4');
        $ctx = new RunContext(agent: $agent, turn: 0, metadata: []);
        $this->assertSame($agent, $ctx->agent);
        $this->assertSame(0, $ctx->turn);
    }

    public function testNonStreamingChatCompletionFlow(): void
    {
        $client = new ClientFake([
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

        $agent = new BaseAgent(model: 'gpt-4', stream: false, client: $client);
        $result = $agent->run([['role' => 'user', 'content' => 'ping']]);
        $this->assertSame('pong', $result);
    }

    public function testStreamingAccumulatesContentChunks(): void
    {
        // Build a fake SSE stream with three content chunks.
        $sse = $this->buildSseStream([
            ['delta' => ['role' => 'assistant'], 'finish_reason' => null],
            ['delta' => ['content' => 'Hello'], 'finish_reason' => null],
            ['delta' => ['content' => ', '],    'finish_reason' => null],
            ['delta' => ['content' => 'world'], 'finish_reason' => 'stop'],
        ]);

        $client = new ClientFake([
            CreateStreamedResponse::fake($sse),
        ]);

        $agent = new BaseAgent(model: 'gpt-4', stream: true, client: $client);
        $result = $agent->run([['role' => 'user', 'content' => 'hi']]);
        $this->assertSame('Hello, world', $result);
    }

    public function testStreamingAccumulatesToolCallChunks(): void
    {
        // Tool-call deltas split across chunks: id arrives first, then name,
        // then incremental arguments JSON.
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

        // Capture the assistant message via handleResponse override.
        $agent = new class(
            model: 'gpt-4', stream: true,
            client: new ClientFake([CreateStreamedResponse::fake($sse)]),
        ) extends BaseAgent {
            public ?array $captured = null;
            protected function handleResponse(array $response, RunContext $context): ?array
            {
                $this->captured = $response;
                return null; // stop the loop after the first turn
            }
        };

        $agent->run([['role' => 'user', 'content' => 'add 3 and 4']]);

        $this->assertNotNull($agent->captured);
        $this->assertSame('assistant', $agent->captured['role']);
        $this->assertNotNull($agent->captured['tool_calls']);
        $this->assertCount(1, $agent->captured['tool_calls']);
        $tc = $agent->captured['tool_calls'][0];
        $this->assertSame('call_abc', $tc['id']);
        $this->assertSame('add', $tc['function']['name']);
        $this->assertSame('{"a":3,"b":4}', $tc['function']['arguments']);
    }

    /**
     * Build an in-memory SSE stream resource from a list of chunk-shaped arrays.
     * Each input becomes a `data: {…}` line followed by a blank line.
     *
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
