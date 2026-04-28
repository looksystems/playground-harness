<?php

declare(strict_types=1);

namespace AgentHarness;

use AgentHarness\Llm\AnthropicClient;
use AgentHarness\Llm\ClientInterface as LlmClient;
use AgentHarness\Llm\OpenAIClient;
use OpenAI\Contracts\ClientContract;

class BaseAgent
{
    protected LlmClient $llmClient;

    /**
     * @param array<string, mixed>          $completionParams Extra options forwarded to the chosen client
     * @param ClientContract|null           $client           Inject a pre-built openai-php SDK client
     *                                                         (e.g. OpenAI\Testing\ClientFake) for tests.
     *                                                         Wrapped in an OpenAIClient internally.
     * @param string|null                   $provider         "openai" (default) | "anthropic". Controls
     *                                                         which default client is built when neither
     *                                                         $llmClient nor $client is supplied.
     * @param LlmClient|null                $llmClient        Inject a pre-built ClientInterface
     *                                                         implementation. Wins over $client and $provider.
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
        public readonly ?string $provider = null,
        ?LlmClient $llmClient = null,
    ) {
        if ($llmClient !== null) {
            $this->llmClient = $llmClient;
        } elseif ($client !== null) {
            // Backward compat: a raw OpenAI SDK client is wrapped in OpenAIClient.
            $this->llmClient = new OpenAIClient(client: $client);
        } else {
            $this->llmClient = $this->buildDefaultClient();
        }
    }

    private function buildDefaultClient(): LlmClient
    {
        $provider = $this->provider ?? 'openai';
        return match ($provider) {
            'anthropic' => new AnthropicClient(apiKey: $this->apiKey, baseUrl: $this->baseUrl),
            'openai' => new OpenAIClient(apiKey: $this->apiKey, baseUrl: $this->baseUrl),
            default => throw new \InvalidArgumentException("Unknown provider: {$provider}"),
        };
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
     * Call the LLM via the bound ClientInterface, with retry logic.
     *
     * @param array<int, array<string, mixed>> $messages
     * @param array<int, array<string, mixed>>|null $toolsSchema
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    protected function callLlm(array $messages, ?array $toolsSchema = null): array
    {
        $request = [
            'model' => $this->model,
            'messages' => $messages,
            'stream' => $this->stream,
            'completionParams' => $this->completionParams,
        ];
        if ($toolsSchema !== null && count($toolsSchema) > 0) {
            $request['tools'] = $toolsSchema;
        }

        $lastException = null;
        for ($attempt = 0; $attempt <= $this->maxRetries; $attempt++) {
            try {
                return $this->llmClient->callLlm($request);
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
