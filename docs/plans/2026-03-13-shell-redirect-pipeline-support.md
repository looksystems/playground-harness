# Plan: Complete Shell Redirect & Pipeline Support

## Context

The virtual shell emulator (tokenizer + parser + AST evaluator over VirtualFS) is missing 4 features that real bash supports:
1. **stderr redirection** (`2>`, `2>>`, `2>&1`, `&>`)
2. **stdin redirection** (`<`)
3. **stderr passthrough in pipelines** (intermediate stages' stderr is silently dropped)
4. **Pipeline continuation on failure** (currently short-circuits; bash runs all stages)

All changes must be made in all 3 language implementations (TS, Python, PHP) which are functionally identical.

## Approach

- **TDD**: For each step, write failing tests first (testing only the public `Shell.exec()` API), then implement until tests pass.
- **Lightweight & minimal**: This is a virtual shell emulator, not a full bash replacement. Keep changes small and focused. No over-engineering.

## Files to Modify

| Language | Source | Tests |
|----------|--------|-------|
| TS | `src/typescript/shell.ts` | `tests/typescript/shell.test.ts` |
| Python | `src/python/shell.py` | `tests/python/test_shell.py` |
| PHP | `src/php/Shell.php` | `tests/php/ShellTest.php` |

## Implementation Order

Features 4 → 3 → 2 → 1 (simplest first; later features depend on earlier ones being correct)

---

## Step 1: Pipeline continues on failure (Feature 4)

Remove the early-break on non-zero exit in `_evalPipeline`.

**TS** `shell.ts:861` — delete `if (lastResult.exitCode !== 0) break;`
**Python** `shell.py:797-798` — delete `if last_result.exit_code != 0: break`
**PHP** `Shell.php:906-908` — delete `if ($lastResult->exitCode !== 0) { break; }`

Exit code becomes the last command's exit code (already the case since `lastResult` is overwritten each iteration).

---

## Step 2: stderr passthrough in pipelines (Feature 3)

Accumulate stderr from all pipeline stages. Combined with Step 1, `_evalPipeline` becomes:

```
_evalPipeline(node, stdin):
    currentStdin = stdin
    lastResult = empty
    allStderr = ""
    for cmd in node.commands:
        lastResult = eval(cmd, currentStdin)
        allStderr += lastResult.stderr
        currentStdin = lastResult.stdout
    env["?"] = lastResult.exitCode
    return Result(lastResult.stdout, allStderr, lastResult.exitCode)
```

---

## Step 3: stdin redirection `<` (Feature 2)

### 3a. Token type
Add `REDIRECT_IN` to token types in all 3 languages.

### 3b. Tokenizer
Add `<` handling after the `>` handlers (TS after line 113, before `(`). Currently `<` is a meta-character (line 230) that breaks words but isn't consumed — this would cause it to be silently skipped. The new handler consumes it:

```
if (c === "<") {
    tokens.push({ type: "REDIRECT_IN", value: "<" });
    i++; continue;
}
```

### 3c. Redirect data structure
Extend the existing redirect type to support `mode: "read"` (alongside `"write"` and `"append"`).

### 3d. Parser (`parseSimpleCommand`)
Add `REDIRECT_IN` handling alongside existing `REDIRECT_OUT`/`REDIRECT_APPEND`:
```
if (t.type === "REDIRECT_IN") {
    advance(); target = expect("WORD");
    redirects.push({ mode: "read", target: target.value });
}
```

### 3e. Evaluator (`_evalCommand`)
Process input redirects BEFORE calling the handler. Add before the handler call:
```
for redir in node.redirects:
    if redir.mode == "read":
        path = resolve(expandWord(redir.target))
        if !fs.exists(path): return error "No such file or directory"
        stdin = fs.read(path)
```
In the post-handler output-redirect loop, skip `"read"` redirects with `continue`.

---

## Step 4: stderr redirection (Feature 1)

### 4a. Token types
Add 4 new token types:
- `REDIRECT_ERR_OUT` — `2>`
- `REDIRECT_ERR_APPEND` — `2>>`
- `REDIRECT_ERR_DUP` — `2>&1`
- `REDIRECT_BOTH_OUT` — `&>` and `>&`

### 4b. Tokenizer
Insert new operator checks in the tokenizer. Order matters for correct matching.

**After `&&` check** (TS line 103): Add `&>` check:
```
if (c === "&" && input[i+1] === ">") {
    tokens.push({ type: "REDIRECT_BOTH_OUT", value: "&>" });
    i += 2; continue;
}
```

**After `>>` check** (TS line 108): Add `>&` check:
```
if (c === ">" && input[i+1] === "&") {
    tokens.push({ type: "REDIRECT_BOTH_OUT", value: ">&" });
    i += 2; continue;
}
```

**After `#` comment handler, before word-builder** (TS line 129): Add `2>` family checks. These must go here because `2` is a digit that would otherwise enter the word loop. Use word-boundary detection (previous char is whitespace or operator):

```
if (c === "2" && i+1 < len && input[i+1] === ">") {
    const prev = i > 0 ? input[i-1] : " ";
    if (" \t\n|;&()".includes(prev)) {
        if (input[i+2] === ">" && input[i+3] !== undefined) {  // 2>>
            tokens.push({ type: "REDIRECT_ERR_APPEND", value: "2>>" });
            i += 3; continue;
        }
        if (input[i+2] === "&" && input[i+3] === "1") {  // 2>&1
            tokens.push({ type: "REDIRECT_ERR_DUP", value: "2>&1" });
            i += 4; continue;
        }
        // plain 2>
        tokens.push({ type: "REDIRECT_ERR_OUT", value: "2>" });
        i += 2; continue;
    }
}
```

### 4c. Redirect data structure
Add optional fields to the existing redirect object (all languages):

```
{
  mode: "write" | "append" | "read",
  target: string,
  fd?: 1 | 2,       // which fd to redirect (default 1)
  dupTarget?: 1,     // for 2>&1: dup stderr to stdout
  both?: boolean     // for &>: redirect both streams
}
```

Python: add `fd: int = 1`, `dup_target: int = 0`, `both: bool = False` to `Redirect` dataclass.
PHP: add optional array keys `'fd'`, `'dup_target'`, `'both'`.

### 4d. Parser (`parseSimpleCommand`)
Handle new tokens:
- `REDIRECT_ERR_OUT` → `{ mode: "write", target, fd: 2 }`
- `REDIRECT_ERR_APPEND` → `{ mode: "append", target, fd: 2 }`
- `REDIRECT_ERR_DUP` → `{ mode: "write", target: "", dupTarget: 1 }` (no WORD consumed)
- `REDIRECT_BOTH_OUT` → `{ mode: "write", target, both: true }`

### 4e. Evaluator (`_evalCommand`)
Rewrite the post-handler redirect loop:

```
for redir in redirects:
    if redir.mode == "read": continue  // handled pre-handler

    if redir.dupTarget == 1:
        // 2>&1: merge stderr into stdout
        result = Result(stdout + stderr, "", exitCode)
        continue

    if redir.both:
        // &>: write both streams to file
        content = result.stdout + result.stderr
        writeToFile(path, content, redir.mode)
        result = Result("", "", exitCode)
        continue

    // Normal fd-targeted redirect
    fd = redir.fd ?? 1
    stream = (fd == 2) ? result.stderr : result.stdout
    writeToFile(path, stream, redir.mode)
    // Clear the redirected stream
    if fd == 2: result = Result(stdout, "", exitCode)
    else:       result = Result("", stderr, exitCode)
```

### 4f. `2>&1` + pipes interaction
This works naturally: `cmd 2>&1 | grep err` — the `2>&1` redirect is applied inside `_evalCommand` before returning to `_evalPipeline`, so the merged stdout flows through the pipe. No special handling needed.

### 4g. Simplification: redirect ordering
Real bash tracks fd destinations for ordering (`> file 2>&1` vs `2>&1 > file` differ). For v1, use simplified model: redirects are applied sequentially to the result object. The common pattern `cmd 2>&1 | ...` works correctly. For `> file 2>&1`, users should write `&> file` instead.

---

## Verification

### Tests to add (all 3 languages)

**Steps 1+2 (pipeline)**:
- `false | echo hello` → stdout `hello\n`, exit code 0
- Register custom command that writes stderr; `errcmd | cat` → stderr preserved in result
- `echo a | false | echo b` → stdout `b\n`, exit code 0

**Step 3 (stdin redirect)**:
- `cat < /file.txt` → reads file content
- `sort < /data.txt` → sorts file content
- `cat < /nonexistent` → stderr error, exit code 1
- `cat < /in.txt > /out.txt` → combined input+output redirect

**Step 4 (stderr redirect)**:
- `errcmd 2> /err.txt` → stderr in file, stdout in result
- `errcmd 2>> /err.txt` → stderr appended
- `errcmd 2>&1` → stderr merged into stdout
- `errcmd 2>&1 | grep pattern` → merged stream piped
- `errcmd &> /all.txt` → both streams in file
- `cmd > /out.txt 2> /err.txt` → separate files per stream

### Run existing tests
```
cd tests/typescript && npx vitest run shell.test.ts
cd tests/python && pytest test_shell.py
cd tests/php && phpunit ShellTest.php
```
Ensure all existing pipe and redirect tests still pass (the pipeline behavior change may require updating tests that relied on short-circuit behavior).
