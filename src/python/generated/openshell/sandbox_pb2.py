"""Hand-written stubs matching openshell/sandbox.proto.

Provides request/response message classes for the OpenShell gRPC service.
Run `make proto` to regenerate from vendored .proto files when grpcio-tools
is available.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .datamodel_pb2 import Sandbox, SandboxSpec


@dataclass
class CreateSandboxRequest:
    name: str = ""
    spec: SandboxSpec = field(default_factory=SandboxSpec)


@dataclass
class CreateSandboxResponse:
    sandbox: Sandbox = field(default_factory=Sandbox)


@dataclass
class DeleteSandboxRequest:
    name: str = ""


@dataclass
class DeleteSandboxResponse:
    pass


@dataclass
class GetSandboxRequest:
    name: str = ""


@dataclass
class GetSandboxResponse:
    sandbox: Sandbox = field(default_factory=Sandbox)


@dataclass
class ExecSandboxRequest:
    sandbox_id: str = ""
    command: list[str] = field(default_factory=list)
    env: dict[str, str] = field(default_factory=dict)
    working_dir: str = ""


@dataclass
class StdoutChunk:
    data: bytes = b""


@dataclass
class StderrChunk:
    data: bytes = b""


@dataclass
class ExitStatus:
    code: int = 0


@dataclass
class ExecSandboxEvent:
    stdout: StdoutChunk | None = None
    stderr: StderrChunk | None = None
    exit: ExitStatus | None = None
