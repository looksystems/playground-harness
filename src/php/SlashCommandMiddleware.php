<?php

declare(strict_types=1);

namespace AgentHarness;

class SlashCommandMiddleware extends BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        if (empty($messages)) {
            return $messages;
        }

        $last = end($messages);

        if (($last['role'] ?? '') !== 'user') {
            return $messages;
        }

        $content = $last['content'] ?? '';

        if (!is_string($content) || !str_starts_with($content, '/')) {
            return $messages;
        }

        $agent = $context->agent ?? $context;

        if (!method_exists($agent, 'interceptSlashCommand')) {
            return $messages;
        }

        $result = $agent->interceptSlashCommand($content);

        if ($result === null) {
            return $messages;
        }

        [$name, $args] = $result;
        $output = $agent->executeSlashCommand($name, $args);

        $messages[array_key_last($messages)] = array_merge($last, [
            'content' => "[Slash command /{$name} result]: {$output}",
        ]);

        return $messages;
    }
}
