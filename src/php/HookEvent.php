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
    case ShellCall = 'shell_call';
    case ShellResult = 'shell_result';
    case ShellNotFound = 'shell_not_found';
    case ShellCwd = 'shell_cwd';
    case ToolRegister = 'tool_register';
    case ToolUnregister = 'tool_unregister';
    case CommandRegister = 'command_register';
    case CommandUnregister = 'command_unregister';
    case SlashCommandRegister = 'slash_command_register';
    case SlashCommandUnregister = 'slash_command_unregister';
    case SlashCommandCall = 'slash_command_call';
    case SlashCommandResult = 'slash_command_result';
}
