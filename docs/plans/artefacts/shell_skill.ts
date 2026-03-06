/**
 * shell_skill.ts — A virtual filesystem + shell skill for agent_harness.ts
 *
 * Inspired by vercel-labs/just-bash: instead of many specialized tools,
 * give the agent a single `exec` tool over an in-memory filesystem.
 *
 * Two modes:
 *   1. Built-in lightweight shell (zero deps, ~400 lines)
 *   2. just-bash integration (full bash fidelity, `npm install just-bash`)
 *
 * Usage:
 *   // Lightweight mode (no deps)
 *   const skill = new ShellSkill();
 *   skill.write("/data/users.json", JSON.stringify(users));
 *   skill.mountDir("/docs", { "schema.md": "...", "api.md": "..." });
 *   skills.mount(skill);
 *
 *   // just-bash mode (full bash)
 *   const skill = ShellSkill.withJustBash({
 *     files: { "/data/users.json": JSON.stringify(users) },
 *   });
 *   skills.mount(skill);
 */

import {
  Skill,
  SkillManager,
  type SkillContext,
} from "./skills";
import { defineTool, type ToolDef } from "./agent_harness";

// ---------------------------------------------------------------------------
// Virtual filesystem
// ---------------------------------------------------------------------------

export class VirtualFS {
  private files = new Map<string, string>();
  private lazy = new Map<string, () => string | Promise<string>>();

  constructor(files?: Record<string, string>) {
    if (files) {
      for (const [path, content] of Object.entries(files)) {
        this.write(path, content);
      }
    }
  }

  private norm(path: string): string {
    // Normalize: ensure leading /, collapse //, resolve . and ..
    const parts = ("/" + path).split("/").filter(Boolean);
    const resolved: string[] = [];
    for (const p of parts) {
      if (p === "..") resolved.pop();
      else if (p !== ".") resolved.push(p);
    }
    return "/" + resolved.join("/");
  }

  write(path: string, content: string): void {
    this.files.set(this.norm(path), content);
  }

  writeLazy(path: string, provider: () => string | Promise<string>): void {
    this.lazy.set(this.norm(path), provider);
  }

  async read(path: string): Promise<string> {
    path = this.norm(path);
    if (this.lazy.has(path)) {
      const content = await this.lazy.get(path)!();
      this.files.set(path, content);
      this.lazy.delete(path);
    }
    const content = this.files.get(path);
    if (content === undefined) throw new Error(`${path}: No such file`);
    return content;
  }

  readSync(path: string): string {
    path = this.norm(path);
    const content = this.files.get(path);
    if (content === undefined) throw new Error(`${path}: No such file`);
    return content;
  }

  exists(path: string): boolean {
    path = this.norm(path);
    return this.files.has(path) || this.lazy.has(path) || this.isDir(path);
  }

  remove(path: string): void {
    path = this.norm(path);
    this.files.delete(path);
    this.lazy.delete(path);
  }

  private allPaths(): string[] {
    return [...this.files.keys(), ...this.lazy.keys()];
  }

  isDir(path: string): boolean {
    const prefix = this.norm(path).replace(/\/$/, "") + "/";
    return this.allPaths().some((p) => p.startsWith(prefix));
  }

  listdir(path: string = "/"): string[] {
    let prefix = this.norm(path).replace(/\/$/, "") + "/";
    if (prefix === "//") prefix = "/";
    const entries = new Set<string>();
    for (const p of this.allPaths()) {
      if (p.startsWith(prefix) && p !== prefix) {
        const rest = p.slice(prefix.length);
        entries.add(rest.split("/")[0]);
      }
    }
    return [...entries].sort();
  }

  find(root: string = "/", pattern: string = "*"): string[] {
    const rootNorm = this.norm(root).replace(/\/$/, "");
    const regex = new RegExp(
      "^" + pattern.replace(/\*/g, ".*").replace(/\?/g, ".") + "$"
    );
    return this.allPaths()
      .filter((p) => p.startsWith(rootNorm))
      .filter((p) => regex.test(p.split("/").pop()!))
      .sort();
  }

  stat(path: string): { path: string; type: string; size?: number } {
    path = this.norm(path);
    if (this.isDir(path)) return { path, type: "directory" };
    const content = this.files.get(path);
    if (content === undefined) throw new Error(`${path}: No such file`);
    return { path, type: "file", size: new TextEncoder().encode(content).length };
  }
}

// ---------------------------------------------------------------------------
// Lightweight shell interpreter
// ---------------------------------------------------------------------------

interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

type CmdFn = (args: string[], stdin: string) => ExecResult;

export class Shell {
  private static MAX_OUTPUT = 16_000;
  private builtins: Map<string, CmdFn>;
  public cwd: string;

  constructor(
    public fs: VirtualFS,
    cwd = "/",
    public env: Record<string, string> = {},
    allowedCommands?: Set<string>
  ) {
    this.cwd = cwd;
    const all: [string, CmdFn][] = [
      ["cat", this.cat], ["echo", this.echo], ["pwd", this.pwdCmd],
      ["cd", this.cd], ["ls", this.ls], ["find", this.findCmd],
      ["grep", this.grep], ["head", this.head], ["tail", this.tail],
      ["wc", this.wc], ["sort", this.sortCmd], ["uniq", this.uniq],
      ["cut", this.cut], ["tr", this.trCmd], ["sed", this.sed],
      ["jq", this.jqCmd], ["tree", this.tree], ["tee", this.teeCmd],
      ["touch", this.touch], ["mkdir", this.mkdirCmd],
      ["cp", this.cp], ["rm", this.rm], ["stat", this.statCmd],
    ];
    this.builtins = new Map(
      allowedCommands
        ? all.filter(([name]) => allowedCommands.has(name))
        : all
    );
    // Bind all
    for (const [name, fn] of this.builtins) {
      this.builtins.set(name, fn.bind(this));
    }
  }

  private resolve(path: string): string {
    if (path.startsWith("/")) return this.normPath(path);
    return this.normPath(this.cwd + "/" + path);
  }

  private normPath(p: string): string {
    const parts = p.split("/").filter(Boolean);
    const resolved: string[] = [];
    for (const s of parts) {
      if (s === "..") resolved.pop();
      else if (s !== ".") resolved.push(s);
    }
    return "/" + resolved.join("/");
  }

  exec(command: string): ExecResult {
    command = command.trim();
    if (!command) return { stdout: "", stderr: "", exitCode: 0 };

    // Handle ; chaining
    if (command.includes(";")) {
      const segments = this.splitUnquoted(command, ";");
      if (segments.length > 1) {
        let stdout = "", stderr = "", exitCode = 0;
        for (const seg of segments) {
          const r = this.exec(seg.trim());
          stdout += r.stdout;
          stderr += r.stderr;
          exitCode = r.exitCode;
        }
        return { stdout, stderr, exitCode };
      }
    }

    // Handle pipes
    const segments = this.splitUnquoted(command, "|");
    let stdin = "";
    let result: ExecResult = { stdout: "", stderr: "", exitCode: 0 };

    for (const seg of segments) {
      result = this.execSingle(seg.trim(), stdin);
      stdin = result.stdout;
      if (result.exitCode !== 0) break;
    }

    if (result.stdout.length > Shell.MAX_OUTPUT) {
      result.stdout =
        result.stdout.slice(0, Shell.MAX_OUTPUT) +
        `\n... [truncated, ${result.stdout.length} total chars]`;
    }
    return result;
  }

  private execSingle(command: string, stdin: string): ExecResult {
    // Basic redirect
    let redirectPath: string | null = null;
    let append = false;

    for (const op of [">>", ">"]) {
      const idx = command.indexOf(op);
      if (idx !== -1) {
        redirectPath = command.slice(idx + op.length).trim().split(/\s+/)[0];
        command = command.slice(0, idx).trim();
        append = op === ">>";
        break;
      }
    }

    const parts = this.shellSplit(command);
    if (parts.length === 0) return { stdout: "", stderr: "", exitCode: 0 };

    const cmdName = parts[0];
    const args = parts.slice(1);

    const handler = this.builtins.get(cmdName);
    if (!handler) {
      return { stdout: "", stderr: `${cmdName}: command not found\n`, exitCode: 127 };
    }

    let result = handler(args, stdin);

    if (redirectPath) {
      const path = this.resolve(redirectPath);
      if (append && this.fs.exists(path)) {
        this.fs.write(path, this.fs.readSync(path) + result.stdout);
      } else {
        this.fs.write(path, result.stdout);
      }
      result = { stdout: "", stderr: result.stderr, exitCode: result.exitCode };
    }

    return result;
  }

  private shellSplit(s: string): string[] {
    const parts: string[] = [];
    let current = "";
    let inSingle = false, inDouble = false;
    for (const c of s) {
      if (c === "'" && !inDouble) { inSingle = !inSingle; continue; }
      if (c === '"' && !inSingle) { inDouble = !inDouble; continue; }
      if (c === " " && !inSingle && !inDouble) {
        if (current) { parts.push(current); current = ""; }
        continue;
      }
      current += c;
    }
    if (current) parts.push(current);
    return parts;
  }

  private splitUnquoted(s: string, sep: string): string[] {
    const parts: string[] = [];
    let current = "";
    let inSingle = false, inDouble = false;
    for (let i = 0; i < s.length; i++) {
      const c = s[i];
      if (c === "'" && !inDouble) { inSingle = !inSingle; current += c; }
      else if (c === '"' && !inSingle) { inDouble = !inDouble; current += c; }
      else if (s.slice(i, i + sep.length) === sep && !inSingle && !inDouble) {
        parts.push(current); current = ""; i += sep.length - 1;
      } else { current += c; }
    }
    parts.push(current);
    return parts;
  }

  // -- Commands ------------------------------------------------------------

  private ok(stdout: string): ExecResult { return { stdout, stderr: "", exitCode: 0 }; }
  private err(stderr: string, code = 1): ExecResult { return { stdout: "", stderr, exitCode: code }; }

  private cat(args: string[], stdin: string): ExecResult {
    if (!args.length) return this.ok(stdin);
    const parts: string[] = [];
    for (const a of args) {
      try { parts.push(this.fs.readSync(this.resolve(a))); }
      catch (e: any) { return this.err(e.message + "\n"); }
    }
    return this.ok(parts.join(""));
  }

  private echo(args: string[], _: string): ExecResult {
    return this.ok(args.join(" ") + "\n");
  }

  private pwdCmd(_a: string[], _s: string): ExecResult {
    return this.ok(this.cwd + "\n");
  }

  private cd(args: string[], _: string): ExecResult {
    const target = args[0] || "/";
    const resolved = this.resolve(target);
    if (!this.fs.isDir(resolved) && resolved !== "/") {
      return this.err(`cd: ${target}: No such directory\n`);
    }
    this.cwd = resolved;
    return this.ok("");
  }

  private ls(args: string[], _: string): ExecResult {
    const long = args.includes("-l");
    const paths = args.filter((a) => !a.startsWith("-"));
    const target = paths[0] || this.cwd;
    const entries = this.fs.listdir(this.resolve(target));
    if (long) {
      const lines = entries.map((e) => {
        const full = this.normPath(this.resolve(target) + "/" + e);
        const s = this.fs.stat(full);
        return s.type === "directory"
          ? `drwxr-xr-x  -  ${e}/`
          : `-rw-r--r--  ${String(s.size ?? 0).padStart(8)}  ${e}`;
      });
      return this.ok(lines.join("\n") + "\n");
    }
    return this.ok(entries.join("\n") + (entries.length ? "\n" : ""));
  }

  private findCmd(args: string[], _: string): ExecResult {
    let root = ".";
    let nameFilter = "*";
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-name" && args[i + 1]) { nameFilter = args[++i]; }
      else if (!args[i].startsWith("-")) { root = args[i]; }
    }
    const results = this.fs.find(this.resolve(root), nameFilter);
    return this.ok(results.join("\n") + (results.length ? "\n" : ""));
  }

  private grep(args: string[], stdin: string): ExecResult {
    const flags = new Set(args.filter((a) => a.startsWith("-")).flatMap((a) => [...a.slice(1)]));
    const positional = args.filter((a) => !a.startsWith("-"));
    if (!positional.length) return this.err("grep: missing pattern\n", 2);

    const pattern = positional[0];
    const files = positional.slice(1);
    const re = new RegExp(pattern, flags.has("i") ? "i" : undefined);

    const grepText = (text: string, label?: string): string[] =>
      text.splitLines().reduce<string[]>((acc, line, i) => {
        let match = re.test(line);
        if (flags.has("v")) match = !match;
        if (match) {
          const prefix = label ? `${label}:` : "";
          const num = flags.has("n") ? `${i + 1}:` : "";
          acc.push(`${prefix}${num}${line}`);
        }
        return acc;
      }, []);

    let matches: string[] = [];
    if (!files.length) {
      matches = grepText(stdin);
    } else {
      for (const f of files) {
        try {
          const text = this.fs.readSync(this.resolve(f));
          const label = files.length > 1 ? f : undefined;
          matches.push(...grepText(text, label));
        } catch (e: any) {
          return this.err(`grep: ${f}: No such file\n`, 2);
        }
      }
    }

    if (flags.has("c")) return { stdout: matches.length + "\n", stderr: "", exitCode: matches.length ? 0 : 1 };
    return { stdout: matches.join("\n") + (matches.length ? "\n" : ""), stderr: "", exitCode: matches.length ? 0 : 1 };
  }

  private head(args: string[], stdin: string): ExecResult {
    let n = 10;
    const files: string[] = [];
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-n" && args[i + 1]) n = parseInt(args[++i]);
      else if (args[i].startsWith("-") && /^\d+$/.test(args[i].slice(1))) n = parseInt(args[i].slice(1));
      else files.push(args[i]);
    }
    const text = files.length ? this.fs.readSync(this.resolve(files[0])) : stdin;
    const lines = text.splitLines().slice(0, n);
    return this.ok(lines.join("\n") + (lines.length ? "\n" : ""));
  }

  private tail(args: string[], stdin: string): ExecResult {
    let n = 10;
    const files: string[] = [];
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-n" && args[i + 1]) n = parseInt(args[++i]);
      else files.push(args[i]);
    }
    const text = files.length ? this.fs.readSync(this.resolve(files[0])) : stdin;
    const lines = text.splitLines().slice(-n);
    return this.ok(lines.join("\n") + (lines.length ? "\n" : ""));
  }

  private wc(args: string[], stdin: string): ExecResult {
    const flags = new Set(args.filter((a) => a.startsWith("-")).flatMap((a) => [...a.slice(1)]));
    const files = args.filter((a) => !a.startsWith("-"));
    const text = files.length ? this.fs.readSync(this.resolve(files[0])) : stdin;
    const lc = (text.match(/\n/g) || []).length;
    const wc = text.split(/\s+/).filter(Boolean).length;
    const cc = new TextEncoder().encode(text).length;
    if (flags.has("l")) return this.ok(`${lc}\n`);
    if (flags.has("w")) return this.ok(`${wc}\n`);
    if (flags.has("c")) return this.ok(`${cc}\n`);
    return this.ok(`  ${lc}  ${wc}  ${cc}${files.length ? " " + files[0] : ""}\n`);
  }

  private sortCmd(args: string[], stdin: string): ExecResult {
    const flags = new Set(args.filter((a) => a.startsWith("-")).flatMap((a) => [...a.slice(1)]));
    const files = args.filter((a) => !a.startsWith("-"));
    const text = files.length ? this.fs.readSync(this.resolve(files[0])) : stdin;
    let lines = text.splitLines();
    if (flags.has("n")) lines.sort((a, b) => parseFloat(a) - parseFloat(b));
    else lines.sort();
    if (flags.has("r")) lines.reverse();
    if (flags.has("u")) lines = [...new Set(lines)];
    return this.ok(lines.join("\n") + "\n");
  }

  private uniq(args: string[], stdin: string): ExecResult {
    const count = args.includes("-c");
    const lines = stdin.splitLines();
    const result: string[] = [];
    let prev: string | null = null, cnt = 0;
    for (const line of lines) {
      if (line === prev) { cnt++; }
      else { if (prev !== null) result.push(count ? `  ${cnt} ${prev}` : prev); prev = line; cnt = 1; }
    }
    if (prev !== null) result.push(count ? `  ${cnt} ${prev}` : prev);
    return this.ok(result.join("\n") + (result.length ? "\n" : ""));
  }

  private cut(args: string[], stdin: string): ExecResult {
    let delim = "\t";
    let fields: number[] = [];
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-d" && args[i + 1]) delim = args[++i];
      else if (args[i] === "-f" && args[i + 1]) {
        fields = args[++i].split(",").map((n) => parseInt(n));
      }
    }
    const out = stdin.splitLines().map((line) => {
      const parts = line.split(delim);
      return fields.map((f) => parts[f - 1] ?? "").join(delim);
    });
    return this.ok(out.join("\n") + "\n");
  }

  private trCmd(args: string[], stdin: string): ExecResult {
    const positional = args.filter((a) => !a.startsWith("-"));
    if (args.includes("-d") && positional.length >= 1) {
      const chars = new Set(positional[0]);
      return this.ok([...stdin].filter((c) => !chars.has(c)).join(""));
    }
    if (positional.length >= 2) {
      const [set1, set2] = positional;
      let result = stdin;
      for (let i = 0; i < set1.length; i++) {
        result = result.replaceAll(set1[i], set2[i] ?? set2[set2.length - 1]);
      }
      return this.ok(result);
    }
    return this.ok(stdin);
  }

  private sed(args: string[], stdin: string): ExecResult {
    const positional = args.filter((a) => !a.startsWith("-"));
    const expr = positional[0];
    if (!expr) return this.ok(stdin);
    const m = expr.match(/^s(.)(.+?)\1(.*?)\1(\w*)$/);
    if (!m) return this.ok(stdin);
    const [, , pat, repl, flags] = m;
    const re = new RegExp(pat, flags.includes("g") ? "g" : "");
    return this.ok(stdin.replace(re, repl));
  }

  private jqCmd(args: string[], stdin: string): ExecResult {
    const raw = args.includes("-r");
    const positional = args.filter((a) => !a.startsWith("-"));
    const query = positional[0] || ".";
    const files = positional.slice(1);
    const text = files.length ? this.fs.readSync(this.resolve(files[0])) : stdin;

    try {
      const data = JSON.parse(text);
      const result = this.jqQuery(data, query);
      if (Array.isArray(result) && query.endsWith("[]")) {
        return this.ok(result.map((item) =>
          raw && typeof item === "string" ? item : JSON.stringify(item)
        ).join("\n") + "\n");
      }
      if (raw && typeof result === "string") return this.ok(result + "\n");
      return this.ok(JSON.stringify(result, null, 2) + "\n");
    } catch (e: any) {
      return this.err(`jq: ${e.message}\n`, 2);
    }
  }

  private jqQuery(data: any, query: string): any {
    if (query === ".") return data;
    const parts = query.match(/\.\w+|\[\d+\]|\[\]/g) || [];
    let current = data;
    for (const part of parts) {
      if (part === "[]") return Array.isArray(current) ? current : [];
      if (part.startsWith("[")) current = current[parseInt(part.slice(1))];
      else current = current[part.slice(1)];
    }
    return current;
  }

  private tree(args: string[], _: string): ExecResult {
    const target = args.find((a) => !a.startsWith("-")) || this.cwd;
    const resolved = this.resolve(target);
    const lines = [resolved];
    this.treeRecurse(resolved, "", lines);
    return this.ok(lines.join("\n") + "\n");
  }

  private treeRecurse(path: string, prefix: string, lines: string[]): void {
    const entries = this.fs.listdir(path);
    entries.forEach((entry, i) => {
      const isLast = i === entries.length - 1;
      const full = this.normPath(path + "/" + entry);
      const isDir = this.fs.isDir(full);
      lines.push(`${prefix}${isLast ? "└── " : "├── "}${entry}${isDir ? "/" : ""}`);
      if (isDir) this.treeRecurse(full, prefix + (isLast ? "    " : "│   "), lines);
    });
  }

  private teeCmd(args: string[], stdin: string): ExecResult {
    const files = args.filter((a) => !a.startsWith("-"));
    for (const f of files) this.fs.write(this.resolve(f), stdin);
    return this.ok(stdin);
  }

  private touch(args: string[], _: string): ExecResult {
    for (const f of args) {
      const p = this.resolve(f);
      if (!this.fs.exists(p)) this.fs.write(p, "");
    }
    return this.ok("");
  }

  private mkdirCmd(args: string[], _: string): ExecResult {
    for (const a of args.filter((a) => !a.startsWith("-"))) {
      this.fs.write(this.resolve(a) + "/.keep", "");
    }
    return this.ok("");
  }

  private cp(args: string[], _: string): ExecResult {
    const paths = args.filter((a) => !a.startsWith("-"));
    if (paths.length < 2) return this.err("cp: missing operand\n");
    try {
      this.fs.write(this.resolve(paths[1]), this.fs.readSync(this.resolve(paths[0])));
    } catch (e: any) { return this.err(e.message + "\n"); }
    return this.ok("");
  }

  private rm(args: string[], _: string): ExecResult {
    for (const f of args.filter((a) => !a.startsWith("-"))) {
      this.fs.remove(this.resolve(f));
    }
    return this.ok("");
  }

  private statCmd(args: string[], _: string): ExecResult {
    const f = args.find((a) => !a.startsWith("-"));
    if (!f) return this.ok("");
    try {
      return this.ok(JSON.stringify(this.fs.stat(this.resolve(f)), null, 2) + "\n");
    } catch (e: any) { return this.err(e.message + "\n"); }
  }
}

// Polyfill for splitLines
declare global {
  interface String {
    splitLines(): string[];
  }
}
String.prototype.splitLines = function (): string[] {
  return this.split("\n").filter((_, i, a) => i < a.length - 1 || a[i] !== "");
};

// ---------------------------------------------------------------------------
// ShellSkill — plugs into agent harness
// ---------------------------------------------------------------------------

export class ShellSkill extends Skill {
  name = "shell";
  description = "Execute bash commands over a virtual filesystem";
  instructions =
    "You have access to a virtual filesystem via the `exec` tool. " +
    "Use standard Unix commands: ls, cat, grep, find, head, tail, wc, " +
    "sort, uniq, cut, sed, jq, tree. Pipes (|) and redirects (>, >>) work. " +
    "Use `tree /` to see the full file layout.";

  public fs: VirtualFS;
  private shell: Shell | null = null;
  private initCwd: string;
  private initEnv: Record<string, string>;

  constructor(opts?: {
    files?: Record<string, string>;
    cwd?: string;
    env?: Record<string, string>;
    allowedCommands?: Set<string>;
  }) {
    super();
    this.fs = new VirtualFS(opts?.files);
    this.initCwd = opts?.cwd ?? "/home/user";
    this.initEnv = opts?.env ?? {};
  }

  // -- Convenience methods -------------------------------------------------

  write(path: string, content: string): void { this.fs.write(path, content); }
  writeLazy(path: string, provider: () => string | Promise<string>): void { this.fs.writeLazy(path, provider); }
  mountDir(prefix: string, files: Record<string, string>): void {
    for (const [name, content] of Object.entries(files)) {
      this.fs.write(`${prefix}/${name}`, content);
    }
  }
  mountJson(path: string, data: any): void {
    this.fs.write(path, JSON.stringify(data, null, 2));
  }

  // -- Skill lifecycle -----------------------------------------------------

  async setup(ctx: SkillContext): Promise<void> {
    this.shell = new Shell(this.fs, this.initCwd, this.initEnv);
  }

  tools(): ToolDef[] {
    return [
      defineTool({
        name: "exec",
        description:
          "Execute a bash command. Supports ls, cat, grep, find, head, tail, " +
          "wc, sort, uniq, cut, sed, jq, tree, cp, rm, mkdir, touch, tee, " +
          "cd, pwd, tr, echo, stat. Pipes (|) and redirects (>, >>) work.",
        parameters: {
          type: "object",
          properties: {
            command: { type: "string", description: "The bash command to run" },
          },
          required: ["command"],
        },
        execute: async ({ command }: { command: string }): Promise<string> => {
          const result = this.shell!.exec(command);
          const parts: string[] = [];
          if (result.stdout) parts.push(result.stdout);
          if (result.stderr) parts.push(`[stderr] ${result.stderr}`);
          if (result.exitCode !== 0) parts.push(`[exit code: ${result.exitCode}]`);
          return parts.join("") || "(no output)";
        },
      }),
    ];
  }

  // -- just-bash integration (optional) ------------------------------------

  /**
   * Create a ShellSkill backed by just-bash for full bash fidelity.
   *
   * Requires: npm install just-bash
   *
   *   const skill = await ShellSkill.withJustBash({
   *     files: { "/data/users.json": "..." },
   *   });
   */
  static async withJustBash(opts?: {
    files?: Record<string, string>;
    cwd?: string;
  }): Promise<Skill> {
    // Dynamic import — only loaded if this method is called
    const { Bash } = await import("just-bash");

    const bash = new Bash({
      files: opts?.files,
      cwd: opts?.cwd ?? "/home/user",
    });

    const skill = new (class extends Skill {
      name = "shell";
      description = "Execute bash commands (full bash via just-bash)";
      instructions =
        "You have access to a full bash environment via the `exec` tool. " +
        "Use any standard Unix commands. The filesystem is in-memory. " +
        "Use `tree /` to see the full file layout.";

      tools(): ToolDef[] {
        return [
          defineTool({
            name: "exec",
            description: "Execute a bash command in the virtual environment",
            parameters: {
              type: "object",
              properties: {
                command: { type: "string", description: "The bash command to run" },
              },
              required: ["command"],
            },
            execute: async ({ command }: { command: string }): Promise<string> => {
              const result = await bash.exec(command);
              const parts: string[] = [];
              if (result.stdout) parts.push(result.stdout);
              if (result.stderr) parts.push(`[stderr] ${result.stderr}`);
              if (result.exitCode !== 0) parts.push(`[exit code: ${result.exitCode}]`);
              return parts.join("") || "(no output)";
            },
          }),
        ];
      }
    })();

    return skill;
  }
}
