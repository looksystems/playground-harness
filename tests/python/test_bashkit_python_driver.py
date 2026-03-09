"""Tests for BashkitPythonDriver using a MockBash object."""

from __future__ import annotations

import base64

import pytest

from src.python.bashkit_python_driver import BashkitPythonDriver, _DirtyTrackingFS
from src.python.drivers import ShellDriver, FilesystemDriver
from src.python.shell import ExecResult


class MockResult:
    """Simulates bashkit ExecResult."""

    def __init__(self, stdout="", stderr="", exit_code=0, error=None):
        self.stdout = stdout
        self.stderr = stderr
        self.exit_code = exit_code
        self.error = error


class MockBash:
    """Simulates bashkit.Bash for testing."""

    def __init__(self):
        self.calls: list[str] = []
        self.next_result = MockResult()
        self._results_queue: list[MockResult] = []

    def execute_sync(self, commands: str) -> MockResult:
        self.calls.append(commands)
        if self._results_queue:
            return self._results_queue.pop(0)
        return self.next_result

    def reset(self) -> None:
        pass

    def enqueue_result(self, result: MockResult) -> None:
        """Queue a specific result for the next execute_sync call."""
        self._results_queue.append(result)


def _make_sync_back_result(files: dict[str, str] | None = None) -> MockResult:
    """Build a MockResult simulating the batched find+base64 sync-back output."""
    if files is None:
        return MockResult(stdout="", exit_code=1)
    lines: list[str] = []
    for path, content in files.items():
        lines.append(f"===FILE:{path}===")
        lines.append(base64.b64encode(content.encode()).decode())
    return MockResult(stdout="\n".join(lines))


def make_driver(**kwargs) -> tuple[BashkitPythonDriver, MockBash]:
    """Create a BashkitPythonDriver with a MockBash injected."""
    mock = MockBash()
    driver = BashkitPythonDriver(bash_override=mock, **kwargs)
    return driver, mock


class TestBashkitPythonDriverContract:
    """BashkitPythonDriver implements ShellDriver ABC."""

    def test_is_shell_driver(self):
        driver, _ = make_driver()
        assert isinstance(driver, ShellDriver)

    def test_fs_is_filesystem_driver(self):
        driver, _ = make_driver()
        assert isinstance(driver.fs, FilesystemDriver)

    def test_cwd_default(self):
        driver, _ = make_driver()
        assert driver.cwd == "/"

    def test_cwd_custom(self):
        driver, _ = make_driver(cwd="/home")
        assert driver.cwd == "/home"

    def test_env_default(self):
        driver, _ = make_driver()
        assert driver.env == {}

    def test_env_custom(self):
        driver, _ = make_driver(env={"FOO": "bar"})
        assert driver.env == {"FOO": "bar"}


class TestBashkitPythonDriverExec:
    """Basic exec returns stdout/stderr/exit_code."""

    def test_exec_returns_stdout(self):
        driver, mock = make_driver()
        # Queue: command result, then sync-back result (empty = no files)
        mock.enqueue_result(MockResult(stdout="hello world"))
        mock.enqueue_result(_make_sync_back_result())
        result = driver.exec("echo hello world")
        assert isinstance(result, ExecResult)
        assert result.stdout == "hello world"
        assert result.exit_code == 0

    def test_exec_returns_stderr(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stderr="error occurred", exit_code=1))
        mock.enqueue_result(_make_sync_back_result())
        result = driver.exec("bad_cmd")
        assert result.stderr == "error occurred"
        assert result.exit_code == 1

    def test_exec_returns_exit_code(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stdout="", stderr="", exit_code=42))
        mock.enqueue_result(_make_sync_back_result())
        result = driver.exec("exit 42")
        assert result.exit_code == 42

    def test_exec_calls_execute_sync(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("ls -la")
        assert any("ls -la" in call for call in mock.calls)

    def test_exec_with_multiline_output(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stdout="line1\nline2\nline3"))
        mock.enqueue_result(_make_sync_back_result())
        result = driver.exec("cat file")
        assert result.stdout == "line1\nline2\nline3"


class TestBashkitPythonDriverVfsSync:
    """VFS dirty tracking and sync."""

    def test_dirty_tracking_on_write(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        assert "/hello.txt" in driver._fs_driver.dirty

    def test_dirty_cleared_after_exec(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        # preamble, command, sync-back
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("echo hi")
        assert len(driver._fs_driver.dirty) == 0

    def test_dirty_file_synced_before_exec(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        # preamble, command, sync-back
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("cat /hello.txt")
        assert len(mock.calls) >= 2
        sync_cmd = mock.calls[0]
        assert "/hello.txt" in sync_cmd
        assert "base64" in sync_cmd

    def test_no_sync_when_no_dirty_files(self):
        driver, mock = make_driver()
        # command, sync-back
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("echo hi")
        # command + sync-back = 2 calls, no preamble
        assert mock.calls[0] == "echo hi"

    def test_dirty_tracking_on_write_lazy(self):
        driver, mock = make_driver()
        driver.fs.write_lazy("/lazy.txt", lambda: "lazy content")
        assert "/lazy.txt" in driver._fs_driver.dirty

    def test_dirty_tracking_on_remove(self):
        driver, mock = make_driver()
        driver.fs.write("/file.txt", "data")
        driver._fs_driver.clear_dirty()
        driver.fs.remove("/file.txt")
        assert "/file.txt" in driver._fs_driver.dirty

    def test_removed_file_synced_as_rm(self):
        driver, mock = make_driver()
        driver.fs.write("/file.txt", "data")
        driver._fs_driver.clear_dirty()
        driver.fs.remove("/file.txt")
        # preamble, command, sync-back
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("ls")
        assert len(mock.calls) >= 2
        assert "rm -f" in mock.calls[0]
        assert "/file.txt" in mock.calls[0]


class TestBashkitPythonDriverCommands:
    """register_command and unregister_command."""

    def test_register_command_stores_handler(self):
        driver, mock = make_driver()
        handler = lambda args, stdin="": ExecResult(stdout="hello")
        driver.register_command("greet", handler)
        assert "greet" in driver._commands
        assert driver._commands["greet"] is handler

    def test_unregister_command_removes_handler(self):
        driver, mock = make_driver()
        driver.register_command("greet", lambda args, stdin="": ExecResult(stdout="hi"))
        driver.unregister_command("greet")
        assert "greet" not in driver._commands

    def test_unregister_nonexistent_command_is_noop(self):
        driver, mock = make_driver()
        driver.unregister_command("nonexistent")  # should not raise

    def test_register_multiple_commands(self):
        driver, mock = make_driver()
        driver.register_command("cmd1", lambda args, stdin="": "r1")
        driver.register_command("cmd2", lambda args, stdin="": "r2")
        assert "cmd1" in driver._commands
        assert "cmd2" in driver._commands

    def test_exec_still_works_after_register(self):
        driver, mock = make_driver()
        driver.register_command("greet", lambda args, stdin="": ExecResult(stdout="hi"))
        mock.enqueue_result(MockResult(stdout="done"))
        mock.enqueue_result(_make_sync_back_result())
        result = driver.exec("greet world")
        assert result.stdout == "done"


class TestBashkitPythonDriverWrapHandler:
    """Handler adaptation between our signature and ScriptedTool signature."""

    def test_wrap_handler_with_exec_result(self):
        driver, _ = make_driver()

        def handler(args, stdin=""):
            return ExecResult(stdout=f"hello {' '.join(args)}")

        wrapped = driver._wrap_handler(handler)
        # ScriptedTool uses --flag value params; positional args go under "" key
        result = wrapped({"": "world"}, None)
        assert result == "hello world"

    def test_wrap_handler_with_string_result(self):
        driver, _ = make_driver()

        def handler(args, stdin=""):
            return f"result: {' '.join(args)}"

        wrapped = driver._wrap_handler(handler)
        # ScriptedTool flag-based params become --key value pairs in args list
        result = wrapped({"name": "foo", "count": "2"}, None)
        assert result == "result: --name foo --count 2"

    def test_wrap_handler_passes_stdin(self):
        driver, _ = make_driver()
        received = {}

        def handler(args, stdin=""):
            received["stdin"] = stdin
            return "ok"

        wrapped = driver._wrap_handler(handler)
        wrapped({"args": []}, "input data")
        assert received["stdin"] == "input data"

    def test_wrap_handler_none_stdin_becomes_empty(self):
        driver, _ = make_driver()
        received = {}

        def handler(args, stdin=""):
            received["stdin"] = stdin
            return "ok"

        wrapped = driver._wrap_handler(handler)
        wrapped({"args": []}, None)
        assert received["stdin"] == ""


class TestBashkitPythonDriverClone:
    """Clone creates independent copy."""

    def test_clone_creates_independent_instance(self):
        driver, mock = make_driver(cwd="/home", env={"A": "1"})
        driver.fs.write("/file.txt", "data")
        driver._fs_driver.clear_dirty()

        cloned = driver.clone()

        assert cloned.cwd == "/home"
        assert cloned.env == {"A": "1"}
        assert cloned.fs.exists("/file.txt")
        assert cloned.fs.read_text("/file.txt") == "data"

        # Independence: modifying clone doesn't affect original
        cloned.fs.write("/clone_only.txt", "clone")
        assert not driver.fs.exists("/clone_only.txt")

    def test_clone_env_is_independent(self):
        driver, _ = make_driver(env={"A": "1"})
        cloned = driver.clone()
        cloned.env["B"] = "2"
        assert "B" not in driver.env

    def test_clone_preserves_commands(self):
        driver, mock = make_driver()
        driver.register_command("cmd1", lambda args, stdin="": "r1")
        cloned = driver.clone()
        assert "cmd1" in cloned._commands

    def test_clone_commands_are_independent(self):
        driver, mock = make_driver()
        driver.register_command("cmd1", lambda args, stdin="": "r1")
        cloned = driver.clone()
        cloned.register_command("cmd2", lambda args, stdin="": "r2")
        assert "cmd2" not in driver._commands

    def test_clone_preserves_on_not_found(self):
        driver, _ = make_driver()
        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        cloned = driver.clone()
        assert cloned.on_not_found is handler


class TestBashkitPythonDriverOnNotFound:
    """on_not_found property."""

    def test_on_not_found_default_is_none(self):
        driver, _ = make_driver()
        assert driver.on_not_found is None

    def test_on_not_found_setter(self):
        driver, _ = make_driver()
        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        assert driver.on_not_found is handler

    def test_on_not_found_can_be_cleared(self):
        driver, _ = make_driver()
        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        driver.on_not_found = None
        assert driver.on_not_found is None


class TestDirtyTrackingFS:
    """Unit tests for _DirtyTrackingFS wrapper."""

    def test_delegates_read(self):
        driver, _ = make_driver()
        driver.fs.write("/test.txt", "content")
        assert driver.fs.read_text("/test.txt") == "content"

    def test_delegates_exists(self):
        driver, _ = make_driver()
        assert not driver.fs.exists("/nope.txt")
        driver.fs.write("/nope.txt", "yes")
        assert driver.fs.exists("/nope.txt")

    def test_delegates_listdir(self):
        driver, _ = make_driver()
        driver.fs.write("/a.txt", "a")
        driver.fs.write("/b.txt", "b")
        entries = driver.fs.listdir("/")
        assert "a.txt" in entries
        assert "b.txt" in entries

    def test_clone_returns_dirty_tracking_fs(self):
        driver, _ = make_driver()
        driver.fs.write("/test.txt", "data")
        cloned = driver.fs.clone()
        assert isinstance(cloned, _DirtyTrackingFS)
        assert len(cloned.dirty) == 0


class TestBashkitPythonDriverSyncBack:
    """Sync-back: files created/modified in bashkit propagate to VFS."""

    def test_new_file_synced_back_to_vfs(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({"/created.txt": "from bashkit"}))
        driver.exec("echo from bashkit > /created.txt")
        assert driver.fs.exists("/created.txt")
        assert driver.fs.read_text("/created.txt") == "from bashkit"

    def test_modified_file_synced_back_to_vfs(self):
        driver, mock = make_driver()
        driver.fs.write("/existing.txt", "original")
        driver._fs_driver.clear_dirty()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({"/existing.txt": "modified"}))
        driver.exec("echo modified > /existing.txt")
        assert driver.fs.read_text("/existing.txt") == "modified"

    def test_deleted_file_removed_from_vfs(self):
        driver, mock = make_driver()
        driver.fs.write("/to_delete.txt", "data")
        driver._fs_driver.clear_dirty()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({}))
        driver.exec("rm /to_delete.txt")
        assert not driver.fs.exists("/to_delete.txt")

    def test_unchanged_file_not_overwritten(self):
        driver, mock = make_driver()
        driver.fs.write("/stable.txt", "constant")
        driver._fs_driver.clear_dirty()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({"/stable.txt": "constant"}))
        driver.exec("cat /stable.txt")
        assert driver.fs.read_text("/stable.txt") == "constant"

    def test_multiple_files_synced_back(self):
        driver, mock = make_driver()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({
            "/a.txt": "alpha",
            "/b.txt": "beta",
        }))
        driver.exec("touch /a.txt /b.txt")
        assert driver.fs.read_text("/a.txt") == "alpha"
        assert driver.fs.read_text("/b.txt") == "beta"

    def test_special_chars_in_content(self):
        driver, mock = make_driver()
        content = "line1\nline2\n'quoted'\n\\backslash\n%percent"
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result({"/special.txt": content}))
        driver.exec("echo special > /special.txt")
        assert driver.fs.read_text("/special.txt") == content

    def test_base64_encoding_in_preamble(self):
        driver, mock = make_driver()
        content = "has 'quotes' and %percent and \\backslash"
        driver.fs.write("/tricky.txt", content)
        # preamble, command, sync-back
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(_make_sync_back_result())
        driver.exec("cat /tricky.txt")
        sync_cmd = mock.calls[0]
        assert "base64" in sync_cmd
        encoded = base64.b64encode(content.encode()).decode()
        assert encoded in sync_cmd

    def test_sync_back_error_result_is_noop(self):
        driver, mock = make_driver()
        driver.fs.write("/keep.txt", "keep me")
        driver._fs_driver.clear_dirty()
        mock.enqueue_result(MockResult(stdout="ok"))
        mock.enqueue_result(MockResult(stdout="", exit_code=1))
        driver.exec("echo hi")
        assert driver.fs.exists("/keep.txt")
