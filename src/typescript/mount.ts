/**
 * Programmatic mount sources for the virtual filesystem.
 *
 * A {@link MountSource} exposes a live, read-only directory tree of unknown
 * shape (a host folder, a GitHub repo, Slack messages, ...). The
 * {@link MountingFilesystemDriver} overlays one or more sources onto an inner
 * writable {@link FilesystemDriver} using copy-up semantics: writes shadow the
 * source in the inner layer (the source is never mutated), and deleting a
 * source-only path throws (read-only, no whiteout/tombstone in v1).
 *
 * See docs/adr/0034-programmatic-mount-sources.md.
 */

import { VirtualFS } from "./virtual-fs.js";
import type { FilesystemDriver } from "./drivers.js";

/** Lightweight stat for a path inside a mount source. */
export interface MountStat {
  isDir: boolean;
  size?: number;
  mtime?: number;
}

/**
 * Read-only provider of a directory tree, addressed by relative subpaths
 * ("" is the mount root). `stat` is called constantly for existence checks and
 * must return `null` for missing paths (no throwing for control flow);
 * `list`/`read` are only called after `stat` confirms existence.
 */
export interface MountSource {
  stat(subpath: string): MountStat | null;
  list(subpath: string): string[];
  read(subpath: string): string;
}

/** Capability interface for filesystems that support mounting sources. */
export interface Mountable {
  mount(mountPoint: string, source: MountSource): void;
  unmount(mountPoint: string): void;
  mounts(): string[];
}

/** Type guard for {@link Mountable}. */
export function isMountable(fs: unknown): fs is Mountable {
  return (
    typeof fs === "object" &&
    fs !== null &&
    typeof (fs as Mountable).mount === "function" &&
    typeof (fs as Mountable).unmount === "function" &&
    typeof (fs as Mountable).mounts === "function"
  );
}

const norm = (p: string): string => VirtualFS._norm(p);

interface MountEntry {
  point: string;
  source: MountSource;
}

/**
 * Overlays read-only {@link MountSource}s onto an inner writable driver.
 * Empty mount table => every method is a transparent passthrough to inner.
 */
export class MountingFilesystemDriver implements FilesystemDriver, Mountable {
  private _inner: FilesystemDriver;
  private _mounts: MountEntry[];

  constructor(inner: FilesystemDriver, mounts?: MountEntry[]) {
    this._inner = inner;
    this._mounts = mounts ? [...mounts] : [];
  }

  // --- Mountable ----------------------------------------------------------

  mount(mountPoint: string, source: MountSource): void {
    mountPoint = norm(mountPoint);
    this.unmount(mountPoint);
    this._mounts.push({ point: mountPoint, source });
  }

  unmount(mountPoint: string): void {
    mountPoint = norm(mountPoint);
    this._mounts = this._mounts.filter((e) => e.point !== mountPoint);
  }

  mounts(): string[] {
    return this._mounts.map((e) => e.point).sort();
  }

  // --- routing helpers ----------------------------------------------------

  private _resolveMount(p: string): { source: MountSource; subpath: string } | null {
    p = norm(p);
    let best: { point: string; source: MountSource; subpath: string } | null = null;
    for (const { point, source } of this._mounts) {
      let subpath: string;
      if (p === point) subpath = "";
      else if (point === "/") subpath = p.slice(1);
      else if (p.startsWith(point + "/")) subpath = p.slice(point.length + 1);
      else continue;
      if (best === null || point.length > best.point.length) {
        best = { point, source, subpath };
      }
    }
    return best ? { source: best.source, subpath: best.subpath } : null;
  }

  private _isMountPointOrAncestor(p: string): boolean {
    p = norm(p);
    const prefix = p === "/" ? "/" : p + "/";
    return this._mounts.some((e) => e.point === p || e.point.startsWith(prefix));
  }

  // --- FilesystemDriver ---------------------------------------------------

  write(path: string, content: string): void {
    this._inner.write(path, content);
  }

  writeLazy(path: string, provider: () => string): void {
    this._inner.writeLazy(path, provider);
  }

  read(path: string): string {
    if (this._inner.exists(path) && !this._inner.isDir(path)) {
      return this._inner.read(path);
    }
    const m = this._resolveMount(path);
    if (m) {
      const st = m.source.stat(m.subpath);
      if (st !== null && !st.isDir) return m.source.read(m.subpath);
    }
    return this._inner.read(path); // throws "No such file"
  }

  readText(path: string): string {
    return this.read(path);
  }

  exists(path: string): boolean {
    if (this._inner.exists(path)) return true;
    if (this._isMountPointOrAncestor(path)) return true;
    const m = this._resolveMount(path);
    return m !== null && m.source.stat(m.subpath) !== null;
  }

  remove(path: string): void {
    if (this._inner.exists(path)) {
      this._inner.remove(path);
      return;
    }
    const m = this._resolveMount(path);
    if (m && m.source.stat(m.subpath) !== null) {
      throw new Error(`${norm(path)}: read-only mount source (cannot delete)`);
    }
    throw new Error(`${norm(path)}: No such file`);
  }

  isDir(path: string): boolean {
    if (this._inner.isDir(path)) return true;
    if (this._isMountPointOrAncestor(path)) return true;
    const m = this._resolveMount(path);
    if (m) {
      const st = m.source.stat(m.subpath);
      return st !== null && st.isDir;
    }
    return false;
  }

  // private alias used by the shell builtins (parity with VirtualFS)
  _isDir(path: string): boolean {
    return this.isDir(path);
  }

  listdir(path: string = "/"): string[] {
    path = norm(path);
    const entries = new Set<string>(this._inner.listdir(path));
    const m = this._resolveMount(path);
    if (m) {
      const st = m.source.stat(m.subpath);
      if (st !== null && st.isDir) {
        for (const name of m.source.list(m.subpath)) entries.add(name);
      }
    }
    const prefix = path === "/" ? "/" : path + "/";
    for (const e of this._mounts) {
      if (e.point.startsWith(prefix)) {
        const seg = e.point.slice(prefix.length).split("/")[0];
        if (seg) entries.add(seg);
      }
    }
    return [...entries].sort();
  }

  find(root: string = "/", pattern: string = "*"): string[] {
    root = norm(root);
    const regex = MountingFilesystemDriver._globToRegex(pattern);
    const results: string[] = [];
    const walk = (dir: string): void => {
      for (const name of this.listdir(dir)) {
        const full = dir === "/" ? "/" + name : dir + "/" + name;
        const isDir = this.isDir(full);
        if (!isDir && regex.test(name)) results.push(full);
        if (isDir) walk(full);
      }
    };
    if (this.isDir(root)) walk(root);
    return results.sort();
  }

  private static _globToRegex(pattern: string): RegExp {
    let regexStr = "^";
    for (const ch of pattern) {
      if (ch === "*") regexStr += ".*";
      else if (ch === "?") regexStr += ".";
      else if (".+^${}()|[]\\".includes(ch)) regexStr += "\\" + ch;
      else regexStr += ch;
    }
    return new RegExp(regexStr + "$");
  }

  stat(path: string): { path: string; type: string; size?: number; mtime?: number } {
    if (this._inner.exists(path)) return this._inner.stat(path);
    const np = norm(path);
    if (this._isMountPointOrAncestor(np)) return { path: np, type: "directory" };
    const m = this._resolveMount(np);
    if (m) {
      const st = m.source.stat(m.subpath);
      if (st !== null) {
        if (st.isDir) return { path: np, type: "directory" };
        return { path: np, type: "file", size: st.size ?? 0, mtime: st.mtime ?? 0 };
      }
    }
    return this._inner.stat(path); // throws "No such file"
  }

  clone(): MountingFilesystemDriver {
    // Sources are read-only and shareable; copy the table by reference.
    return new MountingFilesystemDriver(this._inner.clone(), this._mounts);
  }
}
