# 5. Standalone Message Bus

**Status:** Accepted

## Context

Parsed events need to be routed to handlers. Several options were considered:

- Callbacks registered directly on the parser.
- An integrated event system built into the agent class.
- A standalone pub/sub message bus.

The routing mechanism needed to be reusable outside the agent, support
multiple handlers per event, and allow handlers to publish new events without
causing infinite loops.

## Decision

Use a standalone MessageBus with topic-based subscription, wildcard (`*`)
support for cross-cutting concerns, and cycle detection via a depth counter.
The bus is independent of the agent -- it can be used standalone, shared
across components, or replaced entirely.

## Consequences

**Positive:**

- Reusable in contexts outside the agent (e.g., testing, tooling, pipelines).
- Testable independently without standing up an agent or parser.
- Handlers can publish new events, enabling event chains and reactions.
- Wildcard subscriptions support cross-cutting concerns like logging and
  metrics without per-event wiring.

**Negative:**

- Slightly more wiring needed to connect parser to bus to handlers, compared
  to direct callbacks.
- Depth-based cycle detection requires a sensible default limit and clear
  error messages when the limit is hit.
- Indirection can make it harder to trace event flow during debugging.
