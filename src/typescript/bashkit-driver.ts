/**
 * BashkitDriver: resolves to BashkitCLIDriver.
 */

import { execSync } from "child_process";
import {
  type ShellDriver,
  ShellDriverFactory,
  type ShellDriverOptions,
} from "./drivers.js";
import {
  BashkitCLIDriver,
  type BashkitCLIDriverOptions,
} from "./bashkit-cli-driver.js";

export class BashkitDriver {
  /**
   * Return a bashkit ShellDriver backed by the CLI subprocess.
   */
  static resolve(opts: BashkitCLIDriverOptions = {}): ShellDriver {
    // If test override provided, use it directly
    if (opts._execOverride) {
      return new BashkitCLIDriver(opts);
    }
    // Check if bashkit binary is available
    if (!BashkitDriver._checkCli()) {
      throw new Error(
        "bashkit not found — install with: cargo install bashkit-cli"
      );
    }
    return new BashkitCLIDriver(opts);
  }

  /** Check if bashkit is on PATH. */
  private static _checkCli(): boolean {
    try {
      execSync("which bashkit", { stdio: "ignore" });
      return true;
    } catch {
      return false;
    }
  }
}

/**
 * Register the "bashkit" driver with ShellDriverFactory.
 */
export function registerBashkitDriver(): void {
  ShellDriverFactory.register("bashkit", (opts?: ShellDriverOptions) =>
    BashkitDriver.resolve(opts as BashkitCLIDriverOptions)
  );
}
