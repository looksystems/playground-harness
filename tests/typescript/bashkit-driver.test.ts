/**
 * Tests for BashkitDriver resolver, factory registration, and BashkitCLIDriver VFS sync.
 */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { execSync } from "child_process";
import { BashkitDriver, registerBashkitDriver } from "../../src/typescript/bashkit-driver.js";
import { BashkitCLIDriver } from "../../src/typescript/bashkit-cli-driver.js";
import { ShellDriverFactory } from "../../src/typescript/drivers.js";

// ---------------------------------------------------------------------------
// Detect whether bashkit CLI is available
// ---------------------------------------------------------------------------

let hasBashkit = false;
try {
  execSync("which bashkit", { stdio: "ignore" });
  hasBashkit = true;
} catch {
  hasBashkit = false;
}

function fakeExecOverride(cmd: string) {
  return { stdout: "", stderr: "", exitCode: 0 };
}

describe("BashkitDriver", () => {
  beforeEach(() => {
    ShellDriverFactory.reset();
  });

  afterEach(() => {
    ShellDriverFactory.reset();
  });

  describe("resolve()", () => {
    it("returns BashkitCLIDriver when _execOverride provided", () => {
      const driver = BashkitDriver.resolve({
        _execOverride: fakeExecOverride,
      });
      expect(driver).toBeInstanceOf(BashkitCLIDriver);
    });

    it("passes options through to CLI driver", () => {
      const driver = BashkitDriver.resolve({
        _execOverride: fakeExecOverride,
        cwd: "/tmp",
        env: { FOO: "bar" },
      });
      expect(driver.cwd).toBe("/tmp");
      expect(driver.env).toEqual({ FOO: "bar" });
    });

    it.skipIf(hasBashkit)("throws when bashkit is not available and no override", () => {
      expect(() => BashkitDriver.resolve()).toThrow("bashkit not found");
    });

    it.skipIf(!hasBashkit)("returns BashkitCLIDriver when bashkit is available", () => {
      const driver = BashkitDriver.resolve();
      expect(driver).toBeInstanceOf(BashkitCLIDriver);
    });
  });

  describe("registerBashkitDriver()", () => {
    it.skipIf(hasBashkit)("after registration, create throws 'bashkit not found' when not installed", () => {
      registerBashkitDriver();
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "bashkit not found"
      );
    });

    it.skipIf(!hasBashkit)("after registration, create returns BashkitCLIDriver when installed", () => {
      registerBashkitDriver();
      const driver = ShellDriverFactory.create("bashkit");
      expect(driver).toBeInstanceOf(BashkitCLIDriver);
    });

    it("factory.create returns BashkitCLIDriver with _execOverride", () => {
      registerBashkitDriver();
      const driver = ShellDriverFactory.create("bashkit", {
        _execOverride: fakeExecOverride,
      } as any);
      expect(driver).toBeInstanceOf(BashkitCLIDriver);
    });

    it("factory.create without registration throws 'not registered'", () => {
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "not registered"
      );
    });
  });
});

// ---------------------------------------------------------------------------
// BashkitCLIDriver VFS sync tests (using _execOverride)
// ---------------------------------------------------------------------------

function makeSyncOutput(marker: string, files: Record<string, string>): string {
  const lines: string[] = [];
  for (const [path, content] of Object.entries(files)) {
    lines.push(`===FILE:${path}===`);
    lines.push(Buffer.from(content).toString("base64"));
  }
  return `\n${marker}\n${lines.join("\n")}`;
}

describe("BashkitCLIDriver VFS sync", () => {
  /** Create driver where sync-back is a no-op (exit_code=1 skips sync). */
  function makeDriver(): { driver: BashkitCLIDriver; calls: string[] } {
    const calls: string[] = [];
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    return { driver, calls };
  }

  function makeDriverWithSyncBack(syncFiles: Record<string, string>): { driver: BashkitCLIDriver; calls: string[] } {
    const calls: string[] = [];
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        const markerMatch = cmd.match(/__HARNESS_FS_SYNC_\d+__/);
        const marker = markerMatch ? markerMatch[0] : "";
        return { stdout: `output${makeSyncOutput(marker, syncFiles)}`, stderr: "", exitCode: 0 };
      },
    });
    return { driver, calls };
  }

  it("dirty tracking on write", () => {
    const { driver } = makeDriver();
    driver.fs.write("/hello.txt", "world");
    // Access internal dirty tracking
    expect(driver.fs.exists("/hello.txt")).toBe(true);
  });

  it("dirty file synced as base64 preamble before exec", () => {
    const { driver, calls } = makeDriver();
    driver.fs.write("/hello.txt", "world");
    driver.exec("cat /hello.txt");
    expect(calls.length).toBe(1);
    const cmd = calls[0];
    expect(cmd).toContain("base64");
    expect(cmd).toContain("/hello.txt");
    expect(cmd).toContain(Buffer.from("world").toString("base64"));
  });

  it("no preamble when no dirty files", () => {
    const { driver, calls } = makeDriver();
    driver.exec("echo hi");
    expect(calls.length).toBe(1);
    expect(calls[0]).toMatch(/^echo hi/);
  });

  it("removed file synced as rm -f", () => {
    const calls: string[] = [];
    const driver = new BashkitCLIDriver({
      _execOverride: (cmd: string) => {
        calls.push(cmd);
        return { stdout: "", stderr: "", exitCode: 0 };
      },
    });
    driver.fs.write("/file.txt", "data");
    driver.exec("ls"); // clear dirty via exec
    // File still exists in VFS (no sync-back from mock)
    driver.fs.remove("/file.txt");
    driver.exec("ls");
    const lastCmd = calls[calls.length - 1];
    expect(lastCmd).toContain("rm -f '/file.txt'");
  });

  it("new file from bashkit synced back to VFS", () => {
    const { driver } = makeDriverWithSyncBack({ "/created.txt": "from bashkit" });
    driver.exec("echo from bashkit > /created.txt");
    expect(driver.fs.exists("/created.txt")).toBe(true);
    expect(driver.fs.readText("/created.txt")).toBe("from bashkit");
  });

  it("modified file from bashkit synced back to VFS", () => {
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

  it("stdout is separated from sync data", () => {
    const { driver } = makeDriverWithSyncBack({ "/f.txt": "data" });
    const result = driver.exec("echo hello");
    expect(result.stdout).toBe("output");
    expect(result.stderr).toBe("");
    expect(result.exitCode).toBe(0);
  });

  it("special characters in content survive round-trip", () => {
    const content = "quotes'here\nback\\slash\n%percent";
    const { driver } = makeDriverWithSyncBack({ "/special.txt": content });
    driver.exec("echo special");
    expect(driver.fs.readText("/special.txt")).toBe(content);
  });

  it("clone preserves VFS but has independent dirty state", () => {
    const { driver } = makeDriver();
    driver.fs.write("/a.txt", "alpha");
    driver.exec("ls"); // clear dirty
    const cloned = driver.clone();
    expect(cloned.fs.exists("/a.txt")).toBe(true);
    expect(cloned.fs.readText("/a.txt")).toBe("alpha");
    cloned.fs.write("/b.txt", "beta");
    expect(driver.fs.exists("/b.txt")).toBe(false);
  });
});
