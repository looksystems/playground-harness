import { describe, it, expect, beforeEach } from "vitest";
import { HasShell } from "../../src/typescript/has-shell.js";
import { UsesTools } from "../../src/typescript/uses-tools.js";
import { registerFileTools } from "../../src/typescript/file-tools.js";

class Base {}
const Agent = HasShell(UsesTools(Base));

function makeAgent(): InstanceType<typeof Agent> {
  const a = new Agent();
  a.initHasShell({ cwd: "/work" });
  return a;
}

async function call(agent: any, name: string, args: Record<string, any>): Promise<any> {
  const raw = await agent.executeTool(name, args);
  return JSON.parse(raw);
}

describe("Read", () => {
  it("returns line-numbered content", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.txt", "hello\nworld");
    registerFileTools(a);
    const out = await call(a, "Read", { file_path: "/work/a.txt" });
    expect(out).toContain("     1\thello");
    expect(out).toContain("     2\tworld");
  });

  it("respects offset and limit", async () => {
    const a = makeAgent();
    const lines = Array.from({ length: 10 }, (_, i) => `line${i + 1}`).join("\n");
    a.fs.write("/work/big.txt", lines);
    registerFileTools(a);
    const out = await call(a, "Read", { file_path: "/work/big.txt", offset: 4, limit: 2 });
    expect(out).toContain("     4\tline4");
    expect(out).toContain("     5\tline5");
    expect(out).not.toContain("line3");
    expect(out).not.toContain("line6");
  });

  it("errors on missing file", async () => {
    const a = makeAgent();
    registerFileTools(a);
    const out = await call(a, "Read", { file_path: "/nope.txt" });
    expect(out.error).toMatch(/does not exist/);
  });

  it("errors on a directory", async () => {
    const a = makeAgent();
    a.fs.write("/work/dir/a.txt", "x");
    registerFileTools(a);
    const out = await call(a, "Read", { file_path: "/work/dir" });
    expect(out.error).toMatch(/directory/);
  });

  it("rejects binary", async () => {
    const a = makeAgent();
    a.fs.write("/work/blob.bin", "text\x00stuff");
    registerFileTools(a);
    const out = await call(a, "Read", { file_path: "/work/blob.bin" });
    expect(out.error).toMatch(/binary/);
  });
});

describe("Write", () => {
  it("creates a file", async () => {
    const a = makeAgent();
    registerFileTools(a);
    const out = await call(a, "Write", { file_path: "/work/new.txt", content: "hi" });
    expect(out).toMatch(/written/);
    expect(a.fs.read("/work/new.txt")).toBe("hi");
  });

  it("overwrites a file", async () => {
    const a = makeAgent();
    a.fs.write("/work/x.txt", "old");
    registerFileTools(a);
    await call(a, "Write", { file_path: "/work/x.txt", content: "new" });
    expect(a.fs.read("/work/x.txt")).toBe("new");
  });
});

describe("Edit", () => {
  it("replaces a unique string", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "alpha beta gamma");
    registerFileTools(a);
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "beta", new_string: "BETA" });
    expect(out).toMatch(/edited/);
    expect(a.fs.read("/work/c.txt")).toBe("alpha BETA gamma");
  });

  it("errors when old_string is missing", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "hello");
    registerFileTools(a);
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "zzz", new_string: "qqq" });
    expect(out.error).toMatch(/not found/);
  });

  it("errors on multiple matches without replace_all", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "x x x");
    registerFileTools(a);
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "x", new_string: "y" });
    expect(out.error).toMatch(/Found 3 matches/);
  });

  it("replace_all replaces every occurrence", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "x x x");
    registerFileTools(a);
    await call(a, "Edit", { file_path: "/work/c.txt", old_string: "x", new_string: "y", replace_all: true });
    expect(a.fs.read("/work/c.txt")).toBe("y y y");
  });

  it("read-gate is off by default", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "hello");
    registerFileTools(a);
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "hello", new_string: "bye" });
    expect(out).toMatch(/edited/);
  });

  it("read-gate blocks unread files when enabled", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "hello");
    registerFileTools(a, { enforceReadGate: true });
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "hello", new_string: "bye" });
    expect(out.error).toMatch(/not been read/);
  });

  it("read-gate passes after Read", async () => {
    const a = makeAgent();
    a.fs.write("/work/c.txt", "hello");
    registerFileTools(a, { enforceReadGate: true });
    await call(a, "Read", { file_path: "/work/c.txt" });
    const out = await call(a, "Edit", { file_path: "/work/c.txt", old_string: "hello", new_string: "bye" });
    expect(out).toMatch(/edited/);
    expect(a.fs.read("/work/c.txt")).toBe("bye");
  });
});

describe("Glob", () => {
  it("matches *.ts in cwd", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "x");
    a.fs.write("/work/b.ts", "y");
    a.fs.write("/work/c.txt", "z");
    registerFileTools(a);
    const out = await call(a, "Glob", { pattern: "*.ts" });
    expect((out as string[]).sort()).toEqual(["/work/a.ts", "/work/b.ts"]);
  });

  it("recursive ** pattern", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "x");
    a.fs.write("/work/sub/b.ts", "y");
    a.fs.write("/work/sub/deeper/c.ts", "z");
    registerFileTools(a);
    const out = await call(a, "Glob", { pattern: "**/*.ts" });
    expect((out as string[]).sort()).toEqual(["/work/a.ts", "/work/sub/b.ts", "/work/sub/deeper/c.ts"]);
  });

  it("sorts by mtime, newest first", async () => {
    const a = makeAgent();
    a.fs.write("/work/old.ts", "x");
    a.fs.write("/work/middle.ts", "y");
    a.fs.write("/work/new.ts", "z");
    a.fs.write("/work/new.ts", "z2"); // bump new.ts mtime
    registerFileTools(a);
    const out = await call(a, "Glob", { pattern: "*.ts" });
    expect(out[0]).toBe("/work/new.ts");
  });

  it("path defaults to cwd", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "x");
    a.fs.write("/elsewhere/b.ts", "y");
    registerFileTools(a);
    const out = await call(a, "Glob", { pattern: "*.ts" });
    expect(out).toEqual(["/work/a.ts"]);
  });

  it("explicit path", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "x");
    a.fs.write("/elsewhere/b.ts", "y");
    registerFileTools(a);
    const out = await call(a, "Glob", { pattern: "*.ts", path: "/elsewhere" });
    expect(out).toEqual(["/elsewhere/b.ts"]);
  });
});

describe("Grep", () => {
  it("files_with_matches by default", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "import x\nconsole.log(1)");
    a.fs.write("/work/b.ts", "console.log(2)");
    a.fs.write("/work/c.ts", "x = 1");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "^console" });
    expect((out as string[]).sort()).toEqual(["/work/a.ts", "/work/b.ts"]);
  });

  it("count mode", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "console\nconsole\nconsole");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "console", output_mode: "count" });
    expect(out).toEqual([{ path: "/work/a.ts", count: 3 }]);
  });

  it("content mode with context", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "alpha\nbeta TARGET here\ngamma");
    registerFileTools(a);
    const out = await call(a, "Grep", {
      pattern: "TARGET",
      output_mode: "content",
      line_numbers: true,
      context_before: 1,
      context_after: 1,
    });
    expect(out[0].path).toBe("/work/a.ts");
    expect(out[0].matches[0].line).toBe(2);
    expect(out[0].matches[0].context).toHaveLength(3);
    expect(out[0].matches[0].context[1].match).toBe(true);
    expect(out[0].matches[0].context[1].text).toBe("beta TARGET here");
  });

  it("glob filter", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "console");
    a.fs.write("/work/a.txt", "console");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "console", glob: "*.ts" });
    expect(out).toEqual(["/work/a.ts"]);
  });

  it("type filter", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.ts", "console");
    a.fs.write("/work/a.go", "console");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "console", type: "go" });
    expect(out).toEqual(["/work/a.go"]);
  });

  it("case_insensitive", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.txt", "Hello WORLD");
    registerFileTools(a);
    expect(await call(a, "Grep", { pattern: "world" })).toEqual([]);
    expect(await call(a, "Grep", { pattern: "world", case_insensitive: true })).toEqual(["/work/a.txt"]);
  });

  it("multiline", async () => {
    const a = makeAgent();
    a.fs.write("/work/a.txt", "alpha\nbeta\ngamma");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "alpha.*gamma", multiline: true });
    expect(out).toEqual(["/work/a.txt"]);
  });

  it("head_limit", async () => {
    const a = makeAgent();
    for (let i = 0; i < 5; i++) a.fs.write(`/work/f${i}.txt`, "match");
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "match", head_limit: 2 });
    expect(out).toHaveLength(2);
  });

  it("unknown type errors", async () => {
    const a = makeAgent();
    registerFileTools(a);
    const out = await call(a, "Grep", { pattern: "x", type: "bogus" });
    expect(out.error).toMatch(/Unknown file type/);
  });
});
