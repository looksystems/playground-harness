"""BashkitNativeDriver: FFI-based ShellDriver using bashkit shared library."""

from __future__ import annotations

import ctypes
import json
import os
import sys
from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult

# C callback type: (args_json: c_char_p, userdata: c_void_p) -> c_char_p
CALLBACK_TYPE = ctypes.CFUNCTYPE(ctypes.c_char_p, ctypes.c_char_p, ctypes.c_void_p)


class BashkitNativeDriver(ShellDriver):
    """ShellDriver that calls bashkit via FFI through a shared C library."""

    def __init__(
        self,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        lib_override: Any = None,
        **kwargs: Any,
    ):
        self._cwd = cwd
        self._env = env if env is not None else {}
        self._fs_driver = BuiltinFilesystemDriver()
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None
        self._lib = lib_override or self._load_library()
        # Keep references to C callback wrappers so they aren't GC'd
        self._c_callbacks: dict[str, CALLBACK_TYPE] = {}

        config = json.dumps({"cwd": self._cwd, "env": self._env})
        self._ctx = self._lib.bashkit_create(config.encode("utf-8"))

    def _load_library(self) -> ctypes.CDLL:
        """Load the bashkit shared library via ctypes."""
        path = self.find_library()
        if path is None:
            raise RuntimeError(
                "bashkit shared library not found. Set BASHKIT_LIB_PATH or "
                "install libashkit to a standard library path."
            )
        lib = ctypes.CDLL(path)
        # Set up function signatures
        lib.bashkit_create.argtypes = [ctypes.c_char_p]
        lib.bashkit_create.restype = ctypes.c_void_p
        lib.bashkit_destroy.argtypes = [ctypes.c_void_p]
        lib.bashkit_destroy.restype = None
        lib.bashkit_exec.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        lib.bashkit_exec.restype = ctypes.c_char_p
        lib.bashkit_register_command.argtypes = [
            ctypes.c_void_p, ctypes.c_char_p, CALLBACK_TYPE, ctypes.c_void_p,
        ]
        lib.bashkit_register_command.restype = None
        lib.bashkit_unregister_command.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        lib.bashkit_unregister_command.restype = None
        lib.bashkit_free_string.argtypes = [ctypes.c_char_p]
        lib.bashkit_free_string.restype = None
        return lib

    @staticmethod
    def find_library() -> str | None:
        """Discover the bashkit shared library path.

        Search order:
        1. BASHKIT_LIB_PATH env var (exact path)
        2. Platform library search paths (DYLD_LIBRARY_PATH/LD_LIBRARY_PATH)
        3. Standard system paths (/usr/local/lib, /usr/lib)
        """
        # 1. Explicit env var
        env_path = os.environ.get("BASHKIT_LIB_PATH")
        if env_path is not None:
            return env_path if os.path.isfile(env_path) else None

        # 2. Platform-specific library name
        if sys.platform == "darwin":
            lib_name = "libashkit.dylib"
            path_var = "DYLD_LIBRARY_PATH"
        elif sys.platform == "win32":
            lib_name = "bashkit.dll"
            path_var = "PATH"
        else:
            lib_name = "libashkit.so"
            path_var = "LD_LIBRARY_PATH"

        # Search dirs from env var + standard paths
        search_dirs: list[str] = []
        env_dirs = os.environ.get(path_var, "")
        if env_dirs:
            search_dirs.extend(env_dirs.split(os.pathsep))
        search_dirs.extend(["/usr/local/lib", "/usr/lib"])

        for d in search_dirs:
            candidate = os.path.join(d, lib_name)
            if os.path.isfile(candidate):
                return candidate

        return None

    def _snapshot_fs(self) -> dict[str, str]:
        """Serialize all VFS files to a dict, resolving lazy providers."""
        snapshot: dict[str, str] = {}
        for path in self._fs_driver.find("/", "*"):
            if not self._fs_driver.is_dir(path):
                snapshot[path] = self._fs_driver.read_text(path)
        return snapshot

    def _apply_fs_changes(self, changes: dict) -> None:
        """Apply created files and deleted paths back to the host FS."""
        created = changes.get("created", {})
        deleted = changes.get("deleted", [])
        for path, content in created.items():
            self._fs_driver.write(path, content)
        for path in deleted:
            if self._fs_driver.exists(path):
                self._fs_driver.remove(path)

    def _make_c_callback(self, name: str, handler: Callable) -> CALLBACK_TYPE:
        """Wrap a Python handler into a C-compatible callback function."""

        def c_wrapper(args_json: bytes, userdata: ctypes.c_void_p) -> bytes:
            try:
                request = json.loads(args_json)
                args = request.get("args", [])
                stdin = request.get("stdin", "")
                result = handler(args, stdin=stdin)
                # Support both ExecResult and raw string returns
                if isinstance(result, ExecResult):
                    return json.dumps({
                        "stdout": result.stdout,
                        "stderr": result.stderr,
                        "exitCode": result.exit_code,
                    }).encode("utf-8")
                return json.dumps({"stdout": str(result), "stderr": "", "exitCode": 0}).encode("utf-8")
            except Exception as e:
                return json.dumps({"error": str(e)}).encode("utf-8")

        wrapped = CALLBACK_TYPE(c_wrapper)
        wrapped.__wrapped__ = c_wrapper  # expose for testing without ctypes overhead
        return wrapped

    @property
    def fs(self) -> FilesystemDriver:
        return self._fs_driver

    @property
    def cwd(self) -> str:
        return self._cwd

    @property
    def env(self) -> dict[str, str]:
        return self._env

    @property
    def on_not_found(self) -> Callable | None:
        return self._on_not_found

    @on_not_found.setter
    def on_not_found(self, callback: Callable | None) -> None:
        self._on_not_found = callback

    def exec(self, command: str) -> ExecResult:
        """Execute a command via the bashkit native library."""
        snapshot = self._snapshot_fs()
        request = json.dumps({
            "cmd": command,
            "cwd": self._cwd,
            "env": self._env,
            "fs": snapshot,
        })

        response_bytes = self._lib.bashkit_exec(self._ctx, request.encode("utf-8"))
        response = json.loads(response_bytes)

        if "fs_changes" in response:
            self._apply_fs_changes(response["fs_changes"])

        return ExecResult(
            stdout=response.get("stdout", ""),
            stderr=response.get("stderr", ""),
            exit_code=response.get("exitCode", 0),
        )

    def register_command(self, name: str, handler: Callable) -> None:
        """Register a custom command handler via C FFI callback."""
        self._commands[name] = handler
        c_cb = self._make_c_callback(name, handler)
        self._c_callbacks[name] = c_cb  # prevent GC
        self._lib.bashkit_register_command(
            self._ctx, name.encode("utf-8"), c_cb, None,
        )

    def unregister_command(self, name: str) -> None:
        """Remove a custom command handler."""
        self._commands.pop(name, None)
        self._c_callbacks.pop(name, None)
        self._lib.bashkit_unregister_command(self._ctx, name.encode("utf-8"))

    def clone(self) -> BashkitNativeDriver:
        """Create a new independent instance with cloned FS and same config."""
        new_driver = BashkitNativeDriver(
            cwd=self._cwd,
            env=dict(self._env),
            lib_override=self._lib,
        )
        # Clone the filesystem
        new_driver._fs_driver = self._fs_driver.clone()
        # Copy registered commands and re-register with new context
        new_driver._commands = dict(self._commands)
        new_driver._on_not_found = self._on_not_found
        for name, handler in self._commands.items():
            c_cb = new_driver._make_c_callback(name, handler)
            new_driver._c_callbacks[name] = c_cb
            self._lib.bashkit_register_command(
                new_driver._ctx, name.encode("utf-8"), c_cb, None,
            )
        return new_driver

    def __del__(self) -> None:
        """Destroy the bashkit context on cleanup."""
        lib = getattr(self, "_lib", None)
        ctx = getattr(self, "_ctx", None)
        if lib is not None and ctx is not None:
            try:
                lib.bashkit_destroy(ctx)
            except Exception:
                pass
