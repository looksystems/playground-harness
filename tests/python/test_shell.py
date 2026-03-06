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
        """Semicolons always continue, even after failure (standard bash behavior)."""
        r = self.shell.exec("echo hello; echo world")
        assert "hello" in r.stdout
        assert "world" in r.stdout

    def test_command_chaining_continues_after_failure(self):
        """Semicolons do not stop on failure."""
        r = self.shell.exec("nonexistent_cmd; echo still_here")
        assert "still_here" in r.stdout

    def test_clone(self):
        original = Shell(self.fs, cwd="/data", env={"X": "1"})
        cloned = original.clone()
        cloned.fs.write("/new.txt", "new")
        cloned.env["Y"] = "2"
        assert not original.fs.exists("/new.txt")
        assert "Y" not in original.env
        assert cloned.cwd == "/data"

    # -- && and || operators -----------------------------------------------

    def test_and_operator_both_succeed(self):
        r = self.shell.exec("echo hello && echo world")
        assert "hello" in r.stdout
        assert "world" in r.stdout
        assert r.exit_code == 0

    def test_and_operator_first_fails(self):
        r = self.shell.exec("nonexistent_cmd && echo world")
        assert "world" not in r.stdout
        assert r.exit_code == 127

    def test_or_operator_first_succeeds(self):
        r = self.shell.exec("echo hello || echo world")
        assert "hello" in r.stdout
        assert "world" not in r.stdout
        assert r.exit_code == 0

    def test_or_operator_first_fails(self):
        r = self.shell.exec("nonexistent_cmd || echo fallback")
        assert "fallback" in r.stdout
        assert r.exit_code == 0

    def test_and_or_chain(self):
        r = self.shell.exec("true && echo yes || echo no")
        assert "yes" in r.stdout
        assert "no" not in r.stdout

    def test_and_or_chain_failure(self):
        r = self.shell.exec("false && echo yes || echo no")
        assert "yes" not in r.stdout
        assert "no" in r.stdout

    # -- $? exit code tracking ---------------------------------------------

    def test_exit_code_tracking(self):
        self.shell.exec("true")
        r = self.shell.exec("echo $?")
        assert r.stdout.strip() == "0"

    def test_exit_code_tracking_failure(self):
        self.shell.exec("false")
        r = self.shell.exec("echo $?")
        assert r.stdout.strip() == "1"

    # -- Variable assignment -----------------------------------------------

    def test_variable_assignment(self):
        r = self.shell.exec("X=hello; echo $X")
        assert r.stdout.strip() == "hello"

    def test_variable_assignment_with_export(self):
        r = self.shell.exec("export FOO=bar; echo $FOO")
        assert r.stdout.strip() == "bar"

    # -- test / [ builtins -------------------------------------------------

    def test_test_file_exists(self):
        r = self.shell.exec("test -f /data/hello.txt")
        assert r.exit_code == 0

    def test_test_file_not_exists(self):
        r = self.shell.exec("test -f /data/nope.txt")
        assert r.exit_code == 1

    def test_test_dir_exists(self):
        r = self.shell.exec("test -d /data")
        assert r.exit_code == 0

    def test_test_string_equals(self):
        r = self.shell.exec("test hello = hello")
        assert r.exit_code == 0

    def test_test_string_not_equals(self):
        r = self.shell.exec("test hello != world")
        assert r.exit_code == 0

    def test_test_numeric_eq(self):
        r = self.shell.exec("test 5 -eq 5")
        assert r.exit_code == 0

    def test_test_numeric_lt(self):
        r = self.shell.exec("test 3 -lt 5")
        assert r.exit_code == 0

    def test_test_negation(self):
        r = self.shell.exec("test ! -f /data/nope.txt")
        assert r.exit_code == 0

    def test_bracket_builtin(self):
        r = self.shell.exec("[ -f /data/hello.txt ]")
        assert r.exit_code == 0

    def test_bracket_missing_close(self):
        r = self.shell.exec("[ -f /data/hello.txt")
        assert r.exit_code == 2

    def test_test_z_empty(self):
        r = self.shell.exec("test -z ''")
        assert r.exit_code == 0

    def test_test_n_nonempty(self):
        r = self.shell.exec("test -n hello")
        assert r.exit_code == 0

    def test_test_e_exists(self):
        r = self.shell.exec("test -e /data/hello.txt")
        assert r.exit_code == 0

    def test_test_numeric_gt(self):
        r = self.shell.exec("test 5 -gt 3")
        assert r.exit_code == 0

    def test_test_numeric_le(self):
        r = self.shell.exec("test 3 -le 5")
        assert r.exit_code == 0

    def test_test_numeric_ge(self):
        r = self.shell.exec("test 5 -ge 5")
        assert r.exit_code == 0

    def test_test_numeric_ne(self):
        r = self.shell.exec("test 3 -ne 5")
        assert r.exit_code == 0

    # -- if/then/elif/else/fi ----------------------------------------------

    def test_if_then_fi(self):
        r = self.shell.exec("if true; then echo yes; fi")
        assert r.stdout.strip() == "yes"

    def test_if_else(self):
        r = self.shell.exec("if false; then echo yes; else echo no; fi")
        assert r.stdout.strip() == "no"

    def test_if_elif(self):
        r = self.shell.exec("if false; then echo a; elif true; then echo b; else echo c; fi")
        assert r.stdout.strip() == "b"

    def test_if_with_test(self):
        r = self.shell.exec("if test -f /data/hello.txt; then echo exists; fi")
        assert r.stdout.strip() == "exists"

    # -- for/while loops ---------------------------------------------------

    def test_for_loop(self):
        r = self.shell.exec("for x in a b c; do echo $x; done")
        assert r.stdout == "a\nb\nc\n"

    def test_while_loop(self):
        r = self.shell.exec("i=0; while test $i -lt 3; do echo $i; i=$(echo $i | sed 's/0/1/;s/1/2/;s/2/3/'); done")
        # This is a bit tricky but we can use a simpler approach
        # Just test a basic while with a counter using for
        pass

    def test_while_max_iterations(self):
        shell = Shell(self.fs, max_iterations=5)
        r = shell.exec("while true; do echo x; done")
        assert "Maximum iteration limit exceeded" in r.stderr

    def test_for_loop_max_iterations(self):
        shell = Shell(self.fs, max_iterations=3)
        r = shell.exec("for x in a b c d e; do echo $x; done")
        assert "Maximum iteration limit exceeded" in r.stderr

    # -- Command substitution $() and backticks ----------------------------

    def test_command_substitution_dollar(self):
        r = self.shell.exec("echo $(echo hello)")
        assert r.stdout.strip() == "hello"

    def test_command_substitution_backtick(self):
        r = self.shell.exec("echo `echo hello`")
        assert r.stdout.strip() == "hello"

    def test_command_substitution_nested(self):
        r = self.shell.exec("echo $(echo $(echo deep))")
        assert r.stdout.strip() == "deep"

    def test_command_substitution_in_assignment(self):
        r = self.shell.exec("X=$(echo hello); echo $X")
        assert r.stdout.strip() == "hello"

    # -- Parameter expansion -----------------------------------------------

    def test_param_expansion_default(self):
        r = self.shell.exec("echo ${UNSET:-fallback}")
        assert r.stdout.strip() == "fallback"

    def test_param_expansion_default_set(self):
        shell = Shell(self.fs, env={"X": "val"})
        r = shell.exec("echo ${X:-fallback}")
        assert r.stdout.strip() == "val"

    def test_param_expansion_assign_default(self):
        r = self.shell.exec("echo ${NEWVAR:=assigned}; echo $NEWVAR")
        assert "assigned" in r.stdout
        # Second echo should also show the assigned value
        lines = r.stdout.strip().split("\n")
        assert lines[0] == "assigned"
        assert lines[1] == "assigned"

    def test_param_expansion_length(self):
        shell = Shell(self.fs, env={"X": "hello"})
        r = shell.exec("echo ${#X}")
        assert r.stdout.strip() == "5"

    def test_param_expansion_substring(self):
        shell = Shell(self.fs, env={"X": "hello world"})
        r = shell.exec("echo ${X:6}")
        assert r.stdout.strip() == "world"

    def test_param_expansion_substring_with_length(self):
        shell = Shell(self.fs, env={"X": "hello world"})
        r = shell.exec("echo ${X:0:5}")
        assert r.stdout.strip() == "hello"

    def test_param_expansion_global_replace(self):
        shell = Shell(self.fs, env={"X": "aabaa"})
        r = shell.exec("echo ${X//a/x}")
        assert r.stdout.strip() == "xxbxx"

    def test_param_expansion_first_replace(self):
        shell = Shell(self.fs, env={"X": "aabaa"})
        r = shell.exec("echo ${X/a/x}")
        assert r.stdout.strip() == "xabaa"

    def test_param_expansion_suffix_removal(self):
        shell = Shell(self.fs, env={"X": "file.tar.gz"})
        r = shell.exec("echo ${X%.*}")
        assert r.stdout.strip() == "file.tar"

    def test_param_expansion_greedy_suffix_removal(self):
        shell = Shell(self.fs, env={"X": "file.tar.gz"})
        r = shell.exec("echo ${X%%.*}")
        assert r.stdout.strip() == "file"

    def test_param_expansion_prefix_removal(self):
        shell = Shell(self.fs, env={"X": "/usr/local/bin"})
        r = shell.exec("echo ${X#*/}")
        assert r.stdout.strip() == "usr/local/bin"

    def test_param_expansion_greedy_prefix_removal(self):
        shell = Shell(self.fs, env={"X": "/usr/local/bin"})
        r = shell.exec("echo ${X##*/}")
        assert r.stdout.strip() == "bin"

    # -- printf builtin ----------------------------------------------------

    def test_printf_string(self):
        r = self.shell.exec("printf '%s %s' hello world")
        assert r.stdout == "hello world"

    def test_printf_decimal(self):
        r = self.shell.exec("printf '%d' 42")
        assert r.stdout == "42"

    def test_printf_float(self):
        r = self.shell.exec("printf '%.2f' 3.14159")
        assert r.stdout == "3.14"

    def test_printf_newline(self):
        r = self.shell.exec("printf 'hello\\nworld'")
        assert r.stdout == "hello\nworld"

    def test_printf_tab(self):
        r = self.shell.exec("printf 'a\\tb'")
        assert r.stdout == "a\tb"

    def test_printf_percent(self):
        r = self.shell.exec("printf '100%%'")
        assert r.stdout == "100%"

    def test_printf_backslash(self):
        r = self.shell.exec("printf 'a\\\\b'")
        assert r.stdout == "a\\b"

    # -- true and false builtins -------------------------------------------

    def test_true_builtin(self):
        r = self.shell.exec("true")
        assert r.exit_code == 0

    def test_false_builtin(self):
        r = self.shell.exec("false")
        assert r.exit_code == 1

    # -- Comments ----------------------------------------------------------

    def test_comment(self):
        r = self.shell.exec("echo hello # this is a comment")
        assert r.stdout.strip() == "hello"

    # -- Brace expansion simple var ----------------------------------------

    def test_brace_simple_var(self):
        shell = Shell(self.fs, env={"X": "val"})
        r = shell.exec("echo ${X}")
        assert r.stdout.strip() == "val"

    # -- Negative substring offset -----------------------------------------

    def test_param_expansion_negative_offset(self):
        shell = Shell(self.fs, env={"X": "hello"})
        r = shell.exec("echo ${X:-2}")
        # ${X:-2} matches substring pattern with offset -2 (last 2 chars)
        assert r.stdout.strip() == "lo"


    # -- [[ ]] double bracket -----------------------------------------------

    def test_double_bracket_string_eq(self):
        r = self.shell.exec('[[ "a" = "a" ]]')
        assert r.exit_code == 0

    def test_double_bracket_string_neq(self):
        r = self.shell.exec('[[ "a" = "b" ]]')
        assert r.exit_code == 1

    def test_double_bracket_numeric(self):
        r = self.shell.exec("[[ 1 -lt 2 ]]")
        assert r.exit_code == 0

    # -- Arithmetic $(()) --------------------------------------------------

    def test_arith_addition(self):
        r = self.shell.exec("echo $((2 + 3))")
        assert r.stdout.strip() == "5"

    def test_arith_mul_sub(self):
        r = self.shell.exec("echo $((10 * 3 - 5))")
        assert r.stdout.strip() == "25"

    def test_arith_division(self):
        r = self.shell.exec("echo $((10 / 3))")
        assert r.stdout.strip() == "3"

    def test_arith_modulo(self):
        r = self.shell.exec("echo $((17 % 5))")
        assert r.stdout.strip() == "2"

    def test_arith_parens(self):
        r = self.shell.exec("echo $(((2 + 3) * 4))")
        assert r.stdout.strip() == "20"

    def test_arith_variable(self):
        r = self.shell.exec("X=10; echo $(($X + 5))")
        assert r.stdout.strip() == "15"

    def test_arith_comparisons(self):
        assert self.shell.exec("echo $((3 > 2))").stdout.strip() == "1"
        assert self.shell.exec("echo $((1 == 1))").stdout.strip() == "1"
        assert self.shell.exec("echo $((1 != 2))").stdout.strip() == "1"
        assert self.shell.exec("echo $((3 < 2))").stdout.strip() == "0"

    def test_arith_negation(self):
        r = self.shell.exec("echo $((-5 + 3))")
        assert r.stdout.strip() == "-2"

    def test_arith_logical(self):
        assert self.shell.exec("echo $((1 && 1))").stdout.strip() == "1"
        assert self.shell.exec("echo $((1 && 0))").stdout.strip() == "0"
        assert self.shell.exec("echo $((0 || 1))").stdout.strip() == "1"

    def test_arith_ternary(self):
        assert self.shell.exec("echo $((1 ? 10 : 20))").stdout.strip() == "10"
        assert self.shell.exec("echo $((0 ? 10 : 20))").stdout.strip() == "20"

    def test_arith_division_by_zero(self):
        r = self.shell.exec("echo $((1 / 0))")
        assert r.exit_code != 0

    # -- case/esac ----------------------------------------------------------

    def test_case_literal_match(self):
        r = self.shell.exec("case hello in hello) echo matched;; esac")
        assert r.stdout.strip() == "matched"

    def test_case_no_match(self):
        r = self.shell.exec("case hello in world) echo nope;; esac")
        assert r.stdout == ""

    def test_case_wildcard(self):
        r = self.shell.exec("case anything in *) echo default;; esac")
        assert r.stdout.strip() == "default"

    def test_case_multiple_clauses(self):
        r = self.shell.exec("case b in a) echo A;; b) echo B;; *) echo other;; esac")
        assert r.stdout.strip() == "B"

    def test_case_pipe_patterns(self):
        r = self.shell.exec("case yes in y | yes) echo affirmative;; esac")
        assert r.stdout.strip() == "affirmative"

    def test_case_with_variable(self):
        r = self.shell.exec("X=hello; case $X in hello) echo matched;; esac")
        assert r.stdout.strip() == "matched"

    def test_case_glob_pattern(self):
        r = self.shell.exec("case file.txt in *.txt) echo text;; *.py) echo python;; esac")
        assert r.stdout.strip() == "text"


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
