<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasCommands
{
    /** @var array<string, CommandDef> */
    private array $slashCommands = [];
    private bool $llmCommandsEnabled = true;
    private bool $hasCommandsInitialized = false;

    public function initHasCommands(bool $llmCommandsEnabled = true): void
    {
        $this->slashCommands = [];
        $this->llmCommandsEnabled = $llmCommandsEnabled;
        $this->hasCommandsInitialized = true;
    }

    private function ensureHasCommands(): void
    {
        if (!$this->hasCommandsInitialized) {
            $this->initHasCommands();
        }
    }

    public function registerSlashCommand(CommandDef $def): void
    {
        $this->ensureHasCommands();
        $this->slashCommands[$def->name] = $def;

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SlashCommandRegister, $def);
        }

        if ($def->llmVisible && $this->llmCommandsEnabled && method_exists($this, 'registerTool')) {
            $self = $this;
            $tool = ToolDef::make(
                name: "slash_{$def->name}",
                description: $def->description,
                parameters: $def->parameters,
                execute: function (array $args) use ($self, $def): string {
                    return $self->executeSlashCommand($def->name, $args);
                },
            );
            $this->registerTool($tool);
        }
    }

    public function unregisterSlashCommand(string $name): void
    {
        $this->ensureHasCommands();
        unset($this->slashCommands[$name]);

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SlashCommandUnregister, $name);
        }

        if (method_exists($this, 'unregisterTool')) {
            $this->unregisterTool("slash_{$name}");
        }
    }

    public function executeSlashCommand(string $name, array $args): string
    {
        $this->ensureHasCommands();

        if (!isset($this->slashCommands[$name])) {
            return "Unknown slash command: /{$name}";
        }

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SlashCommandCall, $name, $args);
        }

        $result = $this->slashCommands[$name]->call($args);

        if (method_exists($this, 'emit')) {
            $this->emit(HookEvent::SlashCommandResult, $name, $result);
        }

        return $result;
    }

    public function interceptSlashCommand(string $text): ?array
    {
        $this->ensureHasCommands();

        if (!str_starts_with($text, '/')) {
            return null;
        }

        $text = substr($text, 1); // remove leading /
        $parts = explode(' ', $text, 2);
        $name = $parts[0];
        $rest = $parts[1] ?? '';

        if (!isset($this->slashCommands[$name])) {
            return null;
        }

        $def = $this->slashCommands[$name];

        // If the command has parameter schema with properties, parse key=value pairs
        if (!empty($def->parameters['properties']) && is_array($def->parameters['properties'])) {
            $args = [];
            $tokens = preg_split('/\s+/', $rest, -1, PREG_SPLIT_NO_EMPTY);
            foreach ($tokens as $token) {
                if (str_contains($token, '=')) {
                    [$key, $value] = explode('=', $token, 2);
                    $args[$key] = $value;
                } else {
                    $args['input'] = isset($args['input']) ? $args['input'] . ' ' . $token : $token;
                }
            }
            return [$name, $args];
        }

        // No schema: pass raw input
        $args = $rest !== '' ? ['input' => $rest] : [];
        return [$name, $args];
    }

    /**
     * @return array<string, CommandDef>
     */
    public function commands(): array
    {
        $this->ensureHasCommands();
        return $this->slashCommands;
    }
}
