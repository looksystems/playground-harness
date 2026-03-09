"""Live integration tests for BashkitPythonDriver against real bashkit."""

import pytest
from src.python.bashkit_python_driver import BashkitPythonDriver
from src.python.shell import ExecResult

try:
    import bashkit
    HAS_BASHKIT = True
except ImportError:
    HAS_BASHKIT = False

pytestmark = pytest.mark.skipif(not HAS_BASHKIT, reason="bashkit not installed")


@pytest.fixture
def driver():
    return BashkitPythonDriver()


class TestBasicExecution:
    def test_echo(self, driver):
        r = driver.exec("echo hello world")
        assert r.stdout.strip() == "hello world"
        assert r.exit_code == 0

    def test_pipes(self, driver):
        r = driver.exec("echo hello | tr a-z A-Z")
        assert r.stdout.strip() == "HELLO"

    def test_exit_code_on_failure(self, driver):
        r = driver.exec("false")
        assert r.exit_code != 0

    def test_variable_expansion(self, driver):
        r = driver.exec("X=42; echo $X")
        assert r.stdout.strip() == "42"

    def test_redirects(self, driver):
        r = driver.exec("echo written > /out.txt && cat /out.txt")
        assert r.stdout.strip() == "written"

    def test_for_loop(self, driver):
        r = driver.exec("for i in 1 2 3; do echo $i; done")
        assert r.stdout.strip() == "1\n2\n3"

    def test_stderr_capture(self, driver):
        r = driver.exec("echo err >&2")
        assert "err" in r.stderr


class TestStatePersistence:
    def test_variable_persists_between_exec(self, driver):
        driver.exec("MY_VAR=persistent")
        r = driver.exec("echo $MY_VAR")
        assert r.stdout.strip() == "persistent"

    def test_function_persists_between_exec(self, driver):
        driver.exec("greet() { echo Hello $1; }")
        r = driver.exec("greet World")
        assert r.stdout.strip() == "Hello World"


class TestPOSIXBuiltins:
    def test_seq_wc(self, driver):
        r = driver.exec("seq 1 5 | wc -l")
        assert r.stdout.strip() == "5"

    def test_head(self, driver):
        r = driver.exec("seq 1 10 | head -3")
        assert r.stdout.strip() == "1\n2\n3"

    def test_tail(self, driver):
        r = driver.exec("seq 1 5 | tail -2")
        assert r.stdout.strip() == "4\n5"

    def test_sort(self, driver):
        r = driver.exec("printf '3\n1\n2\n' | sort")
        assert r.stdout.strip() == "1\n2\n3"

    def test_uniq(self, driver):
        r = driver.exec("printf 'a\na\nb\n' | uniq")
        assert r.stdout.strip() == "a\nb"

    def test_grep(self, driver):
        r = driver.exec("printf 'foo\nbar\nbaz\n' | grep ba")
        assert r.stdout.strip() == "bar\nbaz"

    def test_cut(self, driver):
        r = driver.exec("echo 'a:b:c' | cut -d: -f2")
        assert r.stdout.strip() == "b"

    def test_tr(self, driver):
        r = driver.exec("echo 'hello' | tr -d l")
        assert r.stdout.strip() == "heo"

    def test_basename(self, driver):
        r = driver.exec("basename /foo/bar/baz.txt")
        assert r.stdout.strip() == "baz.txt"

    def test_dirname(self, driver):
        r = driver.exec("dirname /foo/bar/baz.txt")
        assert r.stdout.strip() == "/foo/bar"


class TestVFSSync:
    def test_write_in_vfs_read_in_bashkit(self, driver):
        driver.fs.write("/test.txt", "hello from vfs")
        r = driver.exec("cat /test.txt")
        assert r.stdout.strip() == "hello from vfs"

    def test_write_in_bashkit_read_back_via_vfs(self, driver):
        driver.exec("echo 'from bashkit' > /created.txt")
        assert driver.fs.exists("/created.txt")
        assert "from bashkit" in driver.fs.read_text("/created.txt")

    def test_write_in_bashkit_read_back(self, driver):
        driver.exec("echo 'from bashkit' > /created.txt")
        r = driver.exec("cat /created.txt")
        assert r.stdout.strip() == "from bashkit"

    def test_multiline_file_sync(self, driver):
        content = "line1\nline2\nline3\n"
        driver.fs.write("/multi.txt", content)
        r = driver.exec("cat /multi.txt | wc -l")
        assert r.stdout.strip() == "3"

    def test_round_trip_vfs_to_bashkit_to_vfs(self, driver):
        driver.fs.write("/round.txt", "original")
        driver.exec("cat /round.txt | tr a-z A-Z > /upper.txt")
        assert driver.fs.exists("/upper.txt")
        assert driver.fs.read_text("/upper.txt").strip() == "ORIGINAL"

    def test_special_chars_sync(self, driver):
        content = "quotes'here\nback\\slash\n%percent"
        driver.fs.write("/special.txt", content)
        r = driver.exec("cat /special.txt")
        assert r.stdout == content


class TestCustomCommands:
    def test_register_and_exec_with_flags(self, driver):
        """ScriptedTool commands use --flag value syntax."""
        driver.register_command(
            "greet",
            lambda args, stdin="": ExecResult(stdout=f"Hello {' '.join(args)}!\n"),
        )
        r = driver.exec("greet --name world")
        assert "Hello --name world!" in r.stdout

    def test_register_and_exec_via_stdin(self, driver):
        """Positional data can be passed via pipe/stdin."""
        driver.register_command(
            "greet",
            lambda args, stdin="": ExecResult(stdout=f"Hello {stdin.strip()}!\n"),
        )
        r = driver.exec("echo world | greet")
        assert "Hello world!" in r.stdout

    def test_custom_command_with_pipe(self, driver):
        driver.register_command(
            "upper",
            lambda args, stdin="": ExecResult(stdout=stdin.upper()),
        )
        r = driver.exec("echo hello | upper")
        assert "HELLO" in r.stdout

    def test_unregister_command(self, driver):
        driver.register_command(
            "tmp",
            lambda args, stdin="": ExecResult(stdout="tmp\n"),
        )
        r = driver.exec("tmp")
        assert "tmp" in r.stdout
        driver.unregister_command("tmp")
        r = driver.exec("tmp")
        assert r.exit_code != 0 or "tmp" not in r.stdout


class TestClone:
    def test_cloned_driver_independent(self, driver):
        driver.exec("ORIG=yes")
        cloned = driver.clone()
        cloned.exec("CLONE=yes")
        r1 = driver.exec("echo $CLONE")
        r2 = cloned.exec("echo $ORIG")
        # Each has their own bashkit instance
        assert r1.stdout.strip() != "yes" or r2.stdout.strip() != "yes"
