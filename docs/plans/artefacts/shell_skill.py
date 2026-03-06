"""
shell_skill.py — A virtual filesystem + shell skill for agent_harness.py

Inspired by vercel-labs/just-bash: instead of building many specialized tools,
give the agent a single `exec` tool over an in-memory filesystem. The model
explores context with grep, cat, find, ls — commands it already understands.

The key insight: the filesystem IS the interface. Mount your schemas, docs,
configs, data as files. Let the model figure out how to explore them.

Usage:
    from shell_skill import ShellSkill

    skill = ShellSkill()
    skill.write("/data/users.json", '[{"name": "Alice"}, {"name": "Bob"}]')
    skill.write("/docs/schema.md", "# Schema\\n...")
    skill.mount_dir("/config", {"app.yaml": "...", "db.yaml": "..."})

    await agent.mount(skill)
    result = await agent.run("How many users are in the data file?")
    # The model runs: cat /data/users.json | grep name | wc -l
"""

from __future__ import annotations

import fnmatch
import io
import json
import os
import re
import shlex
from dataclasses import dataclass, field
from typing import Any, Callable

from agent_harness import tool
from skills import Skill, SkillContext

# ---------------------------------------------------------------------------
# Virtual filesystem
# ---------------------------------------------------------------------------

class VirtualFS:
    """Simple in-memory filesystem. Paths are always absolute and normalized."""

    def __init__(self, files: dict[str, str | bytes] | None = None):
        self._files: dict[str, str | bytes] = {}
        # Lazy file providers — called on first read, result cached
        self._lazy: dict[str, Callable[[], str | bytes]] = {}
        if files:
            for path, content in files.items():
                self.write(self._norm(path), content)

    @staticmethod
    def _norm(path: str) -> str:
        result = os.path.normpath("/" + path)
        # os.path.normpath preserves leading // on POSIX — collapse it
        if result.startswith("//"):
            result = result[1:]
        return result

    def write(self, path: str, content: str | bytes) -> None:
        path = self._norm(path)
        # Ensure parent dirs implicitly exist
        self._files[path] = content

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        """Register a lazy file — provider called on first read, then cached."""
        path = self._norm(path)
        self._lazy[path] = provider

    def read(self, path: str) -> str | bytes:
        path = self._norm(path)
        # Resolve lazy providers
        if path in self._lazy:
            self._files[path] = self._lazy.pop(path)()
        if path not in self._files:
            raise FileNotFoundError(f"{path}: No such file")
        return self._files[path]

    def read_text(self, path: str) -> str:
        content = self.read(path)
        return content if isinstance(content, str) else content.decode("utf-8", errors="replace")

    def exists(self, path: str) -> bool:
        path = self._norm(path)
        return path in self._files or path in self._lazy or self._is_dir(path)

    def remove(self, path: str) -> None:
        path = self._norm(path)
        if path in self._files:
            del self._files[path]
        elif path in self._lazy:
            del self._lazy[path]
        else:
            raise FileNotFoundError(f"{path}: No such file")

    def _all_paths(self) -> set[str]:
        return set(self._files.keys()) | set(self._lazy.keys())

    def _is_dir(self, path: str) -> bool:
        path = self._norm(path)
        prefix = path.rstrip("/") + "/"
        return any(p.startswith(prefix) for p in self._all_paths())

    def listdir(self, path: str = "/") -> list[str]:
        path = self._norm(path).rstrip("/") + "/"
        if path == "//":
            path = "/"
        entries: set[str] = set()
        for p in self._all_paths():
            if p.startswith(path) and p != path:
                rest = p[len(path):]
                entry = rest.split("/")[0]
                entries.add(entry)
        return sorted(entries)

    def find(self, root: str = "/", pattern: str = "*") -> list[str]:
        root = self._norm(root).rstrip("/")
        results = []
        for p in sorted(self._all_paths()):
            if not p.startswith(root):
                continue
            basename = p.rsplit("/", 1)[-1]
            if fnmatch.fnmatch(basename, pattern):
                results.append(p)
        return results

    def stat(self, path: str) -> dict[str, Any]:
        path = self._norm(path)
        if self._is_dir(path):
            return {"path": path, "type": "directory"}
        content = self.read(path)
        size = len(content.encode("utf-8") if isinstance(content, str) else content)
        return {"path": path, "type": "file", "size": size}


# ---------------------------------------------------------------------------
# Lightweight shell interpreter
# ---------------------------------------------------------------------------

@dataclass
class ExecResult:
    stdout: str = ""
    stderr: str = ""
    exit_code: int = 0


class Shell:
    """
    Minimal shell interpreter over a VirtualFS.
    Supports pipes, redirects, and core Unix commands.
    Not a full bash — just enough for context exploration.
    """

    MAX_OUTPUT = 16_000  # Truncate output to avoid blowing context

    def __init__(
        self,
        fs: VirtualFS,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
        max_iterations: int = 10_000,
    ):
        self.fs = fs
        self.cwd = cwd
        self.env = env or {}
        self.max_iterations = max_iterations

        self._builtins: dict[str, Callable] = {
            "cat": self._cmd_cat,
            "echo": self._cmd_echo,
            "find": self._cmd_find,
            "grep": self._cmd_grep,
            "head": self._cmd_head,
            "ls": self._cmd_ls,
            "pwd": self._cmd_pwd,
            "sort": self._cmd_sort,
            "tail": self._cmd_tail,
            "tee": self._cmd_tee,
            "touch": self._cmd_touch,
            "tree": self._cmd_tree,
            "uniq": self._cmd_uniq,
            "wc": self._cmd_wc,
            "mkdir": self._cmd_mkdir,
            "cp": self._cmd_cp,
            "rm": self._cmd_rm,
            "stat": self._cmd_stat,
            "cut": self._cmd_cut,
            "tr": self._cmd_tr,
            "sed": self._cmd_sed,
            "jq": self._cmd_jq,
            "cd": self._cmd_cd,
        }

        if allowed_commands is not None:
            self._builtins = {
                k: v for k, v in self._builtins.items() if k in allowed_commands
            }

    def _resolve(self, path: str) -> str:
        if path.startswith("/"):
            return os.path.normpath(path)
        return os.path.normpath(os.path.join(self.cwd, path))

    def exec(self, command: str) -> ExecResult:
        """Execute a command string. Supports pipes and basic redirects."""
        command = command.strip()
        if not command:
            return ExecResult()

        # Handle command chaining with ; (basic)
        if ";" in command and not self._in_quotes(command, command.index(";")):
            results = []
            for part in self._split_on(command, ";"):
                r = self.exec(part)
                results.append(r)
                if r.exit_code != 0:
                    break
            return ExecResult(
                stdout="".join(r.stdout for r in results),
                stderr="".join(r.stderr for r in results),
                exit_code=results[-1].exit_code if results else 0,
            )

        # Handle pipes
        segments = self._split_on(command, "|")
        stdin = ""

        last_result = ExecResult()
        for seg in segments:
            last_result = self._exec_single(seg.strip(), stdin)
            stdin = last_result.stdout
            if last_result.exit_code != 0:
                break

        # Truncate
        if len(last_result.stdout) > self.MAX_OUTPUT:
            last_result.stdout = (
                last_result.stdout[: self.MAX_OUTPUT]
                + f"\n... [truncated, {len(last_result.stdout)} total chars]"
            )

        return last_result

    def _exec_single(self, command: str, stdin: str = "") -> ExecResult:
        """Execute a single command (no pipes)."""
        # Handle output redirect
        append = False
        redirect_path = None

        # Very basic redirect parsing
        for op in [">>", ">"]:
            if op in command:
                idx = command.index(op)
                if not self._in_quotes(command, idx):
                    redirect_path = command[idx + len(op) :].strip().split()[0]
                    command = command[:idx].strip()
                    append = op == ">>"
                    break

        try:
            parts = shlex.split(command)
        except ValueError:
            parts = command.split()

        if not parts:
            return ExecResult()

        # Variable expansion
        parts = [self._expand_vars(p) for p in parts]

        cmd_name = parts[0]
        args = parts[1:]

        handler = self._builtins.get(cmd_name)
        if handler is None:
            return ExecResult(
                stderr=f"{cmd_name}: command not found\n", exit_code=127
            )

        result = handler(args, stdin)

        # Handle redirect
        if redirect_path:
            path = self._resolve(redirect_path)
            if append and self.fs.exists(path):
                existing = self.fs.read_text(path)
                self.fs.write(path, existing + result.stdout)
            else:
                self.fs.write(path, result.stdout)
            result = ExecResult(stderr=result.stderr, exit_code=result.exit_code)

        return result

    def _expand_vars(self, s: str) -> str:
        def replacer(m: re.Match) -> str:
            name = m.group(1) or m.group(2)
            return self.env.get(name, "")
        return re.sub(r"\$\{(\w+)\}|\$(\w+)", replacer, s)

    @staticmethod
    def _in_quotes(s: str, pos: int) -> bool:
        in_single = False
        in_double = False
        for i, c in enumerate(s[:pos]):
            if c == "'" and not in_double:
                in_single = not in_single
            elif c == '"' and not in_single:
                in_double = not in_double
        return in_single or in_double

    @staticmethod
    def _split_on(command: str, sep: str) -> list[str]:
        parts = []
        current: list[str] = []
        in_single = False
        in_double = False
        i = 0
        while i < len(command):
            c = command[i]
            if c == "'" and not in_double:
                in_single = not in_single
                current.append(c)
            elif c == '"' and not in_single:
                in_double = not in_double
                current.append(c)
            elif command[i:i+len(sep)] == sep and not in_single and not in_double:
                parts.append("".join(current))
                current = []
                i += len(sep)
                continue
            else:
                current.append(c)
            i += 1
        parts.append("".join(current))
        return parts

    # -- Built-in commands --------------------------------------------------

    def _cmd_cat(self, args: list[str], stdin: str) -> ExecResult:
        if not args:
            return ExecResult(stdout=stdin)
        out = []
        for path in args:
            try:
                out.append(self.fs.read_text(self._resolve(path)))
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult(stdout="".join(out))

    def _cmd_echo(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(stdout=" ".join(args) + "\n")

    def _cmd_pwd(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(stdout=self.cwd + "\n")

    def _cmd_cd(self, args: list[str], stdin: str) -> ExecResult:
        target = args[0] if args else "/"
        resolved = self._resolve(target)
        if not self.fs._is_dir(resolved) and not resolved == "/":
            return ExecResult(stderr=f"cd: {target}: No such directory\n", exit_code=1)
        self.cwd = resolved
        return ExecResult()

    def _cmd_ls(self, args: list[str], stdin: str) -> ExecResult:
        long_format = "-l" in args
        paths = [a for a in args if not a.startswith("-")]
        target = paths[0] if paths else self.cwd
        resolved = self._resolve(target)

        try:
            entries = self.fs.listdir(resolved)
        except FileNotFoundError:
            return ExecResult(stderr=f"ls: {target}: No such directory\n", exit_code=1)

        if long_format:
            lines = []
            for entry in entries:
                full = os.path.normpath(resolved + "/" + entry)
                s = self.fs.stat(full)
                if s["type"] == "directory":
                    lines.append(f"drwxr-xr-x  -  {entry}/")
                else:
                    lines.append(f"-rw-r--r--  {s['size']:>8}  {entry}")
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")
        return ExecResult(stdout="\n".join(entries) + "\n" if entries else "")

    def _cmd_find(self, args: list[str], stdin: str) -> ExecResult:
        root = "."
        pattern = "*"
        name_filter = None
        type_filter = None

        i = 0
        while i < len(args):
            if args[i] == "-name" and i + 1 < len(args):
                name_filter = args[i + 1]
                i += 2
            elif args[i] == "-type" and i + 1 < len(args):
                type_filter = args[i + 1]
                i += 2
            elif not args[i].startswith("-"):
                root = args[i]
                i += 1
            else:
                i += 1

        resolved = self._resolve(root)
        results = self.fs.find(resolved, name_filter or "*")

        if type_filter == "f":
            results = [r for r in results if not self.fs._is_dir(r)]
        elif type_filter == "d":
            results = [r for r in results if self.fs._is_dir(r)]

        return ExecResult(stdout="\n".join(results) + "\n" if results else "")

    def _cmd_grep(self, args: list[str], stdin: str) -> ExecResult:
        case_insensitive = "-i" in args
        count_only = "-c" in args
        line_numbers = "-n" in args
        invert = "-v" in args
        recursive = "-r" in args or "-rn" in args
        filenames = "-l" in args
        args = [a for a in args if not a.startswith("-")]

        if not args:
            return ExecResult(stderr="grep: missing pattern\n", exit_code=2)

        pattern = args[0]
        targets = args[1:]
        flags = re.IGNORECASE if case_insensitive else 0

        try:
            regex = re.compile(pattern, flags)
        except re.error as e:
            return ExecResult(stderr=f"grep: invalid pattern: {e}\n", exit_code=2)

        def grep_text(text: str, label: str = "") -> list[str]:
            matches = []
            for i, line in enumerate(text.splitlines(), 1):
                match = bool(regex.search(line))
                if invert:
                    match = not match
                if match:
                    prefix = f"{label}:" if label else ""
                    num = f"{i}:" if line_numbers else ""
                    matches.append(f"{prefix}{num}{line}")
            return matches

        all_matches: list[str] = []
        matched_files: list[str] = []

        if not targets and stdin:
            all_matches = grep_text(stdin)
        elif recursive and targets:
            for target in targets:
                resolved = self._resolve(target)
                for fpath in self.fs.find(resolved):
                    try:
                        text = self.fs.read_text(fpath)
                        m = grep_text(text, fpath)
                        if m:
                            matched_files.append(fpath)
                            all_matches.extend(m)
                    except (FileNotFoundError, UnicodeDecodeError):
                        pass
        else:
            for target in targets:
                try:
                    text = self.fs.read_text(self._resolve(target))
                    label = target if len(targets) > 1 else ""
                    m = grep_text(text, label)
                    if m:
                        matched_files.append(target)
                        all_matches.extend(m)
                except FileNotFoundError:
                    return ExecResult(stderr=f"grep: {target}: No such file\n", exit_code=2)

        if filenames:
            return ExecResult(
                stdout="\n".join(matched_files) + "\n" if matched_files else "",
                exit_code=0 if matched_files else 1,
            )

        if count_only:
            return ExecResult(stdout=f"{len(all_matches)}\n", exit_code=0 if all_matches else 1)

        return ExecResult(
            stdout="\n".join(all_matches) + "\n" if all_matches else "",
            exit_code=0 if all_matches else 1,
        )

    def _cmd_head(self, args: list[str], stdin: str) -> ExecResult:
        n = 10
        files: list[str] = []
        i = 0
        while i < len(args):
            if args[i] == "-n" and i + 1 < len(args):
                n = int(args[i + 1])
                i += 2
            elif args[i].startswith("-") and args[i][1:].isdigit():
                n = int(args[i][1:])
                i += 1
            else:
                files.append(args[i])
                i += 1

        if not files:
            lines = stdin.splitlines()[:n]
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

        text = self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()[:n]
        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_tail(self, args: list[str], stdin: str) -> ExecResult:
        n = 10
        files: list[str] = []
        i = 0
        while i < len(args):
            if args[i] == "-n" and i + 1 < len(args):
                n = int(args[i + 1])
                i += 2
            else:
                files.append(args[i])
                i += 1

        if not files:
            lines = stdin.splitlines()[-n:]
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

        text = self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()[-n:]
        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_wc(self, args: list[str], stdin: str) -> ExecResult:
        lines_only = "-l" in args
        words_only = "-w" in args
        chars_only = "-c" in args
        files = [a for a in args if not a.startswith("-")]

        if not files:
            text = stdin
        else:
            try:
                text = self.fs.read_text(self._resolve(files[0]))
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)

        lc = text.count("\n")
        wc = len(text.split())
        cc = len(text)

        if lines_only:
            return ExecResult(stdout=f"{lc}\n")
        if words_only:
            return ExecResult(stdout=f"{wc}\n")
        if chars_only:
            return ExecResult(stdout=f"{cc}\n")

        label = f" {files[0]}" if files else ""
        return ExecResult(stdout=f"  {lc}  {wc}  {cc}{label}\n")

    def _cmd_sort(self, args: list[str], stdin: str) -> ExecResult:
        reverse = "-r" in args
        numeric = "-n" in args
        unique = "-u" in args
        files = [a for a in args if not a.startswith("-")]

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()

        if numeric:
            def key(s: str):
                m = re.match(r"-?\d+\.?\d*", s)
                return float(m.group()) if m else 0.0
            lines.sort(key=key, reverse=reverse)
        else:
            lines.sort(reverse=reverse)

        if unique:
            lines = list(dict.fromkeys(lines))

        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_uniq(self, args: list[str], stdin: str) -> ExecResult:
        count = "-c" in args
        lines = stdin.splitlines()
        result: list[str] = []
        prev = None
        cnt = 0
        for line in lines:
            if line == prev:
                cnt += 1
            else:
                if prev is not None:
                    result.append(f"  {cnt} {prev}" if count else prev)
                prev = line
                cnt = 1
        if prev is not None:
            result.append(f"  {cnt} {prev}" if count else prev)
        return ExecResult(stdout="\n".join(result) + "\n" if result else "")

    def _cmd_cut(self, args: list[str], stdin: str) -> ExecResult:
        delimiter = "\t"
        fields: list[int] = []
        i = 0
        while i < len(args):
            if args[i] == "-d" and i + 1 < len(args):
                delimiter = args[i + 1]
                i += 2
            elif args[i] == "-f" and i + 1 < len(args):
                for part in args[i + 1].split(","):
                    if "-" in part:
                        start, end = part.split("-", 1)
                        fields.extend(range(int(start or 1), int(end or 100) + 1))
                    else:
                        fields.append(int(part))
                i += 2
            else:
                i += 1

        lines = stdin.splitlines()
        out = []
        for line in lines:
            parts = line.split(delimiter)
            selected = [parts[f - 1] for f in fields if 0 < f <= len(parts)]
            out.append(delimiter.join(selected))
        return ExecResult(stdout="\n".join(out) + "\n" if out else "")

    def _cmd_tr(self, args: list[str], stdin: str) -> ExecResult:
        delete = "-d" in args
        args = [a for a in args if not a.startswith("-")]

        if delete and args:
            chars = set(args[0])
            return ExecResult(stdout="".join(c for c in stdin if c not in chars))

        if len(args) >= 2:
            set1, set2 = args[0], args[1]
            table = str.maketrans(set1, set2[:len(set1)])
            return ExecResult(stdout=stdin.translate(table))

        return ExecResult(stdout=stdin)

    def _cmd_sed(self, args: list[str], stdin: str) -> ExecResult:
        """Minimal sed: supports s/pattern/replacement/[g] only."""
        files = []
        expr = None
        i = 0
        while i < len(args):
            if args[i] == "-e" and i + 1 < len(args):
                expr = args[i + 1]
                i += 2
            elif not args[i].startswith("-"):
                if expr is None:
                    expr = args[i]
                else:
                    files.append(args[i])
                i += 1
            else:
                i += 1

        if not expr:
            return ExecResult(stdout=stdin)

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))

        # Parse s/pat/repl/flags
        m = re.match(r"s(.)(.*?)\1(.*?)\1(\w*)", expr)
        if not m:
            return ExecResult(stdout=text)

        pat, repl, flags_str = m.group(2), m.group(3), m.group(4)
        count = 0 if "g" in flags_str else 1
        result = re.sub(pat, repl, text, count=count)
        return ExecResult(stdout=result)

    def _cmd_tee(self, args: list[str], stdin: str) -> ExecResult:
        append = "-a" in args
        files = [a for a in args if not a.startswith("-")]
        for f in files:
            path = self._resolve(f)
            if append and self.fs.exists(path):
                self.fs.write(path, self.fs.read_text(path) + stdin)
            else:
                self.fs.write(path, stdin)
        return ExecResult(stdout=stdin)

    def _cmd_touch(self, args: list[str], stdin: str) -> ExecResult:
        for f in args:
            path = self._resolve(f)
            if not self.fs.exists(path):
                self.fs.write(path, "")
        return ExecResult()

    def _cmd_mkdir(self, args: list[str], stdin: str) -> ExecResult:
        # Dirs are implicit in our VFS, just ensure a marker
        for a in args:
            if a.startswith("-"):
                continue
            path = self._resolve(a)
            self.fs.write(path + "/.keep", "")
        return ExecResult()

    def _cmd_cp(self, args: list[str], stdin: str) -> ExecResult:
        args = [a for a in args if not a.startswith("-")]
        if len(args) < 2:
            return ExecResult(stderr="cp: missing operand\n", exit_code=1)
        src, dst = self._resolve(args[0]), self._resolve(args[1])
        try:
            self.fs.write(dst, self.fs.read(src))
        except FileNotFoundError as e:
            return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult()

    def _cmd_rm(self, args: list[str], stdin: str) -> ExecResult:
        files = [a for a in args if not a.startswith("-")]
        for f in files:
            try:
                self.fs.remove(self._resolve(f))
            except FileNotFoundError:
                pass
        return ExecResult()

    def _cmd_stat(self, args: list[str], stdin: str) -> ExecResult:
        for f in args:
            if f.startswith("-"):
                continue
            try:
                s = self.fs.stat(self._resolve(f))
                return ExecResult(stdout=json.dumps(s, indent=2) + "\n")
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult()

    def _cmd_tree(self, args: list[str], stdin: str) -> ExecResult:
        target = args[0] if args and not args[0].startswith("-") else self.cwd
        resolved = self._resolve(target)
        lines = [resolved]
        self._tree_recurse(resolved, "", lines)
        return ExecResult(stdout="\n".join(lines) + "\n")

    def _tree_recurse(self, path: str, prefix: str, lines: list[str]) -> None:
        entries = self.fs.listdir(path)
        for i, entry in enumerate(entries):
            is_last = i == len(entries) - 1
            connector = "└── " if is_last else "├── "
            full = os.path.normpath(path + "/" + entry)
            is_dir = self.fs._is_dir(full)
            lines.append(f"{prefix}{connector}{entry}{'/' if is_dir else ''}")
            if is_dir:
                extension = "    " if is_last else "│   "
                self._tree_recurse(full, prefix + extension, lines)

    def _cmd_jq(self, args: list[str], stdin: str) -> ExecResult:
        """Minimal jq: supports ., .field, .field.sub, .[n], .[] only."""
        raw = "-r" in args
        args = [a for a in args if not a.startswith("-")]
        query = args[0] if args else "."
        files = args[1:]

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))

        try:
            data = json.loads(text)
        except json.JSONDecodeError as e:
            return ExecResult(stderr=f"jq: parse error: {e}\n", exit_code=2)

        try:
            result = self._jq_query(data, query)
        except (KeyError, IndexError, TypeError) as e:
            return ExecResult(stderr=f"jq: error: {e}\n", exit_code=5)

        if isinstance(result, list) and query.endswith("[]"):
            parts = []
            for item in result:
                parts.append(
                    str(item) if raw and isinstance(item, str)
                    else json.dumps(item)
                )
            return ExecResult(stdout="\n".join(parts) + "\n")

        if raw and isinstance(result, str):
            return ExecResult(stdout=result + "\n")
        return ExecResult(stdout=json.dumps(result, indent=2) + "\n")

    @staticmethod
    def _jq_query(data: Any, query: str) -> Any:
        if query == ".":
            return data
        parts = re.findall(r'\.\w+|\[\d+\]|\[\]', query)
        current = data
        for part in parts:
            if part == "[]":
                if not isinstance(current, list):
                    raise TypeError("Cannot iterate over non-array")
                return current  # caller handles iteration
            elif part.startswith("["):
                idx = int(part[1:-1])
                current = current[idx]
            elif part.startswith("."):
                key = part[1:]
                current = current[key]
        return current


# ---------------------------------------------------------------------------
# ShellSkill — plugs into the agent harness
# ---------------------------------------------------------------------------

class ShellSkill(Skill):
    """
    A single `exec` tool backed by an in-memory virtual filesystem.

    Instead of building many specialized tools, mount your context as files
    and let the model explore with standard Unix commands.

    Usage:
        skill = ShellSkill()
        skill.write("/data/schema.yaml", schema_content)
        skill.mount_dir("/docs", {"api.md": "...", "guide.md": "..."})
        await agent.mount(skill)
    """

    name = "shell"
    description = "Execute bash commands over a virtual filesystem"
    instructions = (
        "You have access to a virtual filesystem via the `exec` tool. "
        "Use standard Unix commands to explore and process files: "
        "ls, cat, grep, find, head, tail, wc, sort, uniq, cut, sed, jq, tree. "
        "Pipes (|) and redirects (>, >>) are supported. "
        "Use `tree /` to see the full file layout."
    )

    def __init__(
        self,
        files: dict[str, str] | None = None,
        cwd: str = "/home/user",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
    ):
        super().__init__()
        self._init_files = files or {}
        self._init_cwd = cwd
        self._init_env = env or {}
        self._allowed_commands = allowed_commands
        self.fs = VirtualFS()
        self.shell: Shell | None = None

    # -- Convenience methods for mounting context ---------------------------

    def write(self, path: str, content: str | bytes) -> None:
        """Write a file to the virtual filesystem."""
        self.fs.write(path, content)

    def write_lazy(self, path: str, provider: Callable[[], str | bytes]) -> None:
        """Register a lazy file — loaded only when first read."""
        self.fs.write_lazy(path, provider)

    def mount_dir(self, prefix: str, files: dict[str, str]) -> None:
        """Mount a dict of {filename: content} under a directory prefix."""
        for name, content in files.items():
            self.fs.write(f"{prefix}/{name}", content)

    def mount_json(self, path: str, data: Any) -> None:
        """Write a JSON-serializable object as a file."""
        self.fs.write(path, json.dumps(data, indent=2))

    # -- Skill lifecycle ----------------------------------------------------

    async def setup(self, ctx: SkillContext) -> None:
        for path, content in self._init_files.items():
            self.fs.write(path, content)

        self.shell = Shell(
            fs=self.fs,
            cwd=self._init_cwd,
            env=self._init_env,
            allowed_commands=self._allowed_commands,
        )

    def tools(self) -> list:
        @tool(
            description=(
                "Execute a bash command in the virtual filesystem. "
                "Supports: ls, cat, grep, find, head, tail, wc, sort, uniq, "
                "cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, echo, stat. "
                "Pipes (|) and redirects (>, >>) are supported."
            ),
        )
        async def exec(command: str) -> str:
            assert self.shell is not None
            result = self.shell.exec(command)
            parts = []
            if result.stdout:
                parts.append(result.stdout)
            if result.stderr:
                parts.append(f"[stderr] {result.stderr}")
            if result.exit_code != 0:
                parts.append(f"[exit code: {result.exit_code}]")
            return "".join(parts) or "(no output)"

        return [exec]


# ===========================================================================
# Demo
# ===========================================================================

if __name__ == "__main__":
    # Quick standalone test — no LLM needed
    skill = ShellSkill()
    skill.write("/data/users.json", json.dumps([
        {"name": "Alice", "role": "admin", "active": True},
        {"name": "Bob", "role": "user", "active": True},
        {"name": "Charlie", "role": "user", "active": False},
        {"name": "Diana", "role": "admin", "active": True},
    ], indent=2))
    skill.write("/data/config.yaml", "database:\n  host: localhost\n  port: 5432\n  name: mydb\n")
    skill.write("/docs/README.md", "# My Project\n\nThis is a test project.\n\n## Features\n- Fast\n- Reliable\n")

    import asyncio
    from skills import SkillContext

    async def demo():
        # Simulate setup
        class FakeAgent: pass
        ctx = SkillContext(skill=skill, agent=FakeAgent(), config={})
        await skill.setup(ctx)

        shell = skill.shell
        assert shell is not None

        print("=== tree / ===")
        print(shell.exec("tree /").stdout)

        print("=== cat /data/users.json | jq '.[] | .name' ===")
        # jq is minimal, so we use .[]
        print(shell.exec("cat /data/users.json | jq '.[]'").stdout)

        print("=== grep admin /data/users.json ===")
        print(shell.exec("grep admin /data/users.json").stdout)

        print("=== grep -c active /data/users.json ===")
        print(shell.exec("grep -c active /data/users.json").stdout)

        print("=== cat /data/config.yaml | grep port ===")
        print(shell.exec("cat /data/config.yaml | grep port").stdout)

        print("=== find / -name '*.json' ===")
        print(shell.exec("find / -name '*.json'").stdout)

        print("=== wc -l /docs/README.md ===")
        print(shell.exec("wc -l /docs/README.md").stdout)

        print("=== head -3 /docs/README.md ===")
        print(shell.exec("head -3 /docs/README.md").stdout)

        print("=== echo hello > /tmp/test.txt; cat /tmp/test.txt ===")
        print(shell.exec("echo hello > /tmp/test.txt").stdout)
        print(shell.exec("cat /tmp/test.txt").stdout)

    asyncio.run(demo())
