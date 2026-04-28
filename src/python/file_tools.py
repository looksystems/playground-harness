"""Built-in file tools: Read, Write, Edit, Glob, Grep.

Mirrors Claude Code's file toolset: parameter names and observable behavior
match closely. All five tools delegate to the agent's `FilesystemDriver`, so
swapping the shell backend (builtin / bashkit / OpenShell) automatically swaps
the filesystem they see.
"""

from __future__ import annotations

import os
import posixpath
import re
from dataclasses import dataclass
from typing import Any

from src.python.uses_tools import ToolDef


# Common file extensions Grep's ?type= can filter by.
_TYPE_MAP: dict[str, list[str]] = {
    "py": [".py"],
    "ts": [".ts", ".tsx"],
    "js": [".js", ".jsx", ".mjs", ".cjs"],
    "php": [".php"],
    "go": [".go"],
    "rs": [".rs"],
    "java": [".java"],
    "rb": [".rb"],
    "md": [".md", ".markdown"],
    "txt": [".txt"],
    "json": [".json"],
    "yaml": [".yaml", ".yml"],
    "sh": [".sh", ".bash"],
    "html": [".html", ".htm"],
    "css": [".css"],
    "sql": [".sql"],
}


def _norm(path: str) -> str:
    if not path.startswith("/"):
        path = "/" + path
    return posixpath.normpath(path)


def _is_binary(content: str | bytes) -> bool:
    if isinstance(content, bytes):
        return b"\x00" in content[:8192]
    return "\x00" in content[:8192]


def _format_lines(text: str, offset: int, limit: int) -> str:
    """cat -n style: '%6d\\t%s' per line, line numbers 1-indexed."""
    lines = text.split("\n")
    if lines and lines[-1] == "":
        # Trailing newline produces a phantom empty element — drop it.
        lines = lines[:-1]
    start = max(1, offset)
    end = min(len(lines), start + limit - 1)
    out = []
    for i in range(start, end + 1):
        out.append(f"{i:6d}\t{lines[i - 1]}")
    return "\n".join(out)


def _walk(fs, root: str) -> list[str]:
    """Yield every file path under root, recursively. Skips dotfile dirs."""
    root = _norm(root)
    if not fs.exists(root):
        return []
    if not fs.is_dir(root):
        return [root]
    out: list[str] = []
    stack = [root]
    while stack:
        d = stack.pop()
        for entry in fs.listdir(d):
            if entry.startswith("."):
                continue
            child = posixpath.join(d, entry)
            if fs.is_dir(child):
                stack.append(child)
            else:
                out.append(child)
    return out


def _glob_to_regex(pattern: str) -> re.Pattern[str]:
    """Translate a gitignore-style glob to a regex anchored to the full string.

    `**` matches any number of characters including `/`. `*` and `?` match
    within a single segment (no `/`).
    """
    out: list[str] = []
    i = 0
    while i < len(pattern):
        c = pattern[i]
        if c == "*" and i + 1 < len(pattern) and pattern[i + 1] == "*":
            out.append(".*")
            i += 2
            if i < len(pattern) and pattern[i] == "/":
                i += 1
                if out[-1] == ".*":
                    pass
        elif c == "*":
            out.append("[^/]*")
            i += 1
        elif c == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(c))
            i += 1
    return re.compile("^" + "".join(out) + "$")


def _matches_glob(rel_path: str, pattern: str) -> bool:
    return _glob_to_regex(pattern).match(rel_path) is not None


def _resolve_root(fs_cwd: str, path: str | None) -> str:
    if path is None:
        return _norm(fs_cwd)
    if path.startswith("/"):
        return _norm(path)
    return _norm(posixpath.join(fs_cwd, path))


@dataclass
class _FileToolsState:
    """Per-registration state shared across the five tools."""
    enforce_read_gate: bool = False
    read_set: set[str] | None = None


def _build_tools(agent: Any, state: _FileToolsState) -> list[ToolDef]:
    def _fs():
        return agent.shell.fs

    def _cwd() -> str:
        return agent.shell.cwd

    def read_file(file_path: str, offset: int = 1, limit: int = 2000) -> str:
        path = _norm(file_path)
        if not _fs().exists(path):
            raise FileNotFoundError(f"File does not exist: {path}")
        if _fs().is_dir(path):
            raise IsADirectoryError(f"Path is a directory, not a file: {path}")
        content = _fs().read(path)
        if _is_binary(content):
            raise ValueError("binary file not supported by Read; use a different tool")
        text = content if isinstance(content, str) else content.decode("utf-8", errors="replace")
        if state.read_set is not None:
            state.read_set.add(path)
        return _format_lines(text, offset, limit)

    def write_file(file_path: str, content: str) -> str:
        path = _norm(file_path)
        _fs().write(path, content)
        if state.read_set is not None:
            state.read_set.add(path)
        return f"File written: {path}"

    def edit_file(
        file_path: str,
        old_string: str,
        new_string: str,
        replace_all: bool = False,
    ) -> str:
        path = _norm(file_path)
        if not _fs().exists(path):
            raise FileNotFoundError(f"File does not exist: {path}")
        if state.enforce_read_gate and (state.read_set is None or path not in state.read_set):
            raise PermissionError(
                "File has not been read yet. Read it first before writing to it."
            )
        content = _fs().read(path)
        text = content if isinstance(content, str) else content.decode("utf-8", errors="replace")
        if old_string not in text:
            raise ValueError("String to replace not found in file")
        if replace_all:
            new_text = text.replace(old_string, new_string)
        else:
            count = text.count(old_string)
            if count > 1:
                raise ValueError(
                    f"Found {count} matches of the string to replace, but replace_all is false. "
                    "Provide a larger string with more surrounding context to make it unique, "
                    "or set replace_all to true."
                )
            new_text = text.replace(old_string, new_string, 1)
        _fs().write(path, new_text)
        return f"File edited: {path}"

    def glob_files(pattern: str, path: str | None = None) -> list[str]:
        root = _resolve_root(_cwd(), path)
        files = _walk(_fs(), root)
        prefix = root.rstrip("/") + "/"
        results: list[str] = []
        for p in files:
            rel = p[len(prefix):] if p.startswith(prefix) else p
            if _matches_glob(rel, pattern):
                results.append(p)

        def mtime_of(p: str) -> int:
            try:
                return _fs().stat(p).get("mtime", 0)
            except Exception:
                return 0

        results.sort(key=mtime_of, reverse=True)
        return results

    def grep_files(
        pattern: str,
        path: str | None = None,
        glob: str | None = None,
        type: str | None = None,
        case_insensitive: bool = False,
        line_numbers: bool = False,
        output_mode: str = "files_with_matches",
        head_limit: int | None = None,
        multiline: bool = False,
        context_before: int = 0,
        context_after: int = 0,
    ) -> Any:
        root = _resolve_root(_cwd(), path)
        flags = 0
        if case_insensitive:
            flags |= re.IGNORECASE
        if multiline:
            flags |= re.MULTILINE | re.DOTALL
        regex = re.compile(pattern, flags)

        files = _walk(_fs(), root)
        prefix = root.rstrip("/") + "/"

        if glob is not None:
            files = [
                p for p in files
                if _matches_glob(p[len(prefix):] if p.startswith(prefix) else p, glob)
            ]
        if type is not None:
            exts = _TYPE_MAP.get(type)
            if exts is None:
                raise ValueError(f"Unknown file type: {type}")
            files = [p for p in files if any(p.endswith(e) for e in exts)]

        if output_mode == "files_with_matches":
            matched: list[str] = []
            for p in files:
                try:
                    raw = _fs().read(p)
                except Exception:
                    continue
                text = raw if isinstance(raw, str) else raw.decode("utf-8", errors="replace")
                if multiline:
                    if regex.search(text):
                        matched.append(p)
                else:
                    if any(regex.search(line) for line in text.split("\n")):
                        matched.append(p)
            if head_limit is not None:
                matched = matched[:head_limit]
            return matched

        if output_mode == "count":
            counts: list[dict[str, Any]] = []
            for p in files:
                try:
                    raw = _fs().read(p)
                except Exception:
                    continue
                text = raw if isinstance(raw, str) else raw.decode("utf-8", errors="replace")
                if multiline:
                    n = len(regex.findall(text))
                else:
                    n = sum(1 for line in text.split("\n") if regex.search(line))
                if n > 0:
                    counts.append({"path": p, "count": n})
            if head_limit is not None:
                counts = counts[:head_limit]
            return counts

        if output_mode == "content":
            results: list[dict[str, Any]] = []
            for p in files:
                try:
                    raw = _fs().read(p)
                except Exception:
                    continue
                text = raw if isinstance(raw, str) else raw.decode("utf-8", errors="replace")
                lines = text.split("\n")
                hits: list[dict[str, Any]] = []
                for idx, line in enumerate(lines):
                    if regex.search(line):
                        start = max(0, idx - context_before)
                        end = min(len(lines), idx + context_after + 1)
                        ctx_lines = []
                        for j in range(start, end):
                            entry = {"line": j + 1, "text": lines[j], "match": j == idx} if line_numbers else {"text": lines[j], "match": j == idx}
                            ctx_lines.append(entry)
                        hits.append({"line": idx + 1, "context": ctx_lines})
                if hits:
                    results.append({"path": p, "matches": hits})
                if head_limit is not None and len(results) >= head_limit:
                    break
            return results

        raise ValueError(f"Unknown output_mode: {output_mode}")

    read_def = ToolDef(
        name="Read",
        description=(
            "Read a file from the virtual filesystem. Returns content with `cat -n` "
            "style line numbers (1-indexed). Defaults to the first 2000 lines. Use "
            "`offset` (line number to start from, 1-indexed) and `limit` to page "
            "through larger files. Binary files (containing null bytes in the first "
            "8KB) are rejected with an error."
        ),
        function=read_file,
        parameters={
            "type": "object",
            "properties": {
                "file_path": {"type": "string", "description": "Absolute path of the file to read."},
                "offset": {"type": "integer", "description": "Line number to start from (1-indexed). Default 1."},
                "limit": {"type": "integer", "description": "Maximum number of lines to return. Default 2000."},
            },
            "required": ["file_path"],
        },
    )

    write_def = ToolDef(
        name="Write",
        description="Write content to a file, creating it if it doesn't exist or overwriting if it does.",
        function=write_file,
        parameters={
            "type": "object",
            "properties": {
                "file_path": {"type": "string", "description": "Absolute path of the file to write."},
                "content": {"type": "string", "description": "The content to write."},
            },
            "required": ["file_path", "content"],
        },
    )

    edit_def = ToolDef(
        name="Edit",
        description=(
            "Replace exact strings in a file. Errors if `old_string` is not found, "
            "or appears more than once when `replace_all` is false (the default). "
            "When the read-gate is enabled at registration time, errors if the file "
            "has not been Read in this session."
        ),
        function=edit_file,
        parameters={
            "type": "object",
            "properties": {
                "file_path": {"type": "string", "description": "Absolute path of the file to edit."},
                "old_string": {"type": "string", "description": "The exact string to replace."},
                "new_string": {"type": "string", "description": "The replacement string."},
                "replace_all": {"type": "boolean", "description": "Replace all occurrences. Default false."},
            },
            "required": ["file_path", "old_string", "new_string"],
        },
    )

    glob_def = ToolDef(
        name="Glob",
        description=(
            "Match files by pattern (supports `*`, `?`, and recursive `**`) and return "
            "absolute paths sorted by modification time, newest first. `path` defaults "
            "to the shell's current working directory."
        ),
        function=glob_files,
        parameters={
            "type": "object",
            "properties": {
                "pattern": {"type": "string", "description": "Glob pattern, e.g. `**/*.py`."},
                "path": {"type": "string", "description": "Directory to search in. Defaults to cwd."},
            },
            "required": ["pattern"],
        },
    )

    grep_def = ToolDef(
        name="Grep",
        description=(
            "Search file contents with a regular expression. Recursively walks `path` "
            "(default cwd) and matches against each file's content. `output_mode` "
            "controls return shape: `files_with_matches` (default) → list of paths; "
            "`count` → list of {path, count}; `content` → list of {path, matches} "
            "where each match is {line, context: [{line, text, match}]}. Use `glob` "
            "or `type` (py, ts, js, php, go, rs, java, rb, md, txt, json, yaml, sh, "
            "html, css, sql) to filter the file set. Set `multiline` true to match "
            "across line boundaries; `line_numbers` adds line numbers to context "
            "entries; `context_before`/`context_after` add surrounding lines."
        ),
        function=grep_files,
        parameters={
            "type": "object",
            "properties": {
                "pattern": {"type": "string", "description": "The regular expression to match."},
                "path": {"type": "string", "description": "Directory to search in. Defaults to cwd."},
                "glob": {"type": "string", "description": "Filter the file set by glob pattern (e.g. `**/*.py`)."},
                "type": {"type": "string", "description": "Filter by built-in file type alias (py, ts, js, ...)."},
                "case_insensitive": {"type": "boolean", "description": "Case-insensitive match. Default false."},
                "line_numbers": {"type": "boolean", "description": "Include line numbers in context entries (content mode). Default false."},
                "output_mode": {
                    "type": "string",
                    "description": "files_with_matches (default), count, or content.",
                },
                "head_limit": {"type": "integer", "description": "Return only the first N results."},
                "multiline": {"type": "boolean", "description": "Allow patterns to match across line boundaries. Default false."},
                "context_before": {"type": "integer", "description": "Lines of context before each match (content mode). Default 0."},
                "context_after": {"type": "integer", "description": "Lines of context after each match (content mode). Default 0."},
            },
            "required": ["pattern"],
        },
    )

    return [read_def, write_def, edit_def, glob_def, grep_def]


def register_file_tools(agent: Any, *, enforce_read_gate: bool = False) -> Any:
    """Register the five built-in file tools on `agent`.

    Returns the per-registration state object so callers can inspect or
    manipulate the read-set in tests.
    """
    state = _FileToolsState(
        enforce_read_gate=enforce_read_gate,
        read_set=set() if enforce_read_gate else None,
    )
    for td in _build_tools(agent, state):
        agent.register_tool(td)
    return state
