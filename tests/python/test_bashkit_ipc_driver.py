"""Tests for BashkitIPCDriver using a FakeProcess mock."""

from __future__ import annotations

import json
from unittest.mock import patch

import pytest

from src.python.bashkit_ipc_driver import BashkitIPCDriver
from src.python.drivers import ShellDriver, FilesystemDriver
from src.python.shell import ExecResult


class FakeStdin:
    """Captures writes as JSON messages."""

    def __init__(self):
        self._writes: list[str] = []

    def write(self, data: str) -> None:
        self._writes.append(data)

    def flush(self) -> None:
        pass

    def last_request(self) -> dict:
        # Each write is a JSON line; find the last one
        for raw in reversed(self._writes):
            line = raw.strip()
            if line:
                return json.loads(line)
        raise ValueError("No requests written")

    def all_requests(self) -> list[dict]:
        results = []
        for raw in self._writes:
            line = raw.strip()
            if line:
                results.append(json.loads(line))
        return results


class FakeStdout:
    """Returns enqueued JSON responses line by line."""

    def __init__(self):
        self._lines: list[str] = []
        self._index = 0

    def enqueue(self, obj: dict) -> None:
        self._lines.append(json.dumps(obj) + "\n")

    def readline(self) -> str:
        if self._index < len(self._lines):
            line = self._lines[self._index]
            self._index += 1
            return line
        return ""


class FakeProcess:
    """Mock subprocess.Popen for bashkit-cli."""

    def __init__(self):
        self.stdin = FakeStdin()
        self.stdout = FakeStdout()
        self._terminated = False

    def enqueue_response(self, obj: dict) -> None:
        self.stdout.enqueue(obj)

    def last_request(self) -> dict:
        return self.stdin.last_request()

    def all_requests(self) -> list[dict]:
        return self.stdin.all_requests()

    def poll(self) -> int | None:
        return None if not self._terminated else 0

    def terminate(self) -> None:
        self._terminated = True

    def wait(self, timeout: float | None = None) -> int:
        return 0


def make_driver(**kwargs) -> tuple[BashkitIPCDriver, FakeProcess]:
    """Create a BashkitIPCDriver with a FakeProcess injected."""
    fake = FakeProcess()
    with patch.object(BashkitIPCDriver, "_spawn", return_value=fake):
        driver = BashkitIPCDriver(**kwargs)
    return driver, fake


class TestBashkitIPCDriverContract:
    """BashkitIPCDriver implements ShellDriver ABC."""

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


class TestBashkitIPCDriverVfsSync:
    """VFS snapshot is sent with exec, and fs changes are applied back."""

    def test_exec_sends_snapshot(self):
        driver, fake = make_driver()
        driver.fs.write("/hello.txt", "world")

        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "ok",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        result = driver.exec("echo hi")
        assert isinstance(result, ExecResult)
        assert result.stdout == "ok"
        assert result.exit_code == 0

        req = fake.last_request()
        assert req["method"] == "exec"
        assert req["params"]["cmd"] == "echo hi"
        assert req["params"]["fs"]["/hello.txt"] == "world"

    def test_exec_returns_exec_result(self):
        driver, fake = make_driver()
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "line1\nline2",
                "stderr": "warn",
                "exitCode": 2,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        result = driver.exec("cmd")
        assert result.stdout == "line1\nline2"
        assert result.stderr == "warn"
        assert result.exit_code == 2

    def test_exec_applies_created_files(self):
        driver, fake = make_driver()
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {
                    "created": {"/new.txt": "new content"},
                    "deleted": [],
                },
            },
        })

        driver.exec("touch /new.txt")
        assert driver.fs.exists("/new.txt")
        assert driver.fs.read_text("/new.txt") == "new content"

    def test_exec_applies_deleted_files(self):
        driver, fake = make_driver()
        driver.fs.write("/old.txt", "old")

        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {
                    "created": {},
                    "deleted": ["/old.txt"],
                },
            },
        })

        driver.exec("rm /old.txt")
        assert not driver.fs.exists("/old.txt")

    def test_exec_resolves_lazy_files_in_snapshot(self):
        driver, fake = make_driver()
        driver.fs.write_lazy("/lazy.txt", lambda: "lazy content")

        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        driver.exec("cat /lazy.txt")
        req = fake.last_request()
        assert req["params"]["fs"]["/lazy.txt"] == "lazy content"

    def test_exec_sends_cwd_and_env(self):
        driver, fake = make_driver(cwd="/tmp", env={"PATH": "/bin"})

        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        driver.exec("ls")
        req = fake.last_request()
        assert req["params"]["cwd"] == "/tmp"
        assert req["params"]["env"] == {"PATH": "/bin"}


class TestBashkitIPCDriverCallbacks:
    """Bidirectional JSON-RPC for custom command callbacks."""

    def test_register_command_sends_notification(self):
        driver, fake = make_driver()
        driver.register_command("greet", lambda args, stdin="": "hello")

        reqs = fake.all_requests()
        reg_reqs = [r for r in reqs if r.get("method") == "register_command"]
        assert len(reg_reqs) == 1
        assert reg_reqs[0]["params"]["name"] == "greet"
        assert "id" not in reg_reqs[0]

    def test_invoke_command_callback_during_exec(self):
        driver, fake = make_driver()
        driver.register_command("greet", lambda args, stdin="": f"hello {' '.join(args)}")

        # During exec, bashkit sends an invoke_command callback, then the final result
        fake.enqueue_response({
            "method": "invoke_command",
            "params": {"name": "greet", "args": ["world"]},
            "id": 100,
        })
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "done",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        result = driver.exec("greet world")
        assert result.stdout == "done"

        # The driver should have sent back the callback result
        reqs = fake.all_requests()
        callback_responses = [
            r for r in reqs
            if "result" in r and r.get("id") == 100
        ]
        assert len(callback_responses) == 1
        assert callback_responses[0]["result"] == "hello world"

    def test_unregister_command_sends_notification(self):
        driver, fake = make_driver()
        driver.register_command("greet", lambda args, stdin="": "hello")
        driver.unregister_command("greet")

        reqs = fake.all_requests()
        unreg_reqs = [r for r in reqs if r.get("method") == "unregister_command"]
        assert len(unreg_reqs) == 1
        assert unreg_reqs[0]["params"]["name"] == "greet"
        assert "id" not in unreg_reqs[0]

    def test_unregister_command_removes_handler(self):
        driver, fake = make_driver()
        driver.register_command("greet", lambda args, stdin="": "hello")
        driver.unregister_command("greet")

        # invoke_command for unregistered command during exec should return error
        fake.enqueue_response({
            "method": "invoke_command",
            "params": {"name": "greet", "args": ""},
            "id": 101,
        })
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        driver.exec("greet")
        reqs = fake.all_requests()
        error_responses = [
            r for r in reqs
            if "error" in r and r.get("id") == 101
        ]
        assert len(error_responses) == 1

    def test_callback_passes_stdin_to_handler(self):
        driver, fake = make_driver()
        received = {}

        def handler(args, stdin=""):
            received["args"] = args
            received["stdin"] = stdin
            return "ok"

        driver.register_command("mycmd", handler)

        fake.enqueue_response({
            "method": "invoke_command",
            "params": {"name": "mycmd", "args": ["a", "b"], "stdin": "hello"},
            "id": 200,
        })
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "done",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        driver.exec("mycmd a b")
        assert received["args"] == ["a", "b"]
        assert received["stdin"] == "hello"

    def test_callback_handler_exception_returns_error(self):
        driver, fake = make_driver()

        def bad_handler(args, stdin=""):
            raise RuntimeError("handler blew up")

        driver.register_command("boom", bad_handler)

        fake.enqueue_response({
            "method": "invoke_command",
            "params": {"name": "boom", "args": ""},
            "id": 300,
        })
        fake.enqueue_response({
            "id": 1,
            "result": {
                "stdout": "",
                "stderr": "",
                "exitCode": 0,
                "fs_changes": {"created": {}, "deleted": []},
            },
        })

        result = driver.exec("boom")
        assert result.exit_code == 0  # exec itself succeeds

        reqs = fake.all_requests()
        error_responses = [
            r for r in reqs
            if "error" in r and r.get("id") == 300
        ]
        assert len(error_responses) == 1
        assert "handler blew up" in error_responses[0]["error"]["message"]


class TestBashkitIPCDriverLifecycle:
    """Clone, on_not_found, error handling, and process lifecycle."""

    def test_clone_creates_independent_instance(self):
        driver, fake = make_driver(cwd="/home", env={"A": "1"})
        driver.fs.write("/file.txt", "data")
        driver.register_command("cmd1", lambda args, stdin="": "r1")

        fake2 = FakeProcess()
        with patch.object(BashkitIPCDriver, "_spawn", return_value=fake2):
            cloned = driver.clone()

        assert cloned.cwd == "/home"
        assert cloned.env == {"A": "1"}
        assert cloned.fs.exists("/file.txt")
        assert cloned.fs.read_text("/file.txt") == "data"

        # Independence: modifying clone doesn't affect original
        cloned.fs.write("/clone_only.txt", "clone")
        assert not driver.fs.exists("/clone_only.txt")

        # Clone should re-register commands with new process
        reqs = fake2.all_requests()
        reg_reqs = [r for r in reqs if r.get("method") == "register_command"]
        assert len(reg_reqs) == 1
        assert reg_reqs[0]["params"]["name"] == "cmd1"
        assert "id" not in reg_reqs[0]

    def test_on_not_found_property(self):
        driver, _ = make_driver()
        assert driver.on_not_found is None

        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        assert driver.on_not_found is handler

        driver.on_not_found = None
        assert driver.on_not_found is None

    def test_error_response_returns_exec_result_with_stderr(self):
        driver, fake = make_driver()
        fake.enqueue_response({
            "id": 1,
            "error": {"code": -1, "message": "something broke"},
        })

        result = driver.exec("bad_cmd")
        assert result.exit_code != 0
        assert "something broke" in result.stderr

    def test_del_terminates_process(self):
        driver, fake = make_driver()
        assert not fake._terminated
        driver.__del__()
        assert fake._terminated
