# Agent Harness

> **Warning:** This repository is for exploration and planning purposes only. It is not intended for production use. APIs, structure, and documentation may change without notice. Written entirely using Claude Code in a a few hours, just to try out "what if".

A lightweight, composable framework for building LLM-powered agents. Implemented across Python, TypeScript, and PHP with trait/mixin-based composition.

Harness has two distinctive features. 

The **virtual shell** replaces per-query tools with an in-memory filesystem and shell interpreter — you mount context as files and let the model explore with Unix commands (`grep`, `cat`, `find`, `jq`) it already knows. Register custom commands (`deploy`, `validate`, etc.) that compose naturally with pipes and control flow. Every command is pure emulation with no security risk.

The **inline event system** uses a convention-based YAML format where the last field in an event block is automatically streamable — no special syntax or schema annotations needed. Events are buffered by default; streaming is opt-in per field, and the parser handles framing, async iteration, and backpressure transparently.

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
