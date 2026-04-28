<?php

declare(strict_types=1);

namespace AgentHarness\Llm;

/**
 * Provider-agnostic chat-completion contract for the PHP harness.
 *
 * Mirrors Go's `llm.Client` interface and TypeScript's `LlmClient`. Concrete
 * implementations (OpenAIClient, AnthropicClient) live alongside this file.
 *
 * The harness sends OpenAI-shaped messages and tool schemas (the lingua franca
 * across the four language implementations) and expects an assistant message
 * back in the same shape. Provider adapters that target non-OpenAI APIs are
 * responsible for translating in both directions.
 *
 * The expected request array shape:
 *
 *   [
 *     'model'            => string,
 *     'messages'         => array<array{role:string, content?:?string, tool_calls?:?array, tool_call_id?:string}>,
 *     'tools'            => array<array{type:'function', function:array{name:string, description?:string, parameters:array}}>,  // optional
 *     'stream'           => bool,
 *     'completionParams' => array<string, mixed>,  // pass-through (temperature, max_tokens, …)
 *   ]
 *
 * The expected response array shape (always):
 *
 *   ['role' => 'assistant', 'content' => ?string, 'tool_calls' => ?array]
 */
interface ClientInterface
{
    /**
     * @param array<string, mixed> $request
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    public function callLlm(array $request): array;
}
