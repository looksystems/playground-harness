<?php

declare(strict_types=1);

namespace AgentHarness;

class AgentBuilder
{
    private string $model;
    private ?string $system = null;
    private int $maxTurns = 20;
    private int $maxRetries = 2;
    private bool $stream = true;
    private ?string $baseUrl = null;
    private ?string $apiKey = null;
    private array $completionParams = [];
    /** @var list<ToolDef> */
    private array $tools = [];
    /** @var list<Middleware> */
    private array $middleware = [];
    /** @var list<array{HookEvent, callable}> */
    private array $hooks = [];
    /** @var list<StructuredEvent> */
    private array $events = [];
    /** @var list<array{Skill, array<string, mixed>}> */
    private array $skills = [];
    private ?array $shellOpts = null;
    private ?string $driver = null;
    /** @var list<array{string, \Closure}> */
    private array $commands = [];

    public function __construct(string $model)
    {
        $this->model = $model;
    }

    public function system(string $prompt): static
    {
        $this->system = $prompt;
        return $this;
    }

    public function maxTurns(int $n): static
    {
        $this->maxTurns = $n;
        return $this;
    }

    public function maxRetries(int $n): static
    {
        $this->maxRetries = $n;
        return $this;
    }

    public function stream(bool $enabled = true): static
    {
        $this->stream = $enabled;
        return $this;
    }

    public function baseUrl(string $url): static
    {
        $this->baseUrl = $url;
        return $this;
    }

    public function apiKey(string $key): static
    {
        $this->apiKey = $key;
        return $this;
    }

    public function completionParams(array $params): static
    {
        $this->completionParams = $params;
        return $this;
    }

    public function tool(ToolDef $tool): static
    {
        $this->tools[] = $tool;
        return $this;
    }

    public function tools(ToolDef ...$tools): static
    {
        array_push($this->tools, ...$tools);
        return $this;
    }

    public function middleware(Middleware ...$mws): static
    {
        array_push($this->middleware, ...$mws);
        return $this;
    }

    public function on(HookEvent $event, callable $callback): static
    {
        $this->hooks[] = [$event, $callback];
        return $this;
    }

    public function event(StructuredEvent $eventType): static
    {
        $this->events[] = $eventType;
        return $this;
    }

    public function events(StructuredEvent ...$eventTypes): static
    {
        array_push($this->events, ...$eventTypes);
        return $this;
    }

    public function skill(Skill $skill, array $config = []): static
    {
        $this->skills[] = [$skill, $config];
        return $this;
    }

    public function skills(Skill ...$skills): static
    {
        foreach ($skills as $sk) {
            $this->skills[] = [$sk, []];
        }
        return $this;
    }

    public function shell(array $opts = []): static
    {
        $this->shellOpts = $opts;
        return $this;
    }

    public function driver(string $name): static
    {
        $this->driver = $name;
        return $this;
    }

    public function command(string $name, \Closure $handler): static
    {
        $this->commands[] = [$name, $handler];
        return $this;
    }

    public function create(): StandardAgent
    {
        $agent = new StandardAgent(
            model: $this->model,
            system: $this->system,
            maxTurns: $this->maxTurns,
            maxRetries: $this->maxRetries,
            stream: $this->stream,
            baseUrl: $this->baseUrl,
            apiKey: $this->apiKey,
            completionParams: $this->completionParams,
        );

        foreach ($this->tools as $tool) {
            $agent->registerTool($tool);
        }

        foreach ($this->middleware as $mw) {
            $agent->use($mw);
        }

        foreach ($this->hooks as [$event, $cb]) {
            $agent->on($event, $cb);
        }

        foreach ($this->events as $et) {
            $agent->registerEvent($et);
        }

        if ($this->shellOpts !== null) {
            $agent->initHasShell(...$this->shellOpts, driver: $this->driver);
        } elseif ($this->driver !== null) {
            $agent->initHasShell(driver: $this->driver);
        }

        foreach ($this->commands as [$name, $handler]) {
            $agent->registerCommand($name, $handler);
        }

        // Skills last — they may depend on tools, shell, etc.
        foreach ($this->skills as [$sk, $config]) {
            $agent->mount($sk, $config);
        }

        return $agent;
    }
}
