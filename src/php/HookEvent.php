<?php

declare(strict_types=1);

namespace AgentHarness;

enum HookEvent: string
{
    case RunStart = 'run_start';
    case RunEnd = 'run_end';
    case LlmRequest = 'llm_request';
    case LlmResponse = 'llm_response';
    case ToolCall = 'tool_call';
    case ToolResult = 'tool_result';
    case ToolError = 'tool_error';
    case Retry = 'retry';
    case TokenStream = 'token_stream';
    case Error = 'error';
}
