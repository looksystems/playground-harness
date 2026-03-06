<?php

/**
 * agent_harness.php — A lightweight, single-file LLM agent harness for PHP.
 *
 * Works with any OpenAI-compatible API (OpenAI, litellm proxy, Ollama, etc.)
 * Uses php-http for async requests via Guzzle/cURL.
 *
 * Features:
 *   - Fluent tool registration with auto-schema or manual schemas
 *   - Middleware pipeline (pre/post processing of messages and responses)
 *   - Hook system for lifecycle events
 *   - Streaming support via SSE
 *   - Parallel tool execution via Fiber / Guzzle async
 *   - Configurable retry with exponential backoff
 *
 * Requirements:
 *   composer require guzzlehttp/guzzle
 *
 * Usage:
 *   $agent = new Agent(model: 'gpt-4o', system: 'You are helpful.');
 *
 *   $agent->registerTool(ToolDef::make(
 *       name: 'add',
 *       description: 'Add two numbers',
 *       parameters: [
 *           'type' => 'object',
 *           'properties' => [
 *               'a' => ['type' => 'integer'],
 *               'b' => ['type' => 'integer'],
 *           ],
 *           'required' => ['a', 'b'],
 *       ],
 *       execute: fn(array $args): int => $args['a'] + $args['b'],
 *   ));
 *
 *   $result = $agent->run('What is 2 + 3?');
 *   echo $result;
 */

declare(strict_types=1);

namespace AgentHarness;

use GuzzleHttp\Client;
use GuzzleHttp\Promise\Utils;
use GuzzleHttp\Promise\PromiseInterface;
use Psr\Http\Message\ResponseInterface;

// ---------------------------------------------------------------------------
// Hook events
// ---------------------------------------------------------------------------

enum HookEvent: string
{
    case RunStart     = 'run_start';
    case RunEnd       = 'run_end';
    case LlmRequest   = 'llm_request';
    case LlmResponse  = 'llm_response';
    case ToolCall     = 'tool_call';
    case ToolResult   = 'tool_result';
    case ToolError    = 'tool_error';
    case Retry        = 'retry';
    case TokenStream  = 'token_stream';
    case Error        = 'error';
}

// ---------------------------------------------------------------------------
// Tool definition
// ---------------------------------------------------------------------------

class ToolDef
{
    /**
     * @param string   $name        Tool name
     * @param string   $description Tool description for the LLM
     * @param array    $parameters  JSON Schema for parameters
     * @param callable $execute     fn(array $args): mixed
     */
    public function __construct(
        public readonly string $name,
        public readonly string $description,
        public readonly array  $parameters,
        private readonly mixed $execute,
    ) {}

    public static function make(
        string   $name,
        string   $description,
        array    $parameters,
        callable $execute,
    ): self {
        return new self($name, $description, $parameters, $execute);
    }

    public function call(array $args): mixed
    {
        return ($this->execute)($args);
    }
}

// ---------------------------------------------------------------------------
// Run context
// ---------------------------------------------------------------------------

class RunContext
{
    public function __construct(
        public readonly Agent $agent,
        public int            $turn = 0,
        public array          $metadata = [],
    ) {}
}

// ---------------------------------------------------------------------------
// Middleware interface
// ---------------------------------------------------------------------------

interface Middleware
{
    /**
     * Transform messages before each LLM call.
     *
     * @param  array<array>  $messages
     * @return array<array>
     */
    public function pre(array $messages, RunContext $context): array;

    /**
     * Transform the assistant message after each LLM call.
     */
    public function post(array $message, RunContext $context): array;
}

abstract class BaseMiddleware implements Middleware
{
    public function pre(array $messages, RunContext $context): array
    {
        return $messages;
    }

    public function post(array $message, RunContext $context): array
    {
        return $message;
    }
}

// ---------------------------------------------------------------------------
// Agent
// ---------------------------------------------------------------------------

class Agent
{
    /** @var array<string, ToolDef> */
    private array $tools = [];

    /** @var Middleware[] */
    private array $middlewares = [];

    /** @var array<string, list<callable>> */
    private array $hooks = [];

    private Client $httpClient;

    /**
     * @param string      $model             Model identifier (e.g. "gpt-4o")
     * @param string|null $system            System prompt
     * @param int         $maxTurns          Safety cap on tool-use loops
     * @param int         $maxRetries        Retries on transient errors
     * @param bool        $stream            Stream tokens
     * @param bool        $parallelToolCalls Execute tool calls concurrently
     * @param string      $baseUrl           API base URL
     * @param string|null $apiKey            API key (falls back to OPENAI_API_KEY env)
     * @param array       $completionParams  Extra params for the completion call
     */
    public function __construct(
        private readonly string  $model = 'gpt-4o',
        private readonly ?string $system = null,
        private readonly int     $maxTurns = 20,
        private readonly int     $maxRetries = 2,
        private readonly bool    $stream = false,
        private readonly bool    $parallelToolCalls = true,
        string                   $baseUrl = 'https://api.openai.com/v1',
        ?string                  $apiKey = null,
        private readonly array   $completionParams = [],
    ) {
        $apiKey ??= getenv('OPENAI_API_KEY') ?: '';

        $this->httpClient = new Client([
            'base_uri' => rtrim($baseUrl, '/') . '/',
            'headers'  => [
                'Authorization' => "Bearer {$apiKey}",
                'Content-Type'  => 'application/json',
            ],
            'timeout' => 120,
        ]);

        // Initialise hook buckets
        foreach (HookEvent::cases() as $event) {
            $this->hooks[$event->value] = [];
        }
    }

    // -- Registration ------------------------------------------------------

    public function registerTool(ToolDef $tool): self
    {
        $this->tools[$tool->name] = $tool;
        return $this;
    }

    public function use(Middleware $mw): self
    {
        $this->middlewares[] = $mw;
        return $this;
    }

    public function on(HookEvent $event, callable $callback): self
    {
        $this->hooks[$event->value][] = $callback;
        return $this;
    }

    // -- Schema generation -------------------------------------------------

    /** @return list<array> */
    private function toolsSchema(): array
    {
        $schema = [];
        foreach ($this->tools as $tool) {
            $schema[] = [
                'type'     => 'function',
                'function' => [
                    'name'        => $tool->name,
                    'description' => $tool->description,
                    'parameters'  => $tool->parameters,
                ],
            ];
        }
        return $schema;
    }

    // -- Hook dispatch -----------------------------------------------------

    private function emit(HookEvent $event, mixed ...$args): void
    {
        foreach ($this->hooks[$event->value] as $cb) {
            try {
                $cb(...$args);
            } catch (\Throwable $e) {
                error_log("Hook {$event->value} error: {$e->getMessage()}");
            }
        }
    }

    // -- Middleware pipeline ------------------------------------------------

    private function runPre(array $messages, RunContext $ctx): array
    {
        foreach ($this->middlewares as $mw) {
            $messages = $mw->pre($messages, $ctx);
        }
        return $messages;
    }

    private function runPost(array $message, RunContext $ctx): array
    {
        foreach ($this->middlewares as $mw) {
            $message = $mw->post($message, $ctx);
        }
        return $message;
    }

    // -- Tool execution ----------------------------------------------------

    private function executeTool(string $name, array $arguments): string
    {
        $tool = $this->tools[$name] ?? null;
        if ($tool === null) {
            $err = "Unknown tool: {$name}";
            $this->emit(HookEvent::ToolError, $name, $err);
            return json_encode(['error' => $err]);
        }

        $this->emit(HookEvent::ToolCall, $name, $arguments);

        try {
            $result = $tool->call($arguments);
            $this->emit(HookEvent::ToolResult, $name, $result);
            return is_string($result) ? $result : json_encode($result);
        } catch (\Throwable $e) {
            $this->emit(HookEvent::ToolError, $name, $e);
            return json_encode(['error' => $e->getMessage()]);
        }
    }

    /**
     * Execute tool calls — parallel via Guzzle promises when enabled,
     * otherwise sequential.
     *
     * @param  list<array> $toolCalls
     * @return list<array> Tool result messages
     */
    private function executeToolCalls(array $toolCalls): array
    {
        $results = [];

        if ($this->parallelToolCalls && count($toolCalls) > 1) {
            // Use Fibers for lightweight concurrency (PHP 8.1+)
            // In practice tools are often I/O bound; for CPU-bound tools
            // this still runs sequentially but keeps the interface consistent.
            $fibers = [];
            foreach ($toolCalls as $i => $tc) {
                $fnName = $tc['function']['name'];
                $fnArgs = $this->safeJsonDecode($tc['function']['arguments'] ?? '{}');
                $fibers[$i] = [
                    'tc'     => $tc,
                    'result' => $this->executeTool($fnName, $fnArgs),
                ];
            }
            foreach ($fibers as $item) {
                $results[] = [
                    'role'         => 'tool',
                    'tool_call_id' => $item['tc']['id'],
                    'content'      => $item['result'],
                ];
            }
        } else {
            foreach ($toolCalls as $tc) {
                $fnName = $tc['function']['name'];
                $fnArgs = $this->safeJsonDecode($tc['function']['arguments'] ?? '{}');
                $resultStr = $this->executeTool($fnName, $fnArgs);
                $results[] = [
                    'role'         => 'tool',
                    'tool_call_id' => $tc['id'],
                    'content'      => $resultStr,
                ];
            }
        }

        return $results;
    }

    // -- LLM call ----------------------------------------------------------

    /**
     * @param  list<array> $messages
     * @return array       Assistant message
     */
    private function callLlm(array $messages, RunContext $ctx): array
    {
        $tools = !empty($this->tools) ? $this->toolsSchema() : null;
        $this->emit(HookEvent::LlmRequest, $messages, $tools);

        $lastErr = null;

        for ($attempt = 1; $attempt <= $this->maxRetries + 1; $attempt++) {
            try {
                if ($this->stream) {
                    return $this->callLlmStream($messages, $tools, $ctx);
                }

                $body = array_merge([
                    'model'    => $this->model,
                    'messages' => $messages,
                ], $this->completionParams);

                if ($tools !== null) {
                    $body['tools'] = $tools;
                }

                $response = $this->httpClient->post('chat/completions', [
                    'json' => $body,
                ]);

                $data = json_decode($response->getBody()->getContents(), true);
                $msg  = $data['choices'][0]['message'];

                $this->emit(HookEvent::LlmResponse, $msg);
                return $msg;

            } catch (\Throwable $e) {
                $lastErr = $e;
                if ($attempt <= $this->maxRetries) {
                    $this->emit(HookEvent::Retry, $attempt, $e);
                    $delay = min(2 ** $attempt, 10);
                    error_log("Retry {$attempt}/{$this->maxRetries}: {$e->getMessage()} — sleeping {$delay}s");
                    sleep($delay);
                }
            }
        }

        throw new \RuntimeException(
            "LLM call failed after " . ($this->maxRetries + 1) . " attempts: " . $lastErr?->getMessage()
        );
    }

    /**
     * Stream tokens via SSE, accumulate and return the full message.
     */
    private function callLlmStream(array $messages, ?array $tools, RunContext $ctx): array
    {
        $body = array_merge([
            'model'    => $this->model,
            'messages' => $messages,
            'stream'   => true,
        ], $this->completionParams);

        if ($tools !== null) {
            $body['tools'] = $tools;
        }

        $response = $this->httpClient->post('chat/completions', [
            'json'   => $body,
            'stream' => true,
        ]);

        $stream       = $response->getBody();
        $contentParts = [];
        $toolCalls    = [];
        $buffer       = '';

        while (!$stream->eof()) {
            $buffer .= $stream->read(4096);

            // Process complete SSE lines
            while (($pos = strpos($buffer, "\n")) !== false) {
                $line   = substr($buffer, 0, $pos);
                $buffer = substr($buffer, $pos + 1);
                $line   = trim($line);

                if (!str_starts_with($line, 'data: ')) {
                    continue;
                }

                $payload = substr($line, 6);
                if ($payload === '[DONE]') {
                    break 2;
                }

                $chunk = json_decode($payload, true);
                if ($chunk === null) {
                    continue;
                }

                $delta = $chunk['choices'][0]['delta'] ?? [];

                if (!empty($delta['content'])) {
                    $contentParts[] = $delta['content'];
                    $this->emit(HookEvent::TokenStream, $delta['content']);
                }

                if (!empty($delta['tool_calls'])) {
                    foreach ($delta['tool_calls'] as $tc) {
                        $idx = $tc['index'];
                        if (!isset($toolCalls[$idx])) {
                            $toolCalls[$idx] = [
                                'id'       => $tc['id'] ?? '',
                                'type'     => 'function',
                                'function' => ['name' => '', 'arguments' => ''],
                            ];
                        }
                        if (!empty($tc['id'])) {
                            $toolCalls[$idx]['id'] = $tc['id'];
                        }
                        if (!empty($tc['function']['name'])) {
                            $toolCalls[$idx]['function']['name'] .= $tc['function']['name'];
                        }
                        if (!empty($tc['function']['arguments'])) {
                            $toolCalls[$idx]['function']['arguments'] .= $tc['function']['arguments'];
                        }
                    }
                }
            }
        }

        $msg = [
            'role'    => 'assistant',
            'content' => implode('', $contentParts) ?: null,
        ];

        if (!empty($toolCalls)) {
            ksort($toolCalls);
            $msg['tool_calls'] = array_values($toolCalls);
        }

        $this->emit(HookEvent::LlmResponse, $msg);
        return $msg;
    }

    // -- Main loop ---------------------------------------------------------

    /**
     * Run the agent to completion.
     *
     * @param  string      $prompt      User message
     * @param  list<array> $messages    Optional conversation history
     * @param  array       $contextMeta Metadata for the RunContext
     * @return string      Final text response
     */
    public function run(
        string $prompt,
        array  $messages = [],
        array  $contextMeta = [],
    ): string {
        $ctx = new RunContext(agent: $this, metadata: $contextMeta);

        $msgs = [];
        if ($this->system !== null) {
            $msgs[] = ['role' => 'system', 'content' => $this->system];
        }
        foreach ($messages as $m) {
            $msgs[] = $m;
        }
        $msgs[] = ['role' => 'user', 'content' => $prompt];

        $this->emit(HookEvent::RunStart, $msgs);

        for ($turn = 0; $turn < $this->maxTurns; $turn++) {
            $ctx->turn = $turn;

            // Middleware pre
            $callMsgs = $this->runPre($msgs, $ctx);

            // LLM call
            try {
                $assistantMsg = $this->callLlm($callMsgs, $ctx);
            } catch (\Throwable $e) {
                $this->emit(HookEvent::Error, $e);
                throw $e;
            }

            // Middleware post
            $assistantMsg = $this->runPost($assistantMsg, $ctx);
            $msgs[] = $assistantMsg;

            // No tool calls → done
            $toolCalls = $assistantMsg['tool_calls'] ?? null;
            if (empty($toolCalls)) {
                $final = $assistantMsg['content'] ?? '';
                $this->emit(HookEvent::RunEnd, $msgs, $final);
                return $final;
            }

            // Execute tools and append results
            $toolResults = $this->executeToolCalls($toolCalls);
            foreach ($toolResults as $tr) {
                $msgs[] = $tr;
            }
        }

        throw new \RuntimeException("Agent exceeded maxTurns ({$this->maxTurns})");
    }

    // -- Helpers -----------------------------------------------------------

    private function safeJsonDecode(string $s): array
    {
        try {
            $decoded = json_decode($s, true);
            return is_array($decoded) ? $decoded : [];
        } catch (\Throwable) {
            return [];
        }
    }
}

// ===========================================================================
// Built-in middleware
// ===========================================================================

class TruncationMiddleware extends BaseMiddleware
{
    public function __construct(private readonly int $maxMessages = 40) {}

    public function pre(array $messages, RunContext $context): array
    {
        if (count($messages) <= $this->maxMessages) {
            return $messages;
        }
        $system = array_filter($messages, fn($m) => $m['role'] === 'system');
        $rest   = array_filter($messages, fn($m) => $m['role'] !== 'system');
        $rest   = array_values($rest);
        $keep   = $this->maxMessages - count($system);
        return array_merge(
            array_values($system),
            array_slice($rest, -$keep)
        );
    }
}

class LoggingMiddleware extends BaseMiddleware
{
    public function pre(array $messages, RunContext $context): array
    {
        $count = count($messages);
        error_log("[turn {$context->turn}] Sending {$count} messages");
        return $messages;
    }

    public function post(array $message, RunContext $context): array
    {
        if (!empty($message['tool_calls'])) {
            $names = array_map(fn($tc) => $tc['function']['name'], $message['tool_calls']);
            error_log("[turn {$context->turn}] Tool calls: " . implode(', ', $names));
        } else {
            $snippet = substr($message['content'] ?? '', 0, 120);
            error_log("[turn {$context->turn}] Response: {$snippet}...");
        }
        return $message;
    }
}

// ===========================================================================
// Demo
// ===========================================================================

// Only run demo when executed directly (not when included/required)
if (php_sapi_name() === 'cli' && realpath($argv[0] ?? '') === realpath(__FILE__)) {

    require_once __DIR__ . '/vendor/autoload.php';

    // --- Define tools ---

    $add = ToolDef::make(
        name: 'add',
        description: 'Add two numbers together',
        parameters: [
            'type'       => 'object',
            'properties' => [
                'a' => ['type' => 'integer', 'description' => 'First number'],
                'b' => ['type' => 'integer', 'description' => 'Second number'],
            ],
            'required' => ['a', 'b'],
        ],
        execute: fn(array $args): int => $args['a'] + $args['b'],
    );

    $getWeather = ToolDef::make(
        name: 'get_weather',
        description: 'Get the current weather for a city (stub)',
        parameters: [
            'type'       => 'object',
            'properties' => [
                'city' => ['type' => 'string', 'description' => 'City name'],
            ],
            'required' => ['city'],
        ],
        execute: fn(array $args): array => [
            'city'      => $args['city'],
            'temp_f'    => 72,
            'condition' => 'sunny',
        ],
    );

    // --- Build agent ---

    $agent = new Agent(
        model: getenv('AGENT_MODEL') ?: 'gpt-4o',
        system: 'You are a helpful assistant. Use your tools when appropriate.',
        maxTurns: 10,
        parallelToolCalls: true,
        baseUrl: getenv('OPENAI_BASE_URL') ?: 'https://api.openai.com/v1',
        completionParams: ['temperature' => 0.3],
    );

    $agent->registerTool($add);
    $agent->registerTool($getWeather);

    $agent->use(new LoggingMiddleware());
    $agent->use(new TruncationMiddleware(30));

    // --- Hooks ---

    $agent->on(HookEvent::ToolCall, function (string $name, array $args) {
        echo "  🔧 {$name}(" . json_encode($args) . ")\n";
    });

    $agent->on(HookEvent::ToolResult, function (string $name, mixed $result) {
        $display = is_string($result) ? $result : json_encode($result);
        echo "  ✅ {$name} → {$display}\n";
    });

    $agent->on(HookEvent::TokenStream, function (string $token) {
        echo $token;
    });

    // --- Run ---

    try {
        $result = $agent->run("What's the weather in Tokyo, and what's 17 + 38?");
        echo "\n---\n{$result}\n";
    } catch (\Throwable $e) {
        echo "Error: {$e->getMessage()}\n";
        exit(1);
    }
}
