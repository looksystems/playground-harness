"""OpenShellDriver: resolves to OpenShellGrpcDriver when grpcio is available."""

from __future__ import annotations

from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory


class OpenShellDriver:
    """Resolves the openshell driver (requires grpcio package)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return an OpenShellGrpcDriver if grpcio is installed."""
        # Allow test override to skip import check
        grpc_override = kwargs.get("grpc_override")
        if grpc_override is not None:
            from src.python.openshell_grpc_driver import OpenShellGrpcDriver
            return OpenShellGrpcDriver(**kwargs)
        try:
            import grpc  # noqa: F401
        except ImportError:
            raise RuntimeError(
                "grpcio not found — install with: pip install agent-harness[openshell]"
            )
        from src.python.openshell_grpc_driver import OpenShellGrpcDriver
        return OpenShellGrpcDriver(**kwargs)


def register_openshell_driver() -> None:
    """Register the 'openshell' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("openshell", lambda **kw: OpenShellDriver.resolve(**kw))
