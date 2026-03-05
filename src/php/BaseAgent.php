<?php

declare(strict_types=1);

namespace AgentHarness;

use GuzzleHttp\Client;

class BaseAgent
{
    protected Client $httpClient;

    public function __construct(
        public readonly string $model,
        public readonly ?string $system = null,
        public readonly int $maxTurns = 20,
        public readonly int $maxRetries = 2,
        public readonly bool $stream = true,
        public readonly ?string $baseUrl = null,
        public readonly ?string $apiKey = null,
        public readonly array $completionParams = [],
    ) {
        $clientConfig = [];
        if ($this->baseUrl !== null) {
            $clientConfig['base_uri'] = $this->baseUrl;
        }
        $this->httpClient = new Client($clientConfig);
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
     * Call the LLM with retry logic.
     *
     * @param array $messages
     * @param array|null $toolsSchema
     * @return array The assistant message
     */
    protected function callLlm(array $messages, ?array $toolsSchema = null): array
    {
        $headers = ['Content-Type' => 'application/json'];
        if ($this->apiKey !== null) {
            $headers['Authorization'] = 'Bearer ' . $this->apiKey;
        }

        $body = array_merge([
            'model' => $this->model,
            'messages' => $messages,
        ], $this->completionParams);

        if ($toolsSchema !== null && count($toolsSchema) > 0) {
            $body['tools'] = $toolsSchema;
        }

        if ($this->stream) {
            $body['stream'] = true;
        }

        $lastException = null;
        for ($attempt = 0; $attempt <= $this->maxRetries; $attempt++) {
            try {
                $url = ($this->baseUrl ?? 'https://api.openai.com/v1') . '/chat/completions';
                $response = $this->httpClient->post($url, [
                    'headers' => $headers,
                    'json' => $body,
                ]);
                $data = json_decode($response->getBody()->getContents(), true);
                $msg = $data['choices'][0]['message'] ?? [];
                return [
                    'role' => 'assistant',
                    'content' => $msg['content'] ?? null,
                    'tool_calls' => $msg['tool_calls'] ?? null,
                ];
            } catch (\Throwable $e) {
                $lastException = $e;
                if ($attempt < $this->maxRetries) {
                    $delay = min(2 ** $attempt, 10);
                    sleep($delay);
                }
            }
        }

        throw new \RuntimeException(
            "LLM call failed after " . ($this->maxRetries + 1) . " attempts: " . $lastException->getMessage(),
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
