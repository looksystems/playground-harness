"""Tests for BashkitNativeDriver using a MockBashkitLib."""

from __future__ import annotations

import json
import os
import sys
from unittest.mock import patch

import pytest

from src.python.bashkit_native_driver import BashkitNativeDriver
from src.python.drivers import ShellDriver, FilesystemDriver
from src.python.shell import ExecResult


class MockBashkitLib:
    """Simulates the bashkit C shared library for testing."""

    def __init__(self):
        self._contexts: dict[int, dict] = {}
        self._next_ctx = 1
        self._responses: list[dict] = []
        self._last_request: dict | None = None
        self._callbacks: dict[int, dict[str, tuple]] = {}  # ctx -> {name: (cb, userdata)}
        self._freed_strings: list[bytes] = []

    def enqueue_response(self, response: dict) -> None:
        """Pre-configure a response for the next bashkit_exec call."""
        self._responses.append(response)

    def last_request(self) -> dict | None:
        """Return the last request dict passed to bashkit_exec."""
        return self._last_request

    def invoke_callback(self, name: str, args_json: str, ctx: int = 1) -> str | None:
        """Simulate bashkit calling a registered callback.

        Calls the underlying Python function directly (via __wrapped__)
        rather than the ctypes wrapper to avoid memory management warnings.
        """
        if ctx not in self._callbacks or name not in self._callbacks[ctx]:
            return None
        cb, userdata = self._callbacks[ctx][name]
        # Access the underlying Python callable to avoid ctypes c_char_p
        # memory warnings when calling CFUNCTYPE wrappers from Python.
        py_func = getattr(cb, "__wrapped__", None)
        if py_func is not None:
            result = py_func(args_json.encode("utf-8"), userdata)
        else:
            result = cb(args_json.encode("utf-8"), userdata)
        if result is not None:
            return result.decode("utf-8") if isinstance(result, bytes) else result
        return None

    def bashkit_create(self, config_json: bytes) -> int:
        ctx = self._next_ctx
        self._next_ctx += 1
        self._contexts[ctx] = json.loads(config_json)
        self._callbacks[ctx] = {}
        return ctx

    def bashkit_destroy(self, ctx: int) -> None:
        self._contexts.pop(ctx, None)
        self._callbacks.pop(ctx, None)

    def bashkit_exec(self, ctx: int, request_json: bytes) -> bytes:
        self._last_request = json.loads(request_json)
        if self._responses:
            response = self._responses.pop(0)
        else:
            response = {"stdout": "", "stderr": "", "exitCode": 0,
                        "fs_changes": {"created": {}, "deleted": []}}
        return json.dumps(response).encode("utf-8")

    def bashkit_register_command(self, ctx: int, name: bytes, cb, userdata) -> None:
        name_str = name.decode("utf-8") if isinstance(name, bytes) else name
        self._callbacks.setdefault(ctx, {})[name_str] = (cb, userdata)

    def bashkit_unregister_command(self, ctx: int, name: bytes) -> None:
        name_str = name.decode("utf-8") if isinstance(name, bytes) else name
        if ctx in self._callbacks:
            self._callbacks[ctx].pop(name_str, None)

    def bashkit_free_string(self, s: bytes) -> None:
        self._freed_strings.append(s)


def make_driver(**kwargs) -> tuple[BashkitNativeDriver, MockBashkitLib]:
    """Create a BashkitNativeDriver with a MockBashkitLib injected."""
    mock_lib = MockBashkitLib()
    driver = BashkitNativeDriver(lib_override=mock_lib, **kwargs)
    return driver, mock_lib


class TestBashkitNativeDriverContract:
    """BashkitNativeDriver implements ShellDriver ABC."""

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


class TestBashkitNativeDriverVfsSync:
    """VFS snapshot is sent with exec, and fs changes are applied back."""

    def test_exec_sends_snapshot(self):
        driver, mock_lib = make_driver()
        driver.fs.write("/hello.txt", "world")

        mock_lib.enqueue_response({
            "stdout": "ok",
            "stderr": "",
            "exitCode": 0,
            "fs_changes": {"created": {}, "deleted": []},
        })

        result = driver.exec("echo hi")
        assert isinstance(result, ExecResult)
        assert result.stdout == "ok"
        assert result.exit_code == 0

        req = mock_lib.last_request()
        assert req["cmd"] == "echo hi"
        assert req["fs"]["/hello.txt"] == "world"

    def test_exec_returns_exec_result(self):
        driver, mock_lib = make_driver()
        mock_lib.enqueue_response({
            "stdout": "line1\nline2",
            "stderr": "warn",
            "exitCode": 2,
            "fs_changes": {"created": {}, "deleted": []},
        })

        result = driver.exec("cmd")
        assert result.stdout == "line1\nline2"
        assert result.stderr == "warn"
        assert result.exit_code == 2

    def test_exec_applies_created_files(self):
        driver, mock_lib = make_driver()
        mock_lib.enqueue_response({
            "stdout": "",
            "stderr": "",
            "exitCode": 0,
            "fs_changes": {
                "created": {"/new.txt": "new content"},
                "deleted": [],
            },
        })

        driver.exec("touch /new.txt")
        assert driver.fs.exists("/new.txt")
        assert driver.fs.read_text("/new.txt") == "new content"

    def test_exec_applies_deleted_files(self):
        driver, mock_lib = make_driver()
        driver.fs.write("/old.txt", "old")

        mock_lib.enqueue_response({
            "stdout": "",
            "stderr": "",
            "exitCode": 0,
            "fs_changes": {
                "created": {},
                "deleted": ["/old.txt"],
            },
        })

        driver.exec("rm /old.txt")
        assert not driver.fs.exists("/old.txt")

    def test_exec_resolves_lazy_files_in_snapshot(self):
        driver, mock_lib = make_driver()
        driver.fs.write_lazy("/lazy.txt", lambda: "lazy content")

        mock_lib.enqueue_response({
            "stdout": "",
            "stderr": "",
            "exitCode": 0,
            "fs_changes": {"created": {}, "deleted": []},
        })

        driver.exec("cat /lazy.txt")
        req = mock_lib.last_request()
        assert req["fs"]["/lazy.txt"] == "lazy content"

    def test_exec_sends_cwd_and_env(self):
        driver, mock_lib = make_driver(cwd="/tmp", env={"PATH": "/bin"})

        mock_lib.enqueue_response({
            "stdout": "",
            "stderr": "",
            "exitCode": 0,
            "fs_changes": {"created": {}, "deleted": []},
        })

        driver.exec("ls")
        req = mock_lib.last_request()
        assert req["cwd"] == "/tmp"
        assert req["env"] == {"PATH": "/bin"}


class TestBashkitNativeDriverCallbacks:
    """C FFI callback registration and invocation."""

    def test_register_command_stores_handler(self):
        driver, mock_lib = make_driver()
        handler = lambda args, stdin="": ExecResult(stdout="hello")
        driver.register_command("greet", handler)

        # Callback should be registered with the mock lib
        assert "greet" in mock_lib._callbacks.get(driver._ctx, {})

    def test_unregister_command_removes_handler(self):
        driver, mock_lib = make_driver()
        driver.register_command("greet", lambda args, stdin="": ExecResult(stdout="hi"))
        driver.unregister_command("greet")

        assert "greet" not in mock_lib._callbacks.get(driver._ctx, {})

    def test_callback_invocation(self):
        driver, mock_lib = make_driver()
        driver.register_command("greet", lambda args, stdin="": ExecResult(stdout=f"hello {' '.join(args)}"))

        result_json = mock_lib.invoke_callback(
            "greet",
            json.dumps({"name": "greet", "args": ["world"], "stdin": ""}),
        )
        assert result_json is not None
        result = json.loads(result_json)
        assert result["stdout"] == "hello world"

    def test_callback_passes_stdin(self):
        driver, mock_lib = make_driver()
        received = {}

        def handler(args, stdin=""):
            received["args"] = args
            received["stdin"] = stdin
            return ExecResult(stdout="ok")

        driver.register_command("mycmd", handler)

        mock_lib.invoke_callback(
            "mycmd",
            json.dumps({"name": "mycmd", "args": ["a", "b"], "stdin": "input data"}),
        )
        assert received["args"] == ["a", "b"]
        assert received["stdin"] == "input data"

    def test_callback_exception_returns_error(self):
        driver, mock_lib = make_driver()

        def bad_handler(args, stdin=""):
            raise RuntimeError("handler blew up")

        driver.register_command("boom", bad_handler)

        result_json = mock_lib.invoke_callback(
            "boom",
            json.dumps({"name": "boom", "args": [], "stdin": ""}),
        )
        assert result_json is not None
        result = json.loads(result_json)
        assert "error" in result
        assert "handler blew up" in result["error"]


class TestBashkitNativeDriverLifecycle:
    """Clone, on_not_found, and context lifecycle."""

    def test_clone_creates_independent_instance(self):
        driver, mock_lib = make_driver(cwd="/home", env={"A": "1"})
        driver.fs.write("/file.txt", "data")
        driver.register_command("cmd1", lambda args, stdin="": ExecResult(stdout="r1"))

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

    def test_on_not_found_property(self):
        driver, _ = make_driver()
        assert driver.on_not_found is None

        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        assert driver.on_not_found is handler

        driver.on_not_found = None
        assert driver.on_not_found is None

    def test_on_not_found_cloned(self):
        driver, _ = make_driver()
        handler = lambda cmd, args: ExecResult(stderr="not found", exit_code=127)
        driver.on_not_found = handler
        cloned = driver.clone()
        assert cloned.on_not_found is handler

    def test_destroy_called_on_del(self):
        driver, mock_lib = make_driver()
        ctx = driver._ctx
        assert ctx in mock_lib._contexts
        driver.__del__()
        assert ctx not in mock_lib._contexts


class TestBashkitNativeDriverFindLibrary:
    """Library discovery via env vars and platform search paths."""

    def test_find_library_env_var_exists(self, tmp_path):
        lib_path = tmp_path / "libashkit.dylib"
        lib_path.touch()
        with patch.dict(os.environ, {"BASHKIT_LIB_PATH": str(lib_path)}):
            result = BashkitNativeDriver.find_library()
        assert result == str(lib_path)

    def test_find_library_env_var_nonexistent(self):
        with patch.dict(os.environ, {"BASHKIT_LIB_PATH": "/nonexistent/libashkit.so"}):
            result = BashkitNativeDriver.find_library()
        assert result is None

    def test_find_library_not_found(self):
        env = {k: v for k, v in os.environ.items()
               if k not in ("BASHKIT_LIB_PATH", "DYLD_LIBRARY_PATH", "LD_LIBRARY_PATH")}
        with patch.dict(os.environ, env, clear=True):
            with patch("os.path.isfile", return_value=False):
                result = BashkitNativeDriver.find_library()
        assert result is None

    def test_find_library_in_search_path(self, tmp_path):
        lib_name = "libashkit.dylib" if sys.platform == "darwin" else "libashkit.so"
        lib_path = tmp_path / lib_name
        lib_path.touch()

        env_var = "DYLD_LIBRARY_PATH" if sys.platform == "darwin" else "LD_LIBRARY_PATH"
        env = {k: v for k, v in os.environ.items() if k != "BASHKIT_LIB_PATH"}
        env[env_var] = str(tmp_path)

        with patch.dict(os.environ, env, clear=True):
            result = BashkitNativeDriver.find_library()
        assert result == str(lib_path)
