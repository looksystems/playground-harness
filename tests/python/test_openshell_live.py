"""Live integration tests for OpenShellGrpcDriver against a running OpenShell instance.

Skipped by default. To run, start an OpenShell instance on localhost:50051
and set the OPENSHELL_ENDPOINT env var (or leave it for the default).
"""

import os
import socket

import pytest
from src.python.openshell_grpc_driver import OpenShellGrpcDriver, OpenShellPolicy
from src.python.shell import ExecResult


def _openshell_reachable() -> bool:
    """Check if an OpenShell sandbox SSH endpoint is listening."""
    host = os.environ.get("OPENSHELL_SSH_HOST", "localhost")
    port = int(os.environ.get("OPENSHELL_SSH_PORT", "2222"))
    try:
        with socket.create_connection((host, port), timeout=1):
            return True
    except (OSError, ValueError):
        return False


HAS_OPENSHELL = _openshell_reachable()

pytestmark = pytest.mark.skipif(
    not HAS_OPENSHELL,
    reason="OpenShell sandbox not running (set OPENSHELL_SSH_HOST/PORT or start on localhost:2222)",
)


WORKSPACE = "/tmp/harness"


@pytest.fixture
def driver():
    d = OpenShellGrpcDriver(
        ssh_host=os.environ.get("OPENSHELL_SSH_HOST", "localhost"),
        ssh_port=int(os.environ.get("OPENSHELL_SSH_PORT", "2222")),
        ssh_user=os.environ.get("OPENSHELL_SSH_USER", "sandbox"),
        workspace=WORKSPACE,
    )
    yield d
    d.close()


class TestBasicExecution:
    """Create sandbox, exec commands, verify output."""

    def test_echo(self, driver):
        r = driver.exec("echo hello world")
        assert r.stdout.strip() == "hello world"
        assert r.exit_code == 0

    def test_pipes(self, driver):
        r = driver.exec("echo hello | tr a-z A-Z")
        assert r.stdout.strip() == "HELLO"

    def test_exit_code_on_failure(self, driver):
        r = driver.exec("false")
        assert r.exit_code != 0

    def test_variable_expansion(self, driver):
        r = driver.exec("X=42; echo $X")
        assert r.stdout.strip() == "42"

    def test_redirects(self, driver):
        r = driver.exec("echo written > /tmp/out.txt && cat /tmp/out.txt")
        assert r.stdout.strip() == "written"

    def test_for_loop(self, driver):
        r = driver.exec("for i in 1 2 3; do echo $i; done")
        assert r.stdout.strip() == "1\n2\n3"

    def test_stderr_capture(self, driver):
        r = driver.exec("echo err >&2")
        assert "err" in r.stderr


class TestVFSSync:
    """VFS sync round-trip.

    VFS paths (e.g. /test.txt) are remapped to the workspace directory on the
    remote host (e.g. /tmp/harness/test.txt). Shell commands must reference
    the workspace path.
    """

    def test_write_in_vfs_read_in_sandbox(self, driver):
        driver.fs.write("/test.txt", "hello from vfs")
        r = driver.exec(f"cat {WORKSPACE}/test.txt")
        assert r.stdout.strip() == "hello from vfs"

    def test_write_in_sandbox_read_back_via_vfs(self, driver):
        driver.exec(f"mkdir -p {WORKSPACE} && echo 'from sandbox' > {WORKSPACE}/created.txt")
        assert driver.fs.exists("/created.txt")
        assert "from sandbox" in driver.fs.read_text("/created.txt")

    def test_round_trip_vfs_to_sandbox_to_vfs(self, driver):
        driver.fs.write("/round.txt", "original")
        driver.exec(f"cat {WORKSPACE}/round.txt | tr a-z A-Z > {WORKSPACE}/upper.txt")
        assert driver.fs.exists("/upper.txt")
        assert driver.fs.read_text("/upper.txt").strip() == "ORIGINAL"

    def test_multiline_file_sync(self, driver):
        content = "line1\nline2\nline3\n"
        driver.fs.write("/multi.txt", content)
        r = driver.exec(f"cat {WORKSPACE}/multi.txt | wc -l")
        assert r.stdout.strip() == "3"

    def test_special_chars_sync(self, driver):
        content = "quotes'here\nback\\slash\n%percent"
        driver.fs.write("/special.txt", content)
        r = driver.exec(f"cat {WORKSPACE}/special.txt")
        assert r.stdout == content


class TestPolicyEnforcement:
    """Policy enforcement (requires OpenShell policy support)."""

    def test_default_policy_allows_exec(self, driver):
        r = driver.exec("echo allowed")
        assert r.exit_code == 0

    def test_policy_accessible(self, driver):
        assert driver.policy is not None
        assert driver.policy.inference_routing is True

    def test_custom_policy_passed_to_sandbox(self):
        policy = OpenShellPolicy(
            filesystem_allow=["/tmp"],
            network_rules=[{"deny": "0.0.0.0/0"}],
            inference_routing=False,
        )
        d = OpenShellGrpcDriver(
            ssh_host=os.environ.get("OPENSHELL_SSH_HOST", "localhost"),
            ssh_port=int(os.environ.get("OPENSHELL_SSH_PORT", "2222")),
            ssh_user=os.environ.get("OPENSHELL_SSH_USER", "sandbox"),
            workspace=WORKSPACE,
            policy=policy,
        )
        try:
            assert d.policy.filesystem_allow == ["/tmp"]
            assert d.policy.inference_routing is False
        finally:
            d.close()


class TestLifecycle:
    """Cleanup on close."""

    def test_close_clears_sandbox(self, driver):
        driver.exec("echo hello")
        assert driver.sandbox_id is not None
        driver.close()
        assert driver.sandbox_id is None

    def test_context_manager_cleanup(self):
        with OpenShellGrpcDriver(
            ssh_host=os.environ.get("OPENSHELL_SSH_HOST", "localhost"),
            ssh_port=int(os.environ.get("OPENSHELL_SSH_PORT", "2222")),
            ssh_user=os.environ.get("OPENSHELL_SSH_USER", "sandbox"),
            workspace=WORKSPACE,
        ) as d:
            d.exec("echo hello")
            sandbox_id = d.sandbox_id
        assert d.sandbox_id is None

    def test_clone_creates_independent_sandbox(self, driver):
        driver.fs.write("/orig.txt", "original")
        driver.exec("echo hello")  # syncs orig.txt to remote, then back to VFS
        cloned = driver.clone()
        try:
            assert cloned.sandbox_id is None  # lazy, not created yet
            cloned.exec("echo from clone")
            assert cloned.sandbox_id is not None
            # VFS cloned
            assert cloned.fs.exists("/orig.txt")
            # Independence
            cloned.fs.write("/clone_only.txt", "clone")
            assert not driver.fs.exists("/clone_only.txt")
        finally:
            cloned.close()


class TestCapabilities:
    """Driver capability query."""

    def test_capabilities_include_remote(self, driver):
        caps = driver.capabilities()
        assert "remote" in caps
        assert "policies" in caps
        assert "streaming" in caps
