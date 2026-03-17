"""Tests for OpenShellGrpcDriver using a mock gRPC stub."""

from __future__ import annotations

import base64

import pytest

from src.python.openshell_grpc_driver import OpenShellGrpcDriver, OpenShellPolicy
from src.python.openshell_driver import OpenShellDriver, register_openshell_driver
from src.python.drivers import ShellDriver, FilesystemDriver, ShellDriverFactory
from src.python.shell import ExecResult


class MockExecResult:
    """Simulates a gRPC ExecSandbox response."""

    def __init__(self, stdout="", stderr="", exit_code=0):
        self.stdout = stdout
        self.stderr = stderr
        self.exit_code = exit_code


class MockCreateResult:
    """Simulates a gRPC CreateSandbox response."""

    def __init__(self, sandbox_id="sandbox-001"):
        self.sandbox_id = sandbox_id


class MockGrpcStub:
    """Simulates the OpenShell gRPC service for testing."""

    def __init__(self):
        self.calls: list[tuple[str, ...]] = []
        self.next_exec_result = MockExecResult()
        self._exec_results_queue: list[MockExecResult] = []
        self._create_count = 0
        self._deleted: list[str] = []
        self._last_policy: OpenShellPolicy | None = None

    def create_sandbox(self, policy: OpenShellPolicy | None = None) -> MockCreateResult:
        self._create_count += 1
        self._last_policy = policy
        sandbox_id = f"sandbox-{self._create_count:03d}"
        self.calls.append(("create_sandbox", sandbox_id))
        return MockCreateResult(sandbox_id=sandbox_id)

    def exec_sandbox(self, sandbox_id: str, command: str) -> MockExecResult:
        self.calls.append(("exec_sandbox", sandbox_id, command))
        if self._exec_results_queue:
            return self._exec_results_queue.pop(0)
        return self.next_exec_result

    def delete_sandbox(self, sandbox_id: str) -> None:
        self._deleted.append(sandbox_id)
        self.calls.append(("delete_sandbox", sandbox_id))

    def enqueue_result(self, result: MockExecResult) -> None:
        self._exec_results_queue.append(result)


def _make_sync_output(marker: str, files: dict[str, str]) -> str:
    """Build stdout string with marker-delimited file listing."""
    lines: list[str] = []
    for path, content in files.items():
        lines.append(f"===FILE:{path}===")
        lines.append(base64.b64encode(content.encode()).decode())
    return f"\n{marker}\n" + "\n".join(lines)


def make_driver(**kwargs) -> tuple[OpenShellGrpcDriver, MockGrpcStub]:
    """Create an OpenShellGrpcDriver with mock gRPC injected."""
    mock = MockGrpcStub()
    driver = OpenShellGrpcDriver(grpc_override=mock, **kwargs)
    return driver, mock


class TestOpenShellDriverResolve:
    """Tests for OpenShellDriver.resolve()."""

    def test_resolve_with_grpc_override_returns_driver(self):
        mock = MockGrpcStub()
        driver = OpenShellDriver.resolve(grpc_override=mock)
        assert isinstance(driver, OpenShellGrpcDriver)

    def test_resolve_without_ssh_raises_runtime_error(self):
        # Without grpc_override and without ssh available, should raise
        import shutil
        if shutil.which("ssh") is not None:
            pytest.skip("ssh is available")
        with pytest.raises(RuntimeError, match="ssh not found"):
            OpenShellDriver.resolve()


class TestRegisterOpenShellDriver:
    """Tests for register_openshell_driver() and factory integration."""

    def setup_method(self):
        ShellDriverFactory.reset()

    def teardown_method(self):
        ShellDriverFactory.reset()

    def test_register_adds_to_factory(self):
        register_openshell_driver()
        mock = MockGrpcStub()
        driver = ShellDriverFactory.create("openshell", grpc_override=mock)
        assert isinstance(driver, OpenShellGrpcDriver)

    def test_factory_create_without_registration_raises(self):
        with pytest.raises(KeyError, match="not registered"):
            ShellDriverFactory.create("openshell")


class TestOpenShellGrpcDriverContract:
    """OpenShellGrpcDriver implements ShellDriver ABC."""

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


class TestOpenShellGrpcDriverExec:
    """Basic exec returns stdout/stderr/exit_code."""

    def test_exec_returns_stdout(self):
        driver, mock = make_driver()
        marker_prefix = "__HARNESS_FS_SYNC_"
        mock.next_exec_result = MockExecResult(stdout="hello world")
        result = driver.exec("echo hello world")
        assert isinstance(result, ExecResult)
        # stdout will have marker split applied; if no marker found, full output returned
        assert "hello world" in result.stdout
        assert result.exit_code == 0

    def test_exec_returns_stderr(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stderr="error occurred", exit_code=1)
        result = driver.exec("bad_cmd")
        assert result.stderr == "error occurred"
        assert result.exit_code == 1

    def test_exec_returns_exit_code(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stdout="", stderr="", exit_code=42)
        result = driver.exec("exit 42")
        assert result.exit_code == 42

    def test_exec_with_sync_back(self):
        driver, mock = make_driver()
        # Build output that includes the marker and file listing
        def custom_exec(sandbox_id, command):
            # Extract marker from command
            import re
            m = re.search(r'__HARNESS_FS_SYNC_[0-9a-f]+__', command)
            marker = m.group(0) if m else ""
            stdout = f"hello{_make_sync_output(marker, {'/result.txt': 'from sandbox'})}"
            return MockExecResult(stdout=stdout)

        mock.exec_sandbox = custom_exec
        result = driver.exec("echo hello")
        assert result.stdout == "hello"
        assert driver.fs.exists("/result.txt")
        assert driver.fs.read_text("/result.txt") == "from sandbox"


class TestOpenShellGrpcDriverVfsSync:
    """VFS dirty tracking and sync."""

    def test_dirty_tracking_on_write(self):
        driver, _ = make_driver()
        driver.fs.write("/hello.txt", "world")
        assert "/hello.txt" in driver._fs_driver.dirty

    def test_dirty_cleared_after_exec(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hi")
        assert len(driver._fs_driver.dirty) == 0

    def test_dirty_file_synced_before_exec(self):
        driver, mock = make_driver()
        driver.fs.write("/hello.txt", "world")
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("cat /hello.txt")
        # The exec call should include preamble with base64 content
        exec_calls = [c for c in mock.calls if c[0] == "exec_sandbox"]
        assert len(exec_calls) >= 1
        command = exec_calls[0][2]
        assert "/hello.txt" in command
        assert "base64" in command

    def test_no_preamble_when_no_dirty_files(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hi")
        exec_calls = [c for c in mock.calls if c[0] == "exec_sandbox"]
        assert len(exec_calls) == 1
        command = exec_calls[0][2]
        assert command.startswith("echo hi")

    def test_removed_file_synced_as_rm(self):
        driver, mock = make_driver()
        driver.fs.write("/file.txt", "data")
        driver._fs_driver.clear_dirty()
        driver.fs.remove("/file.txt")
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("ls")
        exec_calls = [c for c in mock.calls if c[0] == "exec_sandbox"]
        command = exec_calls[0][2]
        assert "rm -f" in command
        assert "/file.txt" in command


class TestOpenShellGrpcDriverLifecycle:
    """Sandbox lifecycle: lazy creation, close, context manager."""

    def test_lazy_creation_on_first_exec(self):
        driver, mock = make_driver()
        assert driver.sandbox_id is None
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hello")
        assert driver.sandbox_id is not None
        assert mock._create_count == 1

    def test_close_deletes_sandbox(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hello")
        sandbox_id = driver.sandbox_id
        driver.close()
        assert sandbox_id in mock._deleted
        assert driver.sandbox_id is None

    def test_close_without_sandbox_is_noop(self):
        driver, mock = make_driver()
        driver.close()  # should not raise
        assert mock._create_count == 0

    def test_context_manager(self):
        mock = MockGrpcStub()
        mock.next_exec_result = MockExecResult(stdout="ok")
        with OpenShellGrpcDriver(grpc_override=mock) as driver:
            driver.exec("echo hello")
            sandbox_id = driver.sandbox_id
        assert sandbox_id in mock._deleted

    def test_context_manager_cleanup_on_exception(self):
        mock = MockGrpcStub()
        mock.next_exec_result = MockExecResult(stdout="ok")
        try:
            with OpenShellGrpcDriver(grpc_override=mock) as driver:
                driver.exec("echo hello")
                sandbox_id = driver.sandbox_id
                raise ValueError("test error")
        except ValueError:
            pass
        assert sandbox_id in mock._deleted


class TestOpenShellGrpcDriverPolicy:
    """Policy passed to CreateSandbox correctly."""

    def test_default_policy(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hello")
        assert mock._last_policy is not None
        assert mock._last_policy.inference_routing is True

    def test_custom_policy(self):
        policy = OpenShellPolicy(
            filesystem_allow=["/data", "/tmp"],
            network_rules=[{"allow": "10.0.0.0/8"}],
            inference_routing=False,
        )
        driver, mock = make_driver(policy=policy)
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hello")
        assert mock._last_policy.filesystem_allow == ["/data", "/tmp"]
        assert mock._last_policy.network_rules == [{"allow": "10.0.0.0/8"}]
        assert mock._last_policy.inference_routing is False

    def test_policy_accessible_via_property(self):
        policy = OpenShellPolicy(filesystem_allow=["/data"])
        driver, _ = make_driver(policy=policy)
        assert driver.policy.filesystem_allow == ["/data"]


class TestOpenShellGrpcDriverClone:
    """Clone creates independent sandbox and VFS."""

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

    def test_clone_has_no_sandbox(self):
        driver, mock = make_driver()
        mock.next_exec_result = MockExecResult(stdout="ok")
        driver.exec("echo hello")
        assert driver.sandbox_id is not None

        cloned = driver.clone()
        assert cloned.sandbox_id is None  # New sandbox on first exec

    def test_clone_env_is_independent(self):
        driver, _ = make_driver(env={"A": "1"})
        cloned = driver.clone()
        cloned.env["B"] = "2"
        assert "B" not in driver.env

    def test_clone_preserves_commands(self):
        driver, _ = make_driver()
        driver.register_command("cmd1", lambda args, stdin="": "r1")
        cloned = driver.clone()
        assert "cmd1" in cloned._commands

    def test_clone_commands_are_independent(self):
        driver, _ = make_driver()
        driver.register_command("cmd1", lambda args, stdin="": "r1")
        cloned = driver.clone()
        cloned.register_command("cmd2", lambda args, stdin="": "r2")
        assert "cmd2" not in driver._commands

    def test_clone_policy_is_independent(self):
        policy = OpenShellPolicy(filesystem_allow=["/data"])
        driver, _ = make_driver(policy=policy)
        cloned = driver.clone()
        cloned.policy.filesystem_allow.append("/tmp")
        assert driver.policy.filesystem_allow == ["/data"]


class TestOpenShellGrpcDriverCommands:
    """register_command and unregister_command."""

    def test_register_command_stores_handler(self):
        driver, _ = make_driver()
        handler = lambda args, stdin="": ExecResult(stdout="hello")
        driver.register_command("greet", handler)
        assert "greet" in driver._commands

    def test_unregister_command_removes_handler(self):
        driver, _ = make_driver()
        driver.register_command("greet", lambda args, stdin="": "hi")
        driver.unregister_command("greet")
        assert "greet" not in driver._commands

    def test_unregister_nonexistent_command_is_noop(self):
        driver, _ = make_driver()
        driver.unregister_command("nonexistent")  # should not raise


class TestOpenShellGrpcDriverOnNotFound:
    """on_not_found property."""

    def test_on_not_found_default_is_none(self):
        driver, _ = make_driver()
        assert driver.on_not_found is None

    def test_on_not_found_setter(self):
        driver, _ = make_driver()
        handler = lambda cmd: None
        driver.on_not_found = handler
        assert driver.on_not_found is handler

    def test_on_not_found_can_be_cleared(self):
        driver, _ = make_driver()
        driver.on_not_found = lambda cmd: None
        driver.on_not_found = None
        assert driver.on_not_found is None
