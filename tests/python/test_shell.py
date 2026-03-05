import json
import pytest
from src.python.virtual_fs import VirtualFS
from src.python.shell import Shell, ExecResult, ShellRegistry


class TestShell:
    def setup_method(self):
        self.fs = VirtualFS()
        self.fs.write("/data/hello.txt", "hello world\n")
        self.fs.write("/data/nums.txt", "3\n1\n2\n1\n")
        self.fs.write("/data/users.json", json.dumps([
            {"name": "Alice", "role": "admin"},
            {"name": "Bob", "role": "user"},
        ], indent=2))
        self.shell = Shell(self.fs)

    def test_cat(self):
        r = self.shell.exec("cat /data/hello.txt")
        assert r.stdout == "hello world\n"
        assert r.exit_code == 0

    def test_cat_nonexistent(self):
        r = self.shell.exec("cat /nope")
        assert r.exit_code == 1

    def test_echo(self):
        r = self.shell.exec("echo hello world")
        assert r.stdout == "hello world\n"

    def test_ls(self):
        r = self.shell.exec("ls /data")
        assert "hello.txt" in r.stdout
        assert "users.json" in r.stdout

    def test_grep(self):
        r = self.shell.exec("grep admin /data/users.json")
        assert "admin" in r.stdout
        assert r.exit_code == 0

    def test_grep_no_match(self):
        r = self.shell.exec("grep zzzzz /data/hello.txt")
        assert r.exit_code == 1

    def test_grep_case_insensitive(self):
        r = self.shell.exec("grep -i HELLO /data/hello.txt")
        assert "hello" in r.stdout

    def test_pipe(self):
        r = self.shell.exec("cat /data/nums.txt | sort")
        assert r.stdout == "1\n1\n2\n3\n"

    def test_pipe_chain(self):
        r = self.shell.exec("cat /data/nums.txt | sort | uniq")
        assert r.stdout == "1\n2\n3\n"

    def test_redirect(self):
        self.shell.exec("echo test > /tmp/out.txt")
        assert self.fs.read_text("/tmp/out.txt") == "test\n"

    def test_redirect_append(self):
        self.shell.exec("echo line1 > /tmp/out.txt")
        self.shell.exec("echo line2 >> /tmp/out.txt")
        content = self.fs.read_text("/tmp/out.txt")
        assert "line1" in content
        assert "line2" in content

    def test_find(self):
        r = self.shell.exec("find / -name '*.json'")
        assert "/data/users.json" in r.stdout

    def test_wc_lines(self):
        r = self.shell.exec("wc -l /data/nums.txt")
        assert "4" in r.stdout

    def test_head(self):
        r = self.shell.exec("head -2 /data/nums.txt")
        lines = r.stdout.strip().split("\n")
        assert len(lines) == 2

    def test_tail(self):
        r = self.shell.exec("tail -2 /data/nums.txt")
        lines = r.stdout.strip().split("\n")
        assert len(lines) == 2

    def test_sort(self):
        r = self.shell.exec("cat /data/nums.txt | sort -n")
        assert r.stdout == "1\n1\n2\n3\n"

    def test_jq_dot(self):
        r = self.shell.exec("cat /data/users.json | jq '.'")
        data = json.loads(r.stdout)
        assert len(data) == 2

    def test_jq_field(self):
        r = self.shell.exec("cat /data/users.json | jq '.[]'")
        assert r.exit_code == 0

    def test_tree(self):
        r = self.shell.exec("tree /data")
        assert "hello.txt" in r.stdout
        assert "users.json" in r.stdout

    def test_cd_and_pwd(self):
        self.shell.exec("cd /data")
        r = self.shell.exec("pwd")
        assert r.stdout.strip() == "/data"

    def test_sed(self):
        r = self.shell.exec("echo hello world | sed 's/world/earth/'")
        assert "earth" in r.stdout

    def test_cut(self):
        r = self.shell.exec("echo 'a:b:c' | cut -d ':' -f 2")
        assert r.stdout.strip() == "b"

    def test_env_expansion(self):
        shell = Shell(self.fs, env={"NAME": "test"})
        r = shell.exec("echo $NAME")
        assert r.stdout.strip() == "test"

    def test_command_not_found(self):
        r = self.shell.exec("nonexistent_cmd")
        assert r.exit_code == 127

    def test_allowed_commands(self):
        shell = Shell(self.fs, allowed_commands={"cat", "ls"})
        r = shell.exec("cat /data/hello.txt")
        assert r.exit_code == 0
        r = shell.exec("rm /data/hello.txt")
        assert r.exit_code == 127

    def test_output_truncation(self):
        self.fs.write("/big.txt", "x" * 20000)
        shell = Shell(self.fs, max_output=100)
        r = shell.exec("cat /big.txt")
        assert len(r.stdout) < 200
        assert "truncated" in r.stdout

    def test_command_chaining(self):
        r = self.shell.exec("echo hello; echo world")
        assert "hello" in r.stdout
        assert "world" in r.stdout

    def test_clone(self):
        original = Shell(self.fs, cwd="/data", env={"X": "1"})
        cloned = original.clone()
        cloned.fs.write("/new.txt", "new")
        cloned.env["Y"] = "2"
        assert not original.fs.exists("/new.txt")
        assert "Y" not in original.env
        assert cloned.cwd == "/data"


class TestShellRegistry:
    def setup_method(self):
        ShellRegistry.reset()

    def test_register_and_get(self):
        fs = VirtualFS({"/a.txt": "hello"})
        ShellRegistry.register("test", Shell(fs))
        shell = ShellRegistry.get("test")
        assert shell.exec("cat /a.txt").stdout == "hello"

    def test_get_returns_clone(self):
        fs = VirtualFS({"/a.txt": "hello"})
        ShellRegistry.register("test", Shell(fs))
        s1 = ShellRegistry.get("test")
        s2 = ShellRegistry.get("test")
        s1.fs.write("/b.txt", "new")
        assert not s2.fs.exists("/b.txt")

    def test_get_nonexistent_raises(self):
        with pytest.raises(KeyError):
            ShellRegistry.get("nope")

    def test_has(self):
        ShellRegistry.register("x", Shell(VirtualFS()))
        assert ShellRegistry.has("x")
        assert not ShellRegistry.has("y")

    def test_remove(self):
        ShellRegistry.register("x", Shell(VirtualFS()))
        ShellRegistry.remove("x")
        assert not ShellRegistry.has("x")

    def test_reset(self):
        ShellRegistry.register("a", Shell(VirtualFS()))
        ShellRegistry.register("b", Shell(VirtualFS()))
        ShellRegistry.reset()
        assert not ShellRegistry.has("a")
        assert not ShellRegistry.has("b")
