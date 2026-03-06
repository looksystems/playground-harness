<?php

declare(strict_types=1);

namespace AgentHarness;

trait UsesTools
{
    /** @var array<string, ToolDef> */
    private array $tools = [];

    private function tryEmitFromTools(HookEvent $event, mixed ...$args): void
    {
        if (method_exists($this, 'emit')) {
            $this->emit($event, ...$args);
        }
    }

    public function registerTool(ToolDef $tool): static
    {
        $this->tools[$tool->name] = $tool;
        $this->tryEmitFromTools(HookEvent::ToolRegister, $tool);
        return $this;
    }

    public function unregisterTool(string $name): static
    {
        unset($this->tools[$name]);
        $this->tryEmitFromTools(HookEvent::ToolUnregister, $name);
        return $this;
    }

    /**
     * @return array<int, array<string, mixed>>
     */
    public function toolsSchema(): array
    {
        $schemas = [];
        foreach ($this->tools as $tool) {
            $schemas[] = [
                'type' => 'function',
                'function' => [
                    'name' => $tool->name,
                    'description' => $tool->description,
                    'parameters' => $tool->parameters,
                ],
            ];
        }
        return $schemas;
    }

    public function executeTool(string $name, array $args): string
    {
        $tool = $this->tools[$name] ?? null;
        if ($tool === null) {
            return json_encode(['error' => "Unknown tool: {$name}"]);
        }
        try {
            $result = $tool->call($args);
            return json_encode($result, JSON_THROW_ON_ERROR);
        } catch (\Throwable $e) {
            return json_encode(['error' => $e->getMessage()]);
        }
    }
}
