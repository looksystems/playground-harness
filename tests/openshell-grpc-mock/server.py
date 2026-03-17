"""Minimal mock gRPC server implementing the OpenShell service.

Used for integration testing. Runs commands via subprocess in the local
environment (or a sibling sandbox container when running under docker-compose).
"""

from __future__ import annotations

import json
import subprocess
import sys
import uuid
from concurrent import futures
from dataclasses import asdict

import grpc

sys.path.insert(0, "/app")
from src.python.generated.openshell import (
    sandbox_pb2,
    openshell_pb2,
    datamodel_pb2,
)


def _convert_for_json(obj):
    """Recursively convert bytes to base64 strings for JSON serialization."""
    if isinstance(obj, bytes):
        import base64
        return base64.b64encode(obj).decode("ascii")
    if isinstance(obj, dict):
        return {k: _convert_for_json(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [_convert_for_json(item) for item in obj]
    return obj


def _serialize(msg) -> bytes:
    d = asdict(msg)
    return json.dumps(_convert_for_json(d)).encode()


def _deserialize_create(data: bytes):
    d = json.loads(data.decode())
    spec = None
    if d.get("spec"):
        spec = datamodel_pb2.SandboxSpec(**{
            k: v for k, v in d["spec"].items()
            if k in ("image", "workspace", "env")
        })
    return sandbox_pb2.CreateSandboxRequest(name=d.get("name", ""), spec=spec)


def _deserialize_delete(data: bytes):
    d = json.loads(data.decode())
    return sandbox_pb2.DeleteSandboxRequest(name=d.get("name", ""))


def _deserialize_get(data: bytes):
    d = json.loads(data.decode())
    return sandbox_pb2.GetSandboxRequest(name=d.get("name", ""))


def _deserialize_exec(data: bytes):
    d = json.loads(data.decode())
    return sandbox_pb2.ExecSandboxRequest(
        sandbox_id=d.get("sandbox_id", ""),
        command=d.get("command", []),
        env=d.get("env", {}),
        working_dir=d.get("working_dir", ""),
    )


def _deserialize_health(data: bytes):
    return openshell_pb2.HealthRequest()


class OpenShellServicer:
    """Mock implementation of the OpenShell gRPC service."""

    def __init__(self):
        self._sandboxes: dict[str, datamodel_pb2.Sandbox] = {}

    def Health(self, request, context):
        return openshell_pb2.HealthResponse(status=1)

    def CreateSandbox(self, request, context):
        sandbox_id = f"sandbox-{uuid.uuid4().hex[:8]}"
        sandbox = datamodel_pb2.Sandbox(
            name=request.name,
            sandbox_id=sandbox_id,
            status=datamodel_pb2.SandboxStatus(phase=2),
        )
        self._sandboxes[sandbox_id] = sandbox
        return sandbox_pb2.CreateSandboxResponse(sandbox=sandbox)

    def GetSandbox(self, request, context):
        for sandbox in self._sandboxes.values():
            if sandbox.name == request.name:
                return sandbox_pb2.GetSandboxResponse(sandbox=sandbox)
        context.set_code(grpc.StatusCode.NOT_FOUND)
        context.set_details(f"Sandbox {request.name} not found")
        return sandbox_pb2.GetSandboxResponse()

    def DeleteSandbox(self, request, context):
        to_delete = None
        for sid, sandbox in self._sandboxes.items():
            if sandbox.name == request.name or sid == request.name:
                to_delete = sid
                break
        if to_delete:
            del self._sandboxes[to_delete]
        return sandbox_pb2.DeleteSandboxResponse()

    def ExecSandbox(self, request, context):
        cmd = request.command
        if not cmd:
            yield sandbox_pb2.ExecSandboxEvent(
                exit=sandbox_pb2.ExitStatus(code=1)
            )
            return

        try:
            result = subprocess.run(
                cmd,
                capture_output=True,
                timeout=30,
            )
            if result.stdout:
                yield sandbox_pb2.ExecSandboxEvent(
                    stdout=sandbox_pb2.StdoutChunk(data=result.stdout)
                )
            if result.stderr:
                yield sandbox_pb2.ExecSandboxEvent(
                    stderr=sandbox_pb2.StderrChunk(data=result.stderr)
                )
            yield sandbox_pb2.ExecSandboxEvent(
                exit=sandbox_pb2.ExitStatus(code=result.returncode)
            )
        except Exception as e:
            yield sandbox_pb2.ExecSandboxEvent(
                stderr=sandbox_pb2.StderrChunk(data=str(e).encode())
            )
            yield sandbox_pb2.ExecSandboxEvent(
                exit=sandbox_pb2.ExitStatus(code=1)
            )


class OpenShellGenericHandler(grpc.GenericRpcHandler):
    """Routes RPC calls to the servicer methods."""

    def __init__(self, servicer: OpenShellServicer):
        self._servicer = servicer
        self._handlers = {
            "/openshell.OpenShell/Health": grpc.unary_unary_rpc_method_handler(
                servicer.Health,
                request_deserializer=_deserialize_health,
                response_serializer=_serialize,
            ),
            "/openshell.OpenShell/CreateSandbox": grpc.unary_unary_rpc_method_handler(
                servicer.CreateSandbox,
                request_deserializer=_deserialize_create,
                response_serializer=_serialize,
            ),
            "/openshell.OpenShell/GetSandbox": grpc.unary_unary_rpc_method_handler(
                servicer.GetSandbox,
                request_deserializer=_deserialize_get,
                response_serializer=_serialize,
            ),
            "/openshell.OpenShell/DeleteSandbox": grpc.unary_unary_rpc_method_handler(
                servicer.DeleteSandbox,
                request_deserializer=_deserialize_delete,
                response_serializer=_serialize,
            ),
            "/openshell.OpenShell/ExecSandbox": grpc.unary_stream_rpc_method_handler(
                servicer.ExecSandbox,
                request_deserializer=_deserialize_exec,
                response_serializer=_serialize,
            ),
        }

    def service(self, handler_call_details):
        return self._handlers.get(handler_call_details.method)


def serve(port: int = 50051) -> None:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    servicer = OpenShellServicer()
    server.add_generic_rpc_handlers([OpenShellGenericHandler(servicer)])
    server.add_insecure_port(f"[::]:{port}")
    server.start()
    print(f"Mock OpenShell gRPC server listening on port {port}", flush=True)
    server.wait_for_termination()


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 50051
    serve(port)
