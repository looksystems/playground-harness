<?php

declare(strict_types=1);

namespace AgentHarness;

use OpenAI;
use OpenAI\Contracts\ClientContract;
use OpenAI\Responses\Chat\CreateResponse;
use OpenAI\Responses\Chat\CreateStreamedResponse;
use OpenAI\Responses\StreamResponse;

class BaseAgent
{
    protected ClientContract $client;

    /**
     * @param array<string, mixed> $completionParams Extra options forwarded to chat()->create()
     * @param ClientContract|null  $client           Inject a pre-built client (e.g. ClientFake) for tests
     */
    public function __construct(
        public readonly string $model,
        public readonly ?string $system = null,
        public readonly int $maxTurns = 20,
        public readonly int $maxRetries = 2,
        public readonly bool $stream = true,
        public readonly ?string $baseUrl = null,
        public readonly ?string $apiKey = null,
        public readonly array $completionParams = [],
        ?ClientContract $client = null,
    ) {
        $this->client = $client ?? $this->buildClient();
    }

    private function buildClient(): ClientContract
    {
        $factory = OpenAI::factory();

        $key = $this->apiKey ?? getenv('OPENAI_API_KEY') ?: 'sk-placeholder';
        $factory = $factory->withApiKey($key);

        if ($this->baseUrl !== null) {
            $factory = $factory->withBaseUri($this->baseUrl);
        }

        return $factory->make();
    }

    /**
     * Extension point for building the system prompt.
     */
    protected function buildSystemPrompt(?string $basePrompt, mixed $context): ?string
    {
        return $basePrompt;
    }

    /**
     * Extension point called at the start of a run.
     */
    protected function onRunStart(mixed $context): void
    {
    }

    /**
     * Extension point called at the end of a run.
     */
    protected function onRunEnd(mixed $context): void
    {
    }

    /**
     * Extension point for handling an LLM response before the agentic loop continues.
     * Return null to signal the loop should stop.
     */
    protected function handleResponse(array $response, RunContext $context): ?array
    {
        return $response;
    }

    /**
     * Consume a streaming chat response, accumulating content and tool calls
     * into the same `{role, content, tool_calls}` shape produced by the
     * non-streaming path. Mirrors Python's `_handle_stream` and TS's
     * `_handle_stream` so downstream code is identical regardless of mode.
     *
     * @param StreamResponse<CreateStreamedResponse> $stream
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    protected function handleStream(StreamResponse $stream): array
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
     * Convert a non-streaming CreateResponse into the harness's array shape.
     *
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    protected function handleResponseObject(CreateResponse $response): array
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

    /**
     * Call the LLM with retry logic.
     *
     * @param array $messages
     * @param array|null $toolsSchema
     * @return array The assistant message
     */
    protected function callLlm(array $messages, ?array $toolsSchema = null): array
    {
        $params = array_merge([
            'model' => $this->model,
            'messages' => $messages,
        ], $this->completionParams);

        if ($toolsSchema !== null && count($toolsSchema) > 0) {
            $params['tools'] = $toolsSchema;
        }

        $lastException = null;
        for ($attempt = 0; $attempt <= $this->maxRetries; $attempt++) {
            try {
                if ($this->stream) {
                    $stream = $this->client->chat()->createStreamed($params);
                    return $this->handleStream($stream);
                }
                $response = $this->client->chat()->create($params);
                return $this->handleResponseObject($response);
            } catch (\Throwable $e) {
                $lastException = $e;
                if ($attempt < $this->maxRetries) {
                    $delay = min(2 ** $attempt, 10);
                    sleep($delay);
                }
            }
        }

        throw new \RuntimeException(
            'LLM call failed after ' . ($this->maxRetries + 1) . ' attempts: ' . $lastException->getMessage(),
            0,
            $lastException,
        );
    }

    /**
     * Run the agent loop.
     *
     * @param array $messages
     * @param array $options
     * @return string
     */
    public function run(array $messages, array $options = []): string
    {
        $messages = array_map(fn($m) => $m, $messages); // shallow copy

        $systemPrompt = $this->buildSystemPrompt($this->system, $options);
        if ($systemPrompt !== null) {
            if (!empty($messages) && ($messages[0]['role'] ?? '') === 'system') {
                $messages[0]['content'] = $systemPrompt;
            } else {
                array_unshift($messages, ['role' => 'system', 'content' => $systemPrompt]);
            }
        }

        $context = new RunContext(agent: $this, turn: 0, metadata: []);
        $this->onRunStart($context);

        for ($turn = 0; $turn < $this->maxTurns; $turn++) {
            $context->turn = $turn;
            $assistantMsg = $this->callLlm($messages);
            $result = $this->handleResponse($assistantMsg, $context);

            if ($result === null) {
                $messages[] = $assistantMsg;
                $content = $assistantMsg['content'] ?? '';
                $this->onRunEnd($context);
                return $content;
            }

            $messages[] = $result;
            if (empty($result['tool_calls'])) {
                $this->onRunEnd($context);
                return $result['content'] ?? '';
            }
        }

        $this->onRunEnd($context);
        return end($messages)['content'] ?? '';
    }
}
