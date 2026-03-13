# 19. Recursive-Descent Parser for Virtual Shell

Date: 2026-03-06

## Status

Accepted

## Context

The virtual shell originally used ad-hoc string splitting (`_splitArgs`, `_splitOn`, `shlex.split()`, `shellSplit`) to parse commands. This approach could not support flow control (`if`, `for`, `while`, `case`), logical operators (`&&`, `||`), variable assignment, command substitution, arithmetic expansion, or parameter expansion — all commonly used by LLMs when exploring data via shell commands.

We evaluated external parsers (`bash-parser`, `bashlex`, `tree-sitter-bash`) but found them abandoned, Python-only, or massively overweight. No single library exists across TS, Python, and PHP. Our grammar is ~15 productions — a parser generator is overkill.

## Decision

Replace the ad-hoc splitting helpers with a hand-rolled tokenizer and recursive-descent parser producing a lightweight AST across all three languages.

### Tokenizer

Produces typed tokens: `WORD`, `PIPE`, `SEMI`, `DSEMI` (`;;`), `AND` (`&&`), `OR` (`||`), `REDIRECT_OUT` (`>`), `REDIRECT_APPEND` (`>>`), `REDIRECT_IN` (`<`), `REDIRECT_ERR_OUT` (`2>`), `REDIRECT_ERR_APPEND` (`2>>`), `REDIRECT_ERR_DUP` (`2>&1`), `REDIRECT_BOTH_OUT` (`&>`, `>&`), `LPAREN`, `RPAREN`, `NEWLINE`, `EOF`. Handles single/double quotes, `$VAR`/`${VAR}`, `$(...)` nesting (paren counting), and backtick command substitution.

### Grammar (recursive descent)

```
list        :=  and_or (';' and_or)*
and_or      :=  pipeline ('&&' pipeline | '||' pipeline)*
pipeline    :=  command ('|' command)*
command     :=  if_clause | for_clause | while_clause | case_clause | simple_cmd | assignment
simple_cmd  :=  WORD args* redirect*
redirect    :=  '>' WORD | '>>' WORD | '<' WORD
              | '2>' WORD | '2>>' WORD | '2>&1'
              | '&>' WORD | '>&' WORD
```

### AST Node Types

- `CommandNode` — simple command with args and redirects
- `PipelineNode` — cmd1 | cmd2 | cmd3
- `ListNode` — separated by `;`
- `AndNode` / `OrNode` — `&&` / `||`
- `IfNode` — if/then/elif/else/fi
- `ForNode` — for VAR in WORDS; do BODY; done
- `WhileNode` — while CONDITION; do BODY; done
- `CaseNode` — case WORD in pattern) body;; esac
- `AssignmentNode` — VAR=value

### Language-Specific Representations

- **TypeScript**: Discriminated union (`type: "command" | "pipeline" | ...`)
- **Python**: Dataclasses with a `type` field
- **PHP**: Associative arrays with a `'type'` key

### Safety Limits

| Concern | Mitigation |
|---------|-----------|
| Infinite loops | Shared iteration counter across `for`/`while`; checked against `maxIterations` (10,000) |
| `$()` recursion | Depth limit of 10 levels |
| Memory via variables | Cap variable values at 64KB |
| Expansion bombs | Cap total expansions per `exec()` at 1,000 |
| Stack overflow | Parser nesting depth limit of 50 |

## Consequences

- The shell now supports the full range of bash syntax that LLMs commonly produce
- All three languages share identical grammar, AST structure, and safety limits — behavior is consistent
- No external dependencies added; the parser is ~300 lines per language
- The `DSEMI` (`;;`) token was required for `case/esac` to prevent `parseList` from consuming the first `;` of `;;` as a statement separator
- `export` is a no-op (single scope) — assignments go directly into `env`
- `[[ ]]` delegates to the same `_evalTest` logic as `[` — no distinction between the two
- Arithmetic expressions support the full C-like operator precedence including ternary `? :`
