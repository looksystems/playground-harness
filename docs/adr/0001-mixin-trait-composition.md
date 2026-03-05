# 1. Mixin/Trait-Based Composition

**Status:** Accepted

## Context

The original agent was a monolithic class with all capabilities in one file.
This made it hard to test individual concerns, use only what you need, or
understand responsibilities. As the number of capabilities grew (hooks,
middleware, tools, events), the single class became increasingly difficult to
maintain and reason about.

## Decision

Use trait/mixin-based composition. Each capability (hooks, middleware, tools,
events) is an independent mixin that encapsulates a single concern. BaseAgent
provides only the core loop. StandardAgent composes all mixins together for
the common case.

Consumers who need a subset of functionality can compose their own agent class
from only the mixins they require, rather than inheriting everything.

## Consequences

**Positive:**

- Clear separation of concerns -- each mixin owns one responsibility.
- Each mixin is testable in isolation without standing up a full agent.
- Compose only what you need -- no unused capabilities in lightweight agents.
- New capabilities can be added as new mixins without modifying existing code.

**Negative:**

- Slightly more complex mental model compared to a single class.
- Developers need to understand mixin/trait patterns for their language.
- Mixin interaction order can matter (e.g., middleware wrapping hooks), which
  requires documentation.
