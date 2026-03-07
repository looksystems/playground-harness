/**
 * Tests for BashkitIPCDriver using a FakeProcess mock.
 */
import { describe, it, expect } from "vitest";
import { BashkitIPCDriver } from "../../src/typescript/bashkit-ipc-driver.js";
import type { ShellDriver, FilesystemDriver } from "../../src/typescript/drivers.js";
import type { ExecResult, CmdHandler } from "../../src/typescript/shell.js";

// ---------------------------------------------------------------------------
// FakeProcess infrastructure
// ---------------------------------------------------------------------------

class FakeStdin {
  _writes: string[] = [];

  write(data: string): void {
    this._writes.push(data);
  }

  lastRequest(): Record<string, unknown> {
    for (let i = this._writes.length - 1; i >= 0; i--) {
      const line = this._writes[i].trim();
      if (line) return JSON.parse(line);
    }
    throw new Error("No requests written");
  }

  allRequests(): Record<string, unknown>[] {
    const results: Record<string, unknown>[] = [];
    for (const raw of this._writes) {
      const line = raw.trim();
      if (line) results.push(JSON.parse(line));
    }
    return results;
  }
}

class FakeStdout {
  _lines: string[] = [];
  _index = 0;

  enqueue(obj: Record<string, unknown>): void {
    this._lines.push(JSON.stringify(obj) + "\n");
  }

  readline(): string {
    if (this._index < this._lines.length) {
      return this._lines[this._index++];
    }
    return "";
  }
}

class FakeProcess {
  stdin = new FakeStdin();
  stdout = new FakeStdout();
  _terminated = false;

  enqueueResponse(obj: Record<string, unknown>): void {
    this.stdout.enqueue(obj);
  }

  lastRequest(): Record<string, unknown> {
    return this.stdin.lastRequest();
  }

  allRequests(): Record<string, unknown>[] {
    return this.stdin.allRequests();
  }

  kill(): void {
    this._terminated = true;
  }
}

function createDriverWithFake(opts?: Record<string, unknown>): { driver: BashkitIPCDriver; proc: FakeProcess } {
  const proc = new FakeProcess();
  const driver = new BashkitIPCDriver({
    ...opts,
    _spawnOverride: () => proc,
  });
  return { driver, proc };
}

// ---------------------------------------------------------------------------
// Contract tests
// ---------------------------------------------------------------------------

describe("BashkitIPCDriver contract", () => {
  it("implements ShellDriver shape", () => {
    const { driver } = createDriverWithFake();
    expect(driver.fs).toBeDefined();
    expect(driver.cwd).toBeDefined();
    expect(driver.env).toBeDefined();
    expect(typeof driver.exec).toBe("function");
    expect(typeof driver.registerCommand).toBe("function");
    expect(typeof driver.unregisterCommand).toBe("function");
    expect(typeof driver.clone).toBe("function");
  });

  it("fs is a FilesystemDriver", () => {
    const { driver } = createDriverWithFake();
    expect(driver.fs).toBeDefined();
    expect(typeof driver.fs.write).toBe("function");
    expect(typeof driver.fs.read).toBe("function");
    expect(typeof driver.fs.exists).toBe("function");
  });

  it("cwd defaults to /", () => {
    const { driver } = createDriverWithFake();
    expect(driver.cwd).toBe("/");
  });

  it("cwd uses provided value", () => {
    const { driver } = createDriverWithFake({ cwd: "/home" });
    expect(driver.cwd).toBe("/home");
  });

  it("env defaults to empty object", () => {
    const { driver } = createDriverWithFake();
    expect(driver.env).toEqual({});
  });

  it("env uses provided value", () => {
    const { driver } = createDriverWithFake({ env: { FOO: "bar" } });
    expect(driver.env).toEqual({ FOO: "bar" });
  });
});

// ---------------------------------------------------------------------------
// VFS sync tests
// ---------------------------------------------------------------------------

describe("BashkitIPCDriver VFS sync", () => {
  it("exec sends snapshot with files", () => {
    const { driver, proc } = createDriverWithFake();
    driver.fs.write("/hello.txt", "world");

    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "ok",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    const result = driver.exec("echo hi");
    expect(result.stdout).toBe("ok");
    expect(result.exitCode).toBe(0);

    const req = proc.lastRequest() as any;
    expect(req.method).toBe("exec");
    expect(req.params.cmd).toBe("echo hi");
    expect(req.params.fs["/hello.txt"]).toBe("world");
  });

  it("exec returns ExecResult with correct fields", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "line1\nline2",
        stderr: "warn",
        exitCode: 2,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    const result = driver.exec("cmd");
    expect(result.stdout).toBe("line1\nline2");
    expect(result.stderr).toBe("warn");
    expect(result.exitCode).toBe(2);
  });

  it("exec applies created files from response", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: {
          created: { "/new.txt": "new content" },
          deleted: [],
        },
      },
    });

    driver.exec("touch /new.txt");
    expect(driver.fs.exists("/new.txt")).toBe(true);
    expect(driver.fs.readText("/new.txt")).toBe("new content");
  });

  it("exec applies deleted files from response", () => {
    const { driver, proc } = createDriverWithFake();
    driver.fs.write("/old.txt", "old");

    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: {
          created: {},
          deleted: ["/old.txt"],
        },
      },
    });

    driver.exec("rm /old.txt");
    expect(driver.fs.exists("/old.txt")).toBe(false);
  });

  it("exec resolves lazy files in snapshot", () => {
    const { driver, proc } = createDriverWithFake();
    driver.fs.writeLazy("/lazy.txt", () => "lazy content");

    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    driver.exec("cat /lazy.txt");
    const req = proc.lastRequest() as any;
    expect(req.params.fs["/lazy.txt"]).toBe("lazy content");
  });

  it("exec sends cwd and env in request", () => {
    const { driver, proc } = createDriverWithFake({ cwd: "/tmp", env: { PATH: "/bin" } });

    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    driver.exec("ls");
    const req = proc.lastRequest() as any;
    expect(req.params.cwd).toBe("/tmp");
    expect(req.params.env).toEqual({ PATH: "/bin" });
  });
});

// ---------------------------------------------------------------------------
// Callback tests
// ---------------------------------------------------------------------------

describe("BashkitIPCDriver callbacks", () => {
  it("registerCommand sends notification without id", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("greet", (args, stdin) => ({ stdout: "hello", stderr: "", exitCode: 0 }));

    const reqs = proc.allRequests();
    const regReqs = reqs.filter((r: any) => r.method === "register_command");
    expect(regReqs.length).toBe(1);
    expect((regReqs[0] as any).params.name).toBe("greet");
    expect(regReqs[0]).not.toHaveProperty("id");
  });

  it("invoke_command callback dispatched during exec", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("greet", (args, stdin) => ({
      stdout: `hello ${args.join(" ")}`,
      stderr: "",
      exitCode: 0,
    }));

    // During exec, bashkit sends invoke_command, then final result
    proc.enqueueResponse({
      method: "invoke_command",
      params: { name: "greet", args: ["world"] },
      id: 100,
    });
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "done",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    const result = driver.exec("greet world");
    expect(result.stdout).toBe("done");

    // Driver should have sent back callback result
    const reqs = proc.allRequests();
    const callbackResponses = reqs.filter(
      (r: any) => "result" in r && r.id === 100
    );
    expect(callbackResponses.length).toBe(1);
    expect((callbackResponses[0] as any).result).toEqual({
      stdout: "hello world",
      stderr: "",
      exitCode: 0,
    });
  });

  it("unregisterCommand sends notification without id", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("greet", (args, stdin) => ({ stdout: "hello", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("greet");

    const reqs = proc.allRequests();
    const unregReqs = reqs.filter((r: any) => r.method === "unregister_command");
    expect(unregReqs.length).toBe(1);
    expect((unregReqs[0] as any).params.name).toBe("greet");
    expect(unregReqs[0]).not.toHaveProperty("id");
  });

  it("unregisterCommand removes handler", () => {
    const { driver, proc } = createDriverWithFake();
    driver.registerCommand("greet", (args, stdin) => ({ stdout: "hello", stderr: "", exitCode: 0 }));
    driver.unregisterCommand("greet");

    // invoke_command for unregistered command returns error
    proc.enqueueResponse({
      method: "invoke_command",
      params: { name: "greet", args: [] },
      id: 101,
    });
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    driver.exec("greet");
    const reqs = proc.allRequests();
    const errorResponses = reqs.filter(
      (r: any) => "error" in r && r.id === 101
    );
    expect(errorResponses.length).toBe(1);
  });

  it("callback passes stdin to handler", () => {
    const { driver, proc } = createDriverWithFake();
    const received: Record<string, unknown> = {};

    const handler: CmdHandler = (args, stdin) => {
      received.args = args;
      received.stdin = stdin;
      return { stdout: "ok", stderr: "", exitCode: 0 };
    };

    driver.registerCommand("mycmd", handler);

    proc.enqueueResponse({
      method: "invoke_command",
      params: { name: "mycmd", args: ["a", "b"], stdin: "hello" },
      id: 200,
    });
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "done",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    driver.exec("mycmd a b");
    expect(received.args).toEqual(["a", "b"]);
    expect(received.stdin).toBe("hello");
  });

  it("callback handler exception returns error response", () => {
    const { driver, proc } = createDriverWithFake();

    const badHandler: CmdHandler = (args, stdin) => {
      throw new Error("handler blew up");
    };

    driver.registerCommand("boom", badHandler);

    proc.enqueueResponse({
      method: "invoke_command",
      params: { name: "boom", args: [] },
      id: 300,
    });
    proc.enqueueResponse({
      id: 1,
      result: {
        stdout: "",
        stderr: "",
        exitCode: 0,
        fs_changes: { created: {}, deleted: [] },
      },
    });

    const result = driver.exec("boom");
    expect(result.exitCode).toBe(0); // exec itself succeeds

    const reqs = proc.allRequests();
    const errorResponses = reqs.filter(
      (r: any) => "error" in r && r.id === 300
    );
    expect(errorResponses.length).toBe(1);
    expect((errorResponses[0] as any).error.message).toContain("handler blew up");
  });
});

// ---------------------------------------------------------------------------
// Lifecycle tests
// ---------------------------------------------------------------------------

describe("BashkitIPCDriver lifecycle", () => {
  it("clone creates independent instance with cloned FS", () => {
    const { driver, proc } = createDriverWithFake({ cwd: "/home", env: { A: "1" } });
    driver.fs.write("/file.txt", "data");
    driver.registerCommand("cmd1", (args, stdin) => ({ stdout: "r1", stderr: "", exitCode: 0 }));

    const proc2 = new FakeProcess();
    const cloned = driver.clone({ _spawnOverride: () => proc2 });

    expect(cloned.cwd).toBe("/home");
    expect(cloned.env).toEqual({ A: "1" });
    expect(cloned.fs.exists("/file.txt")).toBe(true);
    expect(cloned.fs.readText("/file.txt")).toBe("data");

    // Independence: modifying clone doesn't affect original
    cloned.fs.write("/clone_only.txt", "clone");
    expect(driver.fs.exists("/clone_only.txt")).toBe(false);

    // Clone should re-register commands with new process
    const reqs = proc2.allRequests();
    const regReqs = reqs.filter((r: any) => r.method === "register_command");
    expect(regReqs.length).toBe(1);
    expect((regReqs[0] as any).params.name).toBe("cmd1");
    expect(regReqs[0]).not.toHaveProperty("id");
  });

  it("onNotFound property get/set", () => {
    const { driver } = createDriverWithFake();
    expect(driver.onNotFound).toBeUndefined();

    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;
    expect(driver.onNotFound).toBe(handler);

    driver.onNotFound = undefined;
    expect(driver.onNotFound).toBeUndefined();
  });

  it("clone copies onNotFound", () => {
    const { driver } = createDriverWithFake();
    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;

    const proc2 = new FakeProcess();
    const cloned = driver.clone({ _spawnOverride: () => proc2 });
    expect(cloned.onNotFound).toBe(handler);
  });

  it("error response returns ExecResult with stderr", () => {
    const { driver, proc } = createDriverWithFake();
    proc.enqueueResponse({
      id: 1,
      error: { code: -1, message: "something broke" },
    });

    const result = driver.exec("bad_cmd");
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr).toContain("something broke");
  });

  it("destroy kills the process", () => {
    const { driver, proc } = createDriverWithFake();
    expect(proc._terminated).toBe(false);
    driver.destroy();
    expect(proc._terminated).toBe(true);
  });
});
