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
import {
  BashkitNativeDriver,
  type BashkitNativeDriverOptions,
} from "./bashkit-native-driver.js";

export interface BashkitResolveOptions extends BashkitIPCDriverOptions, BashkitNativeDriverOptions {
  /** Test override to avoid running `which bashkit-cli` in CI. */
  _cliAvailable?: boolean;
  /** Test override to avoid checking for native library in CI. */
  _nativeAvailable?: boolean;
}

export class BashkitDriver {
  /**
   * Return a bashkit ShellDriver, preferring native over IPC.
   */
  static resolve(opts: BashkitResolveOptions = {}): ShellDriver {
    // 1. Try native FFI
    if (opts._libOverride) {
      return new BashkitNativeDriver(opts);
    }
    const nativeAvailable = opts._nativeAvailable ?? (BashkitNativeDriver.findLibrary() !== undefined);
    if (nativeAvailable) {
      return new BashkitNativeDriver(opts);
    }
    // 2. Fall back to IPC
    const cliAvailable = opts._cliAvailable ?? BashkitDriver._checkCli();
    if (cliAvailable) {
      return new BashkitIPCDriver(opts);
    }
    throw new Error(
      "bashkit not found — install libashkit, bashkit-cli, or the native extension"
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
