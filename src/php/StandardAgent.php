<?php

declare(strict_types=1);

namespace AgentHarness;

class StandardAgent extends BaseAgent
{
    use HasHooks;
    use HasMiddleware;
    use UsesTools;
    use EmitsEvents;
    use HasShell;
    use HasSkills;

    public static function build(string $model): AgentBuilder
    {
        return new AgentBuilder($model);
    }
}
