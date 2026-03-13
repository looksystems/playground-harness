# Why Virtual Bash

This guide explains why the harness uses a single `exec` tool backed by a virtual shell and filesystem, rather than a catalog of typed function calls.

---

## The convergence

Unix made a design decision 50 years ago: everything is a text stream. Small tools each do one thing well, composed via `|` into powerful workflows. Programs describe themselves with `--help`, report success with exit codes, and communicate errors through stderr.

LLMs made an almost identical decision 50 years later: everything is tokens. They only understand text, only produce text. Their thinking is text, their actions are text, and the feedback they receive from the world must be text.

These two decisions converge on the same interface model. The text-based system Unix designed for human terminal operators -- `cat`, `grep`, pipe, exit codes -- isn't just usable by LLMs. It's a natural fit. An LLM is essentially a terminal operator that has already seen vast amounts of shell commands in its training data.

## One tool beats many

Most agent frameworks give LLMs a catalog of independent tools:

```
tools: [search_web, read_file, write_file, run_code, send_email, ...]
```

Before each call, the LLM must select a tool and fill in parameters. More tools means harder selection and lower accuracy. Cognitive load is spent on "which tool?" instead of "what do I need to accomplish?"

The harness exposes a single `exec` tool. All capabilities are CLI commands:

```
exec("cat notes.md")
exec("cat log.txt | grep ERROR | wc -l")
exec("find /data -name '*.json' -exec grep -l 'user_id' {} \\;")
```

This is fundamentally different from choosing among 15 tools with different schemas. Command selection is string composition within a unified namespace -- function selection is context-switching between unrelated APIs.

### Composition via pipes

A single `exec` isn't enough if it can only run one command at a time. The virtual shell supports four Unix operators:

| Operator | Meaning |
|----------|---------|
| `\|` | Pipe: stdout of previous becomes stdin of next |
| `&&` | And: execute next only if previous succeeded |
| `\|\|` | Or: execute next only if previous failed |
| `;` | Seq: execute next regardless |
| `>`, `>>` | Redirect stdout to file (truncate / append) |
| `<` | Redirect file into stdin |
| `2>`, `2>>` | Redirect stderr to file |
| `2>&1` | Merge stderr into stdout |
| `&>` | Redirect both streams to file |

One tool call can be a complete workflow:

```bash
# Read, filter, sort, take top 10
cat access.log | grep "500" | sort | head 10

# Try A, fall back to B
cat config.yaml || echo "config not found, using defaults"
```

### Fewer round-trips

Consider reading a log file and counting errors:

**Function-calling approach (3 tool calls):**
1. `read_file(path="/var/log/app.log")` -- returns entire file
2. `search_text(text=<entire file>, pattern="ERROR")` -- returns matches
3. `count_lines(text=<matched lines>)` -- returns number

**Virtual bash approach (1 tool call):**
```
exec("cat /var/log/app.log | grep ERROR | wc -l")
→ "42"
```

One call replaces three. Not because of special optimization -- because Unix pipes natively support composition.

Anthropic arrived at the same conclusion with [programmatic tool calling](https://www.anthropic.com/engineering/advanced-tool-use): letting the model orchestrate tools through code instead of sequential API calls reduced token usage by 37% and eliminated the inference overhead of 19+ round-trips. The virtual shell takes this further -- composition isn't bolted on via a code sandbox, it's the native interface.

## Filesystem as context

Instead of building a tool for every query pattern, mount context as files and let the model explore. This idea -- demonstrated by [Vercel's d0 agent](https://vercel.com/blog/how-to-build-agents-with-filesystems-and-bash) -- improved their task success rates from 80% to 100%.

The approach works because domain structures naturally map to directory hierarchies. Agents read metadata first, then target specific sections using `grep`, `cat`, and `find`. Context loads on-demand rather than being stuffed upfront into the prompt.

```python
agent.fs.write("/schema/database.yaml", schema_content)
agent.fs.write("/tickets/T-1234.md", ticket_content)

# The agent explores with commands it already knows:
# exec("grep -r 'user_id' /schema")
# exec("cat /tickets/T-1234.md | head 20")
```

Giving the agent a map is far more effective than giving it the entire territory.

## CLI as navigation system

The agent can't Google. It can't ask a colleague. Three techniques make the CLI itself serve as the agent's navigation system.

### Progressive `--help` discovery

Commands self-document through progressive disclosure:

- **Level 0** -- tool description lists all commands with one-line summaries
- **Level 1** -- calling a command with no args returns its usage
- **Level 2** -- calling a subcommand with missing args returns its parameters

The agent discovers on-demand. Each level provides just enough for the next step. This is fundamentally different from stuffing 3,000 words of tool documentation into the system prompt.

### Error messages as navigation

Every error contains both "what went wrong" and "what to do instead":

```
[error] cat: binary image file (182KB). Use: see photo.png
[error] unknown command: foo. Available: cat, ls, grep, ...
[error] clip "sandbox" not found. Use 'clip list' to see available clips
```

The agent's recovery cost is minimal -- usually 1-2 steps to the right path.

### Consistent output format

Every result includes metadata:

```
file1.txt
file2.txt
[exit:0 | 12ms]
```

Exit codes (which LLMs already understand from training data) and duration give the agent signals for success/failure and cost awareness. After seeing `[exit:N | Xs]` dozens of times, the agent internalizes the pattern.

## Two-layer architecture

Raw command output can't go directly to the LLM. Context windows are finite and expensive, and LLMs can't process binary data. But processing output mid-pipeline breaks pipes. Hence, two layers:

```
┌──────────────────────────────────────────┐
│  Layer 2: LLM Presentation Layer         │  ← LLM constraints
│  Binary guard | Truncation | Metadata    │
├──────────────────────────────────────────┤
│  Layer 1: Unix Execution Layer           │  ← Pure Unix semantics
│  Command routing | Pipe | Chain | Exit   │
└──────────────────────────────────────────┘
```

When `cat bigfile.txt | grep error | head 10` executes, Layer 1 passes raw data between pipe stages. If you truncated `cat`'s output inside the pipeline, `grep` would only search a subset. Processing only happens in Layer 2, after the pipe chain completes.

Layer 2 provides:

- **Binary guard** -- detects binary content, returns guidance instead of garbage
- **Overflow mode** -- truncates large output, saves full result to a file the agent can explore with `grep`/`tail`
- **Metadata footer** -- exit code + duration
- **stderr attachment** -- always visible on failure

## Why virtual, not real

The harness implements all of this as pure emulation. No real shell or filesystem is ever accessed:

- **No process spawning** -- no subprocess calls
- **No real filesystem** -- all reads/writes go to an in-memory dictionary
- **No network access** -- no HTTP calls or sockets
- **Bounded loops** -- shared iteration counter prevents runaway execution
- **Output truncation** -- configurable max output prevents context flooding

This provides the full power of shell-based exploration without any OS-level security risk. Every command is a function in the host language operating on in-memory data structures.

When the built-in shell isn't enough, the driver system lets you swap in a real POSIX shell (via bashkit) with a single line of configuration -- keeping the same API while gaining full shell fidelity.

## When not to use this

CLI isn't a silver bullet. Typed APIs may be better for:

- **Strongly-typed interactions** -- database queries, GraphQL, and cases requiring schema validation
- **High-security contexts** -- string concatenation carries injection risks; typed parameters are safer with untrusted input
- **Native multimodal** -- pure audio/video processing where text pipes are a bottleneck

## Further reading

- [Virtual Bash Reference](virtual-bash-reference.md) -- complete syntax, command, and feature reference
- [Advanced Tool Use](https://www.anthropic.com/engineering/advanced-tool-use) -- Anthropic on programmatic tool calling: orchestrating tools through code rather than sequential API calls, reducing token usage by 37% and eliminating round-trip overhead
- [Context Engineering for AI Agents](https://manus.im/blog/Context-Engineering-for-AI-Agents-Lessons-from-Building-Manus) -- Manus team on filesystem as externalized memory and context engineering
- [How to Build Agents with Filesystems and Bash](https://vercel.com/blog/how-to-build-agents-with-filesystems-and-bash) -- Vercel's d0 agent demonstrating the approach
- [ADR-0012: Virtual Shell Architecture](/docs/adr/0012-virtual-shell-architecture.md) -- design decision record
- [ADR-0014: Pure Emulation Security Model](/docs/adr/0014-pure-emulation-security-model.md) -- security guarantees
