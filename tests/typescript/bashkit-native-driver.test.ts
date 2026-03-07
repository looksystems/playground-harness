/**
 * Tests for BashkitNativeDriver using a MockBashkitLib mock.
 */
import { describe, it, expect } from "vitest";
import { BashkitNativeDriver } from "../../src/typescript/bashkit-native-driver.js";
import type { ShellDriver, FilesystemDriver } from "../../src/typescript/drivers.js";
import type { ExecResult, CmdHandler } from "../../src/typescript/shell.js";

// ---------------------------------------------------------------------------
// MockBashkitLib infrastructure
// ---------------------------------------------------------------------------

class MockBashkitLib {
  private _nextCtx = 1;
  private _responses: Record<string, unknown>[] = [];
  private _lastRequestObj: Record<string, unknown> | null = null;
  private _callbacks: Map<string, { cb: (argsJson: string) => string; userdata: unknown }> = new Map();
  private _destroyed: Set<number> = new Set();
  private _registeredCommands: Map<number, Set<string>> = new Map();
  private _freedStrings: string[] = [];

  enqueueResponse(obj: Record<string, unknown>): void {
    this._responses.push(obj);
  }

  lastRequest(): Record<string, unknown> | null {
    return this._lastRequestObj;
  }

  bashkit_create(configJson: string): number {
    const ctx = this._nextCtx++;
    this._registeredCommands.set(ctx, new Set());
    return ctx;
  }

  bashkit_destroy(ctx: number): void {
    this._destroyed.add(ctx);
  }

  bashkit_exec(ctx: number, requestJson: string): string {
    this._lastRequestObj = JSON.parse(requestJson);
    const response = this._responses.shift();
    if (!response) {
      return JSON.stringify({ stdout: "", stderr: "No response queued", exitCode: 1 });
    }
    return JSON.stringify(response);
  }

  bashkit_register_command(ctx: number, name: string, cb: (argsJson: string) => string, userdata: unknown): void {
    this._callbacks.set(name, { cb, userdata });
    this._registeredCommands.get(ctx)?.add(name);
  }

  bashkit_unregister_command(ctx: number, name: string): void {
    this._callbacks.delete(name);
    this._registeredCommands.get(ctx)?.delete(name);
  }

  bashkit_free_string(s: string): void {
    this._freedStrings.push(s);
  }

  invokeCallback(name: string, argsJson: string): string {
    const entry = this._callbacks.get(name);
    if (!entry) throw new Error(`No callback registered for ${name}`);
    return entry.cb(argsJson);
  }

  isDestroyed(ctx: number): boolean {
    return this._destroyed.has(ctx);
  }

  getRegisteredCommands(ctx: number): Set<string> {
    return this._registeredCommands.get(ctx) ?? new Set();
  }
}

function createDriverWithMock(opts?: Record<string, unknown>): { driver: BashkitNativeDriver; lib: MockBashkitLib } {
  const lib = new MockBashkitLib();
  const driver = new BashkitNativeDriver({
    ...opts,
    _libOverride: lib,
  });
  return { driver, lib };
}

// ---------------------------------------------------------------------------
// Contract tests
// ---------------------------------------------------------------------------

describe("BashkitNativeDriver contract", () => {
  it("implements ShellDriver shape", () => {
    const { driver } = createDriverWithMock();
    expect(driver.fs).toBeDefined();
    expect(driver.cwd).toBeDefined();
    expect(driver.env).toBeDefined();
    expect(typeof driver.exec).toBe("function");
    expect(typeof driver.registerCommand).toBe("function");
    expect(typeof driver.unregisterCommand).toBe("function");
    expect(typeof driver.clone).toBe("function");
  });

  it("fs is a FilesystemDriver", () => {
    const { driver } = createDriverWithMock();
    expect(driver.fs).toBeDefined();
    expect(typeof driver.fs.write).toBe("function");
    expect(typeof driver.fs.read).toBe("function");
    expect(typeof driver.fs.exists).toBe("function");
  });

  it("cwd defaults to /", () => {
    const { driver } = createDriverWithMock();
    expect(driver.cwd).toBe("/");
  });

  it("cwd uses provided value", () => {
    const { driver } = createDriverWithMock({ cwd: "/home" });
    expect(driver.cwd).toBe("/home");
  });

  it("env defaults to empty object", () => {
    const { driver } = createDriverWithMock();
    expect(driver.env).toEqual({});
  });

  it("env uses provided value", () => {
    const { driver } = createDriverWithMock({ env: { FOO: "bar" } });
    expect(driver.env).toEqual({ FOO: "bar" });
  });
});

// ---------------------------------------------------------------------------
// VFS sync tests
// ---------------------------------------------------------------------------

describe("BashkitNativeDriver VFS sync", () => {
  it("exec sends snapshot with files", () => {
    const { driver, lib } = createDriverWithMock();
    driver.fs.write("/hello.txt", "world");

    lib.enqueueResponse({
      stdout: "ok",
      stderr: "",
      exitCode: 0,
      fs_changes: { created: {}, deleted: [] },
    });

    const result = driver.exec("echo hi");
    expect(result.stdout).toBe("ok");
    expect(result.exitCode).toBe(0);

    const req = lib.lastRequest() as any;
    expect(req.cmd).toBe("echo hi");
    expect(req.fs["/hello.txt"]).toBe("world");
  });

  it("exec sends cwd and env in request", () => {
    const { driver, lib } = createDriverWithMock({ cwd: "/tmp", env: { PATH: "/bin" } });

    lib.enqueueResponse({
      stdout: "",
      stderr: "",
      exitCode: 0,
      fs_changes: { created: {}, deleted: [] },
    });

    driver.exec("ls");
    const req = lib.lastRequest() as any;
    expect(req.cwd).toBe("/tmp");
    expect(req.env).toEqual({ PATH: "/bin" });
  });

  it("exec returns ExecResult with correct fields", () => {
    const { driver, lib } = createDriverWithMock();
    lib.enqueueResponse({
      stdout: "line1\nline2",
      stderr: "warn",
      exitCode: 2,
      fs_changes: { created: {}, deleted: [] },
    });

    const result = driver.exec("cmd");
    expect(result.stdout).toBe("line1\nline2");
    expect(result.stderr).toBe("warn");
    expect(result.exitCode).toBe(2);
  });

  it("exec applies created files from response", () => {
    const { driver, lib } = createDriverWithMock();
    lib.enqueueResponse({
      stdout: "",
      stderr: "",
      exitCode: 0,
      fs_changes: {
        created: { "/new.txt": "new content" },
        deleted: [],
      },
    });

    driver.exec("touch /new.txt");
    expect(driver.fs.exists("/new.txt")).toBe(true);
    expect(driver.fs.readText("/new.txt")).toBe("new content");
  });

  it("exec applies deleted files from response", () => {
    const { driver, lib } = createDriverWithMock();
    driver.fs.write("/old.txt", "old");

    lib.enqueueResponse({
      stdout: "",
      stderr: "",
      exitCode: 0,
      fs_changes: {
        created: {},
        deleted: ["/old.txt"],
      },
    });

    driver.exec("rm /old.txt");
    expect(driver.fs.exists("/old.txt")).toBe(false);
  });

  it("exec resolves lazy files in snapshot", () => {
    const { driver, lib } = createDriverWithMock();
    driver.fs.writeLazy("/lazy.txt", () => "lazy content");

    lib.enqueueResponse({
      stdout: "",
      stderr: "",
      exitCode: 0,
      fs_changes: { created: {}, deleted: [] },
    });

    driver.exec("cat /lazy.txt");
    const req = lib.lastRequest() as any;
    expect(req.fs["/lazy.txt"]).toBe("lazy content");
  });

  it("exec handles error response", () => {
    const { driver, lib } = createDriverWithMock();
    lib.enqueueResponse({
      stdout: "",
      stderr: "something broke",
      exitCode: 1,
    });

    const result = driver.exec("bad_cmd");
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("something broke");
  });
});

// ---------------------------------------------------------------------------
// Callback tests
// ---------------------------------------------------------------------------

describe("BashkitNativeDriver callbacks", () => {
  it("registerCommand registers on the mock library", () => {
    const { driver, lib } = createDriverWithMock();
    driver.registerCommand("greet", (args, stdin) => ({ stdout: "hello", stderr: "", exitCode: 0 }));

    // The mock should have the callback registered
    const result = lib.invokeCallback("greet", JSON.stringify({ name: "greet", args: ["world"], stdin: "" }));
    expect(result).toBeDefined();
  });

  it("callback invocation returns stdout from handler", () => {
    const { driver, lib } = createDriverWithMock();
    driver.registerCommand("greet", (args, stdin) => ({
      stdout: `hello ${args.join(" ")}`,
      stderr: "",
      exitCode: 0,
    }));

    const result = lib.invokeCallback("greet", JSON.stringify({ name: "greet", args: ["world"], stdin: "" }));
    expect(result).toBe("hello world");
  });

  it("callback passes stdin to handler", () => {
    const { driver, lib } = createDriverWithMock();
    const received: Record<string, unknown> = {};

    driver.registerCommand("mycmd", (args, stdin) => {
      received.args = args;
      received.stdin = stdin;
      return { stdout: "ok", stderr: "", exitCode: 0 };
    });

    lib.invokeCallback("mycmd", JSON.stringify({ name: "mycmd", args: ["a", "b"], stdin: "hello" }));
    expect(received.args).toEqual(["a", "b"]);
    expect(received.stdin).toBe("hello");
  });

  it("callback handler exception returns JSON error", () => {
    const { driver, lib } = createDriverWithMock();

    driver.registerCommand("boom", (args, stdin) => {
      throw new Error("handler blew up");
    });

    const result = lib.invokeCallback("boom", JSON.stringify({ name: "boom", args: [], stdin: "" }));
    const parsed = JSON.parse(result);
    expect(parsed.error).toContain("handler blew up");
  });

  it("unregisterCommand removes handler from mock", () => {
    const { driver, lib } = createDriverWithMock();
    driver.registerCommand("greet", (args, stdin) => ({ stdout: "hello", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("greet");

    expect(() => lib.invokeCallback("greet", JSON.stringify({ name: "greet", args: [], stdin: "" }))).toThrow();
  });
});

// ---------------------------------------------------------------------------
// Lifecycle tests
// ---------------------------------------------------------------------------

describe("BashkitNativeDriver lifecycle", () => {
  it("clone creates independent instance with cloned FS", () => {
    const { driver } = createDriverWithMock({ cwd: "/home", env: { A: "1" } });
    driver.fs.write("/file.txt", "data");

    const cloned = driver.clone();

    expect(cloned.cwd).toBe("/home");
    expect(cloned.env).toEqual({ A: "1" });
    expect(cloned.fs.exists("/file.txt")).toBe(true);
    expect(cloned.fs.readText("/file.txt")).toBe("data");

    // Independence: modifying clone doesn't affect original
    cloned.fs.write("/clone_only.txt", "clone");
    expect(driver.fs.exists("/clone_only.txt")).toBe(false);
  });

  it("clone preserves cwd and env independently", () => {
    const { driver } = createDriverWithMock({ cwd: "/home", env: { A: "1" } });

    const cloned = driver.clone();
    expect(cloned.cwd).toBe("/home");
    expect(cloned.env).toEqual({ A: "1" });

    // Env is a copy, not a reference
    cloned.env.B = "2";
    expect(driver.env).not.toHaveProperty("B");
  });

  it("onNotFound property get/set", () => {
    const { driver } = createDriverWithMock();
    expect(driver.onNotFound).toBeUndefined();

    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;
    expect(driver.onNotFound).toBe(handler);

    driver.onNotFound = undefined;
    expect(driver.onNotFound).toBeUndefined();
  });

  it("clone copies onNotFound", () => {
    const { driver } = createDriverWithMock();
    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;

    const cloned = driver.clone();
    expect(cloned.onNotFound).toBe(handler);
  });

  it("destroy cleans up the context", () => {
    const { driver, lib } = createDriverWithMock();
    driver.destroy();
    // Should not throw when called again (idempotent)
    driver.destroy();
  });
});

// ---------------------------------------------------------------------------
// Library discovery tests
// ---------------------------------------------------------------------------

describe("BashkitNativeDriver.findLibrary", () => {
  it("returns undefined when no library is found", () => {
    const result = BashkitNativeDriver.findLibrary();
    // In test environment, no real library exists
    expect(result).toBeUndefined();
  });
});
