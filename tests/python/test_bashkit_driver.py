"""Tests for BashkitDriver resolver and factory registration."""

from __future__ import annotations

from unittest.mock import patch, MagicMock

import pytest

from src.python.bashkit_driver import BashkitDriver, register_bashkit_driver
from src.python.bashkit_ipc_driver import BashkitIPCDriver
from src.python.drivers import ShellDriver, ShellDriverFactory


@pytest.fixture(autouse=True)
def _reset_factory():
    """Reset ShellDriverFactory before and after each test."""
    ShellDriverFactory.reset()
    yield
    ShellDriverFactory.reset()


class TestBashkitDriverResolve:
    """Tests for BashkitDriver.resolve()."""

    @patch("src.python.bashkit_driver.shutil.which", return_value="/usr/local/bin/bashkit-cli")
    @patch.object(BashkitIPCDriver, "_spawn", return_value=MagicMock())
    def test_resolve_returns_ipc_when_cli_available(self, mock_spawn, mock_which):
        driver = BashkitDriver.resolve()
        assert isinstance(driver, BashkitIPCDriver)
        assert isinstance(driver, ShellDriver)
        mock_which.assert_called_once_with("bashkit-cli")

    @patch("src.python.bashkit_driver.shutil.which", return_value=None)
    def test_resolve_raises_when_cli_not_found(self, mock_which):
        with pytest.raises(RuntimeError, match="bashkit not found"):
            BashkitDriver.resolve()

    @patch("src.python.bashkit_driver.shutil.which", return_value="/usr/local/bin/bashkit-cli")
    @patch.object(BashkitIPCDriver, "_spawn", return_value=MagicMock())
    def test_resolve_passes_kwargs_to_ipc_driver(self, mock_spawn, mock_which):
        driver = BashkitDriver.resolve(cwd="/tmp", env={"FOO": "bar"})
        assert driver.cwd == "/tmp"
        assert driver.env == {"FOO": "bar"}


class TestRegisterBashkitDriver:
    """Tests for register_bashkit_driver() and factory integration."""

    def test_register_adds_to_factory(self):
        register_bashkit_driver()
        # "bashkit" should now be in the registry (create would work)
        # We verify by checking that create doesn't raise KeyError
        with patch("src.python.bashkit_driver.shutil.which", return_value=None):
            with pytest.raises(RuntimeError, match="bashkit not found"):
                ShellDriverFactory.create("bashkit")

    @patch("src.python.bashkit_driver.shutil.which", return_value="/usr/local/bin/bashkit-cli")
    @patch.object(BashkitIPCDriver, "_spawn", return_value=MagicMock())
    def test_factory_create_returns_driver(self, mock_spawn, mock_which):
        register_bashkit_driver()
        driver = ShellDriverFactory.create("bashkit")
        assert isinstance(driver, BashkitIPCDriver)

    def test_factory_create_without_registration_raises_key_error(self):
        with pytest.raises(KeyError, match="not registered"):
            ShellDriverFactory.create("bashkit")

    @patch("src.python.bashkit_driver.shutil.which", return_value=None)
    def test_factory_create_after_register_raises_runtime_not_key_error(self, mock_which):
        """After registration, create('bashkit') should raise RuntimeError, not KeyError."""
        register_bashkit_driver()
        with pytest.raises(RuntimeError, match="bashkit not found"):
            ShellDriverFactory.create("bashkit")
