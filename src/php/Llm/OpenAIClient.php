<?php

declare(strict_types=1);

namespace AgentHarness\Llm;

use OpenAI;
use OpenAI\Contracts\ClientContract;
use OpenAI\Responses\Chat\CreateResponse;
use OpenAI\Responses\Chat\CreateStreamedResponse;
use OpenAI\Responses\StreamResponse;

/**
 * OpenAIClient — ClientInterface implementation backed by openai-php/client.
 *
 * The OpenAI Chat Completions wire format is the harness's canonical message
 * shape, so this adapter is a near-passthrough. It forwards messages and tool
 * schemas verbatim and re-shapes the response into the harness's
 * {role, content, tool_calls} shape.
 *
 * Streaming consumes the SDK's StreamResponse iterator and accumulates
 * delta.content + delta.toolCalls into the same shape as the non-streaming path.
 *
 * To target an OpenAI-compatible endpoint (Anthropic's compatibility shim,
 * OpenRouter, litellm-proxy, …) pass `baseUrl` to the constructor — the SDK
 * routes there transparently.
 */
class OpenAIClient implements ClientInterface
{
    private ClientContract $client;

    public function __construct(
        ?string $apiKey = null,
        ?string $baseUrl = null,
        ?ClientContract $client = null,
    ) {
        if ($client !== null) {
            $this->client = $client;
            return;
        }
        $factory = OpenAI::factory();
        $key = $apiKey ?? getenv('OPENAI_API_KEY') ?: 'sk-placeholder';
        $factory = $factory->withApiKey($key);
        if ($baseUrl !== null) {
            $factory = $factory->withBaseUri($baseUrl);
        }
        $this->client = $factory->make();
    }

    /**
     * @param array<string, mixed> $request
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    public function callLlm(array $request): array
    {
        $params = array_merge([
            'model' => $request['model'],
            'messages' => $request['messages'],
        ], $request['completionParams'] ?? []);

        if (!empty($request['tools'])) {
            $params['tools'] = $request['tools'];
        }

        if (!empty($request['stream'])) {
            $stream = $this->client->chat()->createStreamed($params);
            return $this->consumeStream($stream);
        }

        $response = $this->client->chat()->create($params);
        return $this->collectNonStreaming($response);
    }

    /**
     * @param StreamResponse<CreateStreamedResponse> $stream
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    private function consumeStream(StreamResponse $stream): array
    {
        $contentParts = [];
        $toolCallsByIndex = [];

        /** @var CreateStreamedResponse $chunk */
        foreach ($stream as $chunk) {
            $choice = $chunk->choices[0] ?? null;
            if ($choice === null) {
                continue;
            }
            $delta = $choice->delta;

            if ($delta->content !== null && $delta->content !== '') {
                $contentParts[] = $delta->content;
            }

            foreach ($delta->toolCalls as $tc) {
                $idx = $tc->index ?? count($toolCallsByIndex);
                if (!isset($toolCallsByIndex[$idx])) {
                    $toolCallsByIndex[$idx] = [
                        'id' => $tc->id ?? '',
                        'type' => 'function',
                        'function' => ['name' => '', 'arguments' => ''],
                    ];
                }
                $entry = &$toolCallsByIndex[$idx];
                if ($tc->id !== null && $tc->id !== '') {
                    $entry['id'] = $tc->id;
                }
                if ($tc->function->name !== null && $tc->function->name !== '') {
                    $entry['function']['name'] .= $tc->function->name;
                }
                if ($tc->function->arguments !== '') {
                    $entry['function']['arguments'] .= $tc->function->arguments;
                }
                unset($entry);
            }
        }

        $message = ['role' => 'assistant', 'content' => null, 'tool_calls' => null];
        if ($contentParts !== []) {
            $message['content'] = implode('', $contentParts);
        }
        if ($toolCallsByIndex !== []) {
            ksort($toolCallsByIndex);
            $message['tool_calls'] = array_values($toolCallsByIndex);
        }
        return $message;
    }

    /**
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    private function collectNonStreaming(CreateResponse $response): array
    {
        $msg = $response->choices[0]->message ?? null;
        $content = $msg?->content;
        $toolCalls = null;
        if ($msg !== null && $msg->toolCalls !== []) {
            $toolCalls = array_map(static function ($tc): array {
                return [
                    'id' => $tc->id,
                    'type' => $tc->type,
                    'function' => [
                        'name' => $tc->function->name,
                        'arguments' => $tc->function->arguments,
                    ],
                ];
            }, $msg->toolCalls);
        }
        return ['role' => 'assistant', 'content' => $content, 'tool_calls' => $toolCalls];
    }
}
