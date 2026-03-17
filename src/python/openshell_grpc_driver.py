"""OpenShellGrpcDriver: ShellDriver using SSH or gRPC to execute in an OpenShell sandbox."""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass, field
from typing import Any, Callable, Generator, Literal

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
class ExecStreamEvent:
    """Event yielded by execStream() during streaming gRPC execution."""

    type: Literal["stdout", "stderr", "exit"]
    data: str = ""
    exit_code: int = 0


@dataclass
class OpenShellPolicy:
    """Security policy for an OpenShell sandbox."""

    filesystem_allow: list[str] | None = None
    network_rules: list[dict[str, Any]] | None = None
    inference_routing: bool = True


class OpenShellGrpcDriver(ShellDriver):
    """ShellDriver that executes commands in an OpenShell sandbox via SSH or gRPC.

    Supports two transport modes:
    - "ssh" (default): Commands run over SSH subprocess calls.
    - "grpc": Commands run via native gRPC using the OpenShell service API,
      enabling streaming exec and proper sandbox lifecycle management.

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
        transport: Literal["ssh", "grpc"] = "ssh",
        grpc_client: Any = None,
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
        self._transport = transport
        self._grpc_client = grpc_client
        self._commands: dict[str, Callable] = {}
        self._on_not_found: Callable | None = None

        inner_fs = BuiltinFilesystemDriver()
        self._fs_driver = DirtyTrackingFS(inner_fs)

    def _get_grpc_client(self) -> Any:
        """Lazily create the gRPC client stub."""
        if self._grpc_client is not None:
            return self._grpc_client
        import grpc
        from src.python.generated.openshell.openshell_pb2_grpc import OpenShellStub
        channel = grpc.insecure_channel(self._endpoint)
        self._grpc_client = OpenShellStub(channel)
        return self._grpc_client

    def _build_sandbox_spec(self) -> Any:
        """Map OpenShellPolicy to proto SandboxSpec."""
        from src.python.generated.openshell.datamodel_pb2 import (
            SandboxSpec, SandboxPolicy, FilesystemPolicy,
            NetworkPolicy, InferencePolicy,
        )
        fs_policy = FilesystemPolicy(
            read_write=list(self._policy.filesystem_allow) if self._policy.filesystem_allow else [],
        )
        net_policies = []
        if self._policy.network_rules:
            for rule in self._policy.network_rules:
                action = NetworkPolicy.DENY if rule.get("deny") else NetworkPolicy.ALLOW
                cidr = rule.get("allow") or rule.get("deny", "")
                net_policies.append(NetworkPolicy(cidr=cidr, action=action))
        inf_policy = InferencePolicy(routing_enabled=self._policy.inference_routing)
        policy = SandboxPolicy(
            filesystem=fs_policy,
            network_policies=net_policies,
            inference=inf_policy,
        )
        return SandboxSpec(policy=policy, workspace=self._workspace)

    def _ensure_sandbox(self) -> None:
        """Lazily mark sandbox as active."""
        if self._sandbox_id is not None:
            return

        if self._grpc_override is not None:
            result = self._grpc_override.create_sandbox(self._policy)
            self._sandbox_id = result.sandbox_id
            return

        if self._transport == "grpc":
            return self._ensure_sandbox_grpc()

        # For real OpenShell, the sandbox is already running (created via
        # `openshell sandbox create`). We just mark it as active.
        self._sandbox_id = f"{self._ssh_user}@{self._ssh_host}:{self._ssh_port}"

    def _ensure_sandbox_grpc(self) -> None:
        """Create sandbox via gRPC CreateSandbox RPC."""
        from src.python.generated.openshell.sandbox_pb2 import CreateSandboxRequest
        client = self._get_grpc_client()
        spec = self._build_sandbox_spec()
        request = CreateSandboxRequest(
            name=f"harness-{os.urandom(4).hex()}",
            spec=spec,
        )
        response = client.CreateSandbox(request)
        sandbox = response.sandbox if hasattr(response, "sandbox") else response.get("sandbox", {})
        if isinstance(sandbox, dict):
            self._sandbox_id = sandbox.get("sandbox_id") or sandbox.get("name", "")
        else:
            self._sandbox_id = sandbox.sandbox_id

    def _raw_exec(self, command: str) -> dict[str, Any]:
        """Execute a command in the sandbox via the active transport."""
        self._ensure_sandbox()

        if self._grpc_override is not None:
            result = self._grpc_override.exec_sandbox(self._sandbox_id, command)
            return {
                "stdout": result.stdout,
                "stderr": result.stderr,
                "exitCode": result.exit_code,
            }

        if self._transport == "grpc":
            return self._raw_exec_grpc(command)
        return self._raw_exec_ssh(command)

    def _raw_exec_ssh(self, command: str) -> dict[str, Any]:
        """Execute a command via SSH subprocess."""
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
            env={**os.environ, **self._env},
        )
        return {
            "stdout": result.stdout,
            "stderr": result.stderr,
            "exitCode": result.returncode,
        }

    @staticmethod
    def _extract_event_field(event: Any, field: str) -> Any:
        """Extract a field from a gRPC event (dataclass or dict)."""
        if isinstance(event, dict):
            return event.get(field)
        return getattr(event, field, None)

    @staticmethod
    def _extract_bytes(chunk: Any, field: str = "data") -> str:
        """Extract string data from a chunk (dataclass, dict, or bytes).

        Handles raw bytes, base64-encoded strings (from JSON wire format),
        and plain strings.
        """
        if chunk is None:
            return ""
        if isinstance(chunk, dict):
            raw = chunk.get(field, b"")
        else:
            raw = getattr(chunk, field, b"")
        if isinstance(raw, bytes):
            return raw.decode("utf-8", errors="replace")
        if isinstance(raw, str):
            import base64
            try:
                return base64.b64decode(raw).decode("utf-8", errors="replace")
            except Exception:
                return raw
        return str(raw)

    def _raw_exec_grpc(self, command: str) -> dict[str, Any]:
        """Execute a command via gRPC ExecSandbox (server-streaming)."""
        from src.python.generated.openshell.sandbox_pb2 import ExecSandboxRequest
        client = self._get_grpc_client()
        request = ExecSandboxRequest(
            sandbox_id=self._sandbox_id,
            command=["bash", "-c", command],
            env=dict(self._env),
        )
        stdout_parts: list[str] = []
        stderr_parts: list[str] = []
        exit_code = 0
        for event in client.ExecSandbox(request):
            stdout_chunk = self._extract_event_field(event, "stdout")
            stderr_chunk = self._extract_event_field(event, "stderr")
            exit_info = self._extract_event_field(event, "exit")
            if stdout_chunk:
                stdout_parts.append(self._extract_bytes(stdout_chunk))
            elif stderr_chunk:
                stderr_parts.append(self._extract_bytes(stderr_chunk))
            elif exit_info:
                exit_code = exit_info.get("code", 0) if isinstance(exit_info, dict) else exit_info.code
        return {
            "stdout": "".join(stdout_parts),
            "stderr": "".join(stderr_parts),
            "exitCode": exit_code,
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
        caps = {"custom_commands", "remote", "policies"}
        if self._transport == "grpc":
            caps.add("streaming")
        return caps

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

    def _try_custom_command(self, command: str) -> ExecResult | None:
        """Intercept first-word match against registered custom commands."""
        parts = command.split()
        if not parts:
            return None
        handler = self._commands.get(parts[0])
        if handler is None:
            return None
        return handler(parts[1:], stdin="")

    def exec(self, command: str) -> ExecResult:
        """Execute a command in the OpenShell sandbox."""
        result = self._try_custom_command(command)
        if result is not None:
            return result

        marker = f"__HARNESS_FS_SYNC_{os.urandom(8).hex()}__"

        if self._grpc_override is not None:
            # Mock mode: use standard sync
            preamble = build_sync_preamble(self._fs_driver)
            epilogue = build_sync_epilogue(marker)
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
            stderr=raw["stderr"],
            exit_code=raw["exitCode"],
        )

    def exec_stream(self, command: str) -> Generator[ExecStreamEvent, None, None]:
        """Execute a command via gRPC streaming, yielding events in real-time.

        Requires transport="grpc". VFS sync-back happens after the stream
        completes (VFS state isn't consistent mid-command).

        Raises UnsupportedError if transport is not "grpc".
        """
        if self._transport != "grpc" and self._grpc_override is None:
            raise RuntimeError("execStream requires transport='grpc'")

        result = self._try_custom_command(command)
        if result is not None:
            if result.stdout:
                yield ExecStreamEvent(type="stdout", data=result.stdout)
            if result.stderr:
                yield ExecStreamEvent(type="stderr", data=result.stderr)
            yield ExecStreamEvent(type="exit", exit_code=result.exit_code)
            return

        marker = f"__HARNESS_FS_SYNC_{os.urandom(8).hex()}__"

        if self._grpc_override is not None:
            preamble = build_sync_preamble(self._fs_driver)
            epilogue = build_sync_epilogue(marker)
        else:
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

        self._ensure_sandbox()

        if self._grpc_override is not None:
            result_obj = self._grpc_override.exec_sandbox(self._sandbox_id, full_command)
            raw_stdout = result_obj.stdout
            if result_obj.stdout:
                stdout, files = parse_sync_output(result_obj.stdout, marker)
                yield ExecStreamEvent(type="stdout", data=stdout)
            if result_obj.stderr:
                yield ExecStreamEvent(type="stderr", data=result_obj.stderr)
            yield ExecStreamEvent(type="exit", exit_code=result_obj.exit_code)

            if files is not None:
                remapped: dict[str, str] = {}
                for remote_path, content in files.items():
                    vfs_path = self._unmap_path(remote_path)
                    remapped[vfs_path] = content
                apply_sync_back(self._fs_driver, remapped)
            return

        from src.python.generated.openshell.sandbox_pb2 import ExecSandboxRequest
        client = self._get_grpc_client()
        request = ExecSandboxRequest(
            sandbox_id=self._sandbox_id,
            command=["bash", "-c", full_command],
            env=dict(self._env),
        )

        stdout_accum: list[str] = []
        for event in client.ExecSandbox(request):
            stdout_chunk = self._extract_event_field(event, "stdout")
            stderr_chunk = self._extract_event_field(event, "stderr")
            exit_info = self._extract_event_field(event, "exit")
            if stdout_chunk:
                chunk = self._extract_bytes(stdout_chunk)
                stdout_accum.append(chunk)
                yield ExecStreamEvent(type="stdout", data=chunk)
            elif stderr_chunk:
                chunk = self._extract_bytes(stderr_chunk)
                yield ExecStreamEvent(type="stderr", data=chunk)
            elif exit_info:
                code = exit_info.get("code", 0) if isinstance(exit_info, dict) else exit_info.code
                yield ExecStreamEvent(type="exit", exit_code=code)

        raw_stdout = "".join(stdout_accum)
        _, files = parse_sync_output(raw_stdout, marker)
        if files is not None:
            remapped = {}
            for remote_path, content in files.items():
                vfs_path = self._unmap_path(remote_path)
                remapped[vfs_path] = content
            apply_sync_back(self._fs_driver, remapped)

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
            elif self._transport == "grpc":
                from src.python.generated.openshell.sandbox_pb2 import DeleteSandboxRequest
                client = self._get_grpc_client()
                client.DeleteSandbox(DeleteSandboxRequest(name=self._sandbox_id))
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
        new_driver._transport = self._transport
        new_driver._grpc_client = self._grpc_client
        new_driver._commands = dict(self._commands)
        new_driver._on_not_found = self._on_not_found
        new_driver._fs_driver = self._fs_driver.clone()
        return new_driver
