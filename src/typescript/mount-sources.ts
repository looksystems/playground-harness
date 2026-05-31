/** Concrete {@link MountSource} implementations. */

import * as fs from "node:fs";
import * as nodePath from "node:path";
import type { MountSource, MountStat } from "./mount.js";

/**
 * Mirrors a host directory as a read-only mount source.
 *
 * Path-escape-safe: the real absolute root is resolved once, and every subpath
 * is joined, resolved (following symlinks), and asserted to live at or under
 * the root — defeating "../" and symlink escapes. Absolute subpaths are
 * rejected; out-of-root paths are treated as not-found.
 */
export class LocalFolderSource implements MountSource {
  private readonly root: string;

  constructor(root: string) {
    try {
      this.root = fs.realpathSync(root);
    } catch {
      this.root = nodePath.resolve(root);
    }
  }

  private resolve(subpath: string): string | null {
    if (nodePath.isAbsolute(subpath)) return null;
    const joined = nodePath.join(this.root, subpath);
    let resolved: string;
    try {
      resolved = fs.realpathSync(joined);
    } catch {
      resolved = nodePath.resolve(joined);
    }
    if (resolved !== this.root && !resolved.startsWith(this.root + nodePath.sep)) {
      return null;
    }
    return resolved;
  }

  stat(subpath: string): MountStat | null {
    const resolved = this.resolve(subpath);
    if (resolved === null) return null;
    let info: fs.Stats;
    try {
      info = fs.statSync(resolved);
    } catch {
      return null;
    }
    return {
      isDir: info.isDirectory(),
      size: info.size,
      mtime: Math.floor(info.mtimeMs / 1000),
    };
  }

  list(subpath: string): string[] {
    const resolved = this.resolve(subpath);
    if (resolved === null) throw new Error(`${subpath}: No such file`);
    return fs.readdirSync(resolved).sort();
  }

  read(subpath: string): string {
    const resolved = this.resolve(subpath);
    if (resolved === null) throw new Error(`${subpath}: No such file`);
    return fs.readFileSync(resolved, "utf8");
  }
}
