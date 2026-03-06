# Plan: Extend Virtual Shell with Core Bash Syntax

## Context

The virtual shell currently supports pipes, redirects, `;` chaining, `$VAR` expansion, and 23 built-in commands across three languages (TS, Python, PHP). It lacks flow control (`if`, `for`, `while`), logical operators (`&&`, `||`), variable assignment, command substitution, test expressions, string parameter expansion, and `printf` — all commonly used by LLMs when exploring data via shell commands.

## Parser Approach: Hand-rolled Recursive Descent (no new dependencies)

**Why not an external parser?**
- `bash-parser` (npm): abandoned (2018), 200 weekly downloads
- `bashlex` (PyPI): low-activity, Python-only
- `tree-sitter-bash`: full C/WASM library — massively overweight
- No single library exists for TS, Python, and PHP — different parsers risk divergent behavior
- Our grammar is ~15 productions. A parser generator is overkill.

**Strategy:** Replace the ad-hoc splitting helpers (`_splitOn`/`_splitArgs`/`_inQuotes` in TS; `shlex.split()` in Python; `shellSplit`/`splitOn` in PHP) with a proper tokenizer + recursive-descent parser that produces a lightweight AST, then walk that AST to execute.

## AST Node Types

```
CommandNode      -- simple command: name + args + redirects
PipelineNode     -- cmd1 | cmd2 | cmd3
ListNode         -- separated by ;
AndNode          -- left && right
OrNode           -- left || right
IfNode           -- if/then/elif/else/fi
ForNode          -- for VAR in WORDS; do BODY; done
WhileNode        -- while CONDITION; do BODY; done
AssignmentNode   -- VAR=value
CaseNode         -- case WORD in pattern) body;; esac
```

Command substitution `$(...)` is handled during token expansion, not as an AST node.

## Grammar Precedence (recursive descent)

```
list        :=  and_or (';' and_or)*
and_or      :=  pipeline ('&&' pipeline | '||' pipeline)*
pipeline    :=  command ('|' command)*
command     :=  simple_command | if_clause | for_clause | while_clause | assignment
simple_cmd  :=  WORD args* redirects*
```

## Implementation Phases

All phases are implemented in **TypeScript first**, then ported to **Python**, then **PHP**.

### Phase 1: Tokenizer + parser refactor
Replace ad-hoc splitting with a single tokenizer producing typed tokens: `WORD`, `PIPE`, `SEMI`, `DSEMI` (`;;`), `AND` (`&&`), `OR` (`||`), `REDIRECT_OUT` (`>`), `REDIRECT_APPEND` (`>>`), `LPAREN`, `RPAREN`, plus reserved keywords (`if`, `then`, `elif`, `else`, `fi`, `for`, `in`, `do`, `done`, `while`, `case`, `esac`).

Add recursive-descent parser producing the AST. The evaluator walks the AST (replacing current `exec` → `_execSingle` flow).

Must handle: single/double quotes, `$VAR`/`${VAR}` (deferred to evaluation), `$(...)` nesting (paren counting in tokenizer).

### Phase 2: `&&` / `||` operators + `$?`
- Parse `AndNode` / `OrNode` (bind looser than `|`, tighter than `;`)
- Store last exit code as `$?` in `this.env` after every command
- Highest-value feature for agent workflows

### Phase 3: Variable assignment
- Detect `VAR=value` pattern (no spaces around `=`) → `AssignmentNode`
- Store in `this.env`
- `export` as no-op (single scope)

### Phase 4: `test` / `[` builtin + `[[ ]]`
New `test` builtin:
- File tests: `-f` (exists, is file), `-d` (exists, is dir), `-e` (exists)
- String tests: `-z` (empty), `-n` (non-empty), `=`, `!=`
- Numeric: `-eq`, `-ne`, `-lt`, `-gt`, `-le`, `-ge`
- Logic: `!` (negation)
- `[` is alias for `test` that expects `]` as final arg
- `[[ ]]` same semantics, parsed as command node

### Phase 5: `if/then/elif/else/fi`
Keyword-driven parsing: `parseIf()` calls `parseList()` for condition and body.

### Phase 6: `for/do/done` and `while/do/done`
- Both enforce `maxIterations` via shared counter
- Glob expansion in `for` word list via VirtualFS.find

### Phase 7: Command substitution `$(...)`
- During token expansion, find matching `)` (respecting nesting)
- Recursively call `exec()` on inner string
- Replace with stdout (trailing newline stripped)
- Recursion depth limit: 10 levels
- Backtick form as lower-priority alias

### Phase 8: Bash parameter expansion
- `${var:-default}` — default if unset/empty
- `${var:=default}` — assign default if unset/empty
- `${#var}` — string length
- `${var:offset:length}` — substring
- `${var//pattern/replacement}` — pattern substitution (also `${var/pat/repl}` for first only)
- `${var%%suffix}` / `${var%suffix}` — suffix removal (greedy/short)
- `${var##prefix}` / `${var#prefix}` — prefix removal (greedy/short)

### Phase 9: `printf` builtin
- `printf FORMAT [ARGS...]`
- Support `%s`, `%d`, `%f`, `%%`
- Support `\n`, `\t`, `\\` escape sequences

### Phase 10: Arithmetic `$((...))` and `case/esac`
- `$((...))` arithmetic expansion detected in tokenizer before `$()` command substitution
- Full recursive-descent arithmetic parser with proper precedence: ternary `? :`, logical `|| &&`, bitwise `| ^ &`, equality `== !=`, relational `< > <= >=`, shift `<< >>`, add/sub `+ -`, mul/div/mod `* / %`, unary `- + ! ~`, parens
- Variable expansion within expressions (`$VAR` and bare identifiers resolved from env)
- Division by zero protection (throws error)
- `case WORD in pattern) body;; esac` with `DSEMI` (`;;`) token type
- Pipe-separated patterns (`a | b)`)
- Glob pattern matching via existing `_globToRegex` / `_glob_to_regex` / `globToRegex`
- Wildcard `*` as default catch-all

## Language-Specific Notes

### TypeScript (`src/typescript/shell.ts`, ~890 lines)
- Currently has custom `_splitArgs()`, `_splitOn()`, `_inQuotes()` — all replaced by new tokenizer
- AST types as discriminated union (`type: "command" | "pipeline" | ...`)
- Tests via vitest (`tests/typescript/shell.test.ts`)

### Python (`src/python/shell.py`, ~717 lines)
- Currently uses `shlex.split()` for tokenization — replaced (shlex can't handle `&&`, `||`, keywords)
- Keep `shlex` import only if useful as a fallback
- AST types as dataclasses with a `type` field or `@dataclass` per node
- Tests via pytest (`tests/python/test_shell.py`)

### PHP (`src/php/Shell.php`, ~1047 lines)
- Has `shellSplit()` and `splitOn()` — replaced by new tokenizer
- AST as associative arrays with `'type'` key, or simple value objects
- Tests via phpunit (`tests/php/ShellTest.php` — verify path)

## Safety

| Concern | Mitigation |
|---------|-----------|
| Infinite loops | Shared iteration counter across `for`/`while`; checked against `maxIterations` (10,000) |
| `$()` recursion | Depth limit of 10 levels |
| Memory via variables | Cap variable values at 64KB |
| Code injection | No `eval`/`source` builtins; `$()` runs through same `exec()` with same `allowedCommands` |
| Expansion bombs | Cap total expansions per command (1,000) — tracked via `_expansionCount` / `_expansion_count` / `$expansionCount`, reset per `exec()` call |
| Stack overflow | Nesting depth limit of 50 |

## Files to Modify

**TypeScript (first):**
- `src/typescript/shell.ts` — tokenizer, parser, AST types, evaluator, new builtins (test, printf)
- `tests/typescript/shell.test.ts` — extend with tests per phase
- `src/typescript/has-shell.ts` — update tool description

**Python (second):**
- `src/python/shell.py` — mirror TS implementation
- `tests/python/test_shell.py` — mirror tests
- `src/python/has_shell.py` — update tool description

**PHP (third):**
- `src/php/Shell.php` — mirror implementation
- `tests/php/ShellTest.php` — mirror tests
- `src/php/HasShell.php` — update tool description

Keep each language in single files unless they exceed ~1500 lines, then split tokenizer/parser out.

## Verification

Per phase, add tests covering:
```bash
# Phase 2: && / || / $?
echo ok && echo yes         # -> "ok\nyes\n"
cat /nope && echo yes       # -> no "yes", exitCode != 0
cat /nope || echo fallback  # -> stdout contains "fallback"
echo hi; echo $?            # -> "hi\n0\n"

# Phase 3: Variables
X=hello; echo $X            # -> "hello\n"
X="hello world"; echo $X    # -> "hello world\n"

# Phase 4: test / [
test -f /exists.txt         # -> exitCode 0
[ "a" = "b" ]               # -> exitCode 1
[ 1 -lt 2 ]                 # -> exitCode 0

# Phase 5: if
if [ -f /file ]; then echo yes; else echo no; fi
if [ 1 -eq 2 ]; then echo a; elif [ 1 -eq 1 ]; then echo b; fi  # -> "b\n"

# Phase 6: for / while
for x in a b c; do echo $x; done    # -> "a\nb\nc\n"
while true; do echo x; done         # -> error (maxIterations)

# Phase 7: Command substitution
echo $(echo hello)                   # -> "hello\n"
X=$(cat /file.txt); echo $X         # -> file contents

# Phase 8: Parameter expansion
X=hello; echo ${#X}                  # -> "5\n"
echo ${UNSET:-default}               # -> "default\n"
X=hello_world; echo ${X//_/-}        # -> "hello-world\n"

# Phase 9: printf
printf '%s has %d items\n' foo 3     # -> "foo has 3 items\n"

# Phase 10: Arithmetic
echo $((2 + 3))                      # -> "5\n"
echo $((10 * 3 - 5))                 # -> "25\n"
echo $(((2 + 3) * 4))               # -> "20\n"
X=10; echo $(($X + 5))              # -> "15\n"
echo $((3 > 2))                      # -> "1\n"
echo $((1 ? 10 : 20))               # -> "10\n"

# Phase 10: case/esac
case hello in hello) echo matched;; esac              # -> "matched\n"
case b in a) echo A;; b) echo B;; *) echo other;; esac  # -> "B\n"
case yes in y | yes) echo affirmative;; esac            # -> "affirmative\n"
case file.txt in *.txt) echo text;; *.py) echo py;; esac # -> "text\n"
```

Run `vitest` (TS), `pytest` (Python), `phpunit` (PHP) after each phase.

Consider a shared test spec (YAML) for cross-language parity — each test runner loads the same spec and verifies identical output.
