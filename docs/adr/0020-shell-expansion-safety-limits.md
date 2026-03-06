# 20. Shell Expansion Safety Limits

Date: 2026-03-06

## Status

Accepted

## Context

With the addition of variable expansion, command substitution, parameter expansion, and arithmetic expansion to the virtual shell, the expansion system became a potential vector for denial-of-service through expansion bombs — patterns like `${A}${A}${A}...` repeated thousands of times, or deeply nested `$($($(... )))` substitutions.

The original shell had only `maxIterations` (for loop bounds) and `_cmdSubDepth` (for recursion depth). These were insufficient to guard against expansion-based resource exhaustion.

## Decision

Add a per-`exec()` expansion counter (`_expansionCount` / `_expansion_count` / `$expansionCount`) that increments on every `$` expansion (variable, command substitution, parameter expansion, arithmetic). When the counter exceeds `MAX_EXPANSIONS` (1,000), the shell throws an error.

The counter is:
- Reset to 0 at the start of each `exec()` call
- Incremented in `_expandDollar` / `expandDollar` before processing any `$` form
- Shared across all expansion types (`$VAR`, `${...}`, `$(...)`, `$((...))`  )
- Not reset during command substitution (nested `exec()` calls get their own counter via the outer reset, but the parent's counter is preserved)

### Why 1,000?

- A typical complex shell one-liner uses 10–50 expansions
- A `for` loop with 100 iterations expanding a variable each time uses ~200
- 1,000 provides generous headroom while stopping pathological inputs
- The limit is a constant, not configurable — intentionally not exposed to users to keep the safety boundary simple

### Complete Safety Matrix

| Limit | Value | Scope | Purpose |
|-------|-------|-------|---------|
| `maxIterations` | 10,000 | Per `exec()` | Bounds `for`/`while` loop iterations |
| `_cmdSubDepth` | 10 | Recursive | Bounds `$(...)` nesting depth |
| `MAX_VAR_SIZE` | 64KB | Per assignment | Prevents single variable from consuming unbounded memory |
| `MAX_EXPANSIONS` | 1,000 | Per `exec()` | Prevents expansion bombs |
| Parser depth | 50 | Per parse | Prevents deeply nested AST from stack overflow |

## Consequences

- Expansion bombs are now bounded — any command with more than 1,000 `$` expansions fails with a clear error
- The limit is generous enough that no legitimate shell usage should hit it
- All three languages enforce identical limits
- The counter is lightweight (single integer increment + comparison) with negligible performance impact
