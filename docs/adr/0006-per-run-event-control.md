# 6. Per-Run Event Control

**Status:** Accepted

## Context

Different agent runs may need different events. A coding agent might emit
progress events during generation but suppress them during review. A single
static event configuration does not accommodate this variation without
creating separate agent instances.

## Decision

Agents have a `default_events` list that configures which registered events
are active by default. The `run()` method accepts an optional `events`
parameter to override the active set for that specific run. Ad-hoc EventType
objects can be passed directly in the `events` parameter without prior
registration, enabling one-off or experimental events.

## Consequences

**Positive:**

- Flexible per-run control without reconfiguring or recreating the agent.
- Sensible defaults via `default_events` mean most callers do not need to
  specify anything.
- Ad-hoc events support rapid experimentation and one-off use cases without
  polluting the agent's registered event list.

**Negative:**

- Callers must know event names (or have EventType references) to override
  the default set.
- The interaction between `default_events` and per-run overrides needs clear
  documentation to avoid confusion about merge vs. replace semantics.
