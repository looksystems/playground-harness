# 28. Shell Redirect and Pipeline Completeness

Date: 2026-03-13

## Status

Accepted

## Context

The virtual shell supported basic output redirects (`>`, `>>`) and pipes (`|`), but lacked several features that real bash provides and that LLMs commonly produce:

1. **Pipelines short-circuited on failure** — `false | echo hello` would stop at `false` and never run `echo`. Real bash runs all pipeline stages regardless of intermediate exit codes.
2. **Pipeline stderr was silently dropped** — intermediate stages' stderr was lost; only the last stage's stderr appeared in the result.
3. **No stdin redirection** — `cat < /file.txt` was not supported. The `<` character was a meta-character that broke tokenization but was never consumed as a token.
4. **No stderr redirection** — `2>`, `2>>`, `2>&1`, and `&>` were all unsupported. Agents could not separate or merge output streams.

These gaps meant agents writing natural bash patterns (e.g., `cmd 2>&1 | grep error`, `cmd > /out 2> /err`) would get parse errors or silent misbehavior.

## Decision

Add complete redirect and pipeline support across all three language implementations (TS, Python, PHP).

### Pipeline behavior

- Remove the early-break on non-zero exit code in `_evalPipeline`. All stages now execute regardless of intermediate failures, matching bash semantics. The pipeline's exit code is the last stage's exit code.
- Accumulate stderr from all pipeline stages into a combined `allStderr` string. The result's stderr contains stderr from every stage, not just the last one.

### Stdin redirection (`<`)

- Add a `REDIRECT_IN` token type consumed by the tokenizer when `<` is encountered.
- The parser produces a redirect with `mode: "read"`.
- The evaluator processes input redirects *before* calling the command handler, reading the file content into the effective stdin. If the file does not exist, the command returns exit code 1 with an appropriate error message.

### Stderr redirection (`2>`, `2>>`, `2>&1`, `&>`)

- Add four new token types: `REDIRECT_ERR_OUT`, `REDIRECT_ERR_APPEND`, `REDIRECT_ERR_DUP`, `REDIRECT_BOTH_OUT`.
- Tokenizer placement is order-sensitive:
  - `&>` is checked after `&&` (to avoid consuming the first `&` of `&&`).
  - `>&` is checked after `>>` (to avoid consuming `>` as a plain redirect).
  - `2>` family is checked after the `#` comment handler and before the word-builder, with a word-boundary check on the previous character (so `file2>foo` treats `2` as part of the word, not as fd 2).
- The redirect data structure gains optional fields: `fd` (1 or 2), `dupTarget` (for `2>&1`), and `both` (for `&>`).
- The evaluator's post-handler redirect loop processes these in declaration order:
  - `dupTarget: 1` merges stderr into stdout.
  - `both: true` writes both streams to the target file and clears both.
  - `fd: 2` writes stderr to the target file and clears stderr.
  - Default (`fd: 1`) preserves existing behavior.

### Redirect ordering simplification

Real bash tracks per-fd destinations so `> file 2>&1` and `2>&1 > file` differ. This implementation applies redirects sequentially to the result object — a simpler model that correctly handles the common patterns (`cmd 2>&1 | grep`, `cmd > out 2> err`, `cmd &> all`). For the rare case where ordering matters, users should use `&> file` instead of `> file 2>&1`.

## Consequences

- Agents can now write natural bash redirect patterns without silent failures
- `cmd 2>&1 | grep error` works correctly — `2>&1` merges streams inside `_evalCommand` before the pipeline receives the result
- Pipeline stages no longer silently swallow stderr from intermediate commands
- The tokenizer now has 17 token types (up from 12), but the grammar remains a simple recursive descent
- All three language implementations remain functionally identical
- The redirect ordering simplification is documented; a future v2 could add full fd-tracking if needed
