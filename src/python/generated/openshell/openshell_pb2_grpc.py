"""Hand-written gRPC stub for the OpenShell service.

Provides OpenShellStub for use with grpc.Channel. Run `make proto` to
regenerate from vendored .proto files when grpcio-tools is available.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    import grpc

from . import sandbox_pb2, openshell_pb2


class OpenShellStub:
    """Client stub for the openshell.OpenShell service."""

    def __init__(self, channel: grpc.Channel) -> None:
        import grpc

        self.CreateSandbox = channel.unary_unary(
            "/openshell.OpenShell/CreateSandbox",
            request_serializer=_serialize,
            response_deserializer=_make_deserializer(sandbox_pb2.CreateSandboxResponse),
        )
        self.DeleteSandbox = channel.unary_unary(
            "/openshell.OpenShell/DeleteSandbox",
            request_serializer=_serialize,
            response_deserializer=_make_deserializer(sandbox_pb2.DeleteSandboxResponse),
        )
        self.GetSandbox = channel.unary_unary(
            "/openshell.OpenShell/GetSandbox",
            request_serializer=_serialize,
            response_deserializer=_make_deserializer(sandbox_pb2.GetSandboxResponse),
        )
        self.ExecSandbox = channel.unary_stream(
            "/openshell.OpenShell/ExecSandbox",
            request_serializer=_serialize,
            response_deserializer=_make_deserializer(sandbox_pb2.ExecSandboxEvent),
        )
        self.Health = channel.unary_unary(
            "/openshell.OpenShell/Health",
            request_serializer=_serialize,
            response_deserializer=_make_deserializer(openshell_pb2.HealthResponse),
        )


def _serialize(message: object) -> bytes:
    """Serialize a dataclass message to bytes.

    When using real protobuf messages (from `make proto`), this calls
    SerializeToString(). For the hand-written dataclasses, we use a
    simple JSON-based wire format that the mock server understands.
    """
    if hasattr(message, "SerializeToString"):
        return message.SerializeToString()
    import json
    from dataclasses import asdict
    return json.dumps(asdict(message)).encode()


def _make_deserializer(cls: type):
    """Create a deserializer for the given message class."""
    def _deserialize(data: bytes) -> object:
        if hasattr(cls, "FromString"):
            return cls.FromString(data)
        import json
        d = json.loads(data.decode())
        return cls(**d)
    return _deserialize
