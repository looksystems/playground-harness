"""Hand-written stubs matching openshell/openshell.proto.

Provides Health message classes. Run `make proto` to regenerate from
vendored .proto files when grpcio-tools is available.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass
class HealthRequest:
    pass


class _ServingStatus:
    UNKNOWN = 0
    SERVING = 1
    NOT_SERVING = 2


@dataclass
class HealthResponse:
    status: int = 0

    ServingStatus = _ServingStatus
