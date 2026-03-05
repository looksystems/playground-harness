/**
 * In-memory virtual filesystem with lazy file support.
 * String-only content (no bytes).
 */

export class VirtualFS {
  private _files: Map<string, string> = new Map();
  private _lazy: Map<string, () => string> = new Map();

  constructor(files?: Record<string, string>) {
    if (files) {
      for (const [path, content] of Object.entries(files)) {
        this.write(path, content);
      }
    }
  }

  static _norm(path: string): string {
    if (!path.startsWith("/")) {
      path = "/" + path;
    }
    const parts = path.split("/");
    const resolved: string[] = [];
    for (const part of parts) {
      if (part === "" || part === ".") continue;
      if (part === "..") {
        resolved.pop();
      } else {
        resolved.push(part);
      }
    }
    return "/" + resolved.join("/");
  }

  write(path: string, content: string): void {
    path = VirtualFS._norm(path);
    this._files.set(path, content);
    // Remove any lazy provider if we're writing directly
    this._lazy.delete(path);
  }

  writeLazy(path: string, provider: () => string): void {
    path = VirtualFS._norm(path);
    this._lazy.set(path, provider);
  }

  read(path: string): string {
    path = VirtualFS._norm(path);
    if (this._lazy.has(path)) {
      const provider = this._lazy.get(path)!;
      this._lazy.delete(path);
      const content = provider();
      this._files.set(path, content);
    }
    const content = this._files.get(path);
    if (content === undefined) {
      throw new Error(`${path}: No such file`);
    }
    return content;
  }

  exists(path: string): boolean {
    path = VirtualFS._norm(path);
    return this._files.has(path) || this._lazy.has(path) || this._isDir(path);
  }

  remove(path: string): void {
    path = VirtualFS._norm(path);
    if (this._files.has(path)) {
      this._files.delete(path);
    } else if (this._lazy.has(path)) {
      this._lazy.delete(path);
    } else {
      throw new Error(`${path}: No such file`);
    }
  }

  private _allPaths(): Set<string> {
    const paths = new Set<string>(this._files.keys());
    for (const k of this._lazy.keys()) {
      paths.add(k);
    }
    return paths;
  }

  _isDir(path: string): boolean {
    path = VirtualFS._norm(path);
    const prefix = path.replace(/\/+$/, "") + "/";
    for (const p of this._allPaths()) {
      if (p.startsWith(prefix)) return true;
    }
    return false;
  }

  listdir(path: string = "/"): string[] {
    path = VirtualFS._norm(path).replace(/\/+$/, "") + "/";
    if (path === "//") path = "/";
    const entries = new Set<string>();
    for (const p of this._allPaths()) {
      if (p.startsWith(path) && p !== path) {
        const rest = p.slice(path.length);
        const entry = rest.split("/")[0];
        entries.add(entry);
      }
    }
    return [...entries].sort();
  }

  find(root: string = "/", pattern: string = "*"): string[] {
    root = VirtualFS._norm(root).replace(/\/+$/, "");
    const regex = VirtualFS._globToRegex(pattern);
    const results: string[] = [];
    const allPaths = [...this._allPaths()].sort();
    for (const p of allPaths) {
      if (!p.startsWith(root)) continue;
      const basename = p.split("/").pop() || "";
      if (regex.test(basename)) {
        results.push(p);
      }
    }
    return results;
  }

  private static _globToRegex(pattern: string): RegExp {
    let regexStr = "^";
    for (const ch of pattern) {
      if (ch === "*") regexStr += ".*";
      else if (ch === "?") regexStr += ".";
      else if (".+^${}()|[]\\".includes(ch)) regexStr += "\\" + ch;
      else regexStr += ch;
    }
    regexStr += "$";
    return new RegExp(regexStr);
  }

  stat(path: string): { path: string; type: string; size?: number } {
    path = VirtualFS._norm(path);
    if (this._isDir(path)) {
      return { path, type: "directory" };
    }
    const content = this.read(path);
    const size = new TextEncoder().encode(content).length;
    return { path, type: "file", size };
  }

  clone(): VirtualFS {
    const newFs = new VirtualFS();
    for (const [k, v] of this._files) {
      newFs._files.set(k, v);
    }
    for (const [k, v] of this._lazy) {
      newFs._lazy.set(k, v);
    }
    return newFs;
  }
}
