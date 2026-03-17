"""OpenShellGrpcDriver: ShellDriver using SSH to execute in an OpenShell sandbox."""

from __future__ import annotations

import subprocess
from dataclasses import dataclass, field
from typing import Any, Callable

from src.python.drivers import ShellDriver, FilesystemDriver, BuiltinFilesystemDriver
from src.python.shell import ExecResult
from src.python._remote_sync import (
    DirtyTrackingFS,
    build_sync_preamble,
    build_sync_epilogue,
    parse_sync_output,
    apply_sync_back,
)


@dataclass
class OpenShellPolicy:
    """Security policy for an OpenShell sandbox."""

    filesystem_allow: list[str] | None = None
    network_rules: list[dict[str, Any]] | None = None
    inference_routing: bool = True


class OpenShellGrpcDriver(ShellDriver):
    """ShellDriver that executes commands in an OpenShell sandbox via SSH.

    OpenShell sandboxes are Docker containers accessed via SSH. Each exec()
    call runs a command over SSH. The sandbox is identified by its SSH
    connection parameters (host, port, user).

    VFS sync uses the same preamble/epilogue mechanism as other remote drivers:
    dirty files are injected before the command, and file state is read back
    after via marker-delimited base64 output.
    """

    def __init__(
        self,
        endpoint: str = "localhost:50051",
        sandbox_id: str | None = None,
        policy: OpenShellPolicy | None = None,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        grpc_override: Any = None,
        ssh_host: str = "localhost",
        ssh_port: int = 2222,
        ssh_user: str = "sandbox",
        workspace: str = "/home/sandbox/workspace",
        **kwargs: Any,
    ):
        self._endpoint = endpoint
        self._sandbox_id = sandbox_id
        self._policy = policy or OpenShellPolicy()
        self._cwd = cwd
        self._env = env if env is not None else {}
        self._grpc_override = grpc_override
        self._ssh_host = ssh_host
        self._ssh_port = ssh_port
        self._ssh_user = ssh_user
        self._workspace = workspace
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None

        inner_fs = BuiltinFilesystemDriver()
        self._fs_driver = DirtyTrackingFS(inner_fs)

    def _ensure_sandbox(self) -> None:
        """Lazily mark sandbox as active."""
        if self._sandbox_id is not None:
            return

        if self._grpc_override is not None:
            result = self._grpc_override.create_sandbox(self._policy)
            self._sandbox_id = result.sandbox_id
            return

        # For real OpenShell, the sandbox is already running (created via
        # `openshell sandbox create`). We just mark it as active.
        self._sandbox_id = f"{self._ssh_user}@{self._ssh_host}:{self._ssh_port}"

    def _raw_exec(self, command: str) -> dict[str, Any]:
        """Execute a command in the sandbox via SSH (or mock)."""
        self._ensure_sandbox()

        if self._grpc_override is not None:
            result = self._grpc_override.exec_sandbox(self._sandbox_id, command)
            return {
                "stdout": result.stdout,
                "stderr": result.stderr,
                "exitCode": result.exit_code,
            }

        # Real execution via SSH into the OpenShell sandbox
        result = subprocess.run(
            [
                "ssh",
                "-p", str(self._ssh_port),
                "-o", "StrictHostKeyChecking=no",
                "-o", "UserKnownHostsFile=/dev/null",
                "-o", "LogLevel=ERROR",
                f"{self._ssh_user}@{self._ssh_host}",
                command,
            ],
            capture_output=True,
            text=True,
            timeout=30,
        )
        return {
            "stdout": result.stdout,
            "stderr": result.stderr,
            "exitCode": result.returncode,
        }

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

    @property
    def sandbox_id(self) -> str | None:
        return self._sandbox_id

    @property
    def policy(self) -> OpenShellPolicy:
        return self._policy

    def capabilities(self) -> set[str]:
        return {"custom_commands", "remote", "policies", "streaming"}

    def _remap_path(self, vfs_path: str) -> str:
        """Map a VFS path to the workspace path on the remote host."""
        if self._grpc_override is not None:
            return vfs_path  # Mock mode: no remapping
        # /hello.txt → /home/sandbox/workspace/hello.txt
        return self._workspace.rstrip("/") + "/" + vfs_path.lstrip("/")

    def _unmap_path(self, remote_path: str) -> str:
        """Map a remote workspace path back to a VFS path."""
        if self._grpc_override is not None:
            return remote_path
        prefix = self._workspace.rstrip("/") + "/"
        if remote_path.startswith(prefix):
            return "/" + remote_path[len(prefix):]
        return remote_path

    def exec(self, command: str) -> ExecResult:
        """Execute a command in the OpenShell sandbox."""
        marker = f"__HARNESS_FS_SYNC_{id(self)}__"

        if self._grpc_override is not None:
            # Mock mode: use standard sync
            preamble_commands = build_sync_preamble(self._fs_driver)
            epilogue = build_sync_epilogue(marker)
            preamble = " && ".join(preamble_commands) if preamble_commands else ""
        else:
            # Real mode: remap paths to workspace
            preamble_parts: list[str] = [f"mkdir -p {self._workspace}"]
            for path in list(self._fs_driver.dirty):
                remote = self._remap_path(path)
                if self._fs_driver.exists(path) and not self._fs_driver.is_dir(path):
                    import base64 as b64
                    content = self._fs_driver.read_text(path)
                    encoded = b64.b64encode(content.encode()).decode()
                    preamble_parts.append(
                        f"mkdir -p $(dirname '{remote}') && printf '%s' '{encoded}' | base64 -d > '{remote}'"
                    )
                elif not self._fs_driver.exists(path):
                    preamble_parts.append(f"rm -f '{remote}'")
            self._fs_driver.clear_dirty()
            preamble = " && ".join(preamble_parts)
            epilogue = build_sync_epilogue(marker, root=self._workspace)

        full_command = (
            f"{preamble} && {command}{epilogue}" if preamble
            else f"{command}{epilogue}"
        )

        raw = self._raw_exec(full_command)
        stdout, files = parse_sync_output(raw["stdout"], marker)

        if files is not None:
            # Remap remote paths back to VFS paths
            remapped: dict[str, str] = {}
            for remote_path, content in files.items():
                vfs_path = self._unmap_path(remote_path)
                remapped[vfs_path] = content
            apply_sync_back(self._fs_driver, remapped)

        return ExecResult(
            stdout=stdout,
            stderr=raw.get("stderr", ""),
            exit_code=raw.get("exitCode", 0),
        )

    def register_command(self, name: str, handler: Callable) -> None:
        """Register a custom command (local interception only)."""
        self._commands[name] = handler

    def unregister_command(self, name: str) -> None:
        """Remove a custom command."""
        self._commands.pop(name, None)

    def close(self) -> None:
        """Mark the sandbox as closed."""
        if self._sandbox_id is not None:
            if self._grpc_override is not None:
                self._grpc_override.delete_sandbox(self._sandbox_id)
            self._sandbox_id = None

    def __enter__(self) -> OpenShellGrpcDriver:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    def clone(self) -> OpenShellGrpcDriver:
        """Create a new independent instance with cloned VFS and a new sandbox."""
        new_driver = OpenShellGrpcDriver.__new__(OpenShellGrpcDriver)
        new_driver._endpoint = self._endpoint
        new_driver._sandbox_id = None  # New sandbox on first exec
        new_driver._policy = OpenShellPolicy(
            filesystem_allow=list(self._policy.filesystem_allow) if self._policy.filesystem_allow else None,
            network_rules=list(self._policy.network_rules) if self._policy.network_rules else None,
            inference_routing=self._policy.inference_routing,
        )
        new_driver._cwd = self._cwd
        new_driver._env = dict(self._env)
        new_driver._grpc_override = self._grpc_override
        new_driver._ssh_host = self._ssh_host
        new_driver._ssh_port = self._ssh_port
        new_driver._ssh_user = self._ssh_user
        new_driver._workspace = self._workspace
        new_driver._commands = dict(self._commands)
        new_driver._on_not_found = self._on_not_found
        new_driver._fs_driver = self._fs_driver.clone()
        return new_driver
