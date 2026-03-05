import { describe, it, expect } from "vitest";
import { VirtualFS } from "../../src/typescript/virtual-fs.js";

describe("VirtualFS", () => {
  it("write and read", () => {
    const fs = new VirtualFS();
    fs.write("/hello.txt", "world");
    expect(fs.read("/hello.txt")).toBe("world");
  });

  it("path normalization", () => {
    const fs = new VirtualFS();
    fs.write("foo/bar.txt", "content");
    expect(fs.read("/foo/bar.txt")).toBe("content");

    fs.write("/a/b/../c.txt", "normalized");
    expect(fs.read("/a/c.txt")).toBe("normalized");

    fs.write("/a/./d.txt", "dot");
    expect(fs.read("/a/d.txt")).toBe("dot");
  });

  it("read nonexistent throws", () => {
    const fs = new VirtualFS();
    expect(() => fs.read("/nope")).toThrow("No such file");
  });

  it("exists", () => {
    const fs = new VirtualFS();
    fs.write("/a.txt", "hi");
    expect(fs.exists("/a.txt")).toBe(true);
    expect(fs.exists("/nope")).toBe(false);
  });

  it("exists returns true for directories", () => {
    const fs = new VirtualFS();
    fs.write("/dir/file.txt", "content");
    expect(fs.exists("/dir")).toBe(true);
  });

  it("remove", () => {
    const fs = new VirtualFS();
    fs.write("/a.txt", "hi");
    fs.remove("/a.txt");
    expect(fs.exists("/a.txt")).toBe(false);
  });

  it("remove nonexistent throws", () => {
    const fs = new VirtualFS();
    expect(() => fs.remove("/nope")).toThrow("No such file");
  });

  it("listdir", () => {
    const fs = new VirtualFS();
    fs.write("/dir/a.txt", "a");
    fs.write("/dir/b.txt", "b");
    fs.write("/dir/sub/c.txt", "c");
    const entries = fs.listdir("/dir");
    expect(entries).toEqual(["a.txt", "b.txt", "sub"]);
  });

  it("listdir root", () => {
    const fs = new VirtualFS();
    fs.write("/x.txt", "x");
    fs.write("/y/z.txt", "z");
    const entries = fs.listdir("/");
    expect(entries).toEqual(["x.txt", "y"]);
  });

  it("find", () => {
    const fs = new VirtualFS();
    fs.write("/src/a.ts", "a");
    fs.write("/src/b.js", "b");
    fs.write("/src/sub/c.ts", "c");
    const tsFiles = fs.find("/src", "*.ts");
    expect(tsFiles).toEqual(["/src/a.ts", "/src/sub/c.ts"]);
  });

  it("find all", () => {
    const fs = new VirtualFS();
    fs.write("/a.txt", "a");
    fs.write("/b.txt", "b");
    const all = fs.find("/", "*");
    expect(all).toEqual(["/a.txt", "/b.txt"]);
  });

  it("stat file", () => {
    const fs = new VirtualFS();
    fs.write("/f.txt", "hello");
    const s = fs.stat("/f.txt");
    expect(s.type).toBe("file");
    expect(s.size).toBe(5);
  });

  it("stat directory", () => {
    const fs = new VirtualFS();
    fs.write("/dir/file.txt", "x");
    const s = fs.stat("/dir");
    expect(s.type).toBe("directory");
  });

  it("lazy provider", () => {
    const fs = new VirtualFS();
    let called = 0;
    fs.writeLazy("/lazy.txt", () => {
      called++;
      return "lazy content";
    });
    expect(fs.exists("/lazy.txt")).toBe(true);
    expect(called).toBe(0);
    expect(fs.read("/lazy.txt")).toBe("lazy content");
    expect(called).toBe(1);
    // Second read should use cached value
    expect(fs.read("/lazy.txt")).toBe("lazy content");
    expect(called).toBe(1);
  });

  it("init with files", () => {
    const fs = new VirtualFS({ "/a.txt": "a", "b.txt": "b" });
    expect(fs.read("/a.txt")).toBe("a");
    expect(fs.read("/b.txt")).toBe("b");
  });

  it("clone creates independent copy", () => {
    const fs = new VirtualFS();
    fs.write("/file.txt", "original");
    const cloned = fs.clone();
    cloned.write("/file.txt", "modified");
    expect(fs.read("/file.txt")).toBe("original");
    expect(cloned.read("/file.txt")).toBe("modified");
  });

  it("clone includes lazy providers", () => {
    const fs = new VirtualFS();
    fs.writeLazy("/lazy.txt", () => "lazy");
    const cloned = fs.clone();
    expect(cloned.read("/lazy.txt")).toBe("lazy");
  });

  it("find with question mark pattern", () => {
    const fs = new VirtualFS();
    fs.write("/a1.txt", "a");
    fs.write("/a2.txt", "b");
    fs.write("/ab.txt", "c");
    const found = fs.find("/", "a?.txt");
    expect(found).toEqual(["/a1.txt", "/a2.txt", "/ab.txt"]);
  });
});
