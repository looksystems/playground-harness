/**
 * Tests for OpenShellGrpcDriver using _execOverride mock.
 */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { OpenShellDriver, registerOpenShellDriver } from "../../src/typescript/openshell-driver.js";
import { OpenShellGrpcDriver, type ExecStreamEvent } from "../../src/typescript/openshell-grpc-driver.js";
import { ShellDriverFactory } from "../../src/typescript/drivers.js";

function fakeExecOverride(cmd: string) {
  return { stdout: "", stderr: "", exitCode: 0 };
}

function makeSyncOutput(marker: string, files: Record<string, string>): string {
  const lines: string[] = [];
  for (const [path, content] of Object.entries(files)) {
    lines.push(`===FILE:${path}===`);
    lines.push(Buffer.from(content).toString("base64"));
  }
  return `\n${marker}\n${lines.join("\n")}`;
}

// ---------------------------------------------------------------------------
// Resolver + factory
// ---------------------------------------------------------------------------

describe("OpenShellDriver", () => {
  beforeEach(() => {
    ShellDriverFactory.reset();
  });

  afterEach(() => {
    ShellDriverFactory.reset();
  });

  describe("resolve()", () => {
    it("returns OpenShellGrpcDriver when _execOverride provided", () => {
      const driver = OpenShellDriver.resolve({
        _execOverride: fakeExecOverride,
      });
      expect(driver).toBeInstanceOf(OpenShellGrpcDriver);
    });

    it("passes options through to driver", () => {
      const driver = OpenShellDriver.resolve({
        _execOverride: fakeExecOverride,
        cwd: "/tmp",
        env: { FOO: "bar" },
      });
      expect(driver.cwd).toBe("/tmp");
      expect(driver.env).toEqual({ FOO: "bar" });
    });
  });

  describe("registerOpenShellDriver()", () => {
    it("factory.create returns OpenShellGrpcDriver with _execOverride", () => {
      registerOpenShellDriver();
      const driver = ShellDriverFactory.create("openshell", {
        _execOverride: fakeExecOverride,
      } as any);
      expect(driver).toBeInstanceOf(OpenShellGrpcDriver);
    });

    it("factory.create without registration throws 'not registered'", () => {
      expect(() => ShellDriverFactory.create("openshell")).toThrow(
        "not registered"
      );
    });
  });
});

// ---------------------------------------------------------------------------
// OpenShellGrpcDriver contract
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver contract", () => {
  it("implements ShellDriver interface", () => {
    const driver = new OpenShellGrpcDriver({ _execOverride: fakeExecOverride });
    expect(driver.fs).toBeDefined();
    expect(driver.cwd).toBe("/");
    expect(driver.env).toEqual({});
    expect(typeof driver.exec).toBe("function");
    expect(typeof driver.registerCommand).toBe("function");
    expect(typeof driver.unregisterCommand).toBe("function");
    expect(typeof driver.clone).toBe("function");
  });

  it("custom cwd and env", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      cwd: "/home",
      env: { A: "1" },
    });
    expect(driver.cwd).toBe("/home");
    expect(driver.env).toEqual({ A: "1" });
  });

  it("policy defaults", () => {
    const driver = new OpenShellGrpcDriver({ _execOverride: fakeExecOverride });
    expect(driver.policy.inferenceRouting).toBe(true);
  });

  it("custom policy", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      policy: {
        filesystemAllow: ["/data"],
        networkRules: [{ allow: "10.0.0.0/8" }],
        inferenceRouting: false,
      },
    });
    expect(driver.policy.filesystemAllow).toEqual(["/data"]);
    expect(driver.policy.inferenceRouting).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Exec + VFS sync
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver exec", () => {
  function makeDriver(): { driver: OpenShellGrpcDriver; calls: string[] } {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    return { driver, calls };
  }

  function makeDriverWithSyncBack(syncFiles: Record<string, string>): { driver: OpenShellGrpcDriver; calls: string[] } {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return { stdout: `output${makeSyncOutput(marker, syncFiles)}`, stderr: "", exitCode: 0 };
      },
    });
    return { driver, calls };
  }

  it("exec returns stdout/stderr/exitCode", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return { stdout: `hello\n${marker}\n`, stderr: "warn", exitCode: 0 };
      },
    });
    const result = driver.exec("echo hello");
    expect(result.stdout).toBe("hello");
    expect(result.stderr).toBe("warn");
    expect(result.exitCode).toBe(0);
  });

  it("dirty file synced as base64 preamble", () => {
    const { driver, calls } = makeDriver();
    driver.fs.write("/hello.txt", "world");
    driver.exec("cat /hello.txt");
    expect(calls.length).toBe(1);
    expect(calls[0]).toContain("base64");
    expect(calls[0]).toContain("/hello.txt");
    expect(calls[0]).toContain(Buffer.from("world").toString("base64"));
  });

  it("no preamble when no dirty files", () => {
    const { driver, calls } = makeDriver();
    driver.exec("echo hi");
    expect(calls.length).toBe(1);
    expect(calls[0]).toMatch(/^echo hi/);
  });

  it("removed file synced as rm -f", () => {
    const { driver, calls } = makeDriver();
    driver.fs.write("/file.txt", "data");
    driver.exec("ls");
    driver.fs.remove("/file.txt");
    driver.exec("ls");
    const lastCmd = calls[calls.length - 1];
    expect(lastCmd).toContain("rm -f '/file.txt'");
  });

  it("new file from sandbox synced back to VFS", () => {
    const { driver } = makeDriverWithSyncBack({ "/created.txt": "from sandbox" });
    driver.exec("echo from sandbox > /created.txt");
    expect(driver.fs.exists("/created.txt")).toBe(true);
    expect(driver.fs.readText("/created.txt")).toBe("from sandbox");
  });

  it("modified file synced back", () => {
    const { driver } = makeDriverWithSyncBack({ "/existing.txt": "modified" });
    driver.fs.write("/existing.txt", "original");
    driver.exec("echo modified > /existing.txt");
    expect(driver.fs.readText("/existing.txt")).toBe("modified");
  });

  it("deleted file removed from VFS", () => {
    const { driver } = makeDriverWithSyncBack({});
    driver.fs.write("/to_delete.txt", "data");
    driver.exec("rm /to_delete.txt");
    expect(driver.fs.exists("/to_delete.txt")).toBe(false);
  });

  it("stdout separated from sync data", () => {
    const { driver } = makeDriverWithSyncBack({ "/f.txt": "data" });
    const result = driver.exec("echo hello");
    expect(result.stdout).toBe("output");
  });

  it("special characters survive round-trip", () => {
    const content = "quotes'here\nback\\slash\n%percent";
    const { driver } = makeDriverWithSyncBack({ "/special.txt": content });
    driver.exec("echo special");
    expect(driver.fs.readText("/special.txt")).toBe(content);
  });
});

// ---------------------------------------------------------------------------
// Custom command dispatch
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver custom command dispatch", () => {
  it("custom command executes locally", () => {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.registerCommand("greet", (args, stdin) => ({
      stdout: "hello\n",
      stderr: "",
      exitCode: 0,
    }));
    const result = driver.exec("greet");
    expect(result.stdout).toBe("hello\n");
    expect(result.exitCode).toBe(0);
    expect(calls.length).toBe(0);
  });

  it("custom command receives args", () => {
    const receivedArgs: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
    });
    driver.registerCommand("mycmd", (args, stdin) => {
      receivedArgs.push(...args);
      return { stdout: "ok\n", stderr: "", exitCode: 0 };
    });
    const result = driver.exec("mycmd foo bar");
    expect(receivedArgs).toEqual(["foo", "bar"]);
    expect(result.stdout).toBe("ok\n");
  });

  it("unregistered command falls through to remote", () => {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.exec("echo hello");
    expect(calls.length).toBe(1);
  });

  it("unregister stops interception", () => {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.registerCommand("mycmd", (args, stdin) => ({
      stdout: "local\n",
      stderr: "",
      exitCode: 0,
    }));
    driver.unregisterCommand("mycmd");
    driver.exec("mycmd");
    expect(calls.length).toBe(1);
  });

  it("VFS sync skipped for custom commands", () => {
    const calls: string[] = [];
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.fs.write("/dirty.txt", "data");
    driver.registerCommand("mycmd", (args, stdin) => ({
      stdout: "ok\n",
      stderr: "",
      exitCode: 0,
    }));
    driver.exec("mycmd");
    // No remote call means dirty files were not synced
    expect(calls.length).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Clone
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver clone", () => {
  it("clone creates independent copy", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
    });
    driver.fs.write("/a.txt", "alpha");
    driver.exec("ls");
    const cloned = driver.clone();
    expect(cloned.fs.exists("/a.txt")).toBe(true);
    cloned.fs.write("/b.txt", "beta");
    expect(driver.fs.exists("/b.txt")).toBe(false);
  });

  it("clone has null sandboxId", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      sandboxId: "test-sandbox",
    });
    const cloned = driver.clone();
    expect(cloned.sandboxId).toBeNull();
  });

  it("clone preserves policy", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      policy: { filesystemAllow: ["/data"] },
    });
    const cloned = driver.clone();
    expect(cloned.policy.filesystemAllow).toEqual(["/data"]);
  });

  it("clone env is independent", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      env: { A: "1" },
    });
    const cloned = driver.clone();
    cloned.env["B"] = "2";
    expect(driver.env).not.toHaveProperty("B");
  });
});

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver lifecycle", () => {
  it("close resets sandboxId", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      sandboxId: "test-sandbox",
    });
    expect(driver.sandboxId).toBe("test-sandbox");
    driver.close();
    expect(driver.sandboxId).toBeNull();
  });

  it("onNotFound defaults to undefined", () => {
    const driver = new OpenShellGrpcDriver({ _execOverride: fakeExecOverride });
    expect(driver.onNotFound).toBeUndefined();
  });

  it("onNotFound can be set and cleared", () => {
    const driver = new OpenShellGrpcDriver({ _execOverride: fakeExecOverride });
    const handler = () => {};
    driver.onNotFound = handler;
    expect(driver.onNotFound).toBe(handler);
    driver.onNotFound = undefined;
    expect(driver.onNotFound).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// gRPC transport tests
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver transport selection", () => {
  it("defaults to ssh transport", () => {
    const driver = new OpenShellGrpcDriver({ _execOverride: fakeExecOverride });
    expect((driver as any)._transport).toBe("ssh");
  });

  it("accepts grpc transport option", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      transport: "grpc",
    });
    expect((driver as any)._transport).toBe("grpc");
  });

  it("grpc transport includes streaming capability", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      transport: "grpc",
    });
    expect(driver.capabilities().has("streaming")).toBe(true);
  });

  it("ssh transport excludes streaming capability", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      transport: "ssh",
    });
    expect(driver.capabilities().has("streaming")).toBe(false);
  });

  it("clone preserves transport setting", () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
      transport: "grpc",
    });
    const cloned = driver.clone();
    expect((cloned as any)._transport).toBe("grpc");
  });
});

// ---------------------------------------------------------------------------
// execStream tests
// ---------------------------------------------------------------------------

describe("OpenShellGrpcDriver execStream", () => {
  async function collectEvents(gen: AsyncGenerator<ExecStreamEvent>): Promise<ExecStreamEvent[]> {
    const events: ExecStreamEvent[] = [];
    for await (const event of gen) {
      events.push(event);
    }
    return events;
  }

  it("yields stdout and exit events", async () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return { stdout: `hello\n${marker}\n`, stderr: "", exitCode: 0 };
      },
    });
    const events = await collectEvents(driver.execStream("echo hello"));
    const types = events.map((e) => e.type);
    expect(types).toContain("stdout");
    expect(types).toContain("exit");
  });

  it("exit event carries exitCode", async () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return { stdout: `\n${marker}\n`, stderr: "", exitCode: 42 };
      },
    });
    const events = await collectEvents(driver.execStream("fail"));
    const exitEvent = events.find((e) => e.type === "exit");
    expect(exitEvent).toBeDefined();
    expect((exitEvent as any).exitCode).toBe(42);
  });

  it("VFS sync happens after stream completion", async () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: (cmd: string) => {
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return {
          stdout: `output${makeSyncOutput(marker, { "/result.txt": "streamed" })}`,
          stderr: "",
          exitCode: 0,
        };
      },
    });
    await collectEvents(driver.execStream("echo output"));
    expect(driver.fs.exists("/result.txt")).toBe(true);
    expect(driver.fs.readText("/result.txt")).toBe("streamed");
  });

  it("custom command streams locally", async () => {
    const driver = new OpenShellGrpcDriver({
      _execOverride: fakeExecOverride,
    });
    driver.registerCommand("greet", (args, stdin) => ({
      stdout: "hi\n",
      stderr: "",
      exitCode: 0,
    }));
    const events = await collectEvents(driver.execStream("greet"));
    const stdoutEvents = events.filter((e) => e.type === "stdout");
    expect(stdoutEvents.length).toBe(1);
    expect((stdoutEvents[0] as any).data).toBe("hi\n");
  });

  it("throws without grpc transport or execOverride", async () => {
    const driver = new OpenShellGrpcDriver({ transport: "ssh" });
    await expect(async () => {
      await collectEvents(driver.execStream("echo hi"));
    }).rejects.toThrow("execStream requires");
  });
});
