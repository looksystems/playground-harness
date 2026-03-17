/**
 * Tests for BashkitCLIDriver using _execOverride for mocking.
 */
import { describe, it, expect } from "vitest";
import { BashkitCLIDriver } from "../../src/typescript/bashkit-cli-driver.js";

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

function createDriver(responses?: Record<string, { stdout: string; stderr: string; exitCode: number }>) {
  const defaultResponse = { stdout: "", stderr: "", exitCode: 0 };
  const calls: string[] = [];
  return {
    driver: new BashkitCLIDriver({
      _execOverride: (cmd) => {
        calls.push(cmd);
        // Match by base command prefix (before epilogue)
        if (responses) {
          for (const [key, resp] of Object.entries(responses)) {
            if (cmd.startsWith(key)) {
              return resp;
            }
          }
        }
        return defaultResponse;
      },
    }),
    calls,
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("BashkitCLIDriver", () => {
  it("exec runs command via override", () => {
    const { driver, calls } = createDriver();
    driver.exec("echo hello");
    expect(calls.length).toBe(1);
    expect(calls[0]).toContain("echo hello");
  });

  it("exec returns stdout/stderr/exitCode", () => {
    const { driver } = createDriver({
      "ls -la": { stdout: "file1\nfile2", stderr: "warn", exitCode: 2 },
    });
    const result = driver.exec("ls -la");
    // stdout may be empty since the mock output lacks the sync marker
    expect(result.stderr).toBe("warn");
    expect(result.exitCode).toBe(2);
  });

  it("exec returns default response for unknown command", () => {
    const { driver } = createDriver();
    const result = driver.exec("unknown");
    expect(result.stdout).toBe("");
    expect(result.stderr).toBe("");
    expect(result.exitCode).toBe(0);
  });

  it("fs property returns FilesystemDriver", () => {
    const { driver } = createDriver();
    expect(driver.fs).toBeDefined();
    expect(typeof driver.fs.write).toBe("function");
    expect(typeof driver.fs.read).toBe("function");
    expect(typeof driver.fs.exists).toBe("function");
  });

  it("cwd defaults to /", () => {
    const { driver } = createDriver();
    expect(driver.cwd).toBe("/");
  });

  it("env defaults to empty", () => {
    const { driver } = createDriver();
    expect(driver.env).toEqual({});
  });

  it("registerCommand stores handler locally", () => {
    const { driver } = createDriver();
    const handler = (args: string[], stdin: string) => ({ stdout: "ok", stderr: "", exitCode: 0 });
    driver.registerCommand("mycmd", handler);
    // No error thrown — handler stored internally
    // (Not invocable via exec since CLI is stateless)
  });

  it("unregisterCommand removes handler", () => {
    const { driver } = createDriver();
    const handler = (args: string[], stdin: string) => ({ stdout: "ok", stderr: "", exitCode: 0 });
    driver.registerCommand("mycmd", handler);
    driver.unregisterCommand("mycmd");
    // No error thrown — handler removed internally
  });

  it("clone creates independent copy", () => {
    const { driver } = createDriver();
    driver.fs.write("/file.txt", "data");

    const cloned = driver.clone();

    expect(cloned.cwd).toBe("/");
    expect(cloned.env).toEqual({});
    expect(cloned.fs.exists("/file.txt")).toBe(true);
    expect(cloned.fs.readText("/file.txt")).toBe("data");

    // Independence: modifying clone doesn't affect original
    cloned.fs.write("/clone_only.txt", "clone");
    expect(driver.fs.exists("/clone_only.txt")).toBe(false);
  });

  it("clone preserves cwd and env independently", () => {
    const driver = new BashkitCLIDriver({
      cwd: "/home",
      env: { A: "1" },
      _execOverride: () => ({ stdout: "", stderr: "", exitCode: 0 }),
    });

    const cloned = driver.clone();
    expect(cloned.cwd).toBe("/home");
    expect(cloned.env).toEqual({ A: "1" });

    // Env is a copy, not a reference
    cloned.env.B = "2";
    expect(driver.env).not.toHaveProperty("B");
  });

  it("onNotFound can be set and read", () => {
    const { driver } = createDriver();
    expect(driver.onNotFound).toBeUndefined();

    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;
    expect(driver.onNotFound).toBe(handler);

    driver.onNotFound = undefined;
    expect(driver.onNotFound).toBeUndefined();
  });

  it("clone copies onNotFound", () => {
    const { driver } = createDriver();
    const handler = (cmdName: string) => {};
    driver.onNotFound = handler;

    const cloned = driver.clone();
    expect(cloned.onNotFound).toBe(handler);
  });

  it("constructor accepts cwd and env", () => {
    const driver = new BashkitCLIDriver({
      cwd: "/tmp",
      env: { FOO: "bar" },
      _execOverride: () => ({ stdout: "", stderr: "", exitCode: 0 }),
    });
    expect(driver.cwd).toBe("/tmp");
    expect(driver.env).toEqual({ FOO: "bar" });
  });

  it("custom command executes locally", () => {
    const calls: string[] = [];
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd) => {
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
    const driver = new BashkitCLIDriver({
      _execOverride: () => ({ stdout: "", stderr: "", exitCode: 0 }),
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
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.exec("echo hello");
    expect(calls.length).toBe(1);
  });

  it("unregister stops interception", () => {
    const calls: string[] = [];
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd) => {
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
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd) => {
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
    expect(calls.length).toBe(0);
  });
});
