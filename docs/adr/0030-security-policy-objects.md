# ADR 0030: Security Policy Objects

## Status

Accepted

## Context

Agent Harness currently scatters security-related configuration across constructor parameters:

```python
# Current: mixed in with other options
BuiltinShellDriver(
    cwd="/",
    env={},
    allowed_commands={"echo", "cat"},  # security
    max_output=16_000,                 # security
    max_iterations=10_000,             # security
)
```

OpenShell formalizes security into a composable policy object with filesystem allow-lists, network rules, process isolation, and inference routing. This pattern is more maintainable as the number of security constraints grows.

### Additional patterns from OpenShell

1. **Credential isolation**: Keys never touch the sandbox filesystem; routed through `inference.local`
2. **Streaming hooks**: Real-time stdout/stderr chunks during execution
3. **Driver capability query**: Different drivers support different features

## Decision

### ShellSecurityPolicy

Introduce a first-class security policy dataclass/interface in all three languages:

```python
@dataclass
class ShellSecurityPolicy:
    allowed_commands: set[str] | None = None
    writable_paths: list[str] | None = None
    max_output: int = 16_000
    max_iterations: int = 10_000
    read_only: bool = False
```

Accepted by `ShellDriver` constructors and `AgentBuilder.shell()`. Existing constructor params continue to work (backwards compatible).

### Streaming execution hooks

Add `HookEvent.SHELL_STDOUT_CHUNK` and `HookEvent.SHELL_STDERR_CHUNK` for drivers that support streaming. The existing `SHELL_RESULT` continues to fire after completion. Drivers advertise streaming support via `capabilities()`.

### Driver capability query

Add `capabilities() -> set[str]` to `ShellDriver`:

| Driver | Capabilities |
|--------|-------------|
| builtin | `custom_commands`, `stateful` |
| bashkit (Python) | `custom_commands`, `stateful`, `remote` |
| bashkit (CLI) | `custom_commands`, `remote` |
| openshell | `custom_commands`, `remote`, `policies`, `streaming` |

Known capability strings: `custom_commands`, `stateful`, `streaming`, `policies`, `remote`.

## Consequences

- Security configuration is composable and inspectable
- Streaming hooks enable real-time output monitoring
- Capability queries let consumers adapt behavior to driver features
- Backwards compatible — existing code continues to work unchanged
