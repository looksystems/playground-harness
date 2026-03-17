/**
 * Live integration tests for OpenShellGrpcDriver against a running OpenShell instance.
 *
 * Skipped by default. To run, start an OpenShell instance and ensure SSH
 * access is configured (default: localhost:2222).
 */

import { describe, it, expect } from "vitest";
import { execSync } from "child_process";
import { OpenShellGrpcDriver } from "../../src/typescript/openshell-grpc-driver.js";

function openshellReachable(): boolean {
  const host = process.env.OPENSHELL_SSH_HOST ?? "localhost";
  const port = process.env.OPENSHELL_SSH_PORT ?? "2222";
  try {
    // Use bash to test TCP connection with a short timeout
    execSync(`bash -c 'echo > /dev/tcp/${host}/${port}' 2>/dev/null`, { timeout: 1000 });
    return true;
  } catch {
    return false;
  }
}

const hasOpenShell = openshellReachable();
const describeIf = hasOpenShell ? describe : describe.skip;
const WORKSPACE = "/tmp/harness";

function driver(): OpenShellGrpcDriver {
  return new OpenShellGrpcDriver({
    sshHost: process.env.OPENSHELL_SSH_HOST ?? "localhost",
    sshPort: parseInt(process.env.OPENSHELL_SSH_PORT ?? "2222", 10),
    sshUser: process.env.OPENSHELL_SSH_USER ?? "sandbox",
    workspace: WORKSPACE,
  });
}

describeIf("OpenShellGrpcDriver — live integration", () => {
  // --- Basic execution ---

  describe("basic execution", () => {
    it("echo", () => {
      const r = driver().exec("echo hello world");
      expect(r.stdout.trim()).toBe("hello world");
      expect(r.exitCode).toBe(0);
    });

    it("pipes", () => {
      const r = driver().exec("echo hello | tr a-z A-Z");
      expect(r.stdout.trim()).toBe("HELLO");
    });

    it("exit code on failure", () => {
      const r = driver().exec("false");
      expect(r.exitCode).not.toBe(0);
    });

    it("variable expansion", () => {
      const r = driver().exec("X=42; echo $X");
      expect(r.stdout.trim()).toBe("42");
    });

    it("redirects and cat", () => {
      const r = driver().exec("echo written > /tmp/out.txt && cat /tmp/out.txt");
      expect(r.stdout.trim()).toBe("written");
    });

    it("for loop", () => {
      const r = driver().exec("for i in 1 2 3; do echo $i; done");
      expect(r.stdout.trim()).toBe("1\n2\n3");
    });

    it("stderr capture", () => {
      const r = driver().exec("echo err >&2");
      expect(r.stderr).toContain("err");
    });
  });

  // --- VFS sync ---

  describe("VFS sync", () => {
    it("write in VFS, read in sandbox", () => {
      const d = driver();
      d.fs.write("/test.txt", "hello from vfs");
      const r = d.exec(`cat ${WORKSPACE}/test.txt`);
      expect(r.stdout.trim()).toBe("hello from vfs");
    });

    it("write in sandbox, read back via VFS", () => {
      const d = driver();
      d.exec(`mkdir -p ${WORKSPACE} && echo 'from sandbox' > ${WORKSPACE}/created.txt`);
      expect(d.fs.exists("/created.txt")).toBe(true);
      expect(d.fs.readText("/created.txt").trim()).toBe("from sandbox");
    });

    it("round-trip VFS → sandbox → VFS", () => {
      const d = driver();
      d.fs.write("/round.txt", "original");
      d.exec(`cat ${WORKSPACE}/round.txt | tr a-z A-Z > ${WORKSPACE}/upper.txt`);
      expect(d.fs.exists("/upper.txt")).toBe(true);
      expect(d.fs.readText("/upper.txt").trim()).toBe("ORIGINAL");
    });

    it("special characters survive sync", () => {
      const d = driver();
      const content = "quotes'here\nback\\slash\n%percent";
      d.fs.write("/special.txt", content);
      const r = d.exec(`cat ${WORKSPACE}/special.txt`);
      expect(r.stdout).toBe(content);
    });
  });

  // --- Policy ---

  describe("policy enforcement", () => {
    it("default policy allows execution", () => {
      const r = driver().exec("echo allowed");
      expect(r.exitCode).toBe(0);
    });

    it("policy is accessible", () => {
      const d = driver();
      expect(d.policy.inferenceRouting).toBe(true);
    });
  });

  // --- Lifecycle ---

  describe("lifecycle", () => {
    it("close resets sandboxId", () => {
      const d = driver();
      d.exec("echo hello");
      d.close();
      expect(d.sandboxId).toBeNull();
    });

    it("clone creates independent copy", () => {
      const d = driver();
      d.fs.write("/orig.txt", "original");
      d.exec("echo hello");  // syncs orig.txt to remote
      const cloned = d.clone();
      expect(cloned.sandboxId).toBeNull();
      expect(cloned.fs.exists("/orig.txt")).toBe(true);
      cloned.fs.write("/clone_only.txt", "clone");
      expect(d.fs.exists("/clone_only.txt")).toBe(false);
      cloned.close();
      d.close();
    });
  });

  // --- Capabilities ---

  describe("capabilities", () => {
    it("includes remote, policies, streaming", () => {
      const caps = driver().capabilities();
      expect(caps.has("remote")).toBe(true);
      expect(caps.has("policies")).toBe(true);
      expect(caps.has("streaming")).toBe(true);
    });
  });
});
