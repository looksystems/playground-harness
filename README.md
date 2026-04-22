# Agent Harness

> [!WARNING]
> This repository is for exploration and planning purposes only. It is not intended for production use. APIs, structure, and documentation may change without notice. Written entirely using Claude Code in a few hours, just to try out "what if".

A lightweight, composable framework for building LLM-powered agents. Implemented across Python, TypeScript, PHP, and Go with trait/mixin/embedding-based composition.

Harness has two distinctive features. 

The **virtual shell** replaces per-query tools with an in-memory filesystem and shell interpreter — you mount context as files and let the model explore with Unix commands (`grep`, `cat`, `find`, `jq`) it already knows. Register custom commands (`deploy`, `validate`, etc.) that compose naturally with pipes and control flow. Every command is pure emulation with no security risk. The shell backend is swappable via a driver architecture — the builtin pure-language shell works out of the box, switch to [bashkit](https://github.com/everruns/bashkit) for full POSIX compliance, or use [OpenShell](https://github.com/NVIDIA/OpenShell) for secure sandboxed execution with policy enforcement.

The **inline event system** uses a convention-based YAML format where the last field in an event block is automatically streamable — no special syntax or schema annotations needed. Events are buffered by default; streaming is opt-in per field, and the parser handles framing, async iteration, and backpressure transparently.

Along with typical agentic features:

 * tools
 * skills
 * hooks
 * middleware
 * slash commands

For now, mcp support has been deferred, as integrations would be better implemented with a combination of a custom skill and cli command.

The ultimate aim is for this work to be integrated into an event driven/sourced workflow package, which would then allow durable, complex multi-agent and multi-step orchestration.

## Documentation

- [Overview](docs/overview.md) — Key capabilities and architecture diagram
- [Architecture](docs/architecture.md) — Component internals and composition model
- [Design Principles](docs/principles.md) — Guiding decisions behind the framework
- [Comparison](docs/comparison.md) — How each language implementation compares

### Language Guides

- [Python](docs/guides/python.md)
- [TypeScript](docs/guides/typescript.md)
- [PHP](docs/guides/php.md)
- [Go](docs/guides/go.md)

### Cross-Cutting Guides

- [Skills](docs/guides/skills.md) — Mountable capability bundles (tools + middleware + hooks + commands + instructions)
- [Hooks](docs/guides/hooks.md) — Lifecycle events, handler registration, concurrency, and panic isolation
- [Tools](docs/guides/tools.md) — LLM-callable functions with auto-generated JSON schemas, cross-language registration, and error handling

### Shell Guides

- [Virtual Bash](docs/guides/virtual-bash-reference.md) — Supported syntax, commands, and limitations
- [Bashkit](docs/guides/bashkit.md) — POSIX shell driver setup, usage, and custom commands
- [OpenShell](docs/guides/openshell.md) — Secure sandboxed execution with gRPC, policy enforcement, and streaming

### Architecture Decision Records

Design decisions are documented in [docs/adr/](docs/adr/README.md).

## Notes

Streamed events currently assumes last field is streamed - this needs to be reviewed and api adjusted accordingly.

Need to think further about the message bus and whether there should be any convensions re. emtting events vs commands (and tools). Message bus will be a separate package.

Virtual fs/shell could be extracted into separate packages - in particular, the virtual fs/shell could be a high performance core, say in rust with extensibility in the client languages. The bashkit integration uses the `bashkit` PyO3 package for Python (in-process, stateful, with custom command support) and the `bashkit` CLI for TypeScript/PHP (stateless subprocess).

Need to add support for LLM gateways including rate limits, retries, observability, etc cf middleware.

Need to add support for [open responses](https://www.openresponses.org) ie. create common classes or use standard libraries.

Consider implementing something like [mcpporter](https://github.com/steipete/mcporter) or [mcp2cli](https://github.com/knowsuchagency/mcp2cli) for mcp support.

Consider implementing a todo tool (cf. tick) and a memory tool.

Consider building an event driven workflow layer.

Consider implementing or integration something like [context mode](https://github.com/mksglu/context-mode) for better context management.
