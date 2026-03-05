# 8. Python Lazy Init Pattern

Date: 2026-03-05

## Status

Accepted

## Context

Python's multiple inheritance and Method Resolution Order (MRO) make cooperative
`__init__` with `super()` fragile when mixing independent traits. Each mixin
needs its own state (e.g., `_hooks`, `_middleware`, `_tools`), but requiring
every mixin to call `super().__init__()` correctly creates tight coupling and
makes composition order sensitive.

When a new mixin is added or removed, all `__init__` chains must be re-verified.
Forgetting a single `super().__init__()` call silently breaks downstream mixins.

## Decision

Mixins use lazy initialization via `hasattr` checks instead of cooperative
`__init__`. Each mixin provides an `__init_has_X__()` method that sets up its
private state. Public methods guard access with:

```python
if not hasattr(self, "_hooks"):
    self.__init_has_hooks__()
```

This check runs before any state access, ensuring initialization happens exactly
once regardless of how or when the mixin is composed.

## Consequences

**Positive**

- Mixins work in any combination without `__init__` coordination.
- No MRO issues -- each mixin is fully self-contained.
- Adding or removing a mixin from a class requires no changes to other mixins.

**Negative**

- Slight overhead from `hasattr` checks on every public method call (negligible
  in practice).
- The pattern is unusual and may surprise Python developers who expect all
  initialization to happen in `__init__`.
