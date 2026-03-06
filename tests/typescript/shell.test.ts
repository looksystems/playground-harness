import { describe, it, expect, beforeEach } from "vitest";
import { Shell, ShellRegistry, ExecResult } from "../../src/typescript/shell.js";
import { VirtualFS } from "../../src/typescript/virtual-fs.js";

function makeShell(files?: Record<string, string>): Shell {
  const fs = new VirtualFS(files);
  return new Shell({ fs, cwd: "/home/user" });
}

describe("Shell", () => {
  describe("echo", () => {
    it("echoes arguments", () => {
      const sh = makeShell();
      const r = sh.exec("echo hello world");
      expect(r.stdout).toBe("hello world\n");
      expect(r.exitCode).toBe(0);
    });
  });

  describe("pwd", () => {
    it("prints cwd", () => {
      const sh = makeShell();
      const r = sh.exec("pwd");
      expect(r.stdout).toBe("/home/user\n");
    });
  });

  describe("cd", () => {
    it("changes directory", () => {
      const sh = makeShell({ "/tmp/file.txt": "x" });
      sh.exec("cd /tmp");
      expect(sh.exec("pwd").stdout).toBe("/tmp\n");
    });

    it("fails on nonexistent dir", () => {
      const sh = makeShell();
      const r = sh.exec("cd /nope");
      expect(r.exitCode).toBe(1);
    });
  });

  describe("cat", () => {
    it("reads file", () => {
      const sh = makeShell({ "/hello.txt": "hello world" });
      const r = sh.exec("cat /hello.txt");
      expect(r.stdout).toBe("hello world");
    });

    it("passes stdin when no args", () => {
      const sh = makeShell();
      // echo pipes into cat
      const r = sh.exec("echo hi | cat");
      expect(r.stdout).toBe("hi\n");
    });

    it("fails on missing file", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope.txt");
      expect(r.exitCode).toBe(1);
    });
  });

  describe("ls", () => {
    it("lists directory", () => {
      const sh = makeShell({
        "/dir/a.txt": "a",
        "/dir/b.txt": "b",
      });
      const r = sh.exec("ls /dir");
      expect(r.stdout).toBe("a.txt\nb.txt\n");
    });

    it("long format", () => {
      const sh = makeShell({ "/dir/file.txt": "hello" });
      const r = sh.exec("ls -l /dir");
      expect(r.stdout).toContain("file.txt");
      expect(r.stdout).toContain("-rw-r--r--");
    });
  });

  describe("find", () => {
    it("finds files by name", () => {
      const sh = makeShell({
        "/src/a.ts": "a",
        "/src/b.js": "b",
        "/src/sub/c.ts": "c",
      });
      const r = sh.exec('find /src -name "*.ts"');
      expect(r.stdout).toContain("/src/a.ts");
      expect(r.stdout).toContain("/src/sub/c.ts");
      expect(r.stdout).not.toContain("b.js");
    });

    it("type filter", () => {
      const sh = makeShell({
        "/d/sub/file.txt": "x",
      });
      const r = sh.exec("find /d -type f");
      expect(r.stdout).toContain("/d/sub/file.txt");
    });
  });

  describe("grep", () => {
    it("searches stdin", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'hello\nworld\nhello again' | grep hello");
      expect(r.stdout).toContain("hello");
      expect(r.exitCode).toBe(0);
    });

    it("searches file", () => {
      const sh = makeShell({ "/data.txt": "line1\nline2\nline3\n" });
      const r = sh.exec("grep line2 /data.txt");
      expect(r.stdout).toBe("line2\n");
    });

    it("case insensitive", () => {
      const sh = makeShell({ "/f.txt": "Hello\nworld\n" });
      const r = sh.exec("grep -i hello /f.txt");
      expect(r.stdout).toContain("Hello");
    });

    it("count", () => {
      const sh = makeShell({ "/f.txt": "a\nb\na\n" });
      const r = sh.exec("grep -c a /f.txt");
      expect(r.stdout).toBe("2\n");
    });

    it("line numbers", () => {
      const sh = makeShell({ "/f.txt": "a\nb\nc\n" });
      const r = sh.exec("grep -n b /f.txt");
      expect(r.stdout).toBe("2:b\n");
    });

    it("invert match", () => {
      const sh = makeShell({ "/f.txt": "a\nb\nc\n" });
      const r = sh.exec("grep -v b /f.txt");
      expect(r.stdout).toContain("a");
      expect(r.stdout).toContain("c");
      expect(r.stdout).not.toContain("b\n");
    });

    it("no match returns exit code 1", () => {
      const sh = makeShell({ "/f.txt": "hello\n" });
      const r = sh.exec("grep xyz /f.txt");
      expect(r.exitCode).toBe(1);
    });

    it("recursive search", () => {
      const sh = makeShell({
        "/src/a.ts": "function foo() {}",
        "/src/b.ts": "const bar = 1",
      });
      const r = sh.exec("grep -r foo /src");
      expect(r.stdout).toContain("foo");
    });

    it("filenames only", () => {
      const sh = makeShell({
        "/a.txt": "match",
        "/b.txt": "nope",
      });
      const r = sh.exec("grep -l match /a.txt /b.txt");
      expect(r.stdout).toContain("/a.txt");
    });
  });

  describe("head", () => {
    it("default 10 lines", () => {
      const lines = Array.from({ length: 20 }, (_, i) => `line${i + 1}`).join("\n") + "\n";
      const sh = makeShell({ "/f.txt": lines });
      const r = sh.exec("head /f.txt");
      expect(r.stdout.trim().split("\n")).toHaveLength(10);
    });

    it("custom count", () => {
      const sh = makeShell({ "/f.txt": "a\nb\nc\nd\ne\n" });
      const r = sh.exec("head -n 2 /f.txt");
      expect(r.stdout).toBe("a\nb\n");
    });

    it("from stdin", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\nb\nc' | head -n 1");
      expect(r.stdout).toBe("a\n");
    });
  });

  describe("tail", () => {
    it("last n lines", () => {
      const sh = makeShell({ "/f.txt": "a\nb\nc\nd\ne\n" });
      const r = sh.exec("tail -n 2 /f.txt");
      expect(r.stdout).toBe("d\ne\n");
    });

    it("from stdin", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\nb\nc' | tail -n 1");
      expect(r.stdout).toBe("c\n");
    });
  });

  describe("wc", () => {
    it("full count", () => {
      const sh = makeShell({ "/f.txt": "hello world\n" });
      const r = sh.exec("wc /f.txt");
      expect(r.stdout).toContain("1");
      expect(r.stdout).toContain("2");
    });

    it("line count", () => {
      const sh = makeShell({ "/f.txt": "a\nb\nc\n" });
      const r = sh.exec("wc -l /f.txt");
      expect(r.stdout.trim()).toBe("3");
    });

    it("word count", () => {
      const sh = makeShell({ "/f.txt": "one two three\n" });
      const r = sh.exec("wc -w /f.txt");
      expect(r.stdout.trim()).toBe("3");
    });
  });

  describe("sort", () => {
    it("alphabetical sort", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'c\na\nb' | sort");
      expect(r.stdout).toBe("a\nb\nc\n");
    });

    it("reverse sort", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\nb\nc' | sort -r");
      expect(r.stdout).toBe("c\nb\na\n");
    });

    it("numeric sort", () => {
      const sh = makeShell();
      const r = sh.exec("echo '10\n2\n1' | sort -n");
      expect(r.stdout).toBe("1\n2\n10\n");
    });

    it("unique sort", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\nb\na' | sort -u");
      expect(r.stdout).toBe("a\nb\n");
    });
  });

  describe("uniq", () => {
    it("removes adjacent duplicates", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\na\nb\nb\na' | uniq");
      expect(r.stdout).toBe("a\nb\na\n");
    });

    it("count mode", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\na\nb' | uniq -c");
      expect(r.stdout).toContain("2 a");
      expect(r.stdout).toContain("1 b");
    });
  });

  describe("cut", () => {
    it("cuts fields", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a:b:c' | cut -d : -f 2");
      expect(r.stdout).toBe("b\n");
    });

    it("field ranges", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a:b:c:d' | cut -d : -f 1-2");
      expect(r.stdout).toBe("a:b\n");
    });
  });

  describe("tr", () => {
    it("translates characters", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'hello' | tr l r");
      // echo adds \n, tr translates the content
      expect(r.stdout).toContain("herro");
    });

    it("deletes characters", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'hello' | tr -d l");
      expect(r.stdout).toContain("heo");
    });
  });

  describe("sed", () => {
    it("substitute once", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'foo foo' | sed 's/foo/bar/'");
      expect(r.stdout).toContain("bar foo");
    });

    it("substitute global", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'foo foo' | sed 's/foo/bar/g'");
      expect(r.stdout).toContain("bar bar");
    });
  });

  describe("tee", () => {
    it("writes to file and passes through", () => {
      const sh = makeShell();
      const r = sh.exec("echo hello | tee /out.txt");
      expect(r.stdout).toBe("hello\n");
      expect(sh.fs.read("/out.txt")).toBe("hello\n");
    });

    it("append mode", () => {
      const sh = makeShell({ "/out.txt": "first\n" });
      sh.exec("echo second | tee -a /out.txt");
      expect(sh.fs.read("/out.txt")).toBe("first\nsecond\n");
    });
  });

  describe("touch", () => {
    it("creates empty file", () => {
      const sh = makeShell();
      sh.exec("touch /new.txt");
      expect(sh.fs.exists("/new.txt")).toBe(true);
      expect(sh.fs.read("/new.txt")).toBe("");
    });

    it("does not overwrite existing", () => {
      const sh = makeShell({ "/f.txt": "content" });
      sh.exec("touch /f.txt");
      expect(sh.fs.read("/f.txt")).toBe("content");
    });
  });

  describe("mkdir", () => {
    it("creates directory via .keep", () => {
      const sh = makeShell();
      sh.exec("mkdir /newdir");
      expect(sh.fs.exists("/newdir")).toBe(true);
    });
  });

  describe("cp", () => {
    it("copies file", () => {
      const sh = makeShell({ "/src.txt": "data" });
      sh.exec("cp /src.txt /dst.txt");
      expect(sh.fs.read("/dst.txt")).toBe("data");
    });

    it("missing operand", () => {
      const sh = makeShell();
      const r = sh.exec("cp /src.txt");
      expect(r.exitCode).toBe(1);
    });
  });

  describe("rm", () => {
    it("removes file", () => {
      const sh = makeShell({ "/f.txt": "x" });
      sh.exec("rm /f.txt");
      expect(sh.fs.exists("/f.txt")).toBe(false);
    });

    it("ignores nonexistent", () => {
      const sh = makeShell();
      const r = sh.exec("rm /nope");
      expect(r.exitCode).toBe(0);
    });
  });

  describe("stat", () => {
    it("shows file info", () => {
      const sh = makeShell({ "/f.txt": "hello" });
      const r = sh.exec("stat /f.txt");
      const info = JSON.parse(r.stdout);
      expect(info.type).toBe("file");
      expect(info.size).toBe(5);
    });
  });

  describe("tree", () => {
    it("shows tree", () => {
      const sh = makeShell({
        "/root/a.txt": "a",
        "/root/sub/b.txt": "b",
      });
      const r = sh.exec("tree /root");
      expect(r.stdout).toContain("a.txt");
      expect(r.stdout).toContain("sub/");
      expect(r.stdout).toContain("b.txt");
    });
  });

  describe("jq", () => {
    it("identity", () => {
      const sh = makeShell();
      const r = sh.exec('echo \'{"a":1}\' | jq .');
      expect(JSON.parse(r.stdout)).toEqual({ a: 1 });
    });

    it("field access", () => {
      const sh = makeShell();
      const r = sh.exec('echo \'{"name":"test"}\' | jq -r .name');
      expect(r.stdout.trim()).toBe("test");
    });

    it("array iteration", () => {
      const sh = makeShell();
      const r = sh.exec('echo \'[1,2,3]\' | jq .[]');
      expect(r.stdout.trim()).toBe("1\n2\n3");
    });

    it("nested access", () => {
      const sh = makeShell();
      const r = sh.exec('echo \'{"a":{"b":42}}\' | jq .a.b');
      expect(r.stdout.trim()).toBe("42");
    });

    it("array index", () => {
      const sh = makeShell();
      const r = sh.exec('echo \'[10,20,30]\' | jq .[1]');
      expect(r.stdout.trim()).toBe("20");
    });

    it("parse error", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'not json' | jq .");
      expect(r.exitCode).toBe(2);
    });
  });

  describe("pipes", () => {
    it("pipes output between commands", () => {
      const sh = makeShell({ "/f.txt": "b\na\nc\n" });
      const r = sh.exec("cat /f.txt | sort | head -n 2");
      expect(r.stdout).toBe("a\nb\n");
    });

    it("multi-stage pipe", () => {
      const sh = makeShell();
      const r = sh.exec("echo 'a\nb\na\nb\na' | sort | uniq -c");
      expect(r.stdout).toContain("3 a");
      expect(r.stdout).toContain("2 b");
    });
  });

  describe("redirects", () => {
    it("redirect to file", () => {
      const sh = makeShell();
      sh.exec("echo hello > /out.txt");
      expect(sh.fs.read("/out.txt")).toBe("hello\n");
    });

    it("append redirect", () => {
      const sh = makeShell({ "/out.txt": "first\n" });
      sh.exec("echo second >> /out.txt");
      expect(sh.fs.read("/out.txt")).toBe("first\nsecond\n");
    });
  });

  describe("chaining", () => {
    it("semicolon chains commands", () => {
      const sh = makeShell();
      const r = sh.exec("echo a; echo b");
      expect(r.stdout).toBe("a\nb\n");
    });

    it("continues after failure with semicolon", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope; echo after");
      expect(r.stdout).toContain("after");
    });
  });

  // Phase 2: && / || / $?
  describe("&& operator", () => {
    it("runs second command on success", () => {
      const sh = makeShell();
      const r = sh.exec("echo ok && echo yes");
      expect(r.stdout).toBe("ok\nyes\n");
    });

    it("skips second command on failure", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope && echo yes");
      expect(r.stdout).not.toContain("yes");
      expect(r.exitCode).not.toBe(0);
    });

    it("chains multiple &&", () => {
      const sh = makeShell();
      const r = sh.exec("echo a && echo b && echo c");
      expect(r.stdout).toBe("a\nb\nc\n");
    });
  });

  describe("|| operator", () => {
    it("runs second command on failure", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope || echo fallback");
      expect(r.stdout).toContain("fallback");
    });

    it("skips second command on success", () => {
      const sh = makeShell();
      const r = sh.exec("echo ok || echo fallback");
      expect(r.stdout).toBe("ok\n");
      expect(r.stdout).not.toContain("fallback");
    });
  });

  describe("$? exit code", () => {
    it("stores last exit code", () => {
      const sh = makeShell();
      const r = sh.exec("echo hi; echo $?");
      expect(r.stdout).toBe("hi\n0\n");
    });

    it("stores non-zero exit code", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope; echo $?");
      expect(r.stdout).toBe("1\n");
    });
  });

  describe("mixed && || ;", () => {
    it("&& and || together", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope && echo yes || echo no");
      expect(r.stdout).toContain("no");
      expect(r.stdout).not.toContain("yes");
    });

    it("semicolon separates independent lists", () => {
      const sh = makeShell();
      const r = sh.exec("echo a; echo b && echo c");
      expect(r.stdout).toBe("a\nb\nc\n");
    });
  });

  // Phase 3: Variable assignment
  describe("variable assignment", () => {
    it("assigns and uses variable", () => {
      const sh = makeShell();
      const r = sh.exec("X=hello; echo $X");
      expect(r.stdout).toBe("hello\n");
    });

    it("assigns quoted value", () => {
      const sh = makeShell();
      const r = sh.exec('X="hello world"; echo $X');
      expect(r.stdout).toBe("hello world\n");
    });

    it("export is a no-op", () => {
      const sh = makeShell();
      const r = sh.exec("export X=42; echo $X");
      expect(r.stdout).toBe("42\n");
    });

    it("multiple assignments", () => {
      const sh = makeShell();
      const r = sh.exec("A=1; B=2; echo $A $B");
      expect(r.stdout).toBe("1 2\n");
    });
  });

  // Phase 4: test / [ builtin
  describe("test builtin", () => {
    it("test -f on existing file", () => {
      const sh = makeShell({ "/exists.txt": "data" });
      const r = sh.exec("test -f /exists.txt");
      expect(r.exitCode).toBe(0);
    });

    it("test -f on missing file", () => {
      const sh = makeShell();
      const r = sh.exec("test -f /nope.txt");
      expect(r.exitCode).toBe(1);
    });

    it("test -d on directory", () => {
      const sh = makeShell({ "/dir/file.txt": "x" });
      const r = sh.exec("test -d /dir");
      expect(r.exitCode).toBe(0);
    });

    it("test -e on existing path", () => {
      const sh = makeShell({ "/f.txt": "x" });
      const r = sh.exec("test -e /f.txt");
      expect(r.exitCode).toBe(0);
    });

    it("test -z empty string", () => {
      const sh = makeShell();
      const r = sh.exec('test -z ""');
      expect(r.exitCode).toBe(0);
    });

    it("test -n non-empty string", () => {
      const sh = makeShell();
      const r = sh.exec('test -n "hello"');
      expect(r.exitCode).toBe(0);
    });

    it("test string equality", () => {
      const sh = makeShell();
      const r = sh.exec('test "a" = "a"');
      expect(r.exitCode).toBe(0);
    });

    it("test string inequality", () => {
      const sh = makeShell();
      const r = sh.exec('test "a" != "b"');
      expect(r.exitCode).toBe(0);
    });

    it("test numeric comparison -eq", () => {
      const sh = makeShell();
      const r = sh.exec("test 1 -eq 1");
      expect(r.exitCode).toBe(0);
    });

    it("test numeric comparison -lt", () => {
      const sh = makeShell();
      const r = sh.exec("test 1 -lt 2");
      expect(r.exitCode).toBe(0);
    });

    it("test numeric comparison -gt", () => {
      const sh = makeShell();
      const r = sh.exec("test 2 -gt 1");
      expect(r.exitCode).toBe(0);
    });

    it("test negation !", () => {
      const sh = makeShell();
      const r = sh.exec('test ! -f /nope');
      expect(r.exitCode).toBe(0);
    });

    it("[ ] bracket syntax", () => {
      const sh = makeShell();
      const r = sh.exec('[ "a" = "a" ]');
      expect(r.exitCode).toBe(0);
    });

    it("[ ] with -lt", () => {
      const sh = makeShell();
      const r = sh.exec("[ 1 -lt 2 ]");
      expect(r.exitCode).toBe(0);
    });

    it("[ ] returns 1 for false", () => {
      const sh = makeShell();
      const r = sh.exec('[ "a" = "b" ]');
      expect(r.exitCode).toBe(1);
    });
  });

  // Phase 5: if/then/elif/else/fi
  describe("if/then/else/fi", () => {
    it("basic if true", () => {
      const sh = makeShell({ "/file": "x" });
      const r = sh.exec("if [ -f /file ]; then echo yes; else echo no; fi");
      expect(r.stdout).toBe("yes\n");
    });

    it("basic if false", () => {
      const sh = makeShell();
      const r = sh.exec("if [ -f /nope ]; then echo yes; else echo no; fi");
      expect(r.stdout).toBe("no\n");
    });

    it("if without else", () => {
      const sh = makeShell();
      const r = sh.exec("if [ 1 -eq 1 ]; then echo yes; fi");
      expect(r.stdout).toBe("yes\n");
    });

    it("if false without else", () => {
      const sh = makeShell();
      const r = sh.exec("if [ 1 -eq 2 ]; then echo yes; fi");
      expect(r.stdout).toBe("");
    });

    it("elif", () => {
      const sh = makeShell();
      const r = sh.exec("if [ 1 -eq 2 ]; then echo a; elif [ 1 -eq 1 ]; then echo b; fi");
      expect(r.stdout).toBe("b\n");
    });

    it("elif with else", () => {
      const sh = makeShell();
      const r = sh.exec("if [ 1 -eq 2 ]; then echo a; elif [ 1 -eq 3 ]; then echo b; else echo c; fi");
      expect(r.stdout).toBe("c\n");
    });

    it("if with && condition", () => {
      const sh = makeShell();
      const r = sh.exec("if echo ok && [ 1 -eq 1 ]; then echo yes; fi");
      expect(r.stdout).toContain("yes");
    });
  });

  // Phase 6: for/while loops
  describe("for loop", () => {
    it("iterates over words", () => {
      const sh = makeShell();
      const r = sh.exec("for x in a b c; do echo $x; done");
      expect(r.stdout).toBe("a\nb\nc\n");
    });

    it("iterates with variable in body", () => {
      const sh = makeShell();
      const r = sh.exec("for f in one two; do echo file-$f; done");
      expect(r.stdout).toBe("file-one\nfile-two\n");
    });

    it("empty word list", () => {
      const sh = makeShell();
      const r = sh.exec("for x in; do echo $x; done");
      expect(r.stdout).toBe("");
    });
  });

  describe("while loop", () => {
    it("loops while condition true", () => {
      const sh = makeShell();
      const r = sh.exec("X=3; while [ $X -gt 0 ]; do echo $X; X=$(echo $X | sed 's/3/2/;s/2/1/;s/1/0/'); done");
      expect(r.stdout).toContain("3");
    });

    it("max iterations safety", () => {
      const fs = new VirtualFS();
      const sh = new Shell({ fs, maxIterations: 5 });
      const r = sh.exec("while [ 1 -eq 1 ]; do echo x; done");
      expect(r.exitCode).not.toBe(0);
    });
  });

  // Phase 7: Command substitution
  describe("command substitution", () => {
    it("basic $()", () => {
      const sh = makeShell();
      const r = sh.exec("echo $(echo hello)");
      expect(r.stdout).toBe("hello\n");
    });

    it("stores in variable", () => {
      const sh = makeShell({ "/file.txt": "content\n" });
      const r = sh.exec("X=$(cat /file.txt); echo $X");
      expect(r.stdout).toBe("content\n");
    });

    it("nested substitution", () => {
      const sh = makeShell();
      const r = sh.exec("echo $(echo $(echo deep))");
      expect(r.stdout).toBe("deep\n");
    });

    it("in argument", () => {
      const sh = makeShell({ "/dir/a.txt": "x" });
      const r = sh.exec("ls $(echo /dir)");
      expect(r.stdout).toContain("a.txt");
    });

    it("backtick form", () => {
      const sh = makeShell();
      const r = sh.exec("echo `echo hello`");
      expect(r.stdout).toBe("hello\n");
    });
  });

  // Phase 8: Parameter expansion
  describe("parameter expansion", () => {
    it("${var:-default} when unset", () => {
      const sh = makeShell();
      const r = sh.exec("echo ${UNSET:-default}");
      expect(r.stdout).toBe("default\n");
    });

    it("${var:-default} when set", () => {
      const sh = makeShell();
      sh.env["X"] = "value";
      const r = sh.exec("echo ${X:-default}");
      expect(r.stdout).toBe("value\n");
    });

    it("${var:=default} assigns when unset", () => {
      const sh = makeShell();
      sh.exec("echo ${X:=assigned}");
      expect(sh.env["X"]).toBe("assigned");
    });

    it("${#var} string length", () => {
      const sh = makeShell();
      const r = sh.exec("X=hello; echo ${#X}");
      expect(r.stdout).toBe("5\n");
    });

    it("${var:offset:length} substring", () => {
      const sh = makeShell();
      const r = sh.exec("X=hello; echo ${X:1:3}");
      expect(r.stdout).toBe("ell\n");
    });

    it("${var//pattern/replacement} global replace", () => {
      const sh = makeShell();
      const r = sh.exec("X=hello_world; echo ${X//_/-}");
      expect(r.stdout).toBe("hello-world\n");
    });

    it("${var/pattern/replacement} first replace", () => {
      const sh = makeShell();
      const r = sh.exec("X=aabaa; echo ${X/a/x}");
      expect(r.stdout).toBe("xabaa\n");
    });

    it("${var%%suffix} greedy suffix removal", () => {
      const sh = makeShell();
      const r = sh.exec("X=file.tar.gz; echo ${X%%.*}");
      expect(r.stdout).toBe("file\n");
    });

    it("${var%suffix} short suffix removal", () => {
      const sh = makeShell();
      const r = sh.exec("X=file.tar.gz; echo ${X%.*}");
      expect(r.stdout).toBe("file.tar\n");
    });

    it("${var##prefix} greedy prefix removal", () => {
      const sh = makeShell();
      const r = sh.exec("X=/path/to/file; echo ${X##*/}");
      expect(r.stdout).toBe("file\n");
    });

    it("${var#prefix} short prefix removal", () => {
      const sh = makeShell();
      const r = sh.exec("X=/path/to/file; echo ${X#*/}");
      expect(r.stdout).toBe("path/to/file\n");
    });
  });

  // Phase 9: printf
  describe("printf", () => {
    it("basic format string", () => {
      const sh = makeShell();
      const r = sh.exec("printf '%s has %d items\\n' foo 3");
      expect(r.stdout).toBe("foo has 3 items\n");
    });

    it("percent-s substitution", () => {
      const sh = makeShell();
      const r = sh.exec("printf '%s' hello");
      expect(r.stdout).toBe("hello");
    });

    it("percent-d integer", () => {
      const sh = makeShell();
      const r = sh.exec("printf '%d' 42");
      expect(r.stdout).toBe("42");
    });

    it("percent-f float", () => {
      const sh = makeShell();
      const r = sh.exec("printf '%.2f' 3.14159");
      expect(r.stdout).toBe("3.14");
    });

    it("literal percent", () => {
      const sh = makeShell();
      const r = sh.exec("printf '100%%'");
      expect(r.stdout).toBe("100%");
    });

    it("escape sequences", () => {
      const sh = makeShell();
      const r = sh.exec("printf 'a\\tb\\n'");
      expect(r.stdout).toBe("a\tb\n");
    });

    it("no trailing newline by default", () => {
      const sh = makeShell();
      const r = sh.exec("printf hello");
      expect(r.stdout).toBe("hello");
    });
  });

  describe("env expansion", () => {
    it("expands $VAR", () => {
      const sh = makeShell();
      sh.env["NAME"] = "world";
      const r = sh.exec("echo hello $NAME");
      expect(r.stdout).toBe("hello world\n");
    });

    it("expands ${VAR}", () => {
      const sh = makeShell();
      sh.env["X"] = "42";
      const r = sh.exec("echo value=${X}");
      expect(r.stdout).toBe("value=42\n");
    });
  });

  describe("allowed commands", () => {
    it("restricts to allowed set", () => {
      const fs = new VirtualFS();
      const sh = new Shell({
        fs,
        allowedCommands: new Set(["echo", "cat"]),
      });
      expect(sh.exec("echo hi").exitCode).toBe(0);
      expect(sh.exec("ls /").exitCode).toBe(127);
    });
  });

  describe("truncation", () => {
    it("truncates long output", () => {
      const fs = new VirtualFS();
      fs.write("/big.txt", "x".repeat(20000));
      const sh = new Shell({ fs, maxOutput: 100 });
      const r = sh.exec("cat /big.txt");
      expect(r.stdout).toContain("truncated");
      expect(r.stdout.length).toBeLessThan(20000);
    });
  });

  describe("command not found", () => {
    it("returns 127", () => {
      const sh = makeShell();
      const r = sh.exec("nonexistent");
      expect(r.exitCode).toBe(127);
      expect(r.stderr).toContain("command not found");
    });
  });

  describe("empty command", () => {
    it("returns empty result", () => {
      const sh = makeShell();
      const r = sh.exec("");
      expect(r.stdout).toBe("");
      expect(r.exitCode).toBe(0);
    });
  });

  describe("clone", () => {
    it("creates independent copy", () => {
      const sh = makeShell({ "/f.txt": "original" });
      const cloned = sh.clone();
      cloned.fs.write("/f.txt", "modified");
      expect(sh.fs.read("/f.txt")).toBe("original");
      expect(cloned.fs.read("/f.txt")).toBe("modified");
    });

    it("preserves cwd and env", () => {
      const sh = makeShell();
      sh.env["KEY"] = "value";
      const cloned = sh.clone();
      cloned.env["KEY"] = "changed";
      expect(sh.env["KEY"]).toBe("value");
    });
  });

  describe("[[ ]] double bracket", () => {
    it("string equality", () => {
      const sh = makeShell();
      const r = sh.exec('[[ "a" = "a" ]]');
      expect(r.exitCode).toBe(0);
    });

    it("string inequality", () => {
      const sh = makeShell();
      const r = sh.exec('[[ "a" = "b" ]]');
      expect(r.exitCode).toBe(1);
    });

    it("numeric comparison", () => {
      const sh = makeShell();
      const r = sh.exec("[[ 1 -lt 2 ]]");
      expect(r.exitCode).toBe(0);
    });
  });

  describe("arithmetic $(())", () => {
    it("basic addition", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((2 + 3))");
      expect(r.stdout).toBe("5\n");
    });

    it("multiplication and subtraction", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((10 * 3 - 5))");
      expect(r.stdout).toBe("25\n");
    });

    it("division", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((10 / 3))");
      expect(r.stdout).toBe("3\n");
    });

    it("modulo", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((17 % 5))");
      expect(r.stdout).toBe("2\n");
    });

    it("parentheses", () => {
      const sh = makeShell();
      const r = sh.exec("echo $(((2 + 3) * 4))");
      expect(r.stdout).toBe("20\n");
    });

    it("variable expansion", () => {
      const sh = makeShell();
      const r = sh.exec("X=10; echo $(($X + 5))");
      expect(r.stdout).toBe("15\n");
    });

    it("comparison operators", () => {
      const sh = makeShell();
      expect(sh.exec("echo $((3 > 2))").stdout).toBe("1\n");
      expect(sh.exec("echo $((1 == 1))").stdout).toBe("1\n");
      expect(sh.exec("echo $((1 != 2))").stdout).toBe("1\n");
      expect(sh.exec("echo $((3 < 2))").stdout).toBe("0\n");
    });

    it("unary negation", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((-5 + 3))");
      expect(r.stdout).toBe("-2\n");
    });

    it("logical operators", () => {
      const sh = makeShell();
      expect(sh.exec("echo $((1 && 1))").stdout).toBe("1\n");
      expect(sh.exec("echo $((1 && 0))").stdout).toBe("0\n");
      expect(sh.exec("echo $((0 || 1))").stdout).toBe("1\n");
    });

    it("ternary operator", () => {
      const sh = makeShell();
      expect(sh.exec("echo $((1 ? 10 : 20))").stdout).toBe("10\n");
      expect(sh.exec("echo $((0 ? 10 : 20))").stdout).toBe("20\n");
    });

    it("division by zero", () => {
      const sh = makeShell();
      const r = sh.exec("echo $((1 / 0))");
      expect(r.exitCode).not.toBe(0);
    });
  });

  describe("case/esac", () => {
    it("matches literal pattern", () => {
      const sh = makeShell();
      const r = sh.exec("case hello in hello) echo matched;; esac");
      expect(r.stdout).toBe("matched\n");
    });

    it("no match returns empty", () => {
      const sh = makeShell();
      const r = sh.exec("case hello in world) echo nope;; esac");
      expect(r.stdout).toBe("");
    });

    it("wildcard pattern", () => {
      const sh = makeShell();
      const r = sh.exec("case anything in *) echo default;; esac");
      expect(r.stdout).toBe("default\n");
    });

    it("multiple clauses", () => {
      const sh = makeShell();
      const r = sh.exec("case b in a) echo A;; b) echo B;; *) echo other;; esac");
      expect(r.stdout).toBe("B\n");
    });

    it("pipe-separated patterns", () => {
      const sh = makeShell();
      const r = sh.exec("case yes in y | yes) echo affirmative;; esac");
      expect(r.stdout).toBe("affirmative\n");
    });

    it("with variable expansion", () => {
      const sh = makeShell();
      const r = sh.exec('X=hello; case $X in hello) echo matched;; esac');
      expect(r.stdout).toBe("matched\n");
    });

    it("glob pattern matching", () => {
      const sh = makeShell();
      const r = sh.exec("case file.txt in *.txt) echo text;; *.py) echo python;; esac");
      expect(r.stdout).toBe("text\n");
    });
  });

  describe("expansion cap", () => {
    it("limits expansions per command", () => {
      const sh = makeShell();
      // Create a command with many expansions - a for loop expanding a variable each iteration
      let cmd = "";
      for (let i = 0; i < 100; i++) cmd += "echo $HOME; ";
      // Should work for reasonable numbers
      const r = sh.exec(cmd);
      expect(r.exitCode).toBe(0);
    });
  });
});

describe("ShellRegistry", () => {
  beforeEach(() => {
    ShellRegistry.reset();
  });

  it("register and get", () => {
    const sh = makeShell({ "/f.txt": "data" });
    ShellRegistry.register("test", sh);
    const retrieved = ShellRegistry.get("test");
    expect(retrieved.fs.read("/f.txt")).toBe("data");
  });

  it("get returns clone", () => {
    const sh = makeShell({ "/f.txt": "original" });
    ShellRegistry.register("test", sh);
    const clone = ShellRegistry.get("test");
    clone.fs.write("/f.txt", "modified");
    const another = ShellRegistry.get("test");
    expect(another.fs.read("/f.txt")).toBe("original");
  });

  it("has", () => {
    expect(ShellRegistry.has("nope")).toBe(false);
    ShellRegistry.register("x", makeShell());
    expect(ShellRegistry.has("x")).toBe(true);
  });

  it("remove", () => {
    ShellRegistry.register("x", makeShell());
    ShellRegistry.remove("x");
    expect(ShellRegistry.has("x")).toBe(false);
  });

  it("get nonexistent throws", () => {
    expect(() => ShellRegistry.get("nope")).toThrow("not registered");
  });

  it("reset clears all", () => {
    ShellRegistry.register("a", makeShell());
    ShellRegistry.register("b", makeShell());
    ShellRegistry.reset();
    expect(ShellRegistry.has("a")).toBe(false);
    expect(ShellRegistry.has("b")).toBe(false);
  });
});
