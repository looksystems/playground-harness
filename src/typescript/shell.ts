/**
 * Virtual shell interpreter over a VirtualFS.
 * Supports pipes, redirects, and core Unix commands.
 */

import { VirtualFS } from "./virtual-fs.js";

export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

function makeResult(
  stdout: string = "",
  stderr: string = "",
  exitCode: number = 0
): ExecResult {
  return { stdout, stderr, exitCode };
}

type CmdHandler = (args: string[], stdin: string) => ExecResult;

export interface ShellOptions {
  fs?: VirtualFS;
  cwd?: string;
  env?: Record<string, string>;
  allowedCommands?: Set<string>;
  maxOutput?: number;
  maxIterations?: number;
}

export class Shell {
  fs: VirtualFS;
  cwd: string;
  env: Record<string, string>;
  maxOutput: number;
  maxIterations: number;
  private _allowedCommands: Set<string> | undefined;
  private _builtins: Map<string, CmdHandler>;

  constructor(opts: ShellOptions = {}) {
    this.fs = opts.fs ?? new VirtualFS();
    this.cwd = opts.cwd ?? "/";
    this.env = opts.env ?? {};
    this.maxOutput = opts.maxOutput ?? 16_000;
    this.maxIterations = opts.maxIterations ?? 10_000;
    this._allowedCommands = opts.allowedCommands;

    const all: [string, CmdHandler][] = [
      ["cat", this._cmdCat.bind(this)],
      ["echo", this._cmdEcho.bind(this)],
      ["find", this._cmdFind.bind(this)],
      ["grep", this._cmdGrep.bind(this)],
      ["head", this._cmdHead.bind(this)],
      ["ls", this._cmdLs.bind(this)],
      ["pwd", this._cmdPwd.bind(this)],
      ["sort", this._cmdSort.bind(this)],
      ["tail", this._cmdTail.bind(this)],
      ["tee", this._cmdTee.bind(this)],
      ["touch", this._cmdTouch.bind(this)],
      ["tree", this._cmdTree.bind(this)],
      ["uniq", this._cmdUniq.bind(this)],
      ["wc", this._cmdWc.bind(this)],
      ["mkdir", this._cmdMkdir.bind(this)],
      ["cp", this._cmdCp.bind(this)],
      ["rm", this._cmdRm.bind(this)],
      ["stat", this._cmdStat.bind(this)],
      ["cut", this._cmdCut.bind(this)],
      ["tr", this._cmdTr.bind(this)],
      ["sed", this._cmdSed.bind(this)],
      ["jq", this._cmdJq.bind(this)],
      ["cd", this._cmdCd.bind(this)],
    ];

    if (this._allowedCommands) {
      this._builtins = new Map(
        all.filter(([name]) => this._allowedCommands!.has(name))
      );
    } else {
      this._builtins = new Map(all);
    }
  }

  clone(): Shell {
    return new Shell({
      fs: this.fs.clone(),
      cwd: this.cwd,
      env: { ...this.env },
      allowedCommands: this._allowedCommands
        ? new Set(this._allowedCommands)
        : undefined,
      maxOutput: this.maxOutput,
      maxIterations: this.maxIterations,
    });
  }

  private _resolve(path: string): string {
    if (path.startsWith("/")) {
      return VirtualFS._norm(path);
    }
    return VirtualFS._norm(this.cwd + "/" + path);
  }

  exec(command: string): ExecResult {
    command = command.trim();
    if (!command) return makeResult();

    // Handle command chaining with ;
    if (command.includes(";")) {
      const idx = command.indexOf(";");
      if (!Shell._inQuotes(command, idx)) {
        const results: ExecResult[] = [];
        for (const part of Shell._splitOn(command, ";")) {
          const r = this.exec(part);
          results.push(r);
          if (r.exitCode !== 0) break;
        }
        return makeResult(
          results.map((r) => r.stdout).join(""),
          results.map((r) => r.stderr).join(""),
          results.length > 0 ? results[results.length - 1].exitCode : 0
        );
      }
    }

    // Handle pipes
    const segments = Shell._splitOn(command, "|");
    let stdin = "";
    let lastResult = makeResult();

    for (const seg of segments) {
      lastResult = this._execSingle(seg.trim(), stdin);
      stdin = lastResult.stdout;
      if (lastResult.exitCode !== 0) break;
    }

    // Truncate
    if (lastResult.stdout.length > this.maxOutput) {
      lastResult = {
        ...lastResult,
        stdout:
          lastResult.stdout.slice(0, this.maxOutput) +
          `\n... [truncated, ${lastResult.stdout.length} total chars]`,
      };
    }

    return lastResult;
  }

  private _execSingle(command: string, stdin: string = ""): ExecResult {
    let append = false;
    let redirectPath: string | null = null;

    for (const op of [">>", ">"]) {
      const idx = command.indexOf(op);
      if (idx !== -1 && !Shell._inQuotes(command, idx)) {
        redirectPath = command
          .slice(idx + op.length)
          .trim()
          .split(/\s+/)[0];
        command = command.slice(0, idx).trim();
        append = op === ">>";
        break;
      }
    }

    const parts = Shell._splitArgs(command);
    if (parts.length === 0) return makeResult();

    const expanded = parts.map((p) => this._expandVars(p));
    const cmdName = expanded[0];
    const args = expanded.slice(1);

    const handler = this._builtins.get(cmdName);
    if (!handler) {
      return makeResult("", `${cmdName}: command not found\n`, 127);
    }

    let result = handler(args, stdin);

    if (redirectPath) {
      const path = this._resolve(redirectPath);
      if (append && this.fs.exists(path)) {
        const existing = this.fs.read(path);
        this.fs.write(path, existing + result.stdout);
      } else {
        this.fs.write(path, result.stdout);
      }
      result = makeResult("", result.stderr, result.exitCode);
    }

    return result;
  }

  private _expandVars(s: string): string {
    return s.replace(
      /\$\{(\w+)\}|\$(\w+)/g,
      (_match, braced, plain) => {
        const name = braced || plain;
        return this.env[name] ?? "";
      }
    );
  }

  static _inQuotes(s: string, pos: number): boolean {
    let inSingle = false;
    let inDouble = false;
    for (let i = 0; i < pos; i++) {
      if (s[i] === "'" && !inDouble) inSingle = !inSingle;
      else if (s[i] === '"' && !inSingle) inDouble = !inDouble;
    }
    return inSingle || inDouble;
  }

  static _splitOn(command: string, sep: string): string[] {
    const parts: string[] = [];
    let current: string[] = [];
    let inSingle = false;
    let inDouble = false;
    let i = 0;
    while (i < command.length) {
      const c = command[i];
      if (c === "'" && !inDouble) {
        inSingle = !inSingle;
        current.push(c);
      } else if (c === '"' && !inSingle) {
        inDouble = !inDouble;
        current.push(c);
      } else if (
        command.slice(i, i + sep.length) === sep &&
        !inSingle &&
        !inDouble
      ) {
        parts.push(current.join(""));
        current = [];
        i += sep.length;
        continue;
      } else {
        current.push(c);
      }
      i++;
    }
    parts.push(current.join(""));
    return parts;
  }

  static _splitArgs(command: string): string[] {
    const args: string[] = [];
    let current: string[] = [];
    let inSingle = false;
    let inDouble = false;
    let i = 0;

    while (i < command.length) {
      const c = command[i];
      if (c === "'" && !inDouble) {
        inSingle = !inSingle;
      } else if (c === '"' && !inSingle) {
        inDouble = !inDouble;
      } else if ((c === " " || c === "\t") && !inSingle && !inDouble) {
        if (current.length > 0) {
          args.push(current.join(""));
          current = [];
        }
      } else {
        current.push(c);
      }
      i++;
    }
    if (current.length > 0) {
      args.push(current.join(""));
    }
    return args;
  }

  // -- Built-in commands --

  private _cmdCat(args: string[], stdin: string): ExecResult {
    if (args.length === 0) return makeResult(stdin);
    const out: string[] = [];
    for (const path of args) {
      try {
        out.push(this.fs.read(this._resolve(path)));
      } catch (e: any) {
        return makeResult("", e.message + "\n", 1);
      }
    }
    return makeResult(out.join(""));
  }

  private _cmdEcho(args: string[], _stdin: string): ExecResult {
    return makeResult(args.join(" ") + "\n");
  }

  private _cmdPwd(_args: string[], _stdin: string): ExecResult {
    return makeResult(this.cwd + "\n");
  }

  private _cmdCd(args: string[], _stdin: string): ExecResult {
    const target = args[0] || "/";
    const resolved = this._resolve(target);
    if (!this.fs._isDir(resolved) && resolved !== "/") {
      return makeResult("", `cd: ${target}: No such directory\n`, 1);
    }
    this.cwd = resolved;
    return makeResult();
  }

  private _cmdLs(args: string[], _stdin: string): ExecResult {
    const longFormat = args.includes("-l");
    const paths = args.filter((a) => !a.startsWith("-"));
    const target = paths[0] || this.cwd;
    const resolved = this._resolve(target);

    let entries: string[];
    try {
      entries = this.fs.listdir(resolved);
    } catch {
      return makeResult("", `ls: ${target}: No such directory\n`, 1);
    }

    if (longFormat) {
      const lines: string[] = [];
      for (const entry of entries) {
        const full = VirtualFS._norm(resolved + "/" + entry);
        const s = this.fs.stat(full);
        if (s.type === "directory") {
          lines.push(`drwxr-xr-x  -  ${entry}/`);
        } else {
          const size = String(s.size ?? 0).padStart(8);
          lines.push(`-rw-r--r--  ${size}  ${entry}`);
        }
      }
      return makeResult(lines.length > 0 ? lines.join("\n") + "\n" : "");
    }
    return makeResult(entries.length > 0 ? entries.join("\n") + "\n" : "");
  }

  private _cmdFind(args: string[], _stdin: string): ExecResult {
    let root = ".";
    let nameFilter: string | null = null;
    let typeFilter: string | null = null;

    let i = 0;
    while (i < args.length) {
      if (args[i] === "-name" && i + 1 < args.length) {
        nameFilter = args[i + 1];
        i += 2;
      } else if (args[i] === "-type" && i + 1 < args.length) {
        typeFilter = args[i + 1];
        i += 2;
      } else if (!args[i].startsWith("-")) {
        root = args[i];
        i++;
      } else {
        i++;
      }
    }

    const resolved = this._resolve(root);
    let results = this.fs.find(resolved, nameFilter || "*");

    if (typeFilter === "f") {
      results = results.filter((r) => !this.fs._isDir(r));
    } else if (typeFilter === "d") {
      results = results.filter((r) => this.fs._isDir(r));
    }

    return makeResult(results.length > 0 ? results.join("\n") + "\n" : "");
  }

  private _cmdGrep(args: string[], stdin: string): ExecResult {
    const caseInsensitive = args.includes("-i");
    const countOnly = args.includes("-c");
    const lineNumbers = args.includes("-n");
    const invert = args.includes("-v");
    const recursive = args.includes("-r") || args.includes("-rn");
    const filenames = args.includes("-l");
    const positional = args.filter((a) => !a.startsWith("-"));

    if (positional.length === 0) {
      return makeResult("", "grep: missing pattern\n", 2);
    }

    const pattern = positional[0];
    const targets = positional.slice(1);
    const flags = caseInsensitive ? "i" : "";

    let regex: RegExp;
    try {
      regex = new RegExp(pattern, flags);
    } catch (e: any) {
      return makeResult("", `grep: invalid pattern: ${e.message}\n`, 2);
    }

    function grepText(text: string, label: string = ""): string[] {
      const matches: string[] = [];
      const lines = text.split("\n");
      // Remove trailing empty line from split
      if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
      for (let idx = 0; idx < lines.length; idx++) {
        const line = lines[idx];
        let match = regex.test(line);
        // Reset lastIndex for non-global regex
        regex.lastIndex = 0;
        if (invert) match = !match;
        if (match) {
          const prefix = label ? `${label}:` : "";
          const num = lineNumbers ? `${idx + 1}:` : "";
          matches.push(`${prefix}${num}${line}`);
        }
      }
      return matches;
    }

    let allMatches: string[] = [];
    const matchedFiles: string[] = [];

    if (targets.length === 0 && stdin) {
      allMatches = grepText(stdin);
    } else if (recursive && targets.length > 0) {
      for (const target of targets) {
        const resolved = this._resolve(target);
        for (const fpath of this.fs.find(resolved)) {
          try {
            const text = this.fs.read(fpath);
            const m = grepText(text, fpath);
            if (m.length > 0) {
              matchedFiles.push(fpath);
              allMatches.push(...m);
            }
          } catch {
            // skip
          }
        }
      }
    } else {
      for (const target of targets) {
        try {
          const text = this.fs.read(this._resolve(target));
          const label = targets.length > 1 ? target : "";
          const m = grepText(text, label);
          if (m.length > 0) {
            matchedFiles.push(target);
            allMatches.push(...m);
          }
        } catch {
          return makeResult("", `grep: ${target}: No such file\n`, 2);
        }
      }
    }

    if (filenames) {
      return makeResult(
        matchedFiles.length > 0 ? matchedFiles.join("\n") + "\n" : "",
        "",
        matchedFiles.length > 0 ? 0 : 1
      );
    }

    if (countOnly) {
      return makeResult(
        `${allMatches.length}\n`,
        "",
        allMatches.length > 0 ? 0 : 1
      );
    }

    return makeResult(
      allMatches.length > 0 ? allMatches.join("\n") + "\n" : "",
      "",
      allMatches.length > 0 ? 0 : 1
    );
  }

  private _cmdHead(args: string[], stdin: string): ExecResult {
    let n = 10;
    const files: string[] = [];
    let i = 0;
    while (i < args.length) {
      if (args[i] === "-n" && i + 1 < args.length) {
        n = parseInt(args[i + 1], 10);
        i += 2;
      } else if (args[i].startsWith("-") && /^\d+$/.test(args[i].slice(1))) {
        n = parseInt(args[i].slice(1), 10);
        i++;
      } else {
        files.push(args[i]);
        i++;
      }
    }

    let text: string;
    if (files.length === 0) {
      text = stdin;
    } else {
      text = this.fs.read(this._resolve(files[0]));
    }
    const lines = text.split("\n");
    // Handle trailing newline
    if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
    const selected = lines.slice(0, n);
    return makeResult(selected.length > 0 ? selected.join("\n") + "\n" : "");
  }

  private _cmdTail(args: string[], stdin: string): ExecResult {
    let n = 10;
    const files: string[] = [];
    let i = 0;
    while (i < args.length) {
      if (args[i] === "-n" && i + 1 < args.length) {
        n = parseInt(args[i + 1], 10);
        i += 2;
      } else if (args[i].startsWith("-") && /^\d+$/.test(args[i].slice(1))) {
        n = parseInt(args[i].slice(1), 10);
        i++;
      } else {
        files.push(args[i]);
        i++;
      }
    }

    let text: string;
    if (files.length === 0) {
      text = stdin;
    } else {
      text = this.fs.read(this._resolve(files[0]));
    }
    const lines = text.split("\n");
    if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
    const selected = lines.slice(-n);
    return makeResult(selected.length > 0 ? selected.join("\n") + "\n" : "");
  }

  private _cmdWc(args: string[], stdin: string): ExecResult {
    const linesOnly = args.includes("-l");
    const wordsOnly = args.includes("-w");
    const charsOnly = args.includes("-c");
    const files = args.filter((a) => !a.startsWith("-"));

    let text: string;
    if (files.length === 0) {
      text = stdin;
    } else {
      try {
        text = this.fs.read(this._resolve(files[0]));
      } catch (e: any) {
        return makeResult("", e.message + "\n", 1);
      }
    }

    const lc = (text.match(/\n/g) || []).length;
    const wc = text.split(/\s+/).filter((s) => s.length > 0).length;
    const cc = text.length;

    if (linesOnly) return makeResult(`${lc}\n`);
    if (wordsOnly) return makeResult(`${wc}\n`);
    if (charsOnly) return makeResult(`${cc}\n`);

    const label = files.length > 0 ? ` ${files[0]}` : "";
    return makeResult(`  ${lc}  ${wc}  ${cc}${label}\n`);
  }

  private _cmdSort(args: string[], stdin: string): ExecResult {
    const reverse = args.includes("-r");
    const numeric = args.includes("-n");
    const unique = args.includes("-u");
    const files = args.filter((a) => !a.startsWith("-"));

    const text =
      files.length === 0 ? stdin : this.fs.read(this._resolve(files[0]));
    const lines = text.split("\n");
    if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();

    if (numeric) {
      lines.sort((a, b) => {
        const na = parseFloat(a.match(/^-?\d+\.?\d*/)?.[0] ?? "0");
        const nb = parseFloat(b.match(/^-?\d+\.?\d*/)?.[0] ?? "0");
        return reverse ? nb - na : na - nb;
      });
    } else {
      lines.sort();
      if (reverse) lines.reverse();
    }

    if (unique) {
      const seen = new Set<string>();
      const deduped: string[] = [];
      for (const line of lines) {
        if (!seen.has(line)) {
          seen.add(line);
          deduped.push(line);
        }
      }
      lines.length = 0;
      lines.push(...deduped);
    }

    return makeResult(lines.length > 0 ? lines.join("\n") + "\n" : "");
  }

  private _cmdUniq(args: string[], stdin: string): ExecResult {
    const count = args.includes("-c");
    const lines = stdin.split("\n");
    if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
    const result: string[] = [];
    let prev: string | null = null;
    let cnt = 0;

    for (const line of lines) {
      if (line === prev) {
        cnt++;
      } else {
        if (prev !== null) {
          result.push(count ? `  ${cnt} ${prev}` : prev);
        }
        prev = line;
        cnt = 1;
      }
    }
    if (prev !== null) {
      result.push(count ? `  ${cnt} ${prev}` : prev);
    }
    return makeResult(result.length > 0 ? result.join("\n") + "\n" : "");
  }

  private _cmdCut(args: string[], stdin: string): ExecResult {
    let delimiter = "\t";
    const fields: number[] = [];
    let i = 0;
    while (i < args.length) {
      if (args[i] === "-d" && i + 1 < args.length) {
        delimiter = args[i + 1];
        i += 2;
      } else if (args[i] === "-f" && i + 1 < args.length) {
        for (const part of args[i + 1].split(",")) {
          if (part.includes("-")) {
            const [start, end] = part.split("-", 2);
            const s = parseInt(start || "1", 10);
            const e = parseInt(end || "100", 10);
            for (let f = s; f <= e; f++) fields.push(f);
          } else {
            fields.push(parseInt(part, 10));
          }
        }
        i += 2;
      } else {
        i++;
      }
    }

    const lines = stdin.split("\n");
    if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
    const out: string[] = [];
    for (const line of lines) {
      const parts = line.split(delimiter);
      const selected = fields
        .filter((f) => f > 0 && f <= parts.length)
        .map((f) => parts[f - 1]);
      out.push(selected.join(delimiter));
    }
    return makeResult(out.length > 0 ? out.join("\n") + "\n" : "");
  }

  private _cmdTr(args: string[], stdin: string): ExecResult {
    const deleteMode = args.includes("-d");
    const positional = args.filter((a) => !a.startsWith("-"));

    if (deleteMode && positional.length > 0) {
      const chars = new Set(positional[0]);
      return makeResult(
        [...stdin].filter((c) => !chars.has(c)).join("")
      );
    }

    if (positional.length >= 2) {
      const set1 = positional[0];
      const set2 = positional[1];
      const mapping = new Map<string, string>();
      for (let j = 0; j < set1.length; j++) {
        mapping.set(set1[j], set2[j < set2.length ? j : set2.length - 1]);
      }
      return makeResult(
        [...stdin].map((c) => mapping.get(c) ?? c).join("")
      );
    }

    return makeResult(stdin);
  }

  private _cmdSed(args: string[], stdin: string): ExecResult {
    const files: string[] = [];
    let expr: string | null = null;
    let i = 0;
    while (i < args.length) {
      if (args[i] === "-e" && i + 1 < args.length) {
        expr = args[i + 1];
        i += 2;
      } else if (!args[i].startsWith("-")) {
        if (expr === null) {
          expr = args[i];
        } else {
          files.push(args[i]);
        }
        i++;
      } else {
        i++;
      }
    }

    if (!expr) return makeResult(stdin);

    const text =
      files.length === 0 ? stdin : this.fs.read(this._resolve(files[0]));

    // Match s/pattern/replacement/flags
    const m = expr.match(/^s(.)(.*?)\1(.*?)\1(\w*)$/);
    if (!m) return makeResult(text);

    const pat = m[2];
    const repl = m[3];
    const flagsStr = m[4];
    const global = flagsStr.includes("g");

    const regex = new RegExp(pat, global ? "g" : "");
    const result = text.replace(regex, repl);
    return makeResult(result);
  }

  private _cmdTee(args: string[], stdin: string): ExecResult {
    const appendMode = args.includes("-a");
    const files = args.filter((a) => !a.startsWith("-"));
    for (const f of files) {
      const path = this._resolve(f);
      if (appendMode && this.fs.exists(path)) {
        this.fs.write(path, this.fs.read(path) + stdin);
      } else {
        this.fs.write(path, stdin);
      }
    }
    return makeResult(stdin);
  }

  private _cmdTouch(args: string[], _stdin: string): ExecResult {
    for (const f of args) {
      const path = this._resolve(f);
      if (!this.fs.exists(path)) {
        this.fs.write(path, "");
      }
    }
    return makeResult();
  }

  private _cmdMkdir(args: string[], _stdin: string): ExecResult {
    for (const a of args) {
      if (a.startsWith("-")) continue;
      const path = this._resolve(a);
      this.fs.write(path + "/.keep", "");
    }
    return makeResult();
  }

  private _cmdCp(args: string[], _stdin: string): ExecResult {
    const positional = args.filter((a) => !a.startsWith("-"));
    if (positional.length < 2) {
      return makeResult("", "cp: missing operand\n", 1);
    }
    const src = this._resolve(positional[0]);
    const dst = this._resolve(positional[1]);
    try {
      this.fs.write(dst, this.fs.read(src));
    } catch (e: any) {
      return makeResult("", e.message + "\n", 1);
    }
    return makeResult();
  }

  private _cmdRm(args: string[], _stdin: string): ExecResult {
    const files = args.filter((a) => !a.startsWith("-"));
    for (const f of files) {
      try {
        this.fs.remove(this._resolve(f));
      } catch {
        // ignore
      }
    }
    return makeResult();
  }

  private _cmdStat(args: string[], _stdin: string): ExecResult {
    for (const f of args) {
      if (f.startsWith("-")) continue;
      try {
        const s = this.fs.stat(this._resolve(f));
        return makeResult(JSON.stringify(s, null, 2) + "\n");
      } catch (e: any) {
        return makeResult("", e.message + "\n", 1);
      }
    }
    return makeResult();
  }

  private _cmdTree(args: string[], _stdin: string): ExecResult {
    const target =
      args.length > 0 && !args[0].startsWith("-") ? args[0] : this.cwd;
    const resolved = this._resolve(target);
    const lines = [resolved];
    this._treeRecurse(resolved, "", lines);
    return makeResult(lines.join("\n") + "\n");
  }

  private _treeRecurse(
    path: string,
    prefix: string,
    lines: string[]
  ): void {
    const entries = this.fs.listdir(path);
    for (let i = 0; i < entries.length; i++) {
      const entry = entries[i];
      const isLast = i === entries.length - 1;
      const connector = isLast ? "\u2514\u2500\u2500 " : "\u251c\u2500\u2500 ";
      const full = VirtualFS._norm(path + "/" + entry);
      const isDir = this.fs._isDir(full);
      lines.push(`${prefix}${connector}${entry}${isDir ? "/" : ""}`);
      if (isDir) {
        const extension = isLast ? "    " : "\u2502   ";
        this._treeRecurse(full, prefix + extension, lines);
      }
    }
  }

  private _cmdJq(args: string[], stdin: string): ExecResult {
    const raw = args.includes("-r");
    const positional = args.filter((a) => !a.startsWith("-"));
    const query = positional[0] || ".";
    const files = positional.slice(1);

    const text =
      files.length === 0 ? stdin : this.fs.read(this._resolve(files[0]));

    let data: any;
    try {
      data = JSON.parse(text);
    } catch (e: any) {
      return makeResult("", `jq: parse error: ${e.message}\n`, 2);
    }

    let result: any;
    try {
      result = Shell._jqQuery(data, query);
    } catch (e: any) {
      return makeResult("", `jq: error: ${e.message}\n`, 5);
    }

    if (Array.isArray(result) && query.endsWith("[]")) {
      const parts = result.map((item) =>
        raw && typeof item === "string" ? item : JSON.stringify(item)
      );
      return makeResult(parts.join("\n") + "\n");
    }

    if (raw && typeof result === "string") {
      return makeResult(result + "\n");
    }
    return makeResult(JSON.stringify(result, null, 2) + "\n");
  }

  private static _jqQuery(data: any, query: string): any {
    if (query === ".") return data;
    const parts = query.match(/\.\w+|\[\d+\]|\[\]/g) || [];
    let current = data;
    for (const part of parts) {
      if (part === "[]") {
        if (!Array.isArray(current)) {
          throw new TypeError("Cannot iterate over non-array");
        }
        return current;
      } else if (part.startsWith("[")) {
        const idx = parseInt(part.slice(1, -1), 10);
        current = current[idx];
      } else if (part.startsWith(".")) {
        const key = part.slice(1);
        current = current[key];
      }
    }
    return current;
  }
}

export class ShellRegistry {
  private static _shells: Map<string, Shell> = new Map();

  static register(name: string, shell: Shell): void {
    ShellRegistry._shells.set(name, shell);
  }

  static get(name: string): Shell {
    const shell = ShellRegistry._shells.get(name);
    if (!shell) {
      throw new Error(`Shell '${name}' not registered`);
    }
    return shell.clone();
  }

  static has(name: string): boolean {
    return ShellRegistry._shells.has(name);
  }

  static remove(name: string): void {
    ShellRegistry._shells.delete(name);
  }

  static reset(): void {
    ShellRegistry._shells = new Map();
  }
}
