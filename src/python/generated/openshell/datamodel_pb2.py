"""Hand-written stubs matching openshell/datamodel.proto.

These provide the message classes used by the driver without requiring
protoc. When grpcio-tools is installed, run `make proto` to regenerate
from the vendored .proto files.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class FilesystemPolicy:
    read_only: list[str] = field(default_factory=list)
    read_write: list[str] = field(default_factory=list)


@dataclass
class NetworkPolicy:
    cidr: str = ""
    action: int = 0  # 0=ALLOW, 1=DENY

    ALLOW = 0
    DENY = 1


@dataclass
class InferencePolicy:
    routing_enabled: bool = False
    provider: str = ""


@dataclass
class SandboxPolicy:
    filesystem: FilesystemPolicy = field(default_factory=FilesystemPolicy)
    network_policies: list[NetworkPolicy] = field(default_factory=list)
    inference: InferencePolicy = field(default_factory=InferencePolicy)


@dataclass
class SandboxSpec:
    image: str = ""
    policy: SandboxPolicy = field(default_factory=SandboxPolicy)
    env: dict[str, str] = field(default_factory=dict)
    workspace: str = ""


class _SandboxStatusPhase:
    UNKNOWN = 0
    PENDING = 1
    RUNNING = 2
    STOPPED = 3
    FAILED = 4


@dataclass
class SandboxStatus:
    phase: int = 0
    message: str = ""

    Phase = _SandboxStatusPhase


@dataclass
class Sandbox:
    name: str = ""
    sandbox_id: str = ""
    spec: SandboxSpec = field(default_factory=SandboxSpec)
    status: SandboxStatus = field(default_factory=SandboxStatus)
