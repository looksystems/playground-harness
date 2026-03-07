/**
 * Tests for BashkitDriver resolver and factory registration.
 */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { BashkitDriver, registerBashkitDriver } from "../../src/typescript/bashkit-driver.js";
import { BashkitIPCDriver } from "../../src/typescript/bashkit-ipc-driver.js";
import { ShellDriverFactory } from "../../src/typescript/drivers.js";

// ---------------------------------------------------------------------------
// FakeProcess: minimal stub so BashkitIPCDriver doesn't throw
// ---------------------------------------------------------------------------

function fakeSpawn() {
  return {
    stdin: { write(_data: string) {} },
    stdout: { readline() { return ""; } },
    kill() {},
  };
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("BashkitDriver", () => {
  beforeEach(() => {
    ShellDriverFactory.reset();
  });

  afterEach(() => {
    ShellDriverFactory.reset();
  });

  describe("resolve()", () => {
    it("returns BashkitIPCDriver when CLI is available", () => {
      const driver = BashkitDriver.resolve({
        _cliAvailable: true,
        _spawnOverride: fakeSpawn,
      });
      expect(driver).toBeInstanceOf(BashkitIPCDriver);
    });

    it("throws when CLI is not available", () => {
      expect(() => BashkitDriver.resolve({ _cliAvailable: false })).toThrow(
        "bashkit not found"
      );
    });

    it("passes options through to IPC driver", () => {
      const driver = BashkitDriver.resolve({
        _cliAvailable: true,
        _spawnOverride: fakeSpawn,
        cwd: "/tmp",
        env: { FOO: "bar" },
      });
      expect(driver.cwd).toBe("/tmp");
      expect(driver.env).toEqual({ FOO: "bar" });
    });
  });

  describe("registerBashkitDriver()", () => {
    it("adds bashkit to factory registry", () => {
      registerBashkitDriver();
      // After registration, create should throw RuntimeError, not "not registered"
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "bashkit not found"
      );
    });

    it("factory.create returns IPC driver with CLI available", () => {
      registerBashkitDriver();
      const driver = ShellDriverFactory.create("bashkit", {
        _cliAvailable: true,
        _spawnOverride: fakeSpawn,
      } as any);
      expect(driver).toBeInstanceOf(BashkitIPCDriver);
    });

    it("factory.create without registration throws 'not registered'", () => {
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "not registered"
      );
    });

    it("factory.create after register with no CLI throws 'bashkit not found'", () => {
      registerBashkitDriver();
      expect(() =>
        ShellDriverFactory.create("bashkit", { _cliAvailable: false } as any)
      ).toThrow("bashkit not found");
    });
  });
});
