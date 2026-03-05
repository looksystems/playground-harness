# 11. Custom YAML Parser for PHP

Date: 2026-03-05

## Status

Accepted

## Context

PHP needs to parse simple YAML event bodies that consist of key-value pairs with
at most one level of nesting. Three options were considered:

- **pecl yaml extension** -- full YAML support but requires a C extension that
  is not installed by default and complicates deployment.
- **symfony/yaml** -- well-maintained library with full YAML spec support, but
  adds a Composer dependency for a narrow use case.
- **Custom parser** -- a small, purpose-built parser that handles exactly the
  subset of YAML the event stream produces.

The event format uses only top-level `key: value` pairs and occasionally one
level of indented `sub_key: value` under a parent key. No advanced YAML features
(anchors, multi-line strings, sequences) are used.

## Decision

Use a custom `parseSimpleYaml()` method in EventStreamParser. The parser handles:

- Top-level `key: value` pairs.
- One level of nesting: a bare `key:` followed by indented `sub_key: value`.
- Type casting for booleans (`true`/`false`), null, integers, and floats.

## Consequences

**Positive**

- Zero additional dependencies for YAML parsing.
- Handles exactly the format produced by the event stream -- no unused
  complexity.
- No extension installation required; works in any standard PHP environment.

**Negative**

- Does not handle the full YAML specification (by design).
- If the event format grows to include sequences, multi-line strings, or deeper
  nesting, the parser must be extended or replaced.
