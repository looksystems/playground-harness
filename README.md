# Agent Harness

> [!WARNING]
> This repository is for exploration and planning purposes only. It is not intended for production use. APIs, structure, and documentation may change without notice. Written entirely using Claude Code in a few hours, just to try out "what if".

A lightweight, composable framework for building LLM-powered agents. Implemented across Python, TypeScript, and PHP with trait/mixin-based composition.

Harness has two distinctive features. 

The **virtual shell** replaces per-query tools with an in-memory filesystem and shell interpreter — you mount context as files and let the model explore with Unix commands (`grep`, `cat`, `find`, `jq`) it already knows. Register custom commands (`deploy`, `validate`, etc.) that compose naturally with pipes and control flow. Every command is pure emulation with no security risk. The shell backend is swappable via a driver architecture — the builtin pure-language shell works out of the box, or switch to [bashkit](https://github.com/everruns/bashkit) for full POSIX compliance and 100+ builtins with a single `driver("bashkit")` call.

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

### Integration Guides

- [Bashkit](docs/guides/bashkit.md) — POSIX shell driver setup, usage, and custom commands

### Architecture Decision Records

Design decisions are documented in [docs/adr/](docs/adr/README.md).

## Notes

Streamed events currently assumes last field is streamed - this needs to be reviewed and api adjusted accordingly.

Virtual fs/shell could be extracted into separate packages - in particular, the virtual fs/shell could be a high performance core, say in rust with extensibility in the client languages. The bashkit IPC driver (Phase 2) is a step in this direction; Phase 3 will add native in-process FFI drivers via PyO3/napi-rs/ext-php-rs.

Need to think further about the message bus and whether there should be any convensions re. emtting events vs commands (and tools). Message bus will be a separate package.

Need to add support for LLM gateways including rate limits, retries, observability, etc cf middleware.

Need to add support for [open responses](https://www.openresponses.org) ie. create common classes or use standard libraries.

Consider implementing something like [mcpporter](https://github.com/steipete/mcporter) for mcp support.
