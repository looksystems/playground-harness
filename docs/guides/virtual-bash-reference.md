# Virtual Bash Reference

Complete reference for the syntax, commands, and features supported by the virtual shell interpreter. All three language implementations (TypeScript, Python, PHP) share identical behavior.

---

## Built-in Commands

### File & Directory

| Command | Flags | Description |
|---------|-------|-------------|
| `cat` | — | Concatenate files. Reads stdin if no arguments |
| `echo` | — | Print arguments separated by spaces, with trailing newline |
| `pwd` | — | Print current working directory |
| `cd` | — | Change directory. Defaults to `/` if no argument |
| `ls` | `-l` | List directory contents. `-l` for long format (permissions, size) |
| `find` | `-name PATTERN`, `-type [f\|d]` | Recursive directory search with glob patterns |
| `mkdir` | — | Create directory |
| `touch` | — | Create empty file if it doesn't exist |
| `cp` | — | Copy a single file |
| `rm` | — | Remove files. Silently ignores missing files |
| `stat` | — | Print file metadata as JSON |
| `tree` | — | Display directory tree with ASCII art |

### Text Processing

| Command | Flags | Description |
|---------|-------|-------------|
| `grep` | `-i`, `-c`, `-n`, `-v`, `-r`, `-rn`, `-l` | Pattern matching (JavaScript/Python regex). Reads files or stdin |
| `head` | `-n NUM`, `-NUM` | First N lines (default 10) |
| `tail` | `-n NUM`, `-NUM` | Last N lines (default 10) |
| `wc` | `-l`, `-w`, `-c` | Count lines, words, or characters |
| `sort` | `-r`, `-n`, `-u` | Sort lines. `-n` numeric, `-r` reverse, `-u` unique |
| `uniq` | `-c` | Remove consecutive duplicates. `-c` counts occurrences |
| `cut` | `-d DELIM`, `-f FIELDS` | Extract delimited fields. Supports ranges (`1-3,5`) |
| `tr` | `-d` | Translate or delete characters |
| `sed` | `-e EXPR` | Stream editor. Substitution only: `s/pattern/replacement/[g]` |
| `tee` | `-a` | Write stdin to file(s) and stdout. `-a` appends |

### JSON

| Command | Flags | Description |
|---------|-------|-------------|
| `jq` | `-r` | Basic JSON queries: `.field`, `[N]`, `[]`. `-r` for raw string output |

### Output & Variables

| Command | Flags | Description |
|---------|-------|-------------|
| `printf` | — | Formatted output. Supports `%s`, `%d`, `%f`, `%%`, `\n`, `\t` |
| `export` | — | Set environment variables: `export NAME=VALUE` |

### Control

| Command | Flags | Description |
|---------|-------|-------------|
| `test` | — | Evaluate test expression, return exit code |
| `[` | — | Alias for `test`, requires closing `]` |
| `[[` | — | Alias for `test`, requires closing `]]` |
| `true` | — | Always exits 0 |
| `false` | — | Always exits 1 |

### Custom Commands

Register domain-specific commands at runtime:

```typescript
shell.registerCommand("mycommand", (args, stdin) => ({
  stdout: "result\n",
  stderr: "",
  exitCode: 0,
}));
```

Custom commands compose with pipes, redirects, and control flow like built-ins.

---

## Operators

### Pipes

```bash
echo hello | cat              # stdout flows left-to-right
cat file | grep ERROR | wc -l # multi-stage
false | echo hello             # all stages run (no short-circuit)
```

Stderr from all pipeline stages is accumulated and returned in the final result. The exit code is the last stage's exit code.

### Logical Operators

```bash
cmd1 && cmd2     # run cmd2 only if cmd1 succeeds (exit 0)
cmd1 || cmd2     # run cmd2 only if cmd1 fails (non-zero)
```

Stdout and stderr accumulate across both sides.

### Sequencing

```bash
cmd1 ; cmd2      # run both, regardless of exit codes
cmd1
cmd2             # newlines also separate commands
```

---

## Redirects

### Output

```bash
echo hello > /file.txt       # write stdout to file (truncate)
echo hello >> /file.txt      # append stdout to file
```

### Input

```bash
cat < /file.txt              # read file into stdin
cat < /in.txt > /out.txt     # combine input and output redirect
```

### Stderr

```bash
cmd 2> /err.txt              # write stderr to file
cmd 2>> /err.txt             # append stderr to file
cmd 2>&1                     # merge stderr into stdout
cmd 2>&1 | grep error        # merged stream flows through pipe
cmd &> /all.txt              # write both stdout+stderr to file
cmd > /out.txt 2> /err.txt   # separate files per stream
```

`>&` is an alias for `&>`.

### Redirect Ordering

Redirects are applied sequentially to the result. The common patterns above all work correctly. For edge cases where bash's fd-tracking order matters (e.g., `> file 2>&1` vs `2>&1 > file`), use `&> file` instead.

---

## Variable Expansion

### Basic

```bash
$VAR                    # value, or empty if unset
${VAR}                  # same, explicit braces
$?                      # exit code of last command
```

### Defaults and Assignment

```bash
${VAR:-default}         # use "default" if VAR is empty/unset
${VAR:=default}         # assign "default" to VAR if empty/unset, then return it
```

### String Operations

```bash
${#VAR}                 # string length
${VAR:offset:length}    # substring (negative offset counts from end)
${VAR/pattern/repl}     # replace first match
${VAR//pattern/repl}    # replace all matches
${VAR#prefix}           # remove shortest matching prefix (glob)
${VAR##prefix}          # remove longest matching prefix (glob)
${VAR%suffix}           # remove shortest matching suffix (glob)
${VAR%%suffix}          # remove longest matching suffix (glob)
```

### Assignment

```bash
NAME=value              # set variable (no spaces around =)
export NAME=value       # same effect (single scope, no distinction)
```

Variable values are capped at 100,000 characters.

---

## Command Substitution

```bash
result=$(echo hello)       # modern syntax, nestable
result=`echo hello`        # backtick syntax
echo "count: $(wc -l < /file.txt)"  # inline in strings
```

Trailing newline is stripped from the captured output. Nesting depth is limited to 10 levels.

---

## Arithmetic Expansion

```bash
echo $((2 + 3))           # → 5
echo $((x * 2))           # variables expanded automatically
echo $((10 > 5 ? 1 : 0))  # ternary operator
```

### Supported Operators

| Category | Operators |
|----------|-----------|
| Arithmetic | `+`, `-`, `*`, `/`, `%` |
| Comparison | `==`, `!=`, `<`, `>`, `<=`, `>=` |
| Logical | `&&`, `\|\|`, `!` |
| Bitwise | `&`, `\|`, `^`, `~`, `<<`, `>>` |
| Ternary | `? :` |
| Grouping | `( )` |

Integer arithmetic only. Undefined variables evaluate to 0.

---

## Quoting

```bash
echo 'no $expansion here'         # single quotes: literal
echo "hello $NAME"                 # double quotes: expansion happens
echo "escaped \$dollar"           # backslash escapes in double quotes
echo hello\ world                  # backslash outside quotes: escape next char
```

Inside double quotes, `\"`, `\$`, `\\`, and `` \` `` are recognized escape sequences.

---

## Control Flow

### if / elif / else

```bash
if [ "$x" = "yes" ]; then
  echo matched
elif [ "$x" = "no" ]; then
  echo negated
else
  echo unknown
fi
```

### for

```bash
for f in a b c; do
  echo "$f"
done
```

Words are expanded and split on whitespace before iteration.

### while

```bash
i=0
while [ "$i" -lt 5 ]; do
  echo "$i"
  i=$((i + 1))
done
```

A shared iteration counter limits total `for`/`while` iterations (default 10,000).

### case

```bash
case "$input" in
  y | yes) echo affirmative;;
  n | no)  echo negative;;
  *)       echo unknown;;
esac
```

Patterns use glob matching (`*`, `?`). Multiple patterns per clause are separated by `|`.

---

## Test Expressions

Used with `test`, `[`, or `[[`:

### File Tests

| Flag | Test |
|------|------|
| `-e` | path exists |
| `-f` | is a regular file |
| `-d` | is a directory |

### String Tests

| Operator | Test |
|----------|------|
| `-z STRING` | string is empty |
| `-n STRING` | string is non-empty |
| `S1 = S2` | strings are equal |
| `S1 != S2` | strings are not equal |
| `STRING` | true if non-empty (single argument) |

### Numeric Comparisons

| Operator | Test |
|----------|------|
| `N1 -eq N2` | equal |
| `N1 -ne N2` | not equal |
| `N1 -lt N2` | less than |
| `N1 -gt N2` | greater than |
| `N1 -le N2` | less than or equal |
| `N1 -ge N2` | greater than or equal |

### Negation

```bash
[ ! -f /file.txt ]     # true if file does NOT exist
```

---

## Glob Patterns

Glob patterns are supported in `case` clauses, `find -name`, and parameter expansion prefix/suffix removal:

| Pattern | Matches |
|---------|---------|
| `*` | any sequence of characters |
| `?` | any single character |

---

## Comments

```bash
# this is a comment
echo hello  # inline comment
```

Everything from `#` to end of line is ignored.

---

## Safety Limits

| Limit | Default | Purpose |
|-------|---------|---------|
| Loop iterations | 10,000 | Shared counter across all `for`/`while` loops per `exec()` |
| Substitution depth | 10 | Max nesting of `$(...)` |
| Variable size | 100,000 chars | Per-variable cap |
| Expansion count | 1,000 | Max expansions per `exec()` call |
| Parser nesting | 50 | Max AST depth |
| Output truncation | configurable | Large stdout is truncated with a pointer to the full output |

---

## Notable Omissions

These are bash features that the virtual shell intentionally does not support:

### Syntax

- **Functions** — `function name() { ... }` and `name() { ... }`
- **Arrays** — `arr=(a b c)`, `${arr[0]}`, `${arr[@]}`
- **Brace expansion** — `{a,b,c}`, `{1..10}`
- **Process substitution** — `<(cmd)`, `>(cmd)`
- **Here documents** — `<<EOF ... EOF`, `<<<string`
- **Subshells** — `(cmd1; cmd2)` grouping
- **Coprocesses** — `coproc`
- **`break` / `continue`** in loops
- **`until`** loops
- **`select`** menus

### Operators

- **`|&`** — pipe stderr (use `2>&1 |` instead)
- **`<>`** — read-write redirect
- **Regex matching** — `[[ $var =~ regex ]]`
- **Logical operators in test** — `[ expr1 -a expr2 ]`, `[ expr1 -o expr2 ]`

### Expansion

- **`${VAR^}`** / **`${VAR,}`** — case modification
- **`${!prefix*}`** — indirect expansion
- **`${PARAMETER@OPERATOR}`** — parameter transformation
- **`$@`**, **`$*`**, **`$#`**, **`$0`** — positional parameters
- **Tilde expansion** — `~`, `~user`
- **Glob character classes** — `[a-z]`, `[!0-9]`

### Commands

Many standard Unix utilities are absent (e.g., `awk`, `xargs`, `diff`, `date`, `basename`, `dirname`, `readlink`, `seq`, `yes`, `env`, `read`). The 30 built-in commands cover the most common patterns for context exploration. Additional commands can be registered at runtime via `registerCommand()`.

### Other

- **Signal handling** — `trap`
- **Job control** — `&`, `fg`, `bg`, `jobs`
- **Aliases** — `alias`, `unalias`
- **History** — `history`, `!!`, `!$`
- **Source** — `.`, `source`
- **Eval** — `eval`
- **Arithmetic assignment** — `((i++))`, `let`
