# 9. TypeScript Function-Based Mixins

Date: 2026-03-05

## Status

Accepted

## Context

TypeScript does not have native traits or multiple inheritance. Several
alternatives exist for composing behavior into classes:

- **Interface + manual implementation** -- verbose, no code reuse.
- **Class decorators** -- limited type inference, experimental API surface.
- **Function-based mixins** -- a function takes a base class and returns an
  anonymous subclass that adds new behavior.

The framework needs to compose multiple independent capabilities (hooks,
middleware, tools, event emission) onto a single agent class.

## Decision

Use function-based mixins. Each capability is expressed as a generic function:

```typescript
function HasHooks<TBase extends Constructor>(Base: TBase) {
  return class extends Base { /* hooks implementation */ };
}
```

StandardAgent is composed by nesting these functions:

```typescript
const StandardAgent = HasCommands(HasShell(EmitsEvents(UsesTools(HasMiddleware(HasHooks(BaseAgent))))));
```

## Consequences

**Positive**

- Fully type-safe -- TypeScript infers the combined type through each layer.
- Composes naturally with the type system; no decorators or experimental
  features required.
- Each mixin is independently testable by applying it to a minimal base class.

**Negative**

- Composition order matters: the outermost mixin's methods take precedence in
  case of name collisions.
- Deeply nested generic types can produce complex type signatures in editor
  tooltips and error messages.
