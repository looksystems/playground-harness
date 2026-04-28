import asyncio
import json

import pytest

from src.python.file_tools import register_file_tools
from src.python.has_shell import HasShell
from src.python.uses_tools import UsesTools


class _Agent(UsesTools, HasShell):
    """Minimal test agent composing UsesTools and HasShell."""

    def __init__(self) -> None:
        self.__init_uses_tools__()
        self.__init_has_shell__(cwd="/work")


def _run(coro):
    return asyncio.run(coro)


def _call(agent, name, **kwargs):
    return _run(agent._execute_tool(name, kwargs))


@pytest.fixture
def agent():
    return _Agent()


# ---------------------------------------------------------------------------
# Read
# ---------------------------------------------------------------------------


class TestRead:
    def test_basic(self, agent):
        agent.fs.write("/work/a.txt", "hello\nworld")
        register_file_tools(agent)
        out = _call(agent, "Read", file_path="/work/a.txt")
        # _execute_tool wraps in JSON; the tool returns a string, which json.dumps quotes.
        decoded = json.loads(out)
        assert "     1\thello" in decoded
        assert "     2\tworld" in decoded

    def test_offset_and_limit(self, agent):
        agent.fs.write("/work/big.txt", "\n".join(f"line{i}" for i in range(1, 11)))
        register_file_tools(agent)
        out = json.loads(_call(agent, "Read", file_path="/work/big.txt", offset=4, limit=2))
        assert "     4\tline4" in out
        assert "     5\tline5" in out
        assert "line3" not in out
        assert "line6" not in out

    def test_missing_file(self, agent):
        register_file_tools(agent)
        out = json.loads(_call(agent, "Read", file_path="/nope.txt"))
        assert "error" in out
        assert "does not exist" in out["error"]

    def test_directory(self, agent):
        agent.fs.write("/work/dir/a.txt", "x")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Read", file_path="/work/dir"))
        assert "error" in out
        assert "directory" in out["error"]

    def test_binary_rejected(self, agent):
        agent.fs.write("/work/blob.bin", b"text\x00stuff\x01\x02")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Read", file_path="/work/blob.bin"))
        assert "error" in out
        assert "binary" in out["error"]


# ---------------------------------------------------------------------------
# Write
# ---------------------------------------------------------------------------


class TestWrite:
    def test_creates_file(self, agent):
        register_file_tools(agent)
        out = json.loads(_call(agent, "Write", file_path="/work/new.txt", content="hi"))
        assert "written" in out
        assert agent.fs.read("/work/new.txt") == "hi"

    def test_overwrites_file(self, agent):
        agent.fs.write("/work/x.txt", "old")
        register_file_tools(agent)
        _call(agent, "Write", file_path="/work/x.txt", content="new")
        assert agent.fs.read("/work/x.txt") == "new"


# ---------------------------------------------------------------------------
# Edit
# ---------------------------------------------------------------------------


class TestEdit:
    def test_unique_replacement(self, agent):
        agent.fs.write("/work/c.txt", "alpha beta gamma")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="beta", new_string="BETA"))
        assert "edited" in out
        assert agent.fs.read("/work/c.txt") == "alpha BETA gamma"

    def test_missing_string(self, agent):
        agent.fs.write("/work/c.txt", "hello")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="zzz", new_string="qqq"))
        assert "error" in out
        assert "not found" in out["error"]

    def test_multiple_matches_no_replace_all(self, agent):
        agent.fs.write("/work/c.txt", "x x x")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="x", new_string="y"))
        assert "error" in out
        assert "Found 3 matches" in out["error"]

    def test_replace_all(self, agent):
        agent.fs.write("/work/c.txt", "x x x")
        register_file_tools(agent)
        _call(agent, "Edit", file_path="/work/c.txt",
              old_string="x", new_string="y", replace_all=True)
        assert agent.fs.read("/work/c.txt") == "y y y"

    def test_read_gate_disabled_by_default(self, agent):
        agent.fs.write("/work/c.txt", "hello")
        register_file_tools(agent)  # default: gate off
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="hello", new_string="bye"))
        assert "edited" in out

    def test_read_gate_blocks_unread(self, agent):
        agent.fs.write("/work/c.txt", "hello")
        register_file_tools(agent, enforce_read_gate=True)
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="hello", new_string="bye"))
        assert "error" in out
        assert "not been read" in out["error"]

    def test_read_gate_passes_after_read(self, agent):
        agent.fs.write("/work/c.txt", "hello")
        register_file_tools(agent, enforce_read_gate=True)
        _call(agent, "Read", file_path="/work/c.txt")
        out = json.loads(_call(agent, "Edit", file_path="/work/c.txt",
                                old_string="hello", new_string="bye"))
        assert "edited" in out
        assert agent.fs.read("/work/c.txt") == "bye"


# ---------------------------------------------------------------------------
# Glob
# ---------------------------------------------------------------------------


class TestGlob:
    def test_basic(self, agent):
        agent.fs.write("/work/a.py", "x")
        agent.fs.write("/work/b.py", "y")
        agent.fs.write("/work/c.txt", "z")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Glob", pattern="*.py"))
        assert sorted(out) == ["/work/a.py", "/work/b.py"]

    def test_recursive(self, agent):
        agent.fs.write("/work/a.py", "x")
        agent.fs.write("/work/sub/b.py", "y")
        agent.fs.write("/work/sub/deeper/c.py", "z")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Glob", pattern="**/*.py"))
        assert sorted(out) == ["/work/a.py", "/work/sub/b.py", "/work/sub/deeper/c.py"]

    def test_mtime_sort_newest_first(self, agent):
        agent.fs.write("/work/old.py", "x")
        agent.fs.write("/work/new.py", "y")
        agent.fs.write("/work/middle.py", "z")
        # touch new.py last
        agent.fs.write("/work/new.py", "y2")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Glob", pattern="*.py"))
        assert out[0] == "/work/new.py"

    def test_path_defaults_to_cwd(self, agent):
        agent.fs.write("/work/a.py", "x")
        agent.fs.write("/elsewhere/b.py", "y")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Glob", pattern="*.py"))
        assert out == ["/work/a.py"]

    def test_explicit_path(self, agent):
        agent.fs.write("/work/a.py", "x")
        agent.fs.write("/elsewhere/b.py", "y")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Glob", pattern="*.py", path="/elsewhere"))
        assert out == ["/elsewhere/b.py"]


# ---------------------------------------------------------------------------
# Grep
# ---------------------------------------------------------------------------


class TestGrep:
    def test_files_with_matches_default(self, agent):
        agent.fs.write("/work/a.py", "import os\nprint(1)")
        agent.fs.write("/work/b.py", "print(2)")
        agent.fs.write("/work/c.py", "x = 1")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern=r"^print"))
        assert sorted(out) == ["/work/a.py", "/work/b.py"]

    def test_count(self, agent):
        agent.fs.write("/work/a.py", "print\nprint\nprint")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="print", output_mode="count"))
        assert out == [{"path": "/work/a.py", "count": 3}]

    def test_content(self, agent):
        agent.fs.write("/work/a.py", "alpha\nbeta TARGET here\ngamma")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="TARGET", output_mode="content",
                               line_numbers=True, context_before=1, context_after=1))
        assert out[0]["path"] == "/work/a.py"
        assert out[0]["matches"][0]["line"] == 2
        ctx = out[0]["matches"][0]["context"]
        # 1 line before + match + 1 after = 3 context entries
        assert len(ctx) == 3
        assert ctx[1]["match"] is True
        assert ctx[1]["text"] == "beta TARGET here"

    def test_glob_filter(self, agent):
        agent.fs.write("/work/a.py", "print")
        agent.fs.write("/work/a.txt", "print")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="print", glob="*.py"))
        assert out == ["/work/a.py"]

    def test_type_filter(self, agent):
        agent.fs.write("/work/a.py", "print")
        agent.fs.write("/work/a.go", "print")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="print", type="go"))
        assert out == ["/work/a.go"]

    def test_case_insensitive(self, agent):
        agent.fs.write("/work/a.txt", "Hello WORLD")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="world"))
        assert out == []
        out = json.loads(_call(agent, "Grep", pattern="world", case_insensitive=True))
        assert out == ["/work/a.txt"]

    def test_multiline(self, agent):
        agent.fs.write("/work/a.txt", "alpha\nbeta\ngamma")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern=r"alpha.*gamma", multiline=True))
        assert out == ["/work/a.txt"]

    def test_head_limit(self, agent):
        for i in range(5):
            agent.fs.write(f"/work/f{i}.txt", "match")
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="match", head_limit=2))
        assert len(out) == 2

    def test_unknown_type(self, agent):
        register_file_tools(agent)
        out = json.loads(_call(agent, "Grep", pattern="x", type="bogus"))
        assert "error" in out
        assert "Unknown file type" in out["error"]
