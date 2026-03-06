# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the Agent Harness framework. ADRs document significant design decisions, their context, and consequences.

## Index

### Core Architecture

| ADR | Decision |
|-----|----------|
| [0001](0001-mixin-trait-composition.md) | Mixin/Trait-Based Composition |
| [0007](0007-language-idiomatic-implementations.md) | Language-Idiomatic Implementations |

### Mixins

| ADR | Decision |
|-----|----------|
| [0015](0015-lifecycle-hooks.md) | Lifecycle Hooks (HasHooks) |
| [0018](0018-middleware-pipeline.md) | Middleware Pipeline (HasMiddleware) |
| [0016](0016-tool-registration.md) | Tool Registration and Schema Generation (UsesTools) |
| [0012](0012-virtual-shell-architecture.md) | Virtual Shell Architecture (HasShell) |
| [0013](0013-shell-registry-singleton.md) | Shell Registry as Global Singleton |
| [0014](0014-pure-emulation-security-model.md) | Pure Emulation Security Model |
| [0019](0019-shell-recursive-descent-parser.md) | Recursive-Descent Parser for Shell Bash Syntax |
| [0020](0020-shell-expansion-safety-limits.md) | Shell Expansion Safety Limits |
| [0021](0021-custom-command-registration.md) | Custom Command Registration for Virtual Shell |
| [0022](0022-shell-and-registration-hooks.md) | Shell, Registration, and CWD Hooks |
| [0023](0023-has-commands-mixin.md) | ~~HasCommands Mixin~~ (Superseded by 0024) |
| [0024](0024-has-skills-mixin.md) | HasSkills Mixin |

### Events and Streaming

| ADR | Decision |
|-----|----------|
| [0002](0002-inline-yaml-events.md) | Inline YAML Events |
| [0003](0003-buffered-by-default-streaming.md) | Buffered-by-Default Streaming |
| [0004](0004-last-field-convention.md) | Last-Field Convention for Streaming |
| [0005](0005-standalone-message-bus.md) | Standalone Message Bus |
| [0006](0006-per-run-event-control.md) | Per-Run Event Control |

### Language-Specific

| ADR | Decision |
|-----|----------|
| [0008](0008-python-lazy-init-pattern.md) | Python Lazy Init Pattern |
| [0009](0009-typescript-function-based-mixins.md) | TypeScript Function-Based Mixins |
| [0010](0010-php-generator-streaming.md) | PHP Generator Streaming |
| [0011](0011-custom-yaml-parser-php.md) | Custom YAML Parser for PHP |

