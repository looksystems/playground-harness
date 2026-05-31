import { describe, it, expect } from "vitest";
import * as os from "node:os";
import * as fsmod from "node:fs";
import * as nodePath from "node:path";
import { BuiltinFilesystemDriver } from "../../src/typescript/drivers.js";
import {
  MountSource,
  MountStat,
  MountingFilesystemDriver,
  isMountable,
} from "../../src/typescript/mount.js";
import { LocalFolderSource } from "../../src/typescript/mount-sources.js";
import { HasShell } from "../../src/typescript/has-shell.js";
import { UsesTools } from "../../src/typescript/uses-tools.js";

class FakeSource implements MountSource {
  private files: Map<string, string>;
  constructor(files: Record<string, string>) {
    this.files = new Map();
    for (const [k, v] of Object.entries(files)) {
      this.files.set(k.replace(/^\/+|\/+$/g, ""), v);
    }
  }
  private dirs(): Set<string> {
    const dirs = new Set<string>([""]);
    for (const p of this.files.keys()) {
      const parts = p.split("/");
      for (let i = 1; i < parts.length; i++) {
        dirs.add(parts.slice(0, i).join("/"));
      }
    }
    return dirs;
  }
  stat(subpath: string): MountStat | null {
    subpath = subpath.replace(/^\/+|\/+$/g, "");
    if (this.files.has(subpath)) {
      return { isDir: false, size: this.files.get(subpath)!.length, mtime: 0 };
    }
    if (this.dirs().has(subpath)) {
      return { isDir: true };
    }
    return null;
  }
  list(subpath: string): string[] {
    subpath = subpath.replace(/^\/+|\/+$/g, "");
    const prefix = subpath ? subpath + "/" : "";
    const names = new Set<string>();
    for (const p of this.files.keys()) {
      if (p.startsWith(prefix) && p !== subpath) {
        names.add(p.slice(prefix.length).split("/")[0]);
      }
    }
    return [...names].sort();
  }
  read(subpath: string): string {
    return this.files.get(subpath.replace(/^\/+|\/+$/g, ""))!;
  }
}

const inner = () => new BuiltinFilesystemDriver();
const mounting = () => new MountingFilesystemDriver(inner());

describe("MountingFilesystemDriver", () => {
  it("empty table passthrough", () => {
    const i = inner();
    i.write("/a.txt", "hi");
    const fs = new MountingFilesystemDriver(i);
    expect(fs.read("/a.txt")).toBe("hi");
    expect(fs.mounts()).toEqual([]);
  });

  it("longest prefix routing", () => {
    const fs = mounting();
    fs.mount("/a", new FakeSource({ "x.txt": "outer" }));
    fs.mount("/a/b", new FakeSource({ "y.txt": "inner" }));
    expect(fs.read("/a/b/y.txt")).toBe("inner");
    expect(fs.read("/a/x.txt")).toBe("outer");
  });

  it("read from source", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "world" }));
    expect(fs.read("/work/hello.txt")).toBe("world");
    expect(fs.readText("/work/hello.txt")).toBe("world");
  });

  it("exists mount point and ancestor", () => {
    const fs = mounting();
    fs.mount("/deep/mnt", new FakeSource({ "f.txt": "x" }));
    expect(fs.exists("/deep/mnt")).toBe(true);
    expect(fs.exists("/deep")).toBe(true);
    expect(fs.exists("/deep/mnt/f.txt")).toBe(true);
    expect(fs.exists("/deep/mnt/nope.txt")).toBe(false);
    expect(fs.isDir("/deep")).toBe(true);
    expect(fs.isDir("/deep/mnt")).toBe(true);
    expect(fs.isDir("/deep/mnt/f.txt")).toBe(false);
  });

  it("listdir merge dedup sorted", () => {
    const i = inner();
    i.write("/work/local.txt", "L");
    i.write("/work/shared.txt", "edited");
    const fs = new MountingFilesystemDriver(i);
    fs.mount("/work", new FakeSource({ "shared.txt": "orig", "sub/deep.txt": "d" }));
    expect(fs.listdir("/work")).toEqual(["local.txt", "shared.txt", "sub"]);
  });

  it("listdir root shows mount point", () => {
    const fs = mounting();
    fs.mount("/repo", new FakeSource({ "a.txt": "x" }));
    expect(fs.listdir("/")).toContain("repo");
  });

  it("copy-up shadowing", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "original" }));
    expect(fs.read("/work/hello.txt")).toBe("original");
    fs.write("/work/hello.txt", "edited");
    expect(fs.read("/work/hello.txt")).toBe("edited");
    expect(fs.listdir("/work").filter((n) => n === "hello.txt").length).toBe(1);
  });

  it("remove copied-up un-shadows", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "original" }));
    fs.write("/work/hello.txt", "edited");
    fs.remove("/work/hello.txt");
    expect(fs.read("/work/hello.txt")).toBe("original");
  });

  it("remove source-only raises", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "original" }));
    expect(() => fs.remove("/work/hello.txt")).toThrow();
    expect(fs.read("/work/hello.txt")).toBe("original");
  });

  it("find merged", () => {
    const i = inner();
    i.write("/work/local.md", "L");
    const fs = new MountingFilesystemDriver(i);
    fs.mount("/work", new FakeSource({ "a.md": "x", "sub/b.md": "y", "c.txt": "z" }));
    expect(fs.find("/work", "*.md").sort()).toEqual([
      "/work/a.md",
      "/work/local.md",
      "/work/sub/b.md",
    ]);
  });

  it("stat source file", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "world" }));
    const s = fs.stat("/work/hello.txt");
    expect(s.type).toBe("file");
    expect(s.size).toBe(5);
    expect(fs.stat("/work").type).toBe("directory");
  });

  it("clone writable diverges, source shared", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "hello.txt": "original" }));
    fs.write("/work/local.txt", "L");
    const clone = fs.clone();
    clone.write("/work/local.txt", "changed");
    expect(fs.read("/work/local.txt")).toBe("L");
    expect(clone.read("/work/local.txt")).toBe("changed");
    expect(clone.mounts()).toEqual(fs.mounts());
    expect(clone.read("/work/hello.txt")).toBe("original");
  });

  it("unmount", () => {
    const fs = mounting();
    fs.mount("/work", new FakeSource({ "a.txt": "x" }));
    expect(fs.mounts()).toEqual(["/work"]);
    fs.unmount("/work");
    expect(fs.mounts()).toEqual([]);
    expect(fs.exists("/work/a.txt")).toBe(false);
  });

  it("is mountable", () => {
    expect(isMountable(mounting())).toBe(true);
    expect(isMountable(inner())).toBe(false);
  });
});

function mkTemp(): string {
  return fsmod.mkdtempSync(nodePath.join(os.tmpdir(), "mount-"));
}

describe("LocalFolderSource", () => {
  it("nested files reflected", () => {
    const root = mkTemp();
    fsmod.writeFileSync(nodePath.join(root, "a.txt"), "alpha");
    fsmod.mkdirSync(nodePath.join(root, "sub"));
    fsmod.writeFileSync(nodePath.join(root, "sub", "b.txt"), "beta");

    const src = new LocalFolderSource(root);
    expect(src.stat("")!.isDir).toBe(true);
    expect(src.stat("a.txt")!.isDir).toBe(false);
    expect(src.stat("a.txt")!.size).toBe(5);
    expect(src.list("").sort()).toEqual(["a.txt", "sub"]);
    expect(src.list("sub")).toEqual(["b.txt"]);
    expect(src.read("a.txt")).toBe("alpha");
    expect(src.read("sub/b.txt")).toBe("beta");
    expect(src.stat("nope.txt")).toBeNull();
  });

  it("parent traversal cannot escape", () => {
    const base = mkTemp();
    const root = nodePath.join(base, "root");
    fsmod.mkdirSync(root);
    fsmod.writeFileSync(nodePath.join(root, "in.txt"), "inside");
    fsmod.writeFileSync(nodePath.join(base, "secret.txt"), "secret");

    const src = new LocalFolderSource(root);
    expect(src.stat("../secret.txt")).toBeNull();
  });

  it("symlink cannot escape", () => {
    const base = mkTemp();
    const root = nodePath.join(base, "root");
    fsmod.mkdirSync(root);
    const outside = nodePath.join(base, "outside.txt");
    fsmod.writeFileSync(outside, "secret");
    try {
      fsmod.symlinkSync(outside, nodePath.join(root, "link.txt"));
    } catch {
      return; // symlinks unsupported
    }
    const src = new LocalFolderSource(root);
    expect(src.stat("link.txt")).toBeNull();
  });
});

class MountAgent extends UsesTools(HasShell(class {})) {}

describe("Mount E2E", () => {
  it("visible to tools and shell", () => {
    const root = mkTemp();
    fsmod.writeFileSync(nodePath.join(root, "hello.txt"), "from-host");
    fsmod.writeFileSync(nodePath.join(root, "notes.md"), "# notes");
    fsmod.mkdirSync(nodePath.join(root, "sub"));
    fsmod.writeFileSync(nodePath.join(root, "sub", "deep.txt"), "deep-content");

    const agent = new MountAgent() as any;
    agent.mountSource("/work", new LocalFolderSource(root));

    expect(agent.exec("ls /work").stdout).toContain("hello.txt");
    expect(agent.exec("cat /work/hello.txt").stdout).toContain("from-host");
    expect(agent.exec("find /work -name '*.md'").stdout).toContain("notes.md");
    expect(agent.exec("grep -r deep /work").stdout).toContain("deep-content");

    const fs = agent.shell.fs;
    expect(fs.readText("/work/sub/deep.txt")).toBe("deep-content");
    expect(fs.exists("/work/notes.md")).toBe(true);
  });

  it("edit mounted file copy-up host unchanged", () => {
    const root = mkTemp();
    const hostFile = nodePath.join(root, "hello.txt");
    fsmod.writeFileSync(hostFile, "from-host");

    const agent = new MountAgent() as any;
    agent.mountSource("/work", new LocalFolderSource(root));

    agent.exec("echo edited > /work/hello.txt");
    expect(agent.exec("cat /work/hello.txt").stdout.trim()).toBe("edited");
    expect(fsmod.readFileSync(hostFile, "utf8")).toBe("from-host");
  });

  it("mounts listing", () => {
    const root = mkTemp();
    const agent = new MountAgent() as any;
    agent.mountSource("/work", new LocalFolderSource(root));
    expect(agent.mountSources()).toEqual(["/work"]);
    agent.unmountSource("/work");
    expect(agent.mountSources()).toEqual([]);
  });
});
