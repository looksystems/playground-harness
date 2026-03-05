<?php

declare(strict_types=1);

namespace AgentHarness;

class StandardAgent extends BaseAgent
{
    use HasHooks;
    use HasMiddleware;
    use UsesTools;
    use EmitsEvents;
}
