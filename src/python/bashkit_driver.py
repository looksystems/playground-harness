"""BashkitDriver: resolves to BashkitPythonDriver when bashkit package is available."""

from __future__ import annotations

from typing import Any

from src.python.drivers import ShellDriver, ShellDriverFactory


class BashkitDriver:
    """Resolves the bashkit driver (requires bashkit Python package)."""

    @staticmethod
    def resolve(**kwargs: Any) -> ShellDriver:
        """Return a BashkitPythonDriver if bashkit is installed."""
        # Allow test override to skip import check
        bash_override = kwargs.get("bash_override")
        if bash_override is not None:
            from src.python.bashkit_python_driver import BashkitPythonDriver
            return BashkitPythonDriver(**kwargs)
        try:
            import bashkit  # noqa: F401
        except ImportError:
            raise RuntimeError(
                "bashkit not found — install with: pip install bashkit"
            )
        from src.python.bashkit_python_driver import BashkitPythonDriver
        return BashkitPythonDriver(**kwargs)


def register_bashkit_driver() -> None:
    """Register the 'bashkit' driver with the ShellDriverFactory."""
    ShellDriverFactory.register("bashkit", lambda **kw: BashkitDriver.resolve(**kw))
