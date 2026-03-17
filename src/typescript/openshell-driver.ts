/**
 * OpenShellDriver: resolves to OpenShellGrpcDriver.
 */

import { execSync } from "child_process";
import {
  type ShellDriver,
  ShellDriverFactory,
  type ShellDriverOptions,
} from "./drivers.js";
import {
  OpenShellGrpcDriver,
  type OpenShellGrpcDriverOptions,
} from "./openshell-grpc-driver.js";

export class OpenShellDriver {
  /**
   * Return an OpenShell ShellDriver backed by gRPC (via SSH for sync exec).
   */
  static resolve(opts: OpenShellGrpcDriverOptions = {}): ShellDriver {
    // If test override provided, use it directly
    if (opts._execOverride) {
      return new OpenShellGrpcDriver(opts);
    }
    // Check if SSH is available (used for sync exec in Node.js)
    if (!OpenShellDriver._checkSsh()) {
      throw new Error(
        "ssh not found — OpenShell driver requires SSH for synchronous execution"
      );
    }
    return new OpenShellGrpcDriver(opts);
  }

  /** Check if ssh is on PATH. */
  private static _checkSsh(): boolean {
    try {
      execSync("which ssh", { stdio: "ignore" });
      return true;
    } catch {
      return false;
    }
  }
}

/**
 * Register the "openshell" driver with ShellDriverFactory.
 */
export function registerOpenShellDriver(): void {
  ShellDriverFactory.register("openshell", (opts?: ShellDriverOptions) =>
    OpenShellDriver.resolve(opts as OpenShellGrpcDriverOptions)
  );
}
