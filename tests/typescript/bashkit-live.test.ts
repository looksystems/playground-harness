/**
 * Live integration tests for BashkitCLIDriver against real bashkit CLI.
 */

import { describe, it, expect, beforeAll } from "vitest";
import { execSync } from "child_process";
import { BashkitCLIDriver } from "../../src/typescript/bashkit-cli-driver.js";

let hasBashkit = false;
try {
  execSync("which bashkit", { encoding: "utf-8" });
  hasBashkit = true;
} catch {
  hasBashkit = false;
}

const describeIf = hasBashkit ? describe : describe.skip;

describeIf("BashkitCLIDriver — live integration", () => {
  function driver() {
    return new BashkitCLIDriver();
  }

  describe("basic execution", () => {
    it("echo", () => {
      const r = driver().exec("echo hello world");
      expect(r.stdout.trim()).toBe("hello world");
      expect(r.exitCode).toBe(0);
    });

    it("pipes", () => {
      const r = driver().exec("echo hello | tr a-z A-Z");
      expect(r.stdout.trim()).toBe("HELLO");
    });

    it("exit code on failure", () => {
      const r = driver().exec("false");
      expect(r.exitCode).not.toBe(0);
    });

    it("variable expansion", () => {
      const r = driver().exec("X=42; echo $X");
      expect(r.stdout.trim()).toBe("42");
    });

    it("redirects and cat", () => {
      const r = driver().exec("echo written > /out.txt && cat /out.txt");
      expect(r.stdout.trim()).toBe("written");
    });

    it("for loop", () => {
      const r = driver().exec("for i in 1 2 3; do echo $i; done");
      expect(r.stdout.trim()).toBe("1\n2\n3");
    });

    it("stderr capture", () => {
      const r = driver().exec("echo err >&2");
      expect(r.stderr).toContain("err");
    });
  });

  describe("POSIX builtins", () => {
    it("seq | wc -l", () => {
      const r = driver().exec("seq 1 5 | wc -l");
      expect(r.stdout.trim()).toBe("5");
    });

    it("head", () => {
      const r = driver().exec("seq 1 10 | head -3");
      expect(r.stdout.trim()).toBe("1\n2\n3");
    });

    it("sort", () => {
      const r = driver().exec("printf '3\\n1\\n2\\n' | sort");
      expect(r.stdout.trim()).toBe("1\n2\n3");
    });

    it("grep", () => {
      const r = driver().exec("printf 'foo\\nbar\\nbaz\\n' | grep ba");
      expect(r.stdout.trim()).toBe("bar\nbaz");
    });

    it("cut", () => {
      const r = driver().exec("echo 'a:b:c' | cut -d: -f2");
      expect(r.stdout.trim()).toBe("b");
    });

    it("basename", () => {
      const r = driver().exec("basename /foo/bar/baz.txt");
      expect(r.stdout.trim()).toBe("baz.txt");
    });

    it("dirname", () => {
      const r = driver().exec("dirname /foo/bar/baz.txt");
      expect(r.stdout.trim()).toBe("/foo/bar");
    });
  });

  describe("VFS sync", () => {
    it("write in VFS, read in bashkit", () => {
      const d = driver();
      d.fs.write("/test.txt", "hello from vfs");
      const r = d.exec("cat /test.txt");
      expect(r.stdout.trim()).toBe("hello from vfs");
    });

    it("write in bashkit, read back via VFS", () => {
      const d = driver();
      d.exec("echo 'from bashkit' > /created.txt");
      expect(d.fs.exists("/created.txt")).toBe(true);
      expect(d.fs.readText("/created.txt").trim()).toBe("from bashkit");
    });

    it("round-trip VFS → bashkit → VFS", () => {
      const d = driver();
      d.fs.write("/round.txt", "original");
      d.exec("cat /round.txt | tr a-z A-Z > /upper.txt");
      expect(d.fs.exists("/upper.txt")).toBe(true);
      expect(d.fs.readText("/upper.txt").trim()).toBe("ORIGINAL");
    });

    it("special characters survive sync", () => {
      const d = driver();
      const content = "quotes'here\nback\\slash\n%percent";
      d.fs.write("/special.txt", content);
      const r = d.exec("cat /special.txt");
      expect(r.stdout).toBe(content);
    });
  });

  describe("stateless behavior", () => {
    it("variables do not persist between exec calls", () => {
      const d = driver();
      d.exec("MY_VAR=hello");
      const r = d.exec("echo $MY_VAR");
      expect(r.stdout.trim()).toBe("");
    });
  });
});
