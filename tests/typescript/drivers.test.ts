import { describe, it, expect } from "vitest";
import {
  BuiltinFilesystemDriver,
  BuiltinShellDriver,
  ShellDriverFactory,
} from "../../src/typescript/drivers.js";
import type { FilesystemDriver, ShellDriver } from "../../src/typescript/drivers.js";

describe("BuiltinFilesystemDriver", () => {
  it("write and read", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/test.txt", "hello");
    expect(fs.read("/test.txt")).toBe("hello");
  });

  it("writeLazy", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.writeLazy("/lazy.txt", () => "lazy");
    expect(fs.read("/lazy.txt")).toBe("lazy");
  });

  it("exists", () => {
    const fs = new BuiltinFilesystemDriver();
    expect(fs.exists("/nope")).toBe(false);
    fs.write("/yes.txt", "y");
    expect(fs.exists("/yes.txt")).toBe(true);
  });

  it("remove", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/rm.txt", "x");
    fs.remove("/rm.txt");
    expect(fs.exists("/rm.txt")).toBe(false);
  });

  it("isDir", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/d/f.txt", "x");
    expect(fs.isDir("/d")).toBe(true);
  });

  it("listdir", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/d/a.txt", "a");
    fs.write("/d/b.txt", "b");
    expect(fs.listdir("/d")).toEqual(["a.txt", "b.txt"]);
  });

  it("find", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/src/a.ts", "a");
    fs.write("/src/b.ts", "b");
    expect(fs.find("/src", "*.ts")).toEqual(["/src/a.ts", "/src/b.ts"]);
  });

  it("stat", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/s.txt", "hello");
    const info = fs.stat("/s.txt");
    expect(info.type).toBe("file");
    expect(info.size).toBe(5);
  });

  it("clone is independent", () => {
    const fs = new BuiltinFilesystemDriver();
    fs.write("/a.txt", "a");
    const cloned = fs.clone();
    cloned.write("/b.txt", "b");
    expect(fs.exists("/b.txt")).toBe(false);
  });
});

describe("BuiltinShellDriver", () => {
  it("exec runs commands", () => {
    const driver = new BuiltinShellDriver();
    driver.fs.write("/test.txt", "hello");
    const result = driver.exec("cat /test.txt");
    expect(result.stdout).toBe("hello");
  });

  it("register and exec custom command", () => {
    const driver = new BuiltinShellDriver();
    driver.registerCommand("greet", () => ({ stdout: "hi\n", stderr: "", exitCode: 0 }));
    expect(driver.exec("greet").stdout).toBe("hi\n");
  });

  it("unregister custom command", () => {
    const driver = new BuiltinShellDriver();
    driver.registerCommand("tmp", () => ({ stdout: "x", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("tmp");
    expect(driver.exec("tmp").exitCode).toBe(127);
  });

  it("clone is independent", () => {
    const driver = new BuiltinShellDriver();
    driver.fs.write("/a.txt", "a");
    const cloned = driver.clone();
    cloned.fs.write("/b.txt", "b");
    expect(driver.fs.exists("/b.txt")).toBe(false);
  });

  it("cwd and env", () => {
    const driver = new BuiltinShellDriver({ cwd: "/tmp", env: { X: "1" } });
    expect(driver.cwd).toBe("/tmp");
    expect(driver.env["X"]).toBe("1");
  });

  it("onNotFound callback", () => {
    const driver = new BuiltinShellDriver();
    const notFound: string[] = [];
    driver.onNotFound = (name) => { notFound.push(name); };
    driver.exec("boguscmd");
    expect(notFound).toEqual(["boguscmd"]);
  });
});

describe("ShellDriverFactory", () => {
  it("default creates builtin", () => {
    ShellDriverFactory.reset();
    const driver = ShellDriverFactory.create();
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
  });

  it("create by name", () => {
    ShellDriverFactory.reset();
    const driver = ShellDriverFactory.create("builtin");
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
  });

  it("unknown driver throws", () => {
    ShellDriverFactory.reset();
    expect(() => ShellDriverFactory.create("nope")).toThrow();
  });

  it("register custom driver", () => {
    ShellDriverFactory.reset();
    ShellDriverFactory.register("custom", (opts) => new BuiltinShellDriver(opts));
    const driver = ShellDriverFactory.create("custom");
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
    ShellDriverFactory.reset();
  });

  it("set default", () => {
    ShellDriverFactory.reset();
    ShellDriverFactory.register("alt", (opts) => new BuiltinShellDriver(opts));
    ShellDriverFactory.default = "alt";
    const driver = ShellDriverFactory.create();
    expect(driver).toBeInstanceOf(BuiltinShellDriver);
    ShellDriverFactory.reset();
  });
});
