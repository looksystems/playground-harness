# Agent Harness

> **Warning:** This repository is for exploration and planning purposes only. It is not intended for production use. APIs, structure, and documentation may change without notice. Written entirely using Claude Code in a a few hours, just to try out "what if".

A lightweight, composable framework for building LLM-powered agents. Implemented across Python, TypeScript, and PHP with trait/mixin-based composition.

Harness has two distinctive features. 

The **virtual shell** replaces per-query tools with an in-memory filesystem and shell interpreter — you mount context as files and let the model explore with Unix commands (`grep`, `cat`, `find`, `jq`) it already knows. Register custom commands (`deploy`, `validate`, etc.) that compose naturally with pipes and control flow. Every command is pure emulation with no security risk.

The **inline event system** uses a convention-based YAML format where the last field in an event block is automatically streamable — no special syntax or schema annotations needed. Events are buffered by default; streaming is opt-in per field, and the parser handles framing, async iteration, and backpressure transparently.

Along with typical agentic features:

 * tools
 * skills
 * hooks
 * middleware

For now, I've excluded mcp support, as this could/should be implemented with a combinatio of a custom skill and cli command.

The ultimate aim is for this work to be integrated into an event driven/sourced workflow package, which would then allow orchestration.

## Documentation

- [Overview](docs/overview.md) — Key capabilities and architecture diagram
- [Architecture](docs/architecture.md) — Component internals and composition model
- [Design Principles](docs/principles.md) — Guiding decisions behind the framework
- [Comparison](docs/comparison.md) — How each language implementation compares

### Language Guides

- [Python](docs/guides/python.md)
- [TypeScript](docs/guides/typescript.md)
- [PHP](docs/guides/php.md)

### Architecture Decision Records

Design decisions are documented in [docs/adr/](docs/adr/README.md).

## TODOs

Streamed events currently assumes last field is streamed - this needs to be reviewed and api adjusted accordingly.

Virtual fs/shell could be extracted into separate packages - in particular, the virtual fs/shell could be a high performance core, say in rust with extensibility in the client languages.

Need to think further about the message bus and whether there should be any convensions re. emtting events vs commands (and tools). Message bus will be a separate package.

Need to add support for LLM gateways and rate limited, retries, observability, etc cf middleware.

Need to add support for [open responses](https://www.openresponses.org) ie. create common classes or use standard libraries.
