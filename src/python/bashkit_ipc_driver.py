"""BashkitIPCDriver: JSON-RPC over stdin/stdout with bashkit-cli."""

from __future__ import annotations

import json
import subprocess
from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult


class BashkitIPCDriver(ShellDriver):
    """ShellDriver that communicates with bashkit-cli via JSON-RPC over stdio."""

    def __init__(self, cwd: str = "/", env: dict[str, str] | None = None, **kwargs: Any):
        self._cwd = cwd
        self._env = env if env is not None else {}
        self._fs_driver = BuiltinFilesystemDriver()
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None
        self._request_id = 0
        self._process = self._spawn()

    def _spawn(self) -> Any:
        """Spawn the bashkit-cli process. Overridden in tests."""
        return subprocess.Popen(
            ["bashkit-cli", "--jsonrpc"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

    def _next_id(self) -> int:
        self._request_id += 1
        return self._request_id

    def _send(self, msg: dict) -> None:
        """Write a JSON-RPC message to the process stdin."""
        self._process.stdin.write(json.dumps(msg) + "\n")
        self._process.stdin.flush()

    def _recv(self) -> dict:
        """Read a JSON-RPC message from the process stdout."""
        line = self._process.stdout.readline()
        if not line:
            return {}
        return json.loads(line.strip())

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
        """Execute a command via bashkit-cli JSON-RPC."""
        req_id = self._next_id()
        snapshot = self._snapshot_fs()

        self._send({
            "id": req_id,
            "method": "exec",
            "params": {
                "cmd": command,
                "cwd": self._cwd,
                "env": self._env,
                "fs": snapshot,
            },
        })

        # Event loop: handle callbacks until we get the final result
        while True:
            response = self._recv()
            if not response:
                return ExecResult(stderr="No response from bashkit-cli", exit_code=1)

            # Error response
            if "error" in response:
                error = response["error"]
                msg = error.get("message", "Unknown error") if isinstance(error, dict) else str(error)
                return ExecResult(stderr=msg, exit_code=1)

            # Callback: invoke_command from bashkit
            if response.get("method") == "invoke_command":
                cb_id = response.get("id")
                params = response.get("params", {})
                name = params.get("name", "")
                args = params.get("args", [])
                stdin = params.get("stdin", "")

                if name in self._commands:
                    try:
                        result = self._commands[name](args, stdin=stdin)
                        self._send({"id": cb_id, "result": result})
                    except Exception as e:
                        self._send({"id": cb_id, "error": {"code": -1, "message": str(e)}})
                else:
                    self._send({"id": cb_id, "error": {"code": -1, "message": f"Unknown command: {name}"}})
                continue

            # Final result
            if "result" in response:
                r = response["result"]
                if "fs_changes" in r:
                    self._apply_fs_changes(r["fs_changes"])
                return ExecResult(
                    stdout=r.get("stdout", ""),
                    stderr=r.get("stderr", ""),
                    exit_code=r.get("exitCode", 0),
                )

    def register_command(self, name: str, handler: Callable) -> None:
        """Register a custom command handler locally and notify bashkit-cli."""
        self._commands[name] = handler
        self._send({
            "method": "register_command",
            "params": {"name": name},
        })

    def unregister_command(self, name: str) -> None:
        """Remove a custom command handler and notify bashkit-cli."""
        self._commands.pop(name, None)
        self._send({
            "method": "unregister_command",
            "params": {"name": name},
        })

    def clone(self) -> BashkitIPCDriver:
        """Create a new independent instance with cloned FS and same config."""
        new_driver = BashkitIPCDriver(cwd=self._cwd, env=dict(self._env))
        # Clone the filesystem
        cloned_fs = self._fs_driver.clone()
        new_driver._fs_driver = cloned_fs
        # Copy registered commands and re-register with the new process
        new_driver._commands = dict(self._commands)
        new_driver._on_not_found = self._on_not_found
        for name in self._commands:
            new_driver._send({
                "method": "register_command",
                "params": {"name": name},
            })
        return new_driver

    def __del__(self) -> None:
        """Terminate the bashkit-cli process on cleanup."""
        proc = getattr(self, "_process", None)
        if proc is not None:
            try:
                proc.terminate()
                proc.wait(timeout=5)
            except Exception:
                pass
