/**
 * Virtual shell interpreter over a VirtualFS.
 * Tokenizer + recursive-descent parser + AST evaluator.
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

// ---------------------------------------------------------------------------
// Token types
// ---------------------------------------------------------------------------

type TokenType =
  | "WORD"
  | "PIPE"
  | "SEMI"
  | "DSEMI"
  | "AND"
  | "OR"
  | "REDIRECT_OUT"
  | "REDIRECT_APPEND"
  | "LPAREN"
  | "RPAREN"
  | "NEWLINE"
  | "EOF";

const KEYWORDS = new Set([
  "if", "then", "elif", "else", "fi",
  "for", "in", "do", "done", "while",
  "case", "esac",
]);

interface Token {
  type: TokenType;
  value: string;
}

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

function tokenize(input: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  while (i < input.length) {
    const c = input[i];

    if (c === " " || c === "\t") { i++; continue; }

    if (c === "\n") {
      tokens.push({ type: "NEWLINE", value: "\n" });
      i++; continue;
    }

    if (c === ";" && input[i + 1] === ";") {
      tokens.push({ type: "DSEMI", value: ";;" });
      i += 2; continue;
    }

    if (c === ";") {
      tokens.push({ type: "SEMI", value: ";" });
      i++; continue;
    }

    if (c === "|" && input[i + 1] === "|") {
      tokens.push({ type: "OR", value: "||" });
      i += 2; continue;
    }

    if (c === "|") {
      tokens.push({ type: "PIPE", value: "|" });
      i++; continue;
    }

    if (c === "&" && input[i + 1] === "&") {
      tokens.push({ type: "AND", value: "&&" });
      i += 2; continue;
    }

    if (c === ">" && input[i + 1] === ">") {
      tokens.push({ type: "REDIRECT_APPEND", value: ">>" });
      i += 2; continue;
    }

    if (c === ">") {
      tokens.push({ type: "REDIRECT_OUT", value: ">" });
      i++; continue;
    }

    if (c === "(") {
      tokens.push({ type: "LPAREN", value: "(" });
      i++; continue;
    }

    if (c === ")") {
      tokens.push({ type: "RPAREN", value: ")" });
      i++; continue;
    }

    // Comment
    if (c === "#") {
      while (i < input.length && input[i] !== "\n") i++;
      continue;
    }

    // Word (includes quoted strings, $(...), `...`, ${...}, $VAR)
    let word = "";
    while (i < input.length) {
      const ch = input[i];

      if (ch === "'" ) {
        // Single-quoted string: collect everything verbatim including the quotes
        word += ch; i++;
        while (i < input.length && input[i] !== "'") { word += input[i]; i++; }
        if (i < input.length) { word += input[i]; i++; }
        continue;
      }

      if (ch === '"') {
        word += ch; i++;
        while (i < input.length && input[i] !== '"') {
          if (input[i] === "\\" && i + 1 < input.length) {
            word += input[i] + input[i + 1]; i += 2;
          } else {
            word += input[i]; i++;
          }
        }
        if (i < input.length) { word += input[i]; i++; }
        continue;
      }

      if (ch === "$" && input[i + 1] === "(") {
        // Command substitution $(...)
        word += "$(";
        i += 2;
        let depth = 1;
        while (i < input.length && depth > 0) {
          if (input[i] === "(") depth++;
          else if (input[i] === ")") { depth--; if (depth === 0) break; }
          else if (input[i] === "'" ) {
            word += input[i]; i++;
            while (i < input.length && input[i] !== "'") { word += input[i]; i++; }
            if (i < input.length) { word += input[i]; i++; }
            continue;
          } else if (input[i] === '"') {
            word += input[i]; i++;
            while (i < input.length && input[i] !== '"') {
              if (input[i] === "\\" && i + 1 < input.length) {
                word += input[i] + input[i + 1]; i += 2;
              } else {
                word += input[i]; i++;
              }
            }
            if (i < input.length) { word += input[i]; i++; }
            continue;
          }
          word += input[i]; i++;
        }
        word += ")";
        if (i < input.length) i++; // skip closing )
        continue;
      }

      if (ch === "$" && input[i + 1] === "{") {
        // Parameter expansion ${...}
        word += "${";
        i += 2;
        let depth = 1;
        while (i < input.length && depth > 0) {
          if (input[i] === "{") depth++;
          else if (input[i] === "}") { depth--; if (depth === 0) break; }
          word += input[i]; i++;
        }
        word += "}";
        if (i < input.length) i++; // skip closing }
        continue;
      }

      if (ch === "`") {
        // Backtick command substitution
        word += "`"; i++;
        while (i < input.length && input[i] !== "`") {
          word += input[i]; i++;
        }
        word += "`";
        if (i < input.length) i++;
        continue;
      }

      if (ch === "\\") {
        // Escape next character
        if (i + 1 < input.length) {
          word += input[i + 1];
          i += 2;
        } else {
          i++;
        }
        continue;
      }

      // Break on meta-characters
      if (
        ch === " " || ch === "\t" || ch === "\n" ||
        ch === "|" || ch === ";" || ch === "&" ||
        ch === ">" || ch === "<" || ch === "(" || ch === ")" ||
        ch === "#"
      ) {
        break;
      }

      word += ch; i++;
    }

    if (word.length > 0) {
      tokens.push({ type: "WORD", value: word });
    }
  }

  tokens.push({ type: "EOF", value: "" });
  return tokens;
}

// ---------------------------------------------------------------------------
// AST node types
// ---------------------------------------------------------------------------

interface CommandNode {
  type: "command";
  args: string[];
  redirects: { mode: "write" | "append"; target: string }[];
}

interface PipelineNode {
  type: "pipeline";
  commands: ASTNode[];
}

interface ListNode {
  type: "list";
  commands: ASTNode[];
}

interface AndNode {
  type: "and";
  left: ASTNode;
  right: ASTNode;
}

interface OrNode {
  type: "or";
  left: ASTNode;
  right: ASTNode;
}

interface IfNode {
  type: "if";
  clauses: { condition: ASTNode; body: ASTNode }[];
  elseBody: ASTNode | null;
}

interface ForNode {
  type: "for";
  variable: string;
  words: string[];
  body: ASTNode;
}

interface WhileNode {
  type: "while";
  condition: ASTNode;
  body: ASTNode;
}

interface AssignmentNode {
  type: "assignment";
  name: string;
  value: string;
}

interface CaseNode {
  type: "case";
  word: string;
  clauses: { patterns: string[]; body: ASTNode }[];
}

type ASTNode =
  | CommandNode
  | PipelineNode
  | ListNode
  | AndNode
  | OrNode
  | IfNode
  | ForNode
  | WhileNode
  | AssignmentNode
  | CaseNode;

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

class Parser {
  private tokens: Token[];
  private pos: number;
  private depth: number;

  constructor(tokens: Token[]) {
    this.tokens = tokens;
    this.pos = 0;
    this.depth = 0;
  }

  private peek(): Token {
    return this.tokens[this.pos] ?? { type: "EOF", value: "" };
  }

  private advance(): Token {
    const t = this.tokens[this.pos];
    this.pos++;
    return t;
  }

  private expect(type: TokenType): Token {
    const t = this.peek();
    if (t.type !== type) {
      throw new Error(`Expected ${type} but got ${t.type} (${t.value})`);
    }
    return this.advance();
  }

  private expectKeyword(kw: string): void {
    const t = this.peek();
    if (t.type !== "WORD" || t.value !== kw) {
      throw new Error(`Expected '${kw}' but got '${t.value}'`);
    }
    this.advance();
  }

  private atEnd(): boolean {
    const t = this.peek();
    return t.type === "EOF";
  }

  private skipNewlines(): void {
    while (this.peek().type === "NEWLINE") this.advance();
  }

  private skipSemiNewlines(): void {
    while (this.peek().type === "NEWLINE" || this.peek().type === "SEMI") this.advance();
  }

  private isCommandTerminator(): boolean {
    const t = this.peek();
    return (
      t.type === "EOF" ||
      t.type === "SEMI" ||
      t.type === "DSEMI" ||
      t.type === "NEWLINE" ||
      t.type === "AND" ||
      t.type === "OR" ||
      t.type === "PIPE" ||
      t.type === "RPAREN" ||
      (t.type === "WORD" && (
        t.value === "then" || t.value === "elif" ||
        t.value === "else" || t.value === "fi" ||
        t.value === "do" || t.value === "done" ||
        t.value === "esac"
      ))
    );
  }

  parse(): ASTNode {
    this.depth++;
    if (this.depth > 50) throw new Error("Nesting depth limit exceeded");
    const node = this.parseList();
    this.depth--;
    return node;
  }

  parseList(): ASTNode {
    this.skipSemiNewlines();
    if (this.atEnd()) return { type: "command", args: [], redirects: [] };

    const nodes: ASTNode[] = [];
    nodes.push(this.parseAndOr());

    while (this.peek().type === "SEMI" || this.peek().type === "NEWLINE") {
      this.skipSemiNewlines();
      if (this.atEnd() || this.isCompoundEnd()) break;
      nodes.push(this.parseAndOr());
    }

    if (nodes.length === 1) return nodes[0];
    return { type: "list", commands: nodes };
  }

  private isCompoundEnd(): boolean {
    const t = this.peek();
    return t.type === "WORD" && (
      t.value === "fi" || t.value === "done" ||
      t.value === "then" || t.value === "elif" ||
      t.value === "else" || t.value === "do" ||
      t.value === "esac"
    );
  }

  private parseAndOr(): ASTNode {
    let left = this.parsePipeline();

    while (true) {
      const t = this.peek();
      if (t.type === "AND") {
        this.advance();
        this.skipNewlines();
        const right = this.parsePipeline();
        left = { type: "and", left, right };
      } else if (t.type === "OR") {
        this.advance();
        this.skipNewlines();
        const right = this.parsePipeline();
        left = { type: "or", left, right };
      } else {
        break;
      }
    }
    return left;
  }

  private parsePipeline(): ASTNode {
    const commands: ASTNode[] = [];
    commands.push(this.parseCommand());

    while (this.peek().type === "PIPE") {
      this.advance();
      this.skipNewlines();
      commands.push(this.parseCommand());
    }

    if (commands.length === 1) return commands[0];
    return { type: "pipeline", commands };
  }

  private parseCommand(): ASTNode {
    const t = this.peek();

    if (t.type === "WORD") {
      if (t.value === "if") return this.parseIf();
      if (t.value === "for") return this.parseFor();
      if (t.value === "while") return this.parseWhile();
      if (t.value === "case") return this.parseCase();
    }

    return this.parseSimpleCommand();
  }

  private parseSimpleCommand(): ASTNode {
    const args: string[] = [];
    const redirects: { mode: "write" | "append"; target: string }[] = [];

    while (!this.isCommandTerminator()) {
      const t = this.peek();

      if (t.type === "REDIRECT_APPEND") {
        this.advance();
        const target = this.expect("WORD");
        redirects.push({ mode: "append", target: target.value });
      } else if (t.type === "REDIRECT_OUT") {
        this.advance();
        const target = this.expect("WORD");
        redirects.push({ mode: "write", target: target.value });
      } else if (t.type === "WORD") {
        args.push(t.value);
        this.advance();
      } else {
        break;
      }
    }

    // Check for assignment: first arg matches VAR=value pattern
    if (args.length >= 1 && redirects.length === 0) {
      let assignIdx = 0;
      // Handle `export VAR=value`
      if (args[0] === "export" && args.length >= 2) {
        assignIdx = 1;
      }
      const m = args[assignIdx]?.match(/^([A-Za-z_]\w*)=(.*)$/);
      if (m && (assignIdx === 0 || args[0] === "export")) {
        if (assignIdx === 1 && args.length === 2) {
          return { type: "assignment", name: m[1], value: m[2] };
        }
        if (assignIdx === 0 && args.length === 1) {
          return { type: "assignment", name: m[1], value: m[2] };
        }
      }
    }

    return { type: "command", args, redirects };
  }

  private parseIf(): ASTNode {
    this.expectKeyword("if");
    this.skipSemiNewlines();
    const clauses: { condition: ASTNode; body: ASTNode }[] = [];

    const condition = this.parseList();
    this.skipSemiNewlines();
    this.expectKeyword("then");
    this.skipSemiNewlines();
    const body = this.parseList();
    clauses.push({ condition, body });

    while (this.peek().type === "WORD" && this.peek().value === "elif") {
      this.advance();
      this.skipSemiNewlines();
      const elifCond = this.parseList();
      this.skipSemiNewlines();
      this.expectKeyword("then");
      this.skipSemiNewlines();
      const elifBody = this.parseList();
      clauses.push({ condition: elifCond, body: elifBody });
    }

    let elseBody: ASTNode | null = null;
    if (this.peek().type === "WORD" && this.peek().value === "else") {
      this.advance();
      this.skipSemiNewlines();
      elseBody = this.parseList();
    }

    this.skipSemiNewlines();
    this.expectKeyword("fi");

    return { type: "if", clauses, elseBody };
  }

  private parseFor(): ASTNode {
    this.expectKeyword("for");
    const varToken = this.expect("WORD");
    const variable = varToken.value;

    this.skipSemiNewlines();
    let words: string[] = [];
    if (this.peek().type === "WORD" && this.peek().value === "in") {
      this.advance();
      while (this.peek().type === "WORD" && !this.isCompoundEnd()) {
        words.push(this.advance().value);
      }
    }

    this.skipSemiNewlines();
    this.expectKeyword("do");
    this.skipSemiNewlines();
    const body = this.parseList();
    this.skipSemiNewlines();
    this.expectKeyword("done");

    return { type: "for", variable, words, body };
  }

  private parseWhile(): ASTNode {
    this.expectKeyword("while");
    this.skipSemiNewlines();
    const condition = this.parseList();
    this.skipSemiNewlines();
    this.expectKeyword("do");
    this.skipSemiNewlines();
    const body = this.parseList();
    this.skipSemiNewlines();
    this.expectKeyword("done");

    return { type: "while", condition, body };
  }

  private parseCase(): ASTNode {
    this.expectKeyword("case");
    const wordToken = this.expect("WORD");
    const word = wordToken.value;
    this.skipSemiNewlines();
    this.expectKeyword("in");
    this.skipSemiNewlines();

    const clauses: { patterns: string[]; body: ASTNode }[] = [];

    while (!(this.peek().type === "WORD" && this.peek().value === "esac") && !this.atEnd()) {
      // Parse patterns: pattern1 | pattern2 )
      const patterns: string[] = [];
      // Skip optional leading (
      if (this.peek().type === "LPAREN") this.advance();

      patterns.push(this.expect("WORD").value);
      while (this.peek().type === "PIPE") {
        this.advance();
        patterns.push(this.expect("WORD").value);
      }
      // Expect )
      if (this.peek().type === "RPAREN") {
        this.advance();
      } else {
        throw new Error(`Expected ')' in case clause but got '${this.peek().value}'`);
      }
      this.skipSemiNewlines();

      // Parse body until ;;
      const body = this.parseList();
      clauses.push({ patterns, body });

      // Expect ;;
      if (this.peek().type === "DSEMI") {
        this.advance();
      }
      this.skipSemiNewlines();
    }

    this.expectKeyword("esac");
    return { type: "case", word, clauses };
  }
}

// ---------------------------------------------------------------------------
// Shell
// ---------------------------------------------------------------------------

const MAX_VAR_SIZE = 64 * 1024;
const MAX_EXPANSIONS = 1_000;

export class Shell {
  fs: VirtualFS;
  cwd: string;
  env: Record<string, string>;
  maxOutput: number;
  maxIterations: number;
  private _allowedCommands: Set<string> | undefined;
  private _builtins: Map<string, CmdHandler>;
  private _iterationCounter: number;
  private _cmdSubDepth: number;
  private _expansionCount: number;

  constructor(opts: ShellOptions = {}) {
    this.fs = opts.fs ?? new VirtualFS();
    this.cwd = opts.cwd ?? "/";
    this.env = opts.env ?? {};
    this.maxOutput = opts.maxOutput ?? 16_000;
    this.maxIterations = opts.maxIterations ?? 10_000;
    this._allowedCommands = opts.allowedCommands;
    this._iterationCounter = 0;
    this._cmdSubDepth = 0;
    this._expansionCount = 0;

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
      ["test", this._cmdTest.bind(this)],
      ["[", this._cmdBracket.bind(this)],
      ["[[", this._cmdDoubleBracket.bind(this)],
      ["printf", this._cmdPrintf.bind(this)],
      ["export", this._cmdExport.bind(this)],
      ["true", this._cmdTrue.bind(this)],
      ["false", this._cmdFalse.bind(this)],
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

    let ast: ASTNode;
    try {
      const tokens = tokenize(command);
      const parser = new Parser(tokens);
      ast = parser.parse();
    } catch (e: any) {
      return makeResult("", `parse error: ${e.message}\n`, 2);
    }

    this._iterationCounter = 0;
    this._expansionCount = 0;

    let result: ExecResult;
    try {
      result = this._eval(ast, "");
    } catch (e: any) {
      return makeResult("", `${e.message}\n`, 1);
    }

    if (result.stdout.length > this.maxOutput) {
      result = {
        ...result,
        stdout:
          result.stdout.slice(0, this.maxOutput) +
          `\n... [truncated, ${result.stdout.length} total chars]`,
      };
    }

    return result;
  }

  // -----------------------------------------------------------------------
  // AST evaluator
  // -----------------------------------------------------------------------

  private _eval(node: ASTNode, stdin: string): ExecResult {
    switch (node.type) {
      case "command": return this._evalCommand(node, stdin);
      case "pipeline": return this._evalPipeline(node, stdin);
      case "list": return this._evalList(node, stdin);
      case "and": return this._evalAnd(node, stdin);
      case "or": return this._evalOr(node, stdin);
      case "if": return this._evalIf(node, stdin);
      case "for": return this._evalFor(node, stdin);
      case "while": return this._evalWhile(node, stdin);
      case "assignment": return this._evalAssignment(node);
      case "case": return this._evalCase(node, stdin);
    }
  }

  private _evalCommand(node: CommandNode, stdin: string): ExecResult {
    if (node.args.length === 0 && node.redirects.length === 0) {
      return makeResult();
    }

    const expanded = this._expandArgs(node.args);
    if (expanded.length === 0) return makeResult();

    const cmdName = expanded[0];
    const args = expanded.slice(1);

    const handler = this._builtins.get(cmdName);
    if (!handler) {
      this.env["?"] = "127";
      return makeResult("", `${cmdName}: command not found\n`, 127);
    }

    let result = handler(args, stdin);
    this.env["?"] = String(result.exitCode);

    for (const redir of node.redirects) {
      const target = this._expandWord(redir.target);
      const path = this._resolve(target);
      if (redir.mode === "append" && this.fs.exists(path)) {
        const existing = this.fs.read(path);
        this.fs.write(path, existing + result.stdout);
      } else {
        this.fs.write(path, result.stdout);
      }
      result = makeResult("", result.stderr, result.exitCode);
    }

    return result;
  }

  private _evalPipeline(node: PipelineNode, stdin: string): ExecResult {
    let currentStdin = stdin;
    let lastResult = makeResult();

    for (const cmd of node.commands) {
      lastResult = this._eval(cmd, currentStdin);
      currentStdin = lastResult.stdout;
      if (lastResult.exitCode !== 0) break;
    }

    this.env["?"] = String(lastResult.exitCode);
    return lastResult;
  }

  private _evalList(node: ListNode, stdin: string): ExecResult {
    const results: ExecResult[] = [];
    for (const cmd of node.commands) {
      const r = this._eval(cmd, stdin);
      results.push(r);
    }
    return makeResult(
      results.map((r) => r.stdout).join(""),
      results.map((r) => r.stderr).join(""),
      results.length > 0 ? results[results.length - 1].exitCode : 0
    );
  }

  private _evalAnd(node: AndNode, stdin: string): ExecResult {
    const left = this._eval(node.left, stdin);
    if (left.exitCode !== 0) return left;
    const right = this._eval(node.right, stdin);
    return makeResult(
      left.stdout + right.stdout,
      left.stderr + right.stderr,
      right.exitCode
    );
  }

  private _evalOr(node: OrNode, stdin: string): ExecResult {
    const left = this._eval(node.left, stdin);
    if (left.exitCode === 0) return left;
    const right = this._eval(node.right, stdin);
    return makeResult(
      left.stdout + right.stdout,
      left.stderr + right.stderr,
      right.exitCode
    );
  }

  private _evalIf(node: IfNode, stdin: string): ExecResult {
    for (const clause of node.clauses) {
      const condResult = this._eval(clause.condition, stdin);
      if (condResult.exitCode === 0) {
        const bodyResult = this._eval(clause.body, stdin);
        return makeResult(
          condResult.stdout + bodyResult.stdout,
          condResult.stderr + bodyResult.stderr,
          bodyResult.exitCode
        );
      }
    }
    if (node.elseBody) {
      return this._eval(node.elseBody, stdin);
    }
    return makeResult();
  }

  private _evalFor(node: ForNode, stdin: string): ExecResult {
    const words = node.words.flatMap((w) => {
      const expanded = this._expandWord(w);
      return expanded.split(/\s+/).filter(Boolean);
    });

    let stdout = "";
    let stderr = "";
    let exitCode = 0;

    for (const word of words) {
      this._iterationCounter++;
      if (this._iterationCounter > this.maxIterations) {
        return makeResult(stdout, stderr + "Maximum iteration limit exceeded\n", 1);
      }
      this.env[node.variable] = word;
      const r = this._eval(node.body, stdin);
      stdout += r.stdout;
      stderr += r.stderr;
      exitCode = r.exitCode;
    }

    return makeResult(stdout, stderr, exitCode);
  }

  private _evalWhile(node: WhileNode, stdin: string): ExecResult {
    let stdout = "";
    let stderr = "";
    let exitCode = 0;

    while (true) {
      this._iterationCounter++;
      if (this._iterationCounter > this.maxIterations) {
        return makeResult(stdout, stderr + "Maximum iteration limit exceeded\n", 1);
      }
      const condResult = this._eval(node.condition, stdin);
      if (condResult.exitCode !== 0) break;
      const bodyResult = this._eval(node.body, stdin);
      stdout += bodyResult.stdout;
      stderr += bodyResult.stderr;
      exitCode = bodyResult.exitCode;
    }

    return makeResult(stdout, stderr, exitCode);
  }

  private _evalAssignment(node: AssignmentNode): ExecResult {
    const value = this._expandWord(node.value);
    const capped = value.length > MAX_VAR_SIZE ? value.slice(0, MAX_VAR_SIZE) : value;
    this.env[node.name] = capped;
    return makeResult();
  }

  private _evalCase(node: CaseNode, stdin: string): ExecResult {
    const word = this._expandWord(node.word);
    for (const clause of node.clauses) {
      for (const pattern of clause.patterns) {
        const expanded = this._expandWord(pattern);
        if (expanded === "*" || this._globMatch(word, expanded)) {
          return this._eval(clause.body, stdin);
        }
      }
    }
    return makeResult();
  }

  private _globMatch(str: string, pattern: string): boolean {
    const regex = this._globToRegex(pattern);
    return regex.test(str);
  }

  private _evalArithmetic(expr: string): number {
    // Expand variables in the expression
    const expanded = expr.replace(/\$\{?(\w+)\}?/g, (_m, name) => {
      return this.env[name] ?? "0";
    }).replace(/([A-Za-z_]\w*)/g, (name) => {
      return this.env[name] ?? "0";
    });

    return this._parseArithExpr(expanded.trim());
  }

  private _parseArithExpr(expr: string): number {
    const tokens = this._tokenizeArith(expr);
    let pos = 0;

    const peek = (): string => tokens[pos] ?? "";
    const advance = (): string => tokens[pos++] ?? "";

    const parseExpr = (): number => parseTernary();

    const parseTernary = (): number => {
      let val = parseOr();
      if (peek() === "?") {
        advance();
        const truthy = parseExpr();
        if (peek() === ":") advance();
        const falsy = parseExpr();
        return val !== 0 ? truthy : falsy;
      }
      return val;
    };

    const parseOr = (): number => {
      let val = parseAnd();
      while (peek() === "||") { advance(); val = (val !== 0 || parseAnd() !== 0) ? 1 : 0; }
      return val;
    };

    const parseAnd = (): number => {
      let val = parseBitOr();
      while (peek() === "&&") { advance(); val = (val !== 0 && parseBitOr() !== 0) ? 1 : 0; }
      return val;
    };

    const parseBitOr = (): number => {
      let val = parseBitXor();
      while (peek() === "|") { advance(); val = val | parseBitXor(); }
      return val;
    };

    const parseBitXor = (): number => {
      let val = parseBitAnd();
      while (peek() === "^") { advance(); val = val ^ parseBitAnd(); }
      return val;
    };

    const parseBitAnd = (): number => {
      let val = parseEquality();
      while (peek() === "&") { advance(); val = val & parseEquality(); }
      return val;
    };

    const parseEquality = (): number => {
      let val = parseRelational();
      while (peek() === "==" || peek() === "!=") {
        const op = advance();
        const right = parseRelational();
        val = op === "==" ? (val === right ? 1 : 0) : (val !== right ? 1 : 0);
      }
      return val;
    };

    const parseRelational = (): number => {
      let val = parseShift();
      while (peek() === "<" || peek() === ">" || peek() === "<=" || peek() === ">=") {
        const op = advance();
        const right = parseShift();
        if (op === "<") val = val < right ? 1 : 0;
        else if (op === ">") val = val > right ? 1 : 0;
        else if (op === "<=") val = val <= right ? 1 : 0;
        else val = val >= right ? 1 : 0;
      }
      return val;
    };

    const parseShift = (): number => {
      let val = parseAdd();
      while (peek() === "<<" || peek() === ">>") {
        const op = advance();
        const right = parseAdd();
        val = op === "<<" ? val << right : val >> right;
      }
      return val;
    };

    const parseAdd = (): number => {
      let val = parseMul();
      while (peek() === "+" || peek() === "-") {
        const op = advance();
        const right = parseMul();
        val = op === "+" ? val + right : val - right;
      }
      return val;
    };

    const parseMul = (): number => {
      let val = parseUnary();
      while (peek() === "*" || peek() === "/" || peek() === "%") {
        const op = advance();
        const right = parseUnary();
        if (op === "*") val = val * right;
        else if (op === "/") { if (right === 0) throw new Error("division by zero"); val = Math.trunc(val / right); }
        else { if (right === 0) throw new Error("division by zero"); val = val % right; }
      }
      return val;
    };

    const parseUnary = (): number => {
      if (peek() === "-") { advance(); return -parseUnary(); }
      if (peek() === "+") { advance(); return parseUnary(); }
      if (peek() === "!") { advance(); return parseUnary() === 0 ? 1 : 0; }
      if (peek() === "~") { advance(); return ~parseUnary(); }
      return parsePrimary();
    };

    const parsePrimary = (): number => {
      if (peek() === "(") {
        advance();
        const val = parseExpr();
        if (peek() === ")") advance();
        return val;
      }
      const tok = advance();
      const n = parseInt(tok, 10);
      return isNaN(n) ? 0 : n;
    };

    return parseExpr();
  }

  private _tokenizeArith(expr: string): string[] {
    const tokens: string[] = [];
    let i = 0;
    while (i < expr.length) {
      if (expr[i] === " " || expr[i] === "\t") { i++; continue; }
      // Multi-char operators
      const two = expr.slice(i, i + 2);
      if (["||", "&&", "==", "!=", "<=", ">=", "<<", ">>"].includes(two)) {
        tokens.push(two); i += 2; continue;
      }
      // Single-char operators/parens
      if ("+-*/%()^&|<>!~?:".includes(expr[i])) {
        tokens.push(expr[i]); i++; continue;
      }
      // Numbers
      if (/\d/.test(expr[i])) {
        let num = "";
        while (i < expr.length && /\d/.test(expr[i])) { num += expr[i]; i++; }
        tokens.push(num); continue;
      }
      i++;
    }
    return tokens;
  }

  // -----------------------------------------------------------------------
  // Expansion
  // -----------------------------------------------------------------------

  private _expandArgs(args: string[]): string[] {
    const result: string[] = [];
    for (const arg of args) {
      const expanded = this._expandWord(arg);
      // Glob expansion could go here; for now just pass through
      result.push(expanded);
    }
    return result;
  }

  private _expandWord(s: string): string {
    let result = "";
    let i = 0;

    while (i < s.length) {
      const c = s[i];

      if (c === "'") {
        // Single quotes: no expansion, strip quotes
        i++;
        while (i < s.length && s[i] !== "'") {
          result += s[i]; i++;
        }
        if (i < s.length) i++; // skip closing '
        continue;
      }

      if (c === '"') {
        // Double quotes: expand variables/command subs, strip quotes
        i++;
        while (i < s.length && s[i] !== '"') {
          if (s[i] === "\\" && i + 1 < s.length && (s[i+1] === '"' || s[i+1] === '$' || s[i+1] === '\\' || s[i+1] === '`')) {
            result += s[i + 1]; i += 2;
          } else if (s[i] === "$") {
            const [val, consumed] = this._expandDollar(s, i);
            result += val;
            i += consumed;
          } else if (s[i] === "`") {
            const [val, consumed] = this._expandBacktick(s, i);
            result += val;
            i += consumed;
          } else {
            result += s[i]; i++;
          }
        }
        if (i < s.length) i++; // skip closing "
        continue;
      }

      if (c === "$") {
        const [val, consumed] = this._expandDollar(s, i);
        result += val;
        i += consumed;
        continue;
      }

      if (c === "`") {
        const [val, consumed] = this._expandBacktick(s, i);
        result += val;
        i += consumed;
        continue;
      }

      result += c; i++;
    }

    return result;
  }

  private _trackExpansion(): void {
    this._expansionCount++;
    if (this._expansionCount > MAX_EXPANSIONS) {
      throw new Error("Maximum expansion limit exceeded");
    }
  }

  private _expandDollar(s: string, i: number): [string, number] {
    this._trackExpansion();

    // $((...)) arithmetic expansion
    if (s[i + 1] === "(" && s[i + 2] === "(") {
      let depth = 2;
      let j = i + 3;
      while (j < s.length && depth > 0) {
        if (s[j] === "(" ) depth++;
        else if (s[j] === ")") depth--;
        j++;
      }
      // j is now past the last ), but we need to consume the outer )) pair
      const inner = s.slice(i + 3, j - 2);
      const val = String(this._evalArithmetic(inner));
      return [val, j - i];
    }

    // $(...) command substitution
    if (s[i + 1] === "(") {
      let depth = 1;
      let j = i + 2;
      while (j < s.length && depth > 0) {
        if (s[j] === "(") depth++;
        else if (s[j] === ")") depth--;
        j++;
      }
      const inner = s.slice(i + 2, j - 1);
      const val = this._commandSubstitution(inner);
      return [val, j - i];
    }

    // ${...} parameter expansion
    if (s[i + 1] === "{") {
      let depth = 1;
      let j = i + 2;
      while (j < s.length && depth > 0) {
        if (s[j] === "{") depth++;
        else if (s[j] === "}") depth--;
        j++;
      }
      const inner = s.slice(i + 2, j - 1);
      const val = this._expandBraceParam(inner);
      return [val, j - i];
    }

    // $? special
    if (s[i + 1] === "?") {
      return [this.env["?"] ?? "0", 2];
    }

    // $VAR
    let j = i + 1;
    while (j < s.length && /\w/.test(s[j])) j++;
    if (j === i + 1) return ["$", 1];
    const name = s.slice(i + 1, j);
    return [this.env[name] ?? "", j - i];
  }

  private _expandBacktick(s: string, i: number): [string, number] {
    let j = i + 1;
    while (j < s.length && s[j] !== "`") j++;
    const inner = s.slice(i + 1, j);
    const val = this._commandSubstitution(inner);
    return [val, j - i + 1];
  }

  private _commandSubstitution(cmd: string): string {
    if (this._cmdSubDepth >= 10) {
      throw new Error("Command substitution recursion depth exceeded");
    }
    this._cmdSubDepth++;
    try {
      const saved = this._iterationCounter;
      const result = this.exec(cmd);
      this._iterationCounter = saved;
      let out = result.stdout;
      // Strip trailing newline
      if (out.endsWith("\n")) out = out.slice(0, -1);
      return out;
    } finally {
      this._cmdSubDepth--;
    }
  }

  private _expandBraceParam(expr: string): string {
    // ${#var} — string length
    if (expr.startsWith("#")) {
      const name = expr.slice(1);
      return String((this.env[name] ?? "").length);
    }

    // ${var:offset:length} — substring
    const substringMatch = expr.match(/^(\w+):(-?\d+)(?::(\d+))?$/);
    if (substringMatch) {
      const val = this.env[substringMatch[1]] ?? "";
      let offset = parseInt(substringMatch[2], 10);
      if (offset < 0) offset = Math.max(0, val.length + offset);
      const len = substringMatch[3] !== undefined ? parseInt(substringMatch[3], 10) : undefined;
      return len !== undefined ? val.slice(offset, offset + len) : val.slice(offset);
    }

    // ${var:-default}
    const defaultMatch = expr.match(/^(\w+):-(.*)$/);
    if (defaultMatch) {
      const val = this.env[defaultMatch[1]];
      return (val !== undefined && val !== "") ? val : this._expandWord(defaultMatch[2]);
    }

    // ${var:=default}
    const assignDefaultMatch = expr.match(/^(\w+):=(.*)$/);
    if (assignDefaultMatch) {
      const val = this.env[assignDefaultMatch[1]];
      if (val !== undefined && val !== "") return val;
      const expanded = this._expandWord(assignDefaultMatch[2]);
      this.env[assignDefaultMatch[1]] = expanded;
      return expanded;
    }

    // ${var//pattern/replacement} — global substitution
    const globalSubMatch = expr.match(/^(\w+)\/\/([^/]*)\/(.*)$/);
    if (globalSubMatch) {
      const val = this.env[globalSubMatch[1]] ?? "";
      const pat = globalSubMatch[2];
      const repl = globalSubMatch[3];
      if (!pat) return val;
      return val.split(pat).join(repl);
    }

    // ${var/pattern/replacement} — first substitution
    const firstSubMatch = expr.match(/^(\w+)\/([^/]*)\/(.*)$/);
    if (firstSubMatch) {
      const val = this.env[firstSubMatch[1]] ?? "";
      const pat = firstSubMatch[2];
      const repl = firstSubMatch[3];
      if (!pat) return val;
      const idx = val.indexOf(pat);
      if (idx === -1) return val;
      return val.slice(0, idx) + repl + val.slice(idx + pat.length);
    }

    // ${var%%suffix} — greedy suffix removal
    const greedySuffixMatch = expr.match(/^(\w+)%%(.+)$/);
    if (greedySuffixMatch) {
      const val = this.env[greedySuffixMatch[1]] ?? "";
      const pat = greedySuffixMatch[2];
      return this._removePattern(val, pat, "suffix", true);
    }

    // ${var%suffix} — shortest suffix removal
    const shortSuffixMatch = expr.match(/^(\w+)%(.+)$/);
    if (shortSuffixMatch) {
      const val = this.env[shortSuffixMatch[1]] ?? "";
      const pat = shortSuffixMatch[2];
      return this._removePattern(val, pat, "suffix", false);
    }

    // ${var##prefix} — greedy prefix removal
    const greedyPrefixMatch = expr.match(/^(\w+)##(.+)$/);
    if (greedyPrefixMatch) {
      const val = this.env[greedyPrefixMatch[1]] ?? "";
      const pat = greedyPrefixMatch[2];
      return this._removePattern(val, pat, "prefix", true);
    }

    // ${var#prefix} — shortest prefix removal
    const shortPrefixMatch = expr.match(/^(\w+)#(.+)$/);
    if (shortPrefixMatch) {
      const val = this.env[shortPrefixMatch[1]] ?? "";
      const pat = shortPrefixMatch[2];
      return this._removePattern(val, pat, "prefix", false);
    }

    // Simple ${var}
    const nameMatch = expr.match(/^(\w+)$/);
    if (nameMatch) {
      return this.env[nameMatch[1]] ?? "";
    }

    return "";
  }

  private _removePattern(val: string, pattern: string, side: "prefix" | "suffix", greedy: boolean): string {
    const regex = this._globToRegex(pattern);
    if (side === "prefix") {
      if (greedy) {
        for (let i = val.length; i >= 0; i--) {
          if (regex.test(val.slice(0, i))) return val.slice(i);
        }
      } else {
        for (let i = 0; i <= val.length; i++) {
          if (regex.test(val.slice(0, i))) return val.slice(i);
        }
      }
    } else {
      if (greedy) {
        for (let i = 0; i <= val.length; i++) {
          if (regex.test(val.slice(i))) return val.slice(0, i);
        }
      } else {
        for (let i = val.length; i >= 0; i--) {
          if (regex.test(val.slice(i))) return val.slice(0, i);
        }
      }
    }
    return val;
  }

  private _globToRegex(pattern: string): RegExp {
    let reg = "^";
    for (const c of pattern) {
      if (c === "*") reg += ".*";
      else if (c === "?") reg += ".";
      else reg += c.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    }
    reg += "$";
    return new RegExp(reg);
  }

  // -----------------------------------------------------------------------
  // Builtins: test, [, printf, export, true, false
  // -----------------------------------------------------------------------

  private _cmdTest(args: string[], _stdin: string): ExecResult {
    return makeResult("", "", this._evalTest(args) ? 0 : 1);
  }

  private _cmdBracket(args: string[], _stdin: string): ExecResult {
    if (args.length === 0 || args[args.length - 1] !== "]") {
      return makeResult("", "[: missing ']'\n", 2);
    }
    return makeResult("", "", this._evalTest(args.slice(0, -1)) ? 0 : 1);
  }

  private _cmdDoubleBracket(args: string[], _stdin: string): ExecResult {
    if (args.length === 0 || args[args.length - 1] !== "]]") {
      return makeResult("", "[[: missing ']]'\n", 2);
    }
    return makeResult("", "", this._evalTest(args.slice(0, -1)) ? 0 : 1);
  }

  private _evalTest(args: string[]): boolean {
    if (args.length === 0) return false;

    // Negation
    if (args[0] === "!") {
      return !this._evalTest(args.slice(1));
    }

    // Unary file tests
    if (args.length === 2) {
      const op = args[0];
      const operand = args[1];
      if (op === "-f") {
        const p = this._resolve(operand);
        return this.fs.exists(p) && !this.fs._isDir(p);
      }
      if (op === "-d") {
        const p = this._resolve(operand);
        return this.fs._isDir(p);
      }
      if (op === "-e") {
        const p = this._resolve(operand);
        return this.fs.exists(p) || this.fs._isDir(p);
      }
      if (op === "-z") return operand.length === 0;
      if (op === "-n") return operand.length > 0;
    }

    // Single arg: true if non-empty
    if (args.length === 1) {
      return args[0].length > 0;
    }

    // Binary operations
    if (args.length === 3) {
      const [left, op, right] = args;
      if (op === "=") return left === right;
      if (op === "!=") return left !== right;
      if (op === "-eq") return parseInt(left, 10) === parseInt(right, 10);
      if (op === "-ne") return parseInt(left, 10) !== parseInt(right, 10);
      if (op === "-lt") return parseInt(left, 10) < parseInt(right, 10);
      if (op === "-gt") return parseInt(left, 10) > parseInt(right, 10);
      if (op === "-le") return parseInt(left, 10) <= parseInt(right, 10);
      if (op === "-ge") return parseInt(left, 10) >= parseInt(right, 10);
    }

    return false;
  }

  private _cmdPrintf(args: string[], _stdin: string): ExecResult {
    if (args.length === 0) return makeResult();
    const format = args[0];
    const fmtArgs = args.slice(1);
    let argIdx = 0;
    let result = "";
    let i = 0;

    while (i < format.length) {
      if (format[i] === "\\") {
        i++;
        if (i < format.length) {
          if (format[i] === "n") { result += "\n"; i++; }
          else if (format[i] === "t") { result += "\t"; i++; }
          else if (format[i] === "\\") { result += "\\"; i++; }
          else { result += format[i]; i++; }
        }
        continue;
      }

      if (format[i] === "%" && i + 1 < format.length) {
        i++;
        if (format[i] === "%") {
          result += "%";
          i++;
          continue;
        }
        // Parse format spec
        let spec = "";
        while (i < format.length && /[\d.\-]/.test(format[i])) {
          spec += format[i]; i++;
        }
        if (i < format.length) {
          const type = format[i]; i++;
          const arg = fmtArgs[argIdx] ?? "";
          argIdx++;

          if (type === "s") {
            result += arg;
          } else if (type === "d") {
            result += String(parseInt(arg, 10) || 0);
          } else if (type === "f") {
            const num = parseFloat(arg) || 0;
            const precMatch = spec.match(/\.(\d+)/);
            const prec = precMatch ? parseInt(precMatch[1], 10) : 6;
            result += num.toFixed(prec);
          } else {
            result += arg;
          }
        }
        continue;
      }

      result += format[i]; i++;
    }

    return makeResult(result);
  }

  private _cmdExport(args: string[], _stdin: string): ExecResult {
    for (const arg of args) {
      const m = arg.match(/^([A-Za-z_]\w*)=(.*)$/);
      if (m) {
        this.env[m[1]] = this._expandWord(m[2]);
      }
    }
    return makeResult();
  }

  private _cmdTrue(_args: string[], _stdin: string): ExecResult {
    return makeResult("", "", 0);
  }

  private _cmdFalse(_args: string[], _stdin: string): ExecResult {
    return makeResult("", "", 1);
  }

  // -----------------------------------------------------------------------
  // Original 23 built-in commands (preserved exactly)
  // -----------------------------------------------------------------------

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
      if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
      for (let idx = 0; idx < lines.length; idx++) {
        const line = lines[idx];
        let match = regex.test(line);
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
