/**
 * BashkitDriver: auto-resolves native (future Phase 3) vs IPC driver.
 */

import {
  type ShellDriver,
  ShellDriverFactory,
  type ShellDriverOptions,
} from "./drivers.js";
import {
  BashkitIPCDriver,
  type BashkitIPCDriverOptions,
} from "./bashkit-ipc-driver.js";

export interface BashkitResolveOptions extends BashkitIPCDriverOptions {
  /** Test override to avoid running `which bashkit-cli` in CI. */
  _cliAvailable?: boolean;
}

export class BashkitDriver {
  /**
   * Return a bashkit ShellDriver, preferring native (Phase 3) over IPC.
   */
  static resolve(opts: BashkitResolveOptions = {}): ShellDriver {
    const cliAvailable = opts._cliAvailable ?? BashkitDriver._checkCli();
    if (cliAvailable) {
      return new BashkitIPCDriver(opts);
    }
    throw new Error(
      "bashkit not found — install bashkit-cli or the native extension"
    );
  }

  /** Check if bashkit-cli is on PATH. */
  private static _checkCli(): boolean {
    try {
      const { execSync } = require("child_process");
      execSync("which bashkit-cli", { stdio: "ignore" });
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
    BashkitDriver.resolve(opts as BashkitResolveOptions)
  );
}
