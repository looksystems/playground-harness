"""Virtual shell interpreter over a VirtualFS.
Tokenizer + recursive-descent parser + AST evaluator.
"""

from __future__ import annotations

import json
import os
import re
from dataclasses import dataclass, field
from typing import Any, Callable

from src.python.virtual_fs import VirtualFS


@dataclass
class ExecResult:
    stdout: str = ""
    stderr: str = ""
    exit_code: int = 0


# ---------------------------------------------------------------------------
# Token types
# ---------------------------------------------------------------------------

KEYWORDS = {"if", "then", "elif", "else", "fi", "for", "in", "do", "done", "while", "case", "esac"}


@dataclass
class Token:
    type: str  # WORD, PIPE, SEMI, AND, OR, REDIRECT_OUT, REDIRECT_APPEND, LPAREN, RPAREN, NEWLINE, EOF
    value: str


# ---------------------------------------------------------------------------
# Tokenizer
# ---------------------------------------------------------------------------

def tokenize(input_str: str) -> list[Token]:
    tokens: list[Token] = []
    i = 0
    n = len(input_str)

    while i < n:
        c = input_str[i]

        if c in (" ", "\t"):
            i += 1
            continue

        if c == "\n":
            tokens.append(Token("NEWLINE", "\n"))
            i += 1
            continue

        if c == ";" and i + 1 < n and input_str[i + 1] == ";":
            tokens.append(Token("DSEMI", ";;"))
            i += 2
            continue

        if c == ";":
            tokens.append(Token("SEMI", ";"))
            i += 1
            continue

        if c == "|" and i + 1 < n and input_str[i + 1] == "|":
            tokens.append(Token("OR", "||"))
            i += 2
            continue

        if c == "|":
            tokens.append(Token("PIPE", "|"))
            i += 1
            continue

        if c == "&" and i + 1 < n and input_str[i + 1] == "&":
            tokens.append(Token("AND", "&&"))
            i += 2
            continue

        if c == "&" and i + 1 < n and input_str[i + 1] == ">":
            tokens.append(Token("REDIRECT_BOTH_OUT", "&>"))
            i += 2
            continue

        if c == ">" and i + 1 < n and input_str[i + 1] == ">":
            tokens.append(Token("REDIRECT_APPEND", ">>"))
            i += 2
            continue

        if c == ">" and i + 1 < n and input_str[i + 1] == "&":
            tokens.append(Token("REDIRECT_BOTH_OUT", ">&"))
            i += 2
            continue

        if c == ">":
            tokens.append(Token("REDIRECT_OUT", ">"))
            i += 1
            continue

        if c == "<":
            tokens.append(Token("REDIRECT_IN", "<"))
            i += 1
            continue

        if c == "(":
            tokens.append(Token("LPAREN", "("))
            i += 1
            continue

        if c == ")":
            tokens.append(Token("RPAREN", ")"))
            i += 1
            continue

        # Comment
        if c == "#":
            while i < n and input_str[i] != "\n":
                i += 1
            continue

        if c == "2" and i + 1 < n and input_str[i + 1] == ">":
            prev = input_str[i - 1] if i > 0 else " "
            if prev in " \t\n|;&()":
                if i + 2 < n and input_str[i + 2] == ">":  # 2>>
                    tokens.append(Token("REDIRECT_ERR_APPEND", "2>>"))
                    i += 3
                    continue
                if i + 2 < n and input_str[i + 2] == "&" and i + 3 < n and input_str[i + 3] == "1":  # 2>&1
                    tokens.append(Token("REDIRECT_ERR_DUP", "2>&1"))
                    i += 4
                    continue
                # plain 2>
                tokens.append(Token("REDIRECT_ERR_OUT", "2>"))
                i += 2
                continue

        # Word (includes quoted strings, $(...), `...`, ${...}, $VAR)
        word = ""
        while i < n:
            ch = input_str[i]

            if ch == "'":
                word += ch
                i += 1
                while i < n and input_str[i] != "'":
                    word += input_str[i]
                    i += 1
                if i < n:
                    word += input_str[i]
                    i += 1
                continue

            if ch == '"':
                word += ch
                i += 1
                while i < n and input_str[i] != '"':
                    if input_str[i] == "\\" and i + 1 < n:
                        word += input_str[i] + input_str[i + 1]
                        i += 2
                    else:
                        word += input_str[i]
                        i += 1
                if i < n:
                    word += input_str[i]
                    i += 1
                continue

            if ch == "$" and i + 1 < n and input_str[i + 1] == "(":
                # Command substitution $(...)
                word += "$("
                i += 2
                depth = 1
                while i < n and depth > 0:
                    if input_str[i] == "(":
                        depth += 1
                    elif input_str[i] == ")":
                        depth -= 1
                        if depth == 0:
                            break
                    elif input_str[i] == "'":
                        word += input_str[i]
                        i += 1
                        while i < n and input_str[i] != "'":
                            word += input_str[i]
                            i += 1
                        if i < n:
                            word += input_str[i]
                            i += 1
                        continue
                    elif input_str[i] == '"':
                        word += input_str[i]
                        i += 1
                        while i < n and input_str[i] != '"':
                            if input_str[i] == "\\" and i + 1 < n:
                                word += input_str[i] + input_str[i + 1]
                                i += 2
                            else:
                                word += input_str[i]
                                i += 1
                        if i < n:
                            word += input_str[i]
                            i += 1
                        continue
                    word += input_str[i]
                    i += 1
                word += ")"
                if i < n:
                    i += 1  # skip closing )
                continue

            if ch == "$" and i + 1 < n and input_str[i + 1] == "{":
                # Parameter expansion ${...}
                word += "${"
                i += 2
                depth = 1
                while i < n and depth > 0:
                    if input_str[i] == "{":
                        depth += 1
                    elif input_str[i] == "}":
                        depth -= 1
                        if depth == 0:
                            break
                    word += input_str[i]
                    i += 1
                word += "}"
                if i < n:
                    i += 1  # skip closing }
                continue

            if ch == "`":
                # Backtick command substitution
                word += "`"
                i += 1
                while i < n and input_str[i] != "`":
                    word += input_str[i]
                    i += 1
                word += "`"
                if i < n:
                    i += 1
                continue

            if ch == "\\":
                # Escape next character
                if i + 1 < n:
                    word += input_str[i + 1]
                    i += 2
                else:
                    i += 1
                continue

            # Break on meta-characters
            if ch in (" ", "\t", "\n", "|", ";", "&", ">", "<", "(", ")", "#"):
                break

            word += ch
            i += 1

        if word:
            tokens.append(Token("WORD", word))

    tokens.append(Token("EOF", ""))
    return tokens


# ---------------------------------------------------------------------------
# AST node types
# ---------------------------------------------------------------------------

@dataclass
class Redirect:
    mode: str  # "write", "append", or "read"
    target: str
    fd: int = 1       # which fd to redirect (1=stdout, 2=stderr)
    dup_target: int = 0  # for 2>&1: dup stderr to stdout
    both: bool = False   # for &>: redirect both streams


@dataclass
class CommandNode:
    type: str = "command"
    args: list[str] = field(default_factory=list)
    redirects: list[Redirect] = field(default_factory=list)


@dataclass
class PipelineNode:
    type: str = "pipeline"
    commands: list[Any] = field(default_factory=list)


@dataclass
class ListNode:
    type: str = "list"
    commands: list[Any] = field(default_factory=list)


@dataclass
class AndNode:
    type: str = "and"
    left: Any = None
    right: Any = None


@dataclass
class OrNode:
    type: str = "or"
    left: Any = None
    right: Any = None


@dataclass
class IfNode:
    type: str = "if"
    clauses: list[dict[str, Any]] = field(default_factory=list)
    else_body: Any = None


@dataclass
class ForNode:
    type: str = "for"
    variable: str = ""
    words: list[str] = field(default_factory=list)
    body: Any = None


@dataclass
class WhileNode:
    type: str = "while"
    condition: Any = None
    body: Any = None


@dataclass
class AssignmentNode:
    type: str = "assignment"
    name: str = ""
    value: str = ""


@dataclass
class CaseNode:
    type: str = "case"
    word: str = ""
    clauses: list[dict[str, Any]] = field(default_factory=list)


ASTNode = CommandNode | PipelineNode | ListNode | AndNode | OrNode | IfNode | ForNode | WhileNode | AssignmentNode | CaseNode


# ---------------------------------------------------------------------------
# Parser
# ---------------------------------------------------------------------------

class Parser:
    def __init__(self, tokens: list[Token]):
        self._tokens = tokens
        self._pos = 0
        self._depth = 0

    def _peek(self) -> Token:
        if self._pos < len(self._tokens):
            return self._tokens[self._pos]
        return Token("EOF", "")

    def _advance(self) -> Token:
        t = self._tokens[self._pos]
        self._pos += 1
        return t

    def _expect(self, token_type: str) -> Token:
        t = self._peek()
        if t.type != token_type:
            raise RuntimeError(f"Expected {token_type} but got {t.type} ({t.value})")
        return self._advance()

    def _expect_keyword(self, kw: str) -> None:
        t = self._peek()
        if t.type != "WORD" or t.value != kw:
            raise RuntimeError(f"Expected '{kw}' but got '{t.value}'")
        self._advance()

    def _at_end(self) -> bool:
        return self._peek().type == "EOF"

    def _skip_newlines(self) -> None:
        while self._peek().type == "NEWLINE":
            self._advance()

    def _skip_semi_newlines(self) -> None:
        while self._peek().type in ("NEWLINE", "SEMI"):
            self._advance()

    def _is_command_terminator(self) -> bool:
        t = self._peek()
        if t.type in ("EOF", "SEMI", "DSEMI", "NEWLINE", "AND", "OR", "PIPE", "RPAREN"):
            return True
        if t.type == "WORD" and t.value in ("then", "elif", "else", "fi", "do", "done", "esac"):
            return True
        return False

    def _is_compound_end(self) -> bool:
        t = self._peek()
        return t.type == "WORD" and t.value in ("fi", "done", "then", "elif", "else", "do", "esac")

    def parse(self) -> ASTNode:
        self._depth += 1
        if self._depth > 50:
            raise RuntimeError("Nesting depth limit exceeded")
        node = self._parse_list()
        self._depth -= 1
        return node

    def _parse_list(self) -> ASTNode:
        self._skip_semi_newlines()
        if self._at_end():
            return CommandNode()

        nodes: list[ASTNode] = []
        nodes.append(self._parse_and_or())

        while self._peek().type in ("SEMI", "NEWLINE"):
            self._skip_semi_newlines()
            if self._at_end() or self._is_compound_end():
                break
            nodes.append(self._parse_and_or())

        if len(nodes) == 1:
            return nodes[0]
        return ListNode(commands=nodes)

    def _parse_and_or(self) -> ASTNode:
        left = self._parse_pipeline()

        while True:
            t = self._peek()
            if t.type == "AND":
                self._advance()
                self._skip_newlines()
                right = self._parse_pipeline()
                left = AndNode(left=left, right=right)
            elif t.type == "OR":
                self._advance()
                self._skip_newlines()
                right = self._parse_pipeline()
                left = OrNode(left=left, right=right)
            else:
                break
        return left

    def _parse_pipeline(self) -> ASTNode:
        commands: list[ASTNode] = []
        commands.append(self._parse_command())

        while self._peek().type == "PIPE":
            self._advance()
            self._skip_newlines()
            commands.append(self._parse_command())

        if len(commands) == 1:
            return commands[0]
        return PipelineNode(commands=commands)

    def _parse_command(self) -> ASTNode:
        t = self._peek()
        if t.type == "WORD":
            if t.value == "if":
                return self._parse_if()
            if t.value == "for":
                return self._parse_for()
            if t.value == "while":
                return self._parse_while()
            if t.value == "case":
                return self._parse_case()
        return self._parse_simple_command()

    def _parse_simple_command(self) -> ASTNode:
        args: list[str] = []
        redirects: list[Redirect] = []

        while not self._is_command_terminator():
            t = self._peek()

            if t.type == "REDIRECT_APPEND":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="append", target=target.value))
            elif t.type == "REDIRECT_OUT":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="write", target=target.value))
            elif t.type == "REDIRECT_IN":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="read", target=target.value))
            elif t.type == "REDIRECT_ERR_OUT":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="write", target=target.value, fd=2))
            elif t.type == "REDIRECT_ERR_APPEND":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="append", target=target.value, fd=2))
            elif t.type == "REDIRECT_ERR_DUP":
                self._advance()
                redirects.append(Redirect(mode="write", target="", dup_target=1))
            elif t.type == "REDIRECT_BOTH_OUT":
                self._advance()
                target = self._expect("WORD")
                redirects.append(Redirect(mode="write", target=target.value, both=True))
            elif t.type == "WORD":
                args.append(t.value)
                self._advance()
            else:
                break

        # Check for assignment: first arg matches VAR=value pattern
        if args and not redirects:
            assign_idx = 0
            # Handle `export VAR=value`
            if args[0] == "export" and len(args) >= 2:
                assign_idx = 1
            if assign_idx < len(args):
                m = re.match(r"^([A-Za-z_]\w*)=(.*)$", args[assign_idx])
                if m and (assign_idx == 0 or args[0] == "export"):
                    if assign_idx == 1 and len(args) == 2:
                        return AssignmentNode(name=m.group(1), value=m.group(2))
                    if assign_idx == 0 and len(args) == 1:
                        return AssignmentNode(name=m.group(1), value=m.group(2))

        return CommandNode(args=args, redirects=redirects)

    def _parse_if(self) -> ASTNode:
        self._expect_keyword("if")
        self._skip_semi_newlines()
        clauses: list[dict[str, Any]] = []

        condition = self._parse_list()
        self._skip_semi_newlines()
        self._expect_keyword("then")
        self._skip_semi_newlines()
        body = self._parse_list()
        clauses.append({"condition": condition, "body": body})

        while self._peek().type == "WORD" and self._peek().value == "elif":
            self._advance()
            self._skip_semi_newlines()
            elif_cond = self._parse_list()
            self._skip_semi_newlines()
            self._expect_keyword("then")
            self._skip_semi_newlines()
            elif_body = self._parse_list()
            clauses.append({"condition": elif_cond, "body": elif_body})

        else_body = None
        if self._peek().type == "WORD" and self._peek().value == "else":
            self._advance()
            self._skip_semi_newlines()
            else_body = self._parse_list()

        self._skip_semi_newlines()
        self._expect_keyword("fi")

        return IfNode(clauses=clauses, else_body=else_body)

    def _parse_for(self) -> ASTNode:
        self._expect_keyword("for")
        var_token = self._expect("WORD")
        variable = var_token.value

        self._skip_semi_newlines()
        words: list[str] = []
        if self._peek().type == "WORD" and self._peek().value == "in":
            self._advance()
            while self._peek().type == "WORD" and not self._is_compound_end():
                words.append(self._advance().value)

        self._skip_semi_newlines()
        self._expect_keyword("do")
        self._skip_semi_newlines()
        body = self._parse_list()
        self._skip_semi_newlines()
        self._expect_keyword("done")

        return ForNode(variable=variable, words=words, body=body)

    def _parse_while(self) -> ASTNode:
        self._expect_keyword("while")
        self._skip_semi_newlines()
        condition = self._parse_list()
        self._skip_semi_newlines()
        self._expect_keyword("do")
        self._skip_semi_newlines()
        body = self._parse_list()
        self._skip_semi_newlines()
        self._expect_keyword("done")

        return WhileNode(condition=condition, body=body)

    def _parse_case(self) -> ASTNode:
        self._expect_keyword("case")
        word_token = self._expect("WORD")
        word = word_token.value
        self._skip_semi_newlines()
        self._expect_keyword("in")
        self._skip_semi_newlines()

        clauses: list[dict[str, Any]] = []

        while not (self._peek().type == "WORD" and self._peek().value == "esac") and self._peek().type != "EOF":
            # Parse patterns: pattern1 | pattern2 )
            patterns: list[str] = []
            # Skip optional leading (
            if self._peek().type == "LPAREN":
                self._advance()

            patterns.append(self._expect("WORD").value)
            while self._peek().type == "PIPE":
                self._advance()
                patterns.append(self._expect("WORD").value)
            # Expect )
            if self._peek().type == "RPAREN":
                self._advance()
            else:
                raise RuntimeError(f"Expected ')' in case clause but got '{self._peek().value}'")
            self._skip_semi_newlines()

            # Parse body until ;;
            body = self._parse_list()
            clauses.append({"patterns": patterns, "body": body})

            # Expect ;;
            if self._peek().type == "DSEMI":
                self._advance()
            self._skip_semi_newlines()

        self._expect_keyword("esac")
        return CaseNode(word=word, clauses=clauses)


# ---------------------------------------------------------------------------
# Shell
# ---------------------------------------------------------------------------

MAX_VAR_SIZE = 64 * 1024
MAX_EXPANSIONS = 1_000


class Shell:
    """
    Shell interpreter over a VirtualFS.
    Tokenizer + recursive-descent parser + AST evaluator.
    Supports pipes, redirects, &&, ||, if/for/while, command substitution,
    parameter expansion, and core Unix commands.
    """

    def __init__(
        self,
        fs: VirtualFS | None = None,
        cwd: str = "/",
        env: dict[str, str] | None = None,
        allowed_commands: set[str] | None = None,
        max_output: int = 16_000,
        max_iterations: int = 10_000,
    ):
        self.fs = fs if fs is not None else VirtualFS()
        self.cwd = cwd
        self.env = env if env is not None else {}
        self.max_output = max_output
        self.max_iterations = max_iterations
        self._allowed_commands = allowed_commands
        self._iteration_counter = 0
        self._cmd_sub_depth = 0
        self._expansion_count = 0
        self.on_not_found: Callable | None = None

        all_builtins: list[tuple[str, Callable]] = [
            ("cat", self._cmd_cat),
            ("echo", self._cmd_echo),
            ("find", self._cmd_find),
            ("grep", self._cmd_grep),
            ("head", self._cmd_head),
            ("ls", self._cmd_ls),
            ("pwd", self._cmd_pwd),
            ("sort", self._cmd_sort),
            ("tail", self._cmd_tail),
            ("tee", self._cmd_tee),
            ("touch", self._cmd_touch),
            ("tree", self._cmd_tree),
            ("uniq", self._cmd_uniq),
            ("wc", self._cmd_wc),
            ("mkdir", self._cmd_mkdir),
            ("cp", self._cmd_cp),
            ("rm", self._cmd_rm),
            ("stat", self._cmd_stat),
            ("cut", self._cmd_cut),
            ("tr", self._cmd_tr),
            ("sed", self._cmd_sed),
            ("jq", self._cmd_jq),
            ("cd", self._cmd_cd),
            ("test", self._cmd_test),
            ("[", self._cmd_bracket),
            ("[[", self._cmd_double_bracket),
            ("printf", self._cmd_printf),
            ("export", self._cmd_export),
            ("true", self._cmd_true),
            ("false", self._cmd_false),
        ]

        if allowed_commands is not None:
            self._builtins: dict[str, Callable] = {
                k: v for k, v in all_builtins if k in allowed_commands
            }
        else:
            self._builtins = dict(all_builtins)
        self._builtin_names: set[str] = {k for k, _ in all_builtins}
        self._custom_commands: dict[str, Callable] = {}

    def register_command(self, name: str, handler: Callable) -> None:
        self._custom_commands[name] = handler
        self._builtins[name] = handler
        if self._allowed_commands is not None:
            self._allowed_commands.add(name)

    def unregister_command(self, name: str) -> None:
        if name in self._builtin_names:
            raise ValueError(f"Cannot unregister built-in command: {name}")
        self._custom_commands.pop(name, None)
        self._builtins.pop(name, None)
        if self._allowed_commands is not None:
            self._allowed_commands.discard(name)

    def clone(self) -> Shell:
        cloned = Shell(
            fs=self.fs.clone(),
            cwd=self.cwd,
            env=dict(self.env),
            allowed_commands=set(self._allowed_commands) if self._allowed_commands is not None else None,
            max_output=self.max_output,
            max_iterations=self.max_iterations,
        )
        for name, handler in self._custom_commands.items():
            cloned.register_command(name, handler)
        return cloned

    def _resolve(self, path: str) -> str:
        if path.startswith("/"):
            return os.path.normpath(path)
        return os.path.normpath(os.path.join(self.cwd, path))

    def exec(self, command: str) -> ExecResult:
        """Execute a command string."""
        command = command.strip()
        if not command:
            return ExecResult()

        try:
            tokens = tokenize(command)
            parser = Parser(tokens)
            ast = parser.parse()
        except Exception as e:
            return ExecResult(stderr=f"parse error: {e}\n", exit_code=2)

        self._iteration_counter = 0
        self._expansion_count = 0

        try:
            result = self._eval(ast, "")
        except Exception as e:
            return ExecResult(stderr=f"{e}\n", exit_code=1)

        if len(result.stdout) > self.max_output:
            result = ExecResult(
                stdout=result.stdout[: self.max_output]
                + f"\n... [truncated, {len(result.stdout)} total chars]",
                stderr=result.stderr,
                exit_code=result.exit_code,
            )

        return result

    # -----------------------------------------------------------------------
    # AST evaluator
    # -----------------------------------------------------------------------

    def _eval(self, node: ASTNode, stdin: str) -> ExecResult:
        if isinstance(node, CommandNode):
            return self._eval_command(node, stdin)
        if isinstance(node, PipelineNode):
            return self._eval_pipeline(node, stdin)
        if isinstance(node, ListNode):
            return self._eval_list(node, stdin)
        if isinstance(node, AndNode):
            return self._eval_and(node, stdin)
        if isinstance(node, OrNode):
            return self._eval_or(node, stdin)
        if isinstance(node, IfNode):
            return self._eval_if(node, stdin)
        if isinstance(node, ForNode):
            return self._eval_for(node, stdin)
        if isinstance(node, WhileNode):
            return self._eval_while(node, stdin)
        if isinstance(node, AssignmentNode):
            return self._eval_assignment(node)
        if isinstance(node, CaseNode):
            return self._eval_case(node, stdin)
        return ExecResult()  # pragma: no cover

    def _eval_command(self, node: CommandNode, stdin: str) -> ExecResult:
        if not node.args and not node.redirects:
            return ExecResult()

        expanded = self._expand_args(node.args)
        if not expanded:
            return ExecResult()

        cmd_name = expanded[0]
        args = expanded[1:]

        handler = self._builtins.get(cmd_name)
        if handler is None:
            self.env["?"] = "127"
            if self.on_not_found is not None:
                self.on_not_found(cmd_name)
            return ExecResult(stderr=f"{cmd_name}: command not found\n", exit_code=127)

        # Process input redirects before calling handler
        effective_stdin = stdin
        for redir in node.redirects:
            if redir.mode == "read":
                target = self._expand_word(redir.target)
                path = self._resolve(target)
                if not self.fs.exists(path):
                    self.env["?"] = "1"
                    return ExecResult(stderr=f"{target}: No such file or directory\n", exit_code=1)
                effective_stdin = self.fs.read_text(path)

        result = handler(args, effective_stdin)
        self.env["?"] = str(result.exit_code)

        for redir in node.redirects:
            if redir.mode == "read":
                continue

            if redir.dup_target == 1:
                # 2>&1: merge stderr into stdout
                result = ExecResult(stdout=result.stdout + result.stderr, exit_code=result.exit_code)
                continue

            target = self._expand_word(redir.target)
            path = self._resolve(target)

            if redir.both:
                # &>: write both streams to file
                content = result.stdout + result.stderr
                if redir.mode == "append" and self.fs.exists(path):
                    existing = self.fs.read_text(path)
                    self.fs.write(path, existing + content)
                else:
                    self.fs.write(path, content)
                result = ExecResult(exit_code=result.exit_code)
                continue

            # Normal fd-targeted redirect
            fd = redir.fd
            stream = result.stderr if fd == 2 else result.stdout
            if redir.mode == "append" and self.fs.exists(path):
                existing = self.fs.read_text(path)
                self.fs.write(path, existing + stream)
            else:
                self.fs.write(path, stream)
            if fd == 2:
                result = ExecResult(stdout=result.stdout, exit_code=result.exit_code)
            else:
                result = ExecResult(stderr=result.stderr, exit_code=result.exit_code)

        return result

    def _eval_pipeline(self, node: PipelineNode, stdin: str) -> ExecResult:
        current_stdin = stdin
        last_result = ExecResult()
        all_stderr = ""

        for cmd in node.commands:
            last_result = self._eval(cmd, current_stdin)
            all_stderr += last_result.stderr
            current_stdin = last_result.stdout

        self.env["?"] = str(last_result.exit_code)
        return ExecResult(stdout=last_result.stdout, stderr=all_stderr, exit_code=last_result.exit_code)

    def _eval_list(self, node: ListNode, stdin: str) -> ExecResult:
        results: list[ExecResult] = []
        for cmd in node.commands:
            r = self._eval(cmd, stdin)
            results.append(r)
        return ExecResult(
            stdout="".join(r.stdout for r in results),
            stderr="".join(r.stderr for r in results),
            exit_code=results[-1].exit_code if results else 0,
        )

    def _eval_and(self, node: AndNode, stdin: str) -> ExecResult:
        left = self._eval(node.left, stdin)
        if left.exit_code != 0:
            return left
        right = self._eval(node.right, stdin)
        return ExecResult(
            stdout=left.stdout + right.stdout,
            stderr=left.stderr + right.stderr,
            exit_code=right.exit_code,
        )

    def _eval_or(self, node: OrNode, stdin: str) -> ExecResult:
        left = self._eval(node.left, stdin)
        if left.exit_code == 0:
            return left
        right = self._eval(node.right, stdin)
        return ExecResult(
            stdout=left.stdout + right.stdout,
            stderr=left.stderr + right.stderr,
            exit_code=right.exit_code,
        )

    def _eval_if(self, node: IfNode, stdin: str) -> ExecResult:
        for clause in node.clauses:
            cond_result = self._eval(clause["condition"], stdin)
            if cond_result.exit_code == 0:
                body_result = self._eval(clause["body"], stdin)
                return ExecResult(
                    stdout=cond_result.stdout + body_result.stdout,
                    stderr=cond_result.stderr + body_result.stderr,
                    exit_code=body_result.exit_code,
                )
        if node.else_body:
            return self._eval(node.else_body, stdin)
        return ExecResult()

    def _eval_for(self, node: ForNode, stdin: str) -> ExecResult:
        words: list[str] = []
        for w in node.words:
            expanded = self._expand_word(w)
            words.extend(s for s in expanded.split() if s)

        stdout = ""
        stderr = ""
        exit_code = 0

        for word in words:
            self._iteration_counter += 1
            if self._iteration_counter > self.max_iterations:
                return ExecResult(
                    stdout=stdout,
                    stderr=stderr + "Maximum iteration limit exceeded\n",
                    exit_code=1,
                )
            self.env[node.variable] = word
            r = self._eval(node.body, stdin)
            stdout += r.stdout
            stderr += r.stderr
            exit_code = r.exit_code

        return ExecResult(stdout=stdout, stderr=stderr, exit_code=exit_code)

    def _eval_while(self, node: WhileNode, stdin: str) -> ExecResult:
        stdout = ""
        stderr = ""
        exit_code = 0

        while True:
            self._iteration_counter += 1
            if self._iteration_counter > self.max_iterations:
                return ExecResult(
                    stdout=stdout,
                    stderr=stderr + "Maximum iteration limit exceeded\n",
                    exit_code=1,
                )
            cond_result = self._eval(node.condition, stdin)
            if cond_result.exit_code != 0:
                break
            body_result = self._eval(node.body, stdin)
            stdout += body_result.stdout
            stderr += body_result.stderr
            exit_code = body_result.exit_code

        return ExecResult(stdout=stdout, stderr=stderr, exit_code=exit_code)

    def _eval_assignment(self, node: AssignmentNode) -> ExecResult:
        value = self._expand_word(node.value)
        if len(value) > MAX_VAR_SIZE:
            value = value[:MAX_VAR_SIZE]
        self.env[node.name] = value
        return ExecResult()

    def _eval_case(self, node: CaseNode, stdin: str) -> ExecResult:
        word = self._expand_word(node.word)
        for clause in node.clauses:
            for pattern in clause["patterns"]:
                expanded = self._expand_word(pattern)
                if expanded == "*" or self._glob_match(word, expanded):
                    return self._eval(clause["body"], stdin)
        return ExecResult()

    def _glob_match(self, s: str, pattern: str) -> bool:
        regex = self._glob_to_regex(pattern)
        return bool(re.match(regex, s))

    def _eval_arithmetic(self, expr: str) -> int:
        # Expand variables
        def replace_var(m: re.Match) -> str:
            name = m.group(1) or m.group(0)
            return self.env.get(name, "0")

        expanded = re.sub(r"\$\{?(\w+)\}?", replace_var, expr)
        expanded = re.sub(r"[A-Za-z_]\w*", lambda m: self.env.get(m.group(0), "0"), expanded)
        return self._parse_arith_expr(expanded.strip())

    def _parse_arith_expr(self, expr: str) -> int:
        tokens = self._tokenize_arith(expr)
        pos = [0]

        def peek() -> str:
            return tokens[pos[0]] if pos[0] < len(tokens) else ""

        def advance() -> str:
            t = tokens[pos[0]] if pos[0] < len(tokens) else ""
            pos[0] += 1
            return t

        def parse_expr() -> int:
            return parse_ternary()

        def parse_ternary() -> int:
            val = parse_or()
            if peek() == "?":
                advance()
                truthy = parse_expr()
                if peek() == ":":
                    advance()
                falsy = parse_expr()
                return truthy if val != 0 else falsy
            return val

        def parse_or() -> int:
            val = parse_and()
            while peek() == "||":
                advance()
                val = 1 if (val != 0 or parse_and() != 0) else 0
            return val

        def parse_and() -> int:
            val = parse_bit_or()
            while peek() == "&&":
                advance()
                val = 1 if (val != 0 and parse_bit_or() != 0) else 0
            return val

        def parse_bit_or() -> int:
            val = parse_bit_xor()
            while peek() == "|":
                advance()
                val = val | parse_bit_xor()
            return val

        def parse_bit_xor() -> int:
            val = parse_bit_and()
            while peek() == "^":
                advance()
                val = val ^ parse_bit_and()
            return val

        def parse_bit_and() -> int:
            val = parse_equality()
            while peek() == "&":
                advance()
                val = val & parse_equality()
            return val

        def parse_equality() -> int:
            val = parse_relational()
            while peek() in ("==", "!="):
                op = advance()
                right = parse_relational()
                val = (1 if val == right else 0) if op == "==" else (1 if val != right else 0)
            return val

        def parse_relational() -> int:
            val = parse_shift()
            while peek() in ("<", ">", "<=", ">="):
                op = advance()
                right = parse_shift()
                if op == "<":
                    val = 1 if val < right else 0
                elif op == ">":
                    val = 1 if val > right else 0
                elif op == "<=":
                    val = 1 if val <= right else 0
                else:
                    val = 1 if val >= right else 0
            return val

        def parse_shift() -> int:
            val = parse_add()
            while peek() in ("<<", ">>"):
                op = advance()
                right = parse_add()
                val = val << right if op == "<<" else val >> right
            return val

        def parse_add() -> int:
            val = parse_mul()
            while peek() in ("+", "-"):
                op = advance()
                right = parse_mul()
                val = val + right if op == "+" else val - right
            return val

        def parse_mul() -> int:
            val = parse_unary()
            while peek() in ("*", "/", "%"):
                op = advance()
                right = parse_unary()
                if op == "*":
                    val = val * right
                elif op == "/":
                    if right == 0:
                        raise RuntimeError("division by zero")
                    val = int(val / right)
                else:
                    if right == 0:
                        raise RuntimeError("division by zero")
                    val = val % right
            return val

        def parse_unary() -> int:
            if peek() == "-":
                advance()
                return -parse_unary()
            if peek() == "+":
                advance()
                return parse_unary()
            if peek() == "!":
                advance()
                return 1 if parse_unary() == 0 else 0
            if peek() == "~":
                advance()
                return ~parse_unary()
            return parse_primary()

        def parse_primary() -> int:
            if peek() == "(":
                advance()
                val = parse_expr()
                if peek() == ")":
                    advance()
                return val
            tok = advance()
            try:
                return int(tok)
            except (ValueError, TypeError):
                return 0

        return parse_expr()

    def _tokenize_arith(self, expr: str) -> list[str]:
        tokens: list[str] = []
        i = 0
        n = len(expr)
        while i < n:
            if expr[i] in (" ", "\t"):
                i += 1
                continue
            two = expr[i : i + 2]
            if two in ("||", "&&", "==", "!=", "<=", ">=", "<<", ">>"):
                tokens.append(two)
                i += 2
                continue
            if expr[i] in "+-*/%()^&|<>!~?:":
                tokens.append(expr[i])
                i += 1
                continue
            if expr[i].isdigit():
                num = ""
                while i < n and expr[i].isdigit():
                    num += expr[i]
                    i += 1
                tokens.append(num)
                continue
            i += 1
        return tokens

    # -----------------------------------------------------------------------
    # Expansion
    # -----------------------------------------------------------------------

    def _expand_args(self, args: list[str]) -> list[str]:
        return [self._expand_word(arg) for arg in args]

    def _expand_word(self, s: str) -> str:
        result = ""
        i = 0
        n = len(s)

        while i < n:
            c = s[i]

            if c == "'":
                # Single quotes: no expansion, strip quotes
                i += 1
                while i < n and s[i] != "'":
                    result += s[i]
                    i += 1
                if i < n:
                    i += 1  # skip closing '
                continue

            if c == '"':
                # Double quotes: expand variables/command subs, strip quotes
                i += 1
                while i < n and s[i] != '"':
                    if s[i] == "\\" and i + 1 < n and s[i + 1] in ('"', "$", "\\", "`"):
                        result += s[i + 1]
                        i += 2
                    elif s[i] == "$":
                        val, consumed = self._expand_dollar(s, i)
                        result += val
                        i += consumed
                    elif s[i] == "`":
                        val, consumed = self._expand_backtick(s, i)
                        result += val
                        i += consumed
                    else:
                        result += s[i]
                        i += 1
                if i < n:
                    i += 1  # skip closing "
                continue

            if c == "$":
                val, consumed = self._expand_dollar(s, i)
                result += val
                i += consumed
                continue

            if c == "`":
                val, consumed = self._expand_backtick(s, i)
                result += val
                i += consumed
                continue

            result += c
            i += 1

        return result

    def _track_expansion(self) -> None:
        self._expansion_count += 1
        if self._expansion_count > MAX_EXPANSIONS:
            raise RuntimeError("Maximum expansion limit exceeded")

    def _expand_dollar(self, s: str, i: int) -> tuple[str, int]:
        self._track_expansion()
        n = len(s)

        # $((...)) arithmetic expansion
        if i + 2 < n and s[i + 1] == "(" and s[i + 2] == "(":
            depth = 2
            j = i + 3
            while j < n and depth > 0:
                if s[j] == "(":
                    depth += 1
                elif s[j] == ")":
                    depth -= 1
                j += 1
            inner = s[i + 3 : j - 2]
            val = str(self._eval_arithmetic(inner))
            return val, j - i

        # $(...) command substitution
        if i + 1 < n and s[i + 1] == "(":
            depth = 1
            j = i + 2
            while j < n and depth > 0:
                if s[j] == "(":
                    depth += 1
                elif s[j] == ")":
                    depth -= 1
                j += 1
            inner = s[i + 2 : j - 1]
            val = self._command_substitution(inner)
            return val, j - i

        # ${...} parameter expansion
        if i + 1 < n and s[i + 1] == "{":
            depth = 1
            j = i + 2
            while j < n and depth > 0:
                if s[j] == "{":
                    depth += 1
                elif s[j] == "}":
                    depth -= 1
                j += 1
            inner = s[i + 2 : j - 1]
            val = self._expand_brace_param(inner)
            return val, j - i

        # $? special
        if i + 1 < n and s[i + 1] == "?":
            return self.env.get("?", "0"), 2

        # $VAR
        j = i + 1
        while j < n and re.match(r"\w", s[j]):
            j += 1
        if j == i + 1:
            return "$", 1
        name = s[i + 1 : j]
        return self.env.get(name, ""), j - i

    def _expand_backtick(self, s: str, i: int) -> tuple[str, int]:
        j = i + 1
        while j < len(s) and s[j] != "`":
            j += 1
        inner = s[i + 1 : j]
        val = self._command_substitution(inner)
        return val, j - i + 1

    def _command_substitution(self, cmd: str) -> str:
        if self._cmd_sub_depth >= 10:
            raise RuntimeError("Command substitution recursion depth exceeded")
        self._cmd_sub_depth += 1
        try:
            saved = self._iteration_counter
            result = self.exec(cmd)
            self._iteration_counter = saved
            out = result.stdout
            # Strip trailing newline
            if out.endswith("\n"):
                out = out[:-1]
            return out
        finally:
            self._cmd_sub_depth -= 1

    def _expand_brace_param(self, expr: str) -> str:
        # ${#var} -- string length
        if expr.startswith("#"):
            name = expr[1:]
            return str(len(self.env.get(name, "")))

        # ${var:offset:length} -- substring
        m = re.match(r"^(\w+):(-?\d+)(?::(\d+))?$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            offset = int(m.group(2))
            if offset < 0:
                offset = max(0, len(val) + offset)
            length = int(m.group(3)) if m.group(3) is not None else None
            if length is not None:
                return val[offset : offset + length]
            return val[offset:]

        # ${var:-default}
        m = re.match(r"^(\w+):-(.*)$", expr)
        if m:
            val = self.env.get(m.group(1))
            if val is not None and val != "":
                return val
            return self._expand_word(m.group(2))

        # ${var:=default}
        m = re.match(r"^(\w+):=(.*)$", expr)
        if m:
            val = self.env.get(m.group(1))
            if val is not None and val != "":
                return val
            expanded = self._expand_word(m.group(2))
            self.env[m.group(1)] = expanded
            return expanded

        # ${var//pattern/replacement} -- global substitution
        m = re.match(r"^(\w+)//([^/]*)/(.*)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            repl = m.group(3)
            if not pat:
                return val
            return val.replace(pat, repl)

        # ${var/pattern/replacement} -- first substitution
        m = re.match(r"^(\w+)/([^/]*)/(.*)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            repl = m.group(3)
            if not pat:
                return val
            idx = val.find(pat)
            if idx == -1:
                return val
            return val[:idx] + repl + val[idx + len(pat) :]

        # ${var%%suffix} -- greedy suffix removal
        m = re.match(r"^(\w+)%%(.+)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            return self._remove_pattern(val, pat, "suffix", greedy=True)

        # ${var%suffix} -- shortest suffix removal
        m = re.match(r"^(\w+)%(.+)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            return self._remove_pattern(val, pat, "suffix", greedy=False)

        # ${var##prefix} -- greedy prefix removal
        m = re.match(r"^(\w+)##(.+)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            return self._remove_pattern(val, pat, "prefix", greedy=True)

        # ${var#prefix} -- shortest prefix removal
        m = re.match(r"^(\w+)#(.+)$", expr)
        if m:
            val = self.env.get(m.group(1), "")
            pat = m.group(2)
            return self._remove_pattern(val, pat, "prefix", greedy=False)

        # Simple ${var}
        m = re.match(r"^(\w+)$", expr)
        if m:
            return self.env.get(m.group(1), "")

        return ""

    def _remove_pattern(self, val: str, pattern: str, side: str, greedy: bool) -> str:
        regex = self._glob_to_regex(pattern)
        if side == "prefix":
            if greedy:
                for i in range(len(val), -1, -1):
                    if regex.match(val[:i]):
                        return val[i:]
            else:
                for i in range(len(val) + 1):
                    if regex.match(val[:i]):
                        return val[i:]
        else:  # suffix
            if greedy:
                for i in range(len(val) + 1):
                    if regex.match(val[i:]):
                        return val[:i]
            else:
                for i in range(len(val), -1, -1):
                    if regex.match(val[i:]):
                        return val[:i]
        return val

    @staticmethod
    def _glob_to_regex(pattern: str) -> re.Pattern:
        reg = "^"
        for c in pattern:
            if c == "*":
                reg += ".*"
            elif c == "?":
                reg += "."
            else:
                reg += re.escape(c)
        reg += "$"
        return re.compile(reg)

    # -----------------------------------------------------------------------
    # Builtins: test, [, printf, export, true, false
    # -----------------------------------------------------------------------

    def _cmd_test(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(exit_code=0 if self._eval_test(args) else 1)

    def _cmd_bracket(self, args: list[str], stdin: str) -> ExecResult:
        if not args or args[-1] != "]":
            return ExecResult(stderr="[: missing ']'\n", exit_code=2)
        return ExecResult(exit_code=0 if self._eval_test(args[:-1]) else 1)

    def _cmd_double_bracket(self, args: list[str], stdin: str) -> ExecResult:
        if not args or args[-1] != "]]":
            return ExecResult(stderr="[[: missing ']]'\n", exit_code=2)
        return ExecResult(exit_code=0 if self._eval_test(args[:-1]) else 1)

    def _eval_test(self, args: list[str]) -> bool:
        if not args:
            return False

        # Negation
        if args[0] == "!":
            return not self._eval_test(args[1:])

        # Unary file tests
        if len(args) == 2:
            op, operand = args[0], args[1]
            if op == "-f":
                p = self._resolve(operand)
                return self.fs.exists(p) and not self.fs._is_dir(p)
            if op == "-d":
                p = self._resolve(operand)
                return self.fs._is_dir(p)
            if op == "-e":
                p = self._resolve(operand)
                return self.fs.exists(p) or self.fs._is_dir(p)
            if op == "-z":
                return len(operand) == 0
            if op == "-n":
                return len(operand) > 0

        # Single arg: true if non-empty
        if len(args) == 1:
            return len(args[0]) > 0

        # Binary operations
        if len(args) == 3:
            left, op, right = args
            if op == "=":
                return left == right
            if op == "!=":
                return left != right
            if op == "-eq":
                return int(left) == int(right)
            if op == "-ne":
                return int(left) != int(right)
            if op == "-lt":
                return int(left) < int(right)
            if op == "-gt":
                return int(left) > int(right)
            if op == "-le":
                return int(left) <= int(right)
            if op == "-ge":
                return int(left) >= int(right)

        return False

    def _cmd_printf(self, args: list[str], stdin: str) -> ExecResult:
        if not args:
            return ExecResult()
        fmt = args[0]
        fmt_args = args[1:]
        arg_idx = 0
        result = ""
        i = 0
        n = len(fmt)

        while i < n:
            if fmt[i] == "\\":
                i += 1
                if i < n:
                    if fmt[i] == "n":
                        result += "\n"
                    elif fmt[i] == "t":
                        result += "\t"
                    elif fmt[i] == "\\":
                        result += "\\"
                    else:
                        result += fmt[i]
                    i += 1
                continue

            if fmt[i] == "%" and i + 1 < n:
                i += 1
                if fmt[i] == "%":
                    result += "%"
                    i += 1
                    continue
                # Parse format spec
                spec = ""
                while i < n and fmt[i] in "0123456789.-":
                    spec += fmt[i]
                    i += 1
                if i < n:
                    type_char = fmt[i]
                    i += 1
                    arg = fmt_args[arg_idx] if arg_idx < len(fmt_args) else ""
                    arg_idx += 1

                    if type_char == "s":
                        result += arg
                    elif type_char == "d":
                        try:
                            result += str(int(arg))
                        except (ValueError, TypeError):
                            result += "0"
                    elif type_char == "f":
                        try:
                            num = float(arg)
                        except (ValueError, TypeError):
                            num = 0.0
                        prec_match = re.search(r"\.(\d+)", spec)
                        prec = int(prec_match.group(1)) if prec_match else 6
                        result += f"{num:.{prec}f}"
                    else:
                        result += arg
                continue

            result += fmt[i]
            i += 1

        return ExecResult(stdout=result)

    def _cmd_export(self, args: list[str], stdin: str) -> ExecResult:
        for arg in args:
            m = re.match(r"^([A-Za-z_]\w*)=(.*)$", arg)
            if m:
                self.env[m.group(1)] = self._expand_word(m.group(2))
        return ExecResult()

    def _cmd_true(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult()

    def _cmd_false(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(exit_code=1)

    # -----------------------------------------------------------------------
    # Original 23 built-in commands (preserved exactly)
    # -----------------------------------------------------------------------

    def _cmd_cat(self, args: list[str], stdin: str) -> ExecResult:
        if not args:
            return ExecResult(stdout=stdin)
        out = []
        for path in args:
            try:
                out.append(self.fs.read_text(self._resolve(path)))
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult(stdout="".join(out))

    def _cmd_echo(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(stdout=" ".join(args) + "\n")

    def _cmd_pwd(self, args: list[str], stdin: str) -> ExecResult:
        return ExecResult(stdout=self.cwd + "\n")

    def _cmd_cd(self, args: list[str], stdin: str) -> ExecResult:
        target = args[0] if args else "/"
        resolved = self._resolve(target)
        if not self.fs._is_dir(resolved) and resolved != "/":
            return ExecResult(stderr=f"cd: {target}: No such directory\n", exit_code=1)
        self.cwd = resolved
        return ExecResult()

    def _cmd_ls(self, args: list[str], stdin: str) -> ExecResult:
        long_format = "-l" in args
        paths = [a for a in args if not a.startswith("-")]
        target = paths[0] if paths else self.cwd
        resolved = self._resolve(target)

        try:
            entries = self.fs.listdir(resolved)
        except FileNotFoundError:
            return ExecResult(stderr=f"ls: {target}: No such directory\n", exit_code=1)

        if long_format:
            lines = []
            for entry in entries:
                full = os.path.normpath(resolved + "/" + entry)
                s = self.fs.stat(full)
                if s["type"] == "directory":
                    lines.append(f"drwxr-xr-x  -  {entry}/")
                else:
                    lines.append(f"-rw-r--r--  {s['size']:>8}  {entry}")
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")
        return ExecResult(stdout="\n".join(entries) + "\n" if entries else "")

    def _cmd_find(self, args: list[str], stdin: str) -> ExecResult:
        root = "."
        name_filter = None
        type_filter = None

        i = 0
        while i < len(args):
            if args[i] == "-name" and i + 1 < len(args):
                name_filter = args[i + 1]
                i += 2
            elif args[i] == "-type" and i + 1 < len(args):
                type_filter = args[i + 1]
                i += 2
            elif not args[i].startswith("-"):
                root = args[i]
                i += 1
            else:
                i += 1

        resolved = self._resolve(root)
        results = self.fs.find(resolved, name_filter or "*")

        if type_filter == "f":
            results = [r for r in results if not self.fs._is_dir(r)]
        elif type_filter == "d":
            results = [r for r in results if self.fs._is_dir(r)]

        return ExecResult(stdout="\n".join(results) + "\n" if results else "")

    def _cmd_grep(self, args: list[str], stdin: str) -> ExecResult:
        case_insensitive = "-i" in args
        count_only = "-c" in args
        line_numbers = "-n" in args
        invert = "-v" in args
        recursive = "-r" in args or "-rn" in args
        filenames = "-l" in args
        args = [a for a in args if not a.startswith("-")]

        if not args:
            return ExecResult(stderr="grep: missing pattern\n", exit_code=2)

        pattern = args[0]
        targets = args[1:]
        flags = re.IGNORECASE if case_insensitive else 0

        try:
            regex = re.compile(pattern, flags)
        except re.error as e:
            return ExecResult(stderr=f"grep: invalid pattern: {e}\n", exit_code=2)

        def grep_text(text: str, label: str = "") -> list[str]:
            matches = []
            for i, line in enumerate(text.splitlines(), 1):
                match = bool(regex.search(line))
                if invert:
                    match = not match
                if match:
                    prefix = f"{label}:" if label else ""
                    num = f"{i}:" if line_numbers else ""
                    matches.append(f"{prefix}{num}{line}")
            return matches

        all_matches: list[str] = []
        matched_files: list[str] = []

        if not targets and stdin:
            all_matches = grep_text(stdin)
        elif recursive and targets:
            for target in targets:
                resolved = self._resolve(target)
                for fpath in self.fs.find(resolved):
                    try:
                        text = self.fs.read_text(fpath)
                        m = grep_text(text, fpath)
                        if m:
                            matched_files.append(fpath)
                            all_matches.extend(m)
                    except (FileNotFoundError, UnicodeDecodeError):
                        pass
        else:
            for target in targets:
                try:
                    text = self.fs.read_text(self._resolve(target))
                    label = target if len(targets) > 1 else ""
                    m = grep_text(text, label)
                    if m:
                        matched_files.append(target)
                        all_matches.extend(m)
                except FileNotFoundError:
                    return ExecResult(
                        stderr=f"grep: {target}: No such file\n", exit_code=2
                    )

        if filenames:
            return ExecResult(
                stdout="\n".join(matched_files) + "\n" if matched_files else "",
                exit_code=0 if matched_files else 1,
            )

        if count_only:
            return ExecResult(
                stdout=f"{len(all_matches)}\n",
                exit_code=0 if all_matches else 1,
            )

        return ExecResult(
            stdout="\n".join(all_matches) + "\n" if all_matches else "",
            exit_code=0 if all_matches else 1,
        )

    def _cmd_head(self, args: list[str], stdin: str) -> ExecResult:
        n = 10
        files: list[str] = []
        i = 0
        while i < len(args):
            if args[i] == "-n" and i + 1 < len(args):
                n = int(args[i + 1])
                i += 2
            elif args[i].startswith("-") and args[i][1:].isdigit():
                n = int(args[i][1:])
                i += 1
            else:
                files.append(args[i])
                i += 1

        if not files:
            lines = stdin.splitlines()[:n]
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

        text = self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()[:n]
        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_tail(self, args: list[str], stdin: str) -> ExecResult:
        n = 10
        files: list[str] = []
        i = 0
        while i < len(args):
            if args[i] == "-n" and i + 1 < len(args):
                n = int(args[i + 1])
                i += 2
            elif args[i].startswith("-") and args[i][1:].isdigit():
                n = int(args[i][1:])
                i += 1
            else:
                files.append(args[i])
                i += 1

        if not files:
            lines = stdin.splitlines()[-n:]
            return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

        text = self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()[-n:]
        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_wc(self, args: list[str], stdin: str) -> ExecResult:
        lines_only = "-l" in args
        words_only = "-w" in args
        chars_only = "-c" in args
        files = [a for a in args if not a.startswith("-")]

        if not files:
            text = stdin
        else:
            try:
                text = self.fs.read_text(self._resolve(files[0]))
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)

        lc = text.count("\n")
        wc = len(text.split())
        cc = len(text)

        if lines_only:
            return ExecResult(stdout=f"{lc}\n")
        if words_only:
            return ExecResult(stdout=f"{wc}\n")
        if chars_only:
            return ExecResult(stdout=f"{cc}\n")

        label = f" {files[0]}" if files else ""
        return ExecResult(stdout=f"  {lc}  {wc}  {cc}{label}\n")

    def _cmd_sort(self, args: list[str], stdin: str) -> ExecResult:
        reverse = "-r" in args
        numeric = "-n" in args
        unique = "-u" in args
        files = [a for a in args if not a.startswith("-")]

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))
        lines = text.splitlines()

        if numeric:
            def key(s: str):
                m = re.match(r"-?\d+\.?\d*", s)
                return float(m.group()) if m else 0.0

            lines.sort(key=key, reverse=reverse)
        else:
            lines.sort(reverse=reverse)

        if unique:
            lines = list(dict.fromkeys(lines))

        return ExecResult(stdout="\n".join(lines) + "\n" if lines else "")

    def _cmd_uniq(self, args: list[str], stdin: str) -> ExecResult:
        count = "-c" in args
        lines = stdin.splitlines()
        result: list[str] = []
        prev = None
        cnt = 0
        for line in lines:
            if line == prev:
                cnt += 1
            else:
                if prev is not None:
                    result.append(f"  {cnt} {prev}" if count else prev)
                prev = line
                cnt = 1
        if prev is not None:
            result.append(f"  {cnt} {prev}" if count else prev)
        return ExecResult(stdout="\n".join(result) + "\n" if result else "")

    def _cmd_cut(self, args: list[str], stdin: str) -> ExecResult:
        delimiter = "\t"
        fields: list[int] = []
        i = 0
        while i < len(args):
            if args[i] == "-d" and i + 1 < len(args):
                delimiter = args[i + 1]
                i += 2
            elif args[i] == "-f" and i + 1 < len(args):
                for part in args[i + 1].split(","):
                    if "-" in part:
                        start, end = part.split("-", 1)
                        fields.extend(
                            range(int(start or 1), int(end or 100) + 1)
                        )
                    else:
                        fields.append(int(part))
                i += 2
            else:
                i += 1

        lines = stdin.splitlines()
        out = []
        for line in lines:
            parts = line.split(delimiter)
            selected = [parts[f - 1] for f in fields if 0 < f <= len(parts)]
            out.append(delimiter.join(selected))
        return ExecResult(stdout="\n".join(out) + "\n" if out else "")

    def _cmd_tr(self, args: list[str], stdin: str) -> ExecResult:
        delete = "-d" in args
        args = [a for a in args if not a.startswith("-")]

        if delete and args:
            chars = set(args[0])
            return ExecResult(stdout="".join(c for c in stdin if c not in chars))

        if len(args) >= 2:
            set1, set2 = args[0], args[1]
            table = str.maketrans(set1, set2[: len(set1)])
            return ExecResult(stdout=stdin.translate(table))

        return ExecResult(stdout=stdin)

    def _cmd_sed(self, args: list[str], stdin: str) -> ExecResult:
        """Minimal sed: supports s/pattern/replacement/[g] only."""
        files = []
        expr = None
        i = 0
        while i < len(args):
            if args[i] == "-e" and i + 1 < len(args):
                expr = args[i + 1]
                i += 2
            elif not args[i].startswith("-"):
                if expr is None:
                    expr = args[i]
                else:
                    files.append(args[i])
                i += 1
            else:
                i += 1

        if not expr:
            return ExecResult(stdout=stdin)

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))

        m = re.match(r"s(.)(.*?)\1(.*?)\1(\w*)", expr)
        if not m:
            return ExecResult(stdout=text)

        pat, repl, flags_str = m.group(2), m.group(3), m.group(4)
        count = 0 if "g" in flags_str else 1
        result = re.sub(pat, repl, text, count=count)
        return ExecResult(stdout=result)

    def _cmd_tee(self, args: list[str], stdin: str) -> ExecResult:
        append = "-a" in args
        files = [a for a in args if not a.startswith("-")]
        for f in files:
            path = self._resolve(f)
            if append and self.fs.exists(path):
                self.fs.write(path, self.fs.read_text(path) + stdin)
            else:
                self.fs.write(path, stdin)
        return ExecResult(stdout=stdin)

    def _cmd_touch(self, args: list[str], stdin: str) -> ExecResult:
        for f in args:
            path = self._resolve(f)
            if not self.fs.exists(path):
                self.fs.write(path, "")
        return ExecResult()

    def _cmd_mkdir(self, args: list[str], stdin: str) -> ExecResult:
        for a in args:
            if a.startswith("-"):
                continue
            path = self._resolve(a)
            self.fs.write(path + "/.keep", "")
        return ExecResult()

    def _cmd_cp(self, args: list[str], stdin: str) -> ExecResult:
        args = [a for a in args if not a.startswith("-")]
        if len(args) < 2:
            return ExecResult(stderr="cp: missing operand\n", exit_code=1)
        src, dst = self._resolve(args[0]), self._resolve(args[1])
        try:
            self.fs.write(dst, self.fs.read(src))
        except FileNotFoundError as e:
            return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult()

    def _cmd_rm(self, args: list[str], stdin: str) -> ExecResult:
        files = [a for a in args if not a.startswith("-")]
        for f in files:
            try:
                self.fs.remove(self._resolve(f))
            except FileNotFoundError:
                pass
        return ExecResult()

    def _cmd_stat(self, args: list[str], stdin: str) -> ExecResult:
        for f in args:
            if f.startswith("-"):
                continue
            try:
                s = self.fs.stat(self._resolve(f))
                return ExecResult(stdout=json.dumps(s, indent=2) + "\n")
            except FileNotFoundError as e:
                return ExecResult(stderr=str(e) + "\n", exit_code=1)
        return ExecResult()

    def _cmd_tree(self, args: list[str], stdin: str) -> ExecResult:
        target = args[0] if args and not args[0].startswith("-") else self.cwd
        resolved = self._resolve(target)
        lines = [resolved]
        self._tree_recurse(resolved, "", lines)
        return ExecResult(stdout="\n".join(lines) + "\n")

    def _tree_recurse(self, path: str, prefix: str, lines: list[str]) -> None:
        entries = self.fs.listdir(path)
        for i, entry in enumerate(entries):
            is_last = i == len(entries) - 1
            connector = "\u2514\u2500\u2500 " if is_last else "\u251c\u2500\u2500 "
            full = os.path.normpath(path + "/" + entry)
            is_dir = self.fs._is_dir(full)
            lines.append(f"{prefix}{connector}{entry}{'/' if is_dir else ''}")
            if is_dir:
                extension = "    " if is_last else "\u2502   "
                self._tree_recurse(full, prefix + extension, lines)

    def _cmd_jq(self, args: list[str], stdin: str) -> ExecResult:
        """Minimal jq: supports ., .field, .field.sub, .[n], .[] only."""
        raw = "-r" in args
        args = [a for a in args if not a.startswith("-")]
        query = args[0] if args else "."
        files = args[1:]

        text = stdin if not files else self.fs.read_text(self._resolve(files[0]))

        try:
            data = json.loads(text)
        except json.JSONDecodeError as e:
            return ExecResult(stderr=f"jq: parse error: {e}\n", exit_code=2)

        try:
            result = self._jq_query(data, query)
        except (KeyError, IndexError, TypeError) as e:
            return ExecResult(stderr=f"jq: error: {e}\n", exit_code=5)

        if isinstance(result, list) and query.endswith("[]"):
            parts = []
            for item in result:
                parts.append(
                    str(item) if raw and isinstance(item, str) else json.dumps(item)
                )
            return ExecResult(stdout="\n".join(parts) + "\n")

        if raw and isinstance(result, str):
            return ExecResult(stdout=result + "\n")
        return ExecResult(stdout=json.dumps(result, indent=2) + "\n")

    @staticmethod
    def _jq_query(data: Any, query: str) -> Any:
        if query == ".":
            return data
        parts = re.findall(r"\.\w+|\[\d+\]|\[\]", query)
        current = data
        for part in parts:
            if part == "[]":
                if not isinstance(current, list):
                    raise TypeError("Cannot iterate over non-array")
                return current
            elif part.startswith("["):
                idx = int(part[1:-1])
                current = current[idx]
            elif part.startswith("."):
                key = part[1:]
                current = current[key]
        return current


class ShellRegistry:
    """Global registry of named Shell instances. get() returns clones."""

    _shells: dict[str, Shell] = {}

    @classmethod
    def register(cls, name: str, shell: Shell) -> None:
        cls._shells[name] = shell

    @classmethod
    def get(cls, name: str) -> Shell:
        if name not in cls._shells:
            raise KeyError(f"Shell '{name}' not registered")
        return cls._shells[name].clone()

    @classmethod
    def has(cls, name: str) -> bool:
        return name in cls._shells

    @classmethod
    def remove(cls, name: str) -> None:
        del cls._shells[name]

    @classmethod
    def reset(cls) -> None:
        cls._shells = {}
