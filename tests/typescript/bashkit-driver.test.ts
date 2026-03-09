/**
 * Tests for BashkitDriver resolver and factory registration.
 */
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { BashkitDriver, registerBashkitDriver } from "../../src/typescript/bashkit-driver.js";
import { BashkitCLIDriver } from "../../src/typescript/bashkit-cli-driver.js";
import { ShellDriverFactory } from "../../src/typescript/drivers.js";

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

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

    it("throws when bashkit is not available and no override", () => {
      // In test environment, bashkit binary is not installed
      expect(() => BashkitDriver.resolve()).toThrow("bashkit not found");
    });
  });

  describe("registerBashkitDriver()", () => {
    it("adds bashkit to factory registry", () => {
      registerBashkitDriver();
      // After registration, create should throw "bashkit not found", not "not registered"
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "bashkit not found"
      );
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

    it("factory.create after register without bashkit throws 'bashkit not found'", () => {
      registerBashkitDriver();
      expect(() => ShellDriverFactory.create("bashkit")).toThrow(
        "bashkit not found"
      );
    });
  });
});
