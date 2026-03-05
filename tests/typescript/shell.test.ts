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

    it("stops on failure", () => {
      const sh = makeShell();
      const r = sh.exec("cat /nope; echo should-not-run");
      expect(r.exitCode).toBe(1);
      expect(r.stdout).not.toContain("should-not-run");
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
