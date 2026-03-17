"""OpenShellDriver: resolves to OpenShellGrpcDriver when grpcio is available."""

from __future__ import annotations

import shutil
from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory


class OpenShellDriver:
    """Resolves the openshell driver (requires ssh binary)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return an OpenShellGrpcDriver if ssh is available."""
        # Allow test override to skip dependency check
        grpc_override = kwargs.get("grpc_override")
        if grpc_override is not None:
            from src.python.openshell_grpc_driver import OpenShellGrpcDriver
            return OpenShellGrpcDriver(**kwargs)
        if shutil.which("ssh") is None:
            raise RuntimeError(
                "ssh not found — OpenShell driver requires SSH for execution"
            )
        from src.python.openshell_grpc_driver import OpenShellGrpcDriver
        return OpenShellGrpcDriver(**kwargs)


def register_openshell_driver() -> None:
    """Register the 'openshell' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("openshell", lambda **kw: OpenShellDriver.resolve(**kw))
