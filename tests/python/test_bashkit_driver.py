"""Tests for BashkitDriver resolver and factory registration."""

from __future__ import annotations

from unittest.mock import patch, MagicMock

import pytest

from src.python.bashkit_driver import BashkitDriver, register_bashkit_driver
from src.python.bashkit_python_driver import BashkitPythonDriver
from src.python.drivers import ShellDriver, ShellDriverFactory


class MockResult:
    """Simulates bashkit ExecResult."""

    def __init__(self, stdout="", stderr="", exit_code=0, error=None):
        self.stdout = stdout
        self.stderr = stderr
        self.exit_code = exit_code
        self.error = error


class MockBash:
    """Simulates bashkit.Bash for testing."""

    def __init__(self):
        self.calls: list[str] = []
        self.next_result = MockResult()

    def execute_sync(self, commands: str) -> MockResult:
        self.calls.append(commands)
        return self.next_result

    def reset(self) -> None:
        pass


@pytest.fixture(autouse=True)
def _reset_factory():
    """Reset ShellDriverFactory before and after each test."""
    ShellDriverFactory.reset()
    yield
    ShellDriverFactory.reset()


class TestBashkitDriverResolve:
    """Tests for BashkitDriver.resolve()."""

    def test_resolve_with_bash_override_returns_python_driver(self):
        mock = MockBash()
        driver = BashkitDriver.resolve(bash_override=mock)
        assert isinstance(driver, BashkitPythonDriver)
        assert isinstance(driver, ShellDriver)

    def test_resolve_with_bash_override_passes_kwargs(self):
        mock = MockBash()
        driver = BashkitDriver.resolve(bash_override=mock, cwd="/tmp", env={"FOO": "bar"})
        assert driver.cwd == "/tmp"
        assert driver.env == {"FOO": "bar"}

    def test_resolve_without_bashkit_raises_runtime_error(self):
        with patch.dict("sys.modules", {"bashkit": None}):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                BashkitDriver.resolve()

    def test_resolve_import_error_raises_runtime_error(self):
        """When bashkit import raises ImportError, resolve raises RuntimeError."""
        with patch("builtins.__import__", side_effect=_make_import_blocker("bashkit")):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                BashkitDriver.resolve()


class TestRegisterBashkitDriver:
    """Tests for register_bashkit_driver() and factory integration."""

    def test_register_adds_to_factory(self):
        register_bashkit_driver()
        # "bashkit" should now be in the registry
        # Verify by checking that create raises RuntimeError (not KeyError)
        with patch("builtins.__import__", side_effect=_make_import_blocker("bashkit")):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                ShellDriverFactory.create("bashkit")

    def test_factory_create_with_bash_override_returns_driver(self):
        register_bashkit_driver()
        mock = MockBash()
        driver = ShellDriverFactory.create("bashkit", bash_override=mock)
        assert isinstance(driver, BashkitPythonDriver)

    def test_factory_create_without_registration_raises_key_error(self):
        with pytest.raises(KeyError, match="not registered"):
            ShellDriverFactory.create("bashkit")

    def test_factory_create_after_register_raises_runtime_not_key_error(self):
        """After registration, create('bashkit') should raise RuntimeError, not KeyError."""
        register_bashkit_driver()
        with patch("builtins.__import__", side_effect=_make_import_blocker("bashkit")):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                ShellDriverFactory.create("bashkit")


def _make_import_blocker(blocked_module: str):
    """Create an import side_effect that blocks a specific module."""
    real_import = __builtins__.__import__ if hasattr(__builtins__, "__import__") else __import__

    def blocker(name, *args, **kwargs):
        if name == blocked_module:
            raise ImportError(f"No module named '{blocked_module}'")
        return real_import(name, *args, **kwargs)

    return blocker
