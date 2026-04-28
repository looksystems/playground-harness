<?php

declare(strict_types=1);

namespace AgentHarness\Llm;

use Anthropic\Client as SdkClient;
use Anthropic\Messages\InputJSONDelta;
use Anthropic\Messages\Message;
use Anthropic\Messages\RawContentBlockDeltaEvent;
use Anthropic\Messages\RawContentBlockStartEvent;
use Anthropic\Messages\TextDelta;
use Anthropic\Messages\ToolUseBlock;

/**
 * AnthropicClient — ClientInterface implementation backed by anthropic-ai/sdk.
 *
 * The harness emits OpenAI-shaped messages and tool schemas; this adapter
 * translates them to Anthropic's native Messages API in both directions.
 * Mirrors the Go adapter (`src/go/llm/anthropic/anthropic.go`) and the
 * TypeScript adapter (`src/typescript/llm/anthropic.ts`).
 *
 * Translations performed:
 *   1. system messages are extracted out of the messages array into the
 *      top-level `system` field; multiple system messages concatenate
 *   2. role mapping: only "user" / "assistant" survive; "tool" results
 *      become tool_result content blocks coalesced into a user-role turn
 *   3. tool definitions: strip the OpenAI {type:function, function:{...}}
 *      wrapper and emit Anthropic's flatter tool shape with input_schema
 *   4. tool-call response: ToolUseBlock content blocks become OpenAI-shaped
 *      tool_calls; concatenated text blocks become content
 *   5. streaming: map Anthropic SSE events to the unified
 *      {role, content, tool_calls} shape via a discriminated switch on
 *      event->type
 */
class AnthropicClient implements ClientInterface
{
    /** Anthropic's API requires max_tokens; harness fallback if request omits it. */
    public const DEFAULT_MAX_TOKENS = 4096;

    private SdkClient $client;

    public function __construct(
        ?string $apiKey = null,
        ?string $baseUrl = null,
        public readonly int $defaultMaxTokens = self::DEFAULT_MAX_TOKENS,
        ?SdkClient $client = null,
    ) {
        if ($client !== null) {
            $this->client = $client;
            return;
        }
        $key = $apiKey ?? getenv('ANTHROPIC_API_KEY') ?: 'sk-ant-placeholder';
        $this->client = new SdkClient(apiKey: $key, baseUrl: $baseUrl);
    }

    /**
     * @param array<string, mixed> $request
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    public function callLlm(array $request): array
    {
        $messages = $request['messages'] ?? [];
        $split = self::splitSystemAndMessages($messages);
        $params = array_merge($request['completionParams'] ?? [], [
            'model' => $request['model'],
            'messages' => $split['messages'],
            'maxTokens' => $this->resolveMaxTokens($request),
        ]);
        if ($split['system'] !== []) {
            $params['system'] = $split['system'];
        }
        if (!empty($request['tools'])) {
            $params['tools'] = array_map(
                fn (array $t): array => self::translateToolSchema($t),
                $request['tools'],
            );
        }

        if (!empty($request['stream'])) {
            $response = $this->client->messages->raw->createStream($params);
            return $this->consumeStream($response->parse());
        }

        $response = $this->client->messages->raw->create($params);
        /** @var Message $message */
        $message = $response->parse();
        return self::collectNonStreaming($message);
    }

    /**
     * @param array<string, mixed> $request
     */
    private function resolveMaxTokens(array $request): int
    {
        $extra = $request['completionParams'] ?? [];
        if (isset($extra['maxTokens']) && is_int($extra['maxTokens']) && $extra['maxTokens'] > 0) {
            return $extra['maxTokens'];
        }
        if (isset($extra['max_tokens']) && is_int($extra['max_tokens']) && $extra['max_tokens'] > 0) {
            return $extra['max_tokens'];
        }
        return $this->defaultMaxTokens;
    }

    /**
     * Iterate the SDK's typed RawMessageStreamEvent stream, accumulating text
     * deltas into content and input_json_delta into tool_call arguments.
     *
     * @param iterable<object> $events
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    private function consumeStream(iterable $events): array
    {
        $contentParts = [];
        $toolCallsByIndex = [];

        foreach ($events as $event) {
            $type = $event->type ?? null;
            if ($type === 'content_block_start' && $event instanceof RawContentBlockStartEvent) {
                $block = $event->contentBlock;
                if ($block instanceof ToolUseBlock) {
                    $toolCallsByIndex[$event->index] = [
                        'id' => $block->id,
                        'type' => 'function',
                        'function' => ['name' => $block->name, 'arguments' => ''],
                    ];
                }
            } elseif ($type === 'content_block_delta' && $event instanceof RawContentBlockDeltaEvent) {
                $delta = $event->delta;
                if ($delta instanceof TextDelta) {
                    $contentParts[] = $delta->text;
                } elseif ($delta instanceof InputJSONDelta) {
                    if (isset($toolCallsByIndex[$event->index])) {
                        $toolCallsByIndex[$event->index]['function']['arguments'] .= $delta->partialJSON;
                    }
                }
            }
            // message_start, content_block_stop, message_delta, message_stop
            // carry metadata only; ignore.
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

    // -----------------------------------------------------------------------
    // Translation helpers (public static so tests can drive them directly)
    // -----------------------------------------------------------------------

    /**
     * Walk the OpenAI-shaped messages array and produce Anthropic's
     * `{system, messages}` split. Tool-result messages collapse into a
     * tool_result content block on the most recent user-role turn (creating
     * one if needed). Adjacent tool results coalesce.
     *
     * @param array<int, array<string, mixed>> $input
     * @return array{system: list<array{type:string, text:string}>, messages: list<array{role:string, content:array}>}
     */
    public static function splitSystemAndMessages(array $input): array
    {
        $system = [];
        $messages = [];

        foreach ($input as $i => $m) {
            $role = $m['role'] ?? null;
            $content = $m['content'] ?? null;
            switch ($role) {
                case 'system':
                    if ($content !== null && $content !== '') {
                        $system[] = ['type' => 'text', 'text' => (string) $content];
                    }
                    break;
                case 'user':
                    $messages[] = [
                        'role' => 'user',
                        'content' => [['type' => 'text', 'text' => (string) ($content ?? '')]],
                    ];
                    break;
                case 'assistant': {
                    $blocks = [];
                    if ($content !== null && $content !== '') {
                        $blocks[] = ['type' => 'text', 'text' => (string) $content];
                    }
                    foreach ($m['tool_calls'] ?? [] as $tc) {
                        $argsStr = $tc['function']['arguments'] ?? '{}';
                        $parsed = $argsStr === '' ? new \stdClass() : (json_decode($argsStr, true) ?? new \stdClass());
                        $blocks[] = [
                            'type' => 'tool_use',
                            'id' => $tc['id'] ?? '',
                            'name' => $tc['function']['name'] ?? '',
                            'input' => $parsed,
                        ];
                    }
                    $messages[] = ['role' => 'assistant', 'content' => $blocks];
                    break;
                }
                case 'tool': {
                    $toolCallId = $m['tool_call_id'] ?? null;
                    if ($toolCallId === null || $toolCallId === '') {
                        throw new \InvalidArgumentException(
                            "anthropic: message {$i} has role=tool but no tool_call_id"
                        );
                    }
                    $block = [
                        'type' => 'tool_result',
                        'tool_use_id' => $toolCallId,
                        'content' => $content === null ? '' : (string) $content,
                    ];
                    $lastIdx = count($messages) - 1;
                    if ($lastIdx >= 0 && $messages[$lastIdx]['role'] === 'user') {
                        $messages[$lastIdx]['content'][] = $block;
                    } else {
                        $messages[] = ['role' => 'user', 'content' => [$block]];
                    }
                    break;
                }
                default:
                    // Unknown / null roles dropped silently for forward-compat.
                    break;
            }
        }

        return ['system' => $system, 'messages' => $messages];
    }

    /**
     * Translate an OpenAI-shaped tool schema to Anthropic's tool shape:
     *   {type:function, function:{name, description, parameters}}
     *     → {name, description, input_schema: parameters}
     *
     * @param array<string, mixed> $tool
     * @return array{name:string, description?:string, input_schema:array<string, mixed>}
     */
    public static function translateToolSchema(array $tool): array
    {
        $fn = $tool['function'] ?? [];
        $out = [
            'name' => $fn['name'] ?? '',
            'input_schema' => $fn['parameters'] ?? ['type' => 'object', 'properties' => new \stdClass()],
        ];
        if (!empty($fn['description'])) {
            $out['description'] = $fn['description'];
        }
        return $out;
    }

    /**
     * Collapse a non-streaming Anthropic Message into the harness's
     * {role, content, tool_calls} shape.
     *
     * @return array{role: string, content: ?string, tool_calls: ?array}
     */
    public static function collectNonStreaming(Message $message): array
    {
        $contentParts = [];
        $toolCalls = [];
        foreach ($message->content as $block) {
            if ($block instanceof \Anthropic\Messages\TextBlock) {
                $contentParts[] = $block->text;
            } elseif ($block instanceof ToolUseBlock) {
                $argsStr = $block->input === [] ? '{}' : json_encode($block->input);
                $toolCalls[] = [
                    'id' => $block->id,
                    'type' => 'function',
                    'function' => [
                        'name' => $block->name,
                        'arguments' => $argsStr,
                    ],
                ];
            }
        }
        return [
            'role' => 'assistant',
            'content' => $contentParts !== [] ? implode('', $contentParts) : null,
            'tool_calls' => $toolCalls !== [] ? $toolCalls : null,
        ];
    }
}
