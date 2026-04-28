/**
 * Built-in file tools: Read, Write, Edit, Glob, Grep.
 *
 * Mirrors Claude Code's file toolset: parameter names and observable behavior
 * match closely. All five tools delegate to the agent's FilesystemDriver, so
 * swapping the shell backend (builtin / bashkit / OpenShell) automatically
 * swaps the filesystem they see.
 */

import type { FilesystemDriver } from "./drivers.js";
import { defineTool, type ToolDef } from "./uses-tools.js";

const TYPE_MAP: Record<string, string[]> = {
  py: [".py"],
  ts: [".ts", ".tsx"],
  js: [".js", ".jsx", ".mjs", ".cjs"],
  php: [".php"],
  go: [".go"],
  rs: [".rs"],
  java: [".java"],
  rb: [".rb"],
  md: [".md", ".markdown"],
  txt: [".txt"],
  json: [".json"],
  yaml: [".yaml", ".yml"],
  sh: [".sh", ".bash"],
  html: [".html", ".htm"],
  css: [".css"],
  sql: [".sql"],
};

function norm(p: string): string {
  if (!p.startsWith("/")) p = "/" + p;
  const parts = p.split("/");
  const out: string[] = [];
  for (const seg of parts) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      out.pop();
      continue;
    }
    out.push(seg);
  }
  return "/" + out.join("/");
}

function isBinary(content: string): boolean {
  const sample = content.length > 8192 ? content.slice(0, 8192) : content;
  return sample.includes("\x00");
}

function formatLines(text: string, offset: number, limit: number): string {
  let lines = text.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines = lines.slice(0, -1);
  const start = Math.max(1, offset);
  const end = Math.min(lines.length, start + limit - 1);
  const out: string[] = [];
  for (let i = start; i <= end; i++) {
    out.push(`${String(i).padStart(6, " ")}\t${lines[i - 1]}`);
  }
  return out.join("\n");
}

function joinPath(a: string, b: string): string {
  if (a.endsWith("/")) return a + b;
  return a + "/" + b;
}

function walk(fs: FilesystemDriver, root: string): string[] {
  root = norm(root);
  if (!fs.exists(root)) return [];
  if (!fs.isDir(root)) return [root];
  const out: string[] = [];
  const stack = [root];
  while (stack.length > 0) {
    const d = stack.pop()!;
    for (const entry of fs.listdir(d)) {
      if (entry.startsWith(".")) continue;
      const child = joinPath(d, entry);
      if (fs.isDir(child)) stack.push(child);
      else out.push(child);
    }
  }
  return out;
}

function globToRegex(pattern: string): RegExp {
  const out: string[] = [];
  let i = 0;
  while (i < pattern.length) {
    const c = pattern[i];
    if (c === "*" && i + 1 < pattern.length && pattern[i + 1] === "*") {
      out.push(".*");
      i += 2;
      if (i < pattern.length && pattern[i] === "/") i += 1;
    } else if (c === "*") {
      out.push("[^/]*");
      i += 1;
    } else if (c === "?") {
      out.push("[^/]");
      i += 1;
    } else if (".+^${}()|[]\\".includes(c)) {
      out.push("\\" + c);
      i += 1;
    } else {
      out.push(c);
      i += 1;
    }
  }
  return new RegExp("^" + out.join("") + "$");
}

function matchesGlob(relPath: string, pattern: string): boolean {
  return globToRegex(pattern).test(relPath);
}

function resolveRoot(cwd: string, path: string | undefined): string {
  if (!path) return norm(cwd);
  if (path.startsWith("/")) return norm(path);
  return norm(joinPath(cwd, path));
}

export interface RegisterFileToolsOptions {
  enforceReadGate?: boolean;
}

export interface FileToolsState {
  enforceReadGate: boolean;
  readSet: Set<string> | null;
}

interface ShellLike {
  fs: FilesystemDriver;
  cwd: string;
}

interface AgentLike {
  shell: ShellLike;
  registerTool(td: ToolDef): unknown;
}

function buildTools(agent: AgentLike, state: FileToolsState): ToolDef[] {
  const fs = (): FilesystemDriver => agent.shell.fs;
  const cwd = (): string => agent.shell.cwd;

  const readTool = defineTool({
    name: "Read",
    description:
      "Read a file from the virtual filesystem. Returns content with `cat -n` " +
      "style line numbers (1-indexed). Defaults to the first 2000 lines. Use " +
      "`offset` (line number to start from, 1-indexed) and `limit` to page " +
      "through larger files. Binary files (containing null bytes in the first " +
      "8KB) are rejected with an error.",
    parameters: {
      type: "object",
      properties: {
        file_path: { type: "string", description: "Absolute path of the file to read." },
        offset: { type: "integer", description: "Line number to start from (1-indexed). Default 1." },
        limit: { type: "integer", description: "Maximum number of lines to return. Default 2000." },
      },
      required: ["file_path"],
    },
    execute: (args) => {
      const path = norm(args.file_path);
      if (!fs().exists(path)) throw new Error(`File does not exist: ${path}`);
      if (fs().isDir(path)) throw new Error(`Path is a directory, not a file: ${path}`);
      const content = fs().read(path);
      if (isBinary(content)) {
        throw new Error("binary file not supported by Read; use a different tool");
      }
      if (state.readSet) state.readSet.add(path);
      const offset = typeof args.offset === "number" ? args.offset : 1;
      const limit = typeof args.limit === "number" ? args.limit : 2000;
      return formatLines(content, offset, limit);
    },
  });

  const writeTool = defineTool({
    name: "Write",
    description: "Write content to a file, creating it if it doesn't exist or overwriting if it does.",
    parameters: {
      type: "object",
      properties: {
        file_path: { type: "string", description: "Absolute path of the file to write." },
        content: { type: "string", description: "The content to write." },
      },
      required: ["file_path", "content"],
    },
    execute: (args) => {
      const path = norm(args.file_path);
      fs().write(path, args.content);
      if (state.readSet) state.readSet.add(path);
      return `File written: ${path}`;
    },
  });

  const editTool = defineTool({
    name: "Edit",
    description:
      "Replace exact strings in a file. Errors if `old_string` is not found, " +
      "or appears more than once when `replace_all` is false (the default). " +
      "When the read-gate is enabled at registration time, errors if the file " +
      "has not been Read in this session.",
    parameters: {
      type: "object",
      properties: {
        file_path: { type: "string", description: "Absolute path of the file to edit." },
        old_string: { type: "string", description: "The exact string to replace." },
        new_string: { type: "string", description: "The replacement string." },
        replace_all: { type: "boolean", description: "Replace all occurrences. Default false." },
      },
      required: ["file_path", "old_string", "new_string"],
    },
    execute: (args) => {
      const path = norm(args.file_path);
      if (!fs().exists(path)) throw new Error(`File does not exist: ${path}`);
      if (state.enforceReadGate && (!state.readSet || !state.readSet.has(path))) {
        throw new Error("File has not been read yet. Read it first before writing to it.");
      }
      const text = fs().read(path);
      const oldStr: string = args.old_string;
      const newStr: string = args.new_string;
      if (!text.includes(oldStr)) {
        throw new Error("String to replace not found in file");
      }
      let updated: string;
      if (args.replace_all) {
        updated = text.split(oldStr).join(newStr);
      } else {
        const count = text.split(oldStr).length - 1;
        if (count > 1) {
          throw new Error(
            `Found ${count} matches of the string to replace, but replace_all is false. ` +
              `Provide a larger string with more surrounding context to make it unique, ` +
              `or set replace_all to true.`,
          );
        }
        updated = text.replace(oldStr, newStr);
      }
      fs().write(path, updated);
      return `File edited: ${path}`;
    },
  });

  const globTool = defineTool({
    name: "Glob",
    description:
      "Match files by pattern (supports `*`, `?`, and recursive `**`) and return " +
      "absolute paths sorted by modification time, newest first. `path` defaults " +
      "to the shell's current working directory.",
    parameters: {
      type: "object",
      properties: {
        pattern: { type: "string", description: "Glob pattern, e.g. `**/*.ts`." },
        path: { type: "string", description: "Directory to search in. Defaults to cwd." },
      },
      required: ["pattern"],
    },
    execute: (args) => {
      const root = resolveRoot(cwd(), args.path);
      const files = walk(fs(), root);
      const prefix = root.endsWith("/") ? root : root + "/";
      const matched: string[] = [];
      for (const p of files) {
        const rel = p.startsWith(prefix) ? p.slice(prefix.length) : p;
        if (matchesGlob(rel, args.pattern)) matched.push(p);
      }
      const mtimeOf = (p: string): number => {
        try {
          return fs().stat(p).mtime ?? 0;
        } catch {
          return 0;
        }
      };
      matched.sort((a, b) => mtimeOf(b) - mtimeOf(a));
      return matched;
    },
  });

  const grepTool = defineTool({
    name: "Grep",
    description:
      "Search file contents with a regular expression. Recursively walks `path` " +
      "(default cwd) and matches against each file's content. `output_mode` " +
      "controls return shape: `files_with_matches` (default) → list of paths; " +
      "`count` → list of {path, count}; `content` → list of {path, matches} " +
      "where each match is {line, context: [{line, text, match}]}. Use `glob` " +
      "or `type` (py, ts, js, php, go, rs, java, rb, md, txt, json, yaml, sh, " +
      "html, css, sql) to filter the file set. Set `multiline` true to match " +
      "across line boundaries; `line_numbers` adds line numbers to context " +
      "entries; `context_before`/`context_after` add surrounding lines.",
    parameters: {
      type: "object",
      properties: {
        pattern: { type: "string", description: "The regular expression to match." },
        path: { type: "string", description: "Directory to search in. Defaults to cwd." },
        glob: { type: "string", description: "Filter the file set by glob pattern (e.g. `**/*.ts`)." },
        type: { type: "string", description: "Filter by built-in file type alias (py, ts, js, ...)." },
        case_insensitive: { type: "boolean", description: "Case-insensitive match. Default false." },
        line_numbers: { type: "boolean", description: "Include line numbers in context entries. Default false." },
        output_mode: { type: "string", description: "files_with_matches (default), count, or content." },
        head_limit: { type: "integer", description: "Return only the first N results." },
        multiline: { type: "boolean", description: "Allow patterns to match across line boundaries. Default false." },
        context_before: { type: "integer", description: "Lines of context before each match. Default 0." },
        context_after: { type: "integer", description: "Lines of context after each match. Default 0." },
      },
      required: ["pattern"],
    },
    execute: (args) => {
      const root = resolveRoot(cwd(), args.path);
      const flags = (args.case_insensitive ? "i" : "") + (args.multiline ? "ms" : "");
      const regex = new RegExp(args.pattern, flags);
      const lineRegex = new RegExp(args.pattern, args.case_insensitive ? "i" : "");
      let files = walk(fs(), root);
      const prefix = root.endsWith("/") ? root : root + "/";

      if (args.glob) {
        files = files.filter((p) =>
          matchesGlob(p.startsWith(prefix) ? p.slice(prefix.length) : p, args.glob),
        );
      }
      if (args.type) {
        const exts = TYPE_MAP[args.type];
        if (!exts) throw new Error(`Unknown file type: ${args.type}`);
        files = files.filter((p) => exts.some((e) => p.endsWith(e)));
      }

      const mode = args.output_mode || "files_with_matches";
      const headLimit: number | undefined = args.head_limit;

      if (mode === "files_with_matches") {
        const matched: string[] = [];
        for (const p of files) {
          let text: string;
          try {
            text = fs().read(p);
          } catch {
            continue;
          }
          const hit = args.multiline
            ? regex.test(text)
            : text.split("\n").some((line) => lineRegex.test(line));
          if (hit) matched.push(p);
        }
        return headLimit !== undefined ? matched.slice(0, headLimit) : matched;
      }

      if (mode === "count") {
        const counts: { path: string; count: number }[] = [];
        for (const p of files) {
          let text: string;
          try {
            text = fs().read(p);
          } catch {
            continue;
          }
          let n: number;
          if (args.multiline) {
            const g = new RegExp(args.pattern, "g" + flags);
            n = (text.match(g) || []).length;
          } else {
            n = text.split("\n").filter((line) => lineRegex.test(line)).length;
          }
          if (n > 0) counts.push({ path: p, count: n });
        }
        return headLimit !== undefined ? counts.slice(0, headLimit) : counts;
      }

      if (mode === "content") {
        const results: { path: string; matches: any[] }[] = [];
        const ctxBefore = args.context_before ?? 0;
        const ctxAfter = args.context_after ?? 0;
        for (const p of files) {
          let text: string;
          try {
            text = fs().read(p);
          } catch {
            continue;
          }
          const lines = text.split("\n");
          const hits: any[] = [];
          for (let idx = 0; idx < lines.length; idx++) {
            if (!lineRegex.test(lines[idx])) continue;
            const start = Math.max(0, idx - ctxBefore);
            const end = Math.min(lines.length, idx + ctxAfter + 1);
            const ctx: any[] = [];
            for (let j = start; j < end; j++) {
              const entry: Record<string, any> = { text: lines[j], match: j === idx };
              if (args.line_numbers) entry.line = j + 1;
              ctx.push(entry);
            }
            hits.push({ line: idx + 1, context: ctx });
          }
          if (hits.length > 0) results.push({ path: p, matches: hits });
          if (headLimit !== undefined && results.length >= headLimit) break;
        }
        return results;
      }

      throw new Error(`Unknown output_mode: ${mode}`);
    },
  });

  return [readTool, writeTool, editTool, globTool, grepTool];
}

export function registerFileTools(
  agent: AgentLike,
  options: RegisterFileToolsOptions = {},
): FileToolsState {
  const enforceReadGate = options.enforceReadGate ?? false;
  const state: FileToolsState = {
    enforceReadGate,
    readSet: enforceReadGate ? new Set<string>() : null,
  };
  for (const td of buildTools(agent, state)) {
    agent.registerTool(td);
  }
  return state;
}
