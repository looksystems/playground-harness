# Agent Harness

> **Warning:** This repository is for exploration and planning purposes only. It is not intended for production use. APIs, structure, and documentation may change without notice.

A lightweight, composable framework for building LLM-powered agents. Implemented across Python, TypeScript, and PHP with trait/mixin-based composition.

Most agent frameworks give you a monolithic base class where every feature is always on. Harness takes the opposite approach: a thin agent loop (`BaseAgent`) with capabilities added through independent mixins — hooks, middleware, tools, events, and a virtual shell. You compose only what you need. The virtual shell is particularly distinctive: instead of building a tool for every query pattern, you mount context as files in an in-memory filesystem and let the model explore with standard Unix commands (`grep`, `cat`, `find`, `jq`) it already knows. Every command is pure emulation — no real shell, no real filesystem, no security risk.

## Documentation

- [Overview](docs/overview.md) — Key capabilities and architecture diagram
- [Architecture](docs/architecture.md) — Component internals and composition model
- [Design Principles](docs/principles.md) — Guiding decisions behind the framework
- [Comparison](docs/comparison.md) — How Harness relates to other agent frameworks

### Language Guides

- [Python](docs/guides/python.md)
- [TypeScript](docs/guides/typescript.md)
- [PHP](docs/guides/php.md)

### Architecture Decision Records

Design decisions are documented in [docs/adr/](docs/adr/README.md).
