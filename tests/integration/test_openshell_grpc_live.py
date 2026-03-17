"""Integration tests for OpenShellGrpcDriver with a live gRPC server.

These tests require a running OpenShell gRPC mock server. Set the
OPENSHELL_ENDPOINT environment variable to enable them:

    docker compose up -d
    OPENSHELL_ENDPOINT=localhost:50051 pytest tests/integration/test_openshell_grpc_live.py -v
    docker compose down

Tests are automatically skipped when OPENSHELL_ENDPOINT is not set.
"""

from __future__ import annotations

import os

import pytest

from src.python.openshell_grpc_driver import OpenShellGrpcDriver, OpenShellPolicy, ExecStreamEvent

ENDPOINT = os.environ.get("OPENSHELL_ENDPOINT")

pytestmark = pytest.mark.skipif(
    ENDPOINT is None,
    reason="OPENSHELL_ENDPOINT not set — skipping live gRPC integration tests",
)


@pytest.fixture
def driver():
    d = OpenShellGrpcDriver(
        endpoint=ENDPOINT,
        transport="grpc",
        policy=OpenShellPolicy(inference_routing=False),
    )
    yield d
    d.close()


class TestGrpcLiveBasic:
    """Basic gRPC operations against a live server."""

    def test_create_and_exec(self, driver):
        result = driver.exec("echo hello")
        assert "hello" in result.stdout
        assert result.exit_code == 0
        assert driver.sandbox_id is not None

    def test_exec_exit_code(self, driver):
        result = driver.exec("exit 42")
        assert result.exit_code == 42

    def test_exec_stderr(self, driver):
        result = driver.exec("echo error >&2")
        assert "error" in result.stderr

    def test_close_deletes_sandbox(self, driver):
        driver.exec("echo hello")
        assert driver.sandbox_id is not None
        driver.close()
        assert driver.sandbox_id is None


class TestGrpcLiveVfs:
    """VFS sync through gRPC transport."""

    def test_vfs_write_and_read_back(self, driver):
        driver.fs.write("/test.txt", "content")
        # VFS sync writes to workspace path on the remote
        result = driver.exec("cat /home/sandbox/workspace/test.txt")
        assert result.exit_code == 0
        assert "content" in result.stdout

    def test_vfs_sync_back(self, driver):
        result = driver.exec("echo 'created' > /tmp/output.txt && cat /tmp/output.txt")
        assert result.exit_code == 0


class TestGrpcLiveStream:
    """execStream through gRPC transport."""

    def test_exec_stream_yields_events(self, driver):
        events = list(driver.exec_stream("echo streamed"))
        types = [e.type for e in events]
        assert "stdout" in types
        assert "exit" in types

    def test_exec_stream_stdout_content(self, driver):
        events = list(driver.exec_stream("echo hello"))
        stdout_data = "".join(e.data for e in events if e.type == "stdout")
        assert "hello" in stdout_data

    def test_exec_stream_exit_code(self, driver):
        events = list(driver.exec_stream("exit 7"))
        exit_events = [e for e in events if e.type == "exit"]
        assert exit_events[0].exit_code == 7
