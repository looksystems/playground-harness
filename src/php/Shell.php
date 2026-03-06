<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Result of executing a shell command.
 */
class ExecResult
{
    public function __construct(
        public readonly string $stdout = '',
        public readonly string $stderr = '',
        public readonly int $exitCode = 0,
    ) {
    }
}

// ---------------------------------------------------------------------------
// Token types
// ---------------------------------------------------------------------------

const TOKEN_WORD = 'WORD';
const TOKEN_PIPE = 'PIPE';
const TOKEN_SEMI = 'SEMI';
const TOKEN_DSEMI = 'DSEMI';
const TOKEN_AND = 'AND';
const TOKEN_OR = 'OR';
const TOKEN_REDIRECT_OUT = 'REDIRECT_OUT';
const TOKEN_REDIRECT_APPEND = 'REDIRECT_APPEND';
const TOKEN_LPAREN = 'LPAREN';
const TOKEN_RPAREN = 'RPAREN';
const TOKEN_NEWLINE = 'NEWLINE';
const TOKEN_EOF = 'EOF';

const KEYWORDS = ['if', 'then', 'elif', 'else', 'fi', 'for', 'in', 'do', 'done', 'while', 'case', 'esac'];

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

/**
 * @return list<array{type: string, value: string}>
 */
function tokenize(string $input): array
{
    $tokens = [];
    $i = 0;
    $len = strlen($input);

    while ($i < $len) {
        $c = $input[$i];

        if ($c === ' ' || $c === "\t") {
            $i++;
            continue;
        }

        if ($c === "\n") {
            $tokens[] = ['type' => TOKEN_NEWLINE, 'value' => "\n"];
            $i++;
            continue;
        }

        if ($c === ';' && $i + 1 < $len && $input[$i + 1] === ';') {
            $tokens[] = ['type' => TOKEN_DSEMI, 'value' => ';;'];
            $i += 2;
            continue;
        }

        if ($c === ';') {
            $tokens[] = ['type' => TOKEN_SEMI, 'value' => ';'];
            $i++;
            continue;
        }

        if ($c === '|' && $i + 1 < $len && $input[$i + 1] === '|') {
            $tokens[] = ['type' => TOKEN_OR, 'value' => '||'];
            $i += 2;
            continue;
        }

        if ($c === '|') {
            $tokens[] = ['type' => TOKEN_PIPE, 'value' => '|'];
            $i++;
            continue;
        }

        if ($c === '&' && $i + 1 < $len && $input[$i + 1] === '&') {
            $tokens[] = ['type' => TOKEN_AND, 'value' => '&&'];
            $i += 2;
            continue;
        }

        if ($c === '>' && $i + 1 < $len && $input[$i + 1] === '>') {
            $tokens[] = ['type' => TOKEN_REDIRECT_APPEND, 'value' => '>>'];
            $i += 2;
            continue;
        }

        if ($c === '>') {
            $tokens[] = ['type' => TOKEN_REDIRECT_OUT, 'value' => '>'];
            $i++;
            continue;
        }

        if ($c === '(') {
            $tokens[] = ['type' => TOKEN_LPAREN, 'value' => '('];
            $i++;
            continue;
        }

        if ($c === ')') {
            $tokens[] = ['type' => TOKEN_RPAREN, 'value' => ')'];
            $i++;
            continue;
        }

        // Comment
        if ($c === '#') {
            while ($i < $len && $input[$i] !== "\n") {
                $i++;
            }
            continue;
        }

        // Word (includes quoted strings, $(...), `...`, ${...}, $VAR)
        $word = '';
        while ($i < $len) {
            $ch = $input[$i];

            if ($ch === "'") {
                // Single-quoted string: collect everything verbatim including the quotes
                $word .= $ch;
                $i++;
                while ($i < $len && $input[$i] !== "'") {
                    $word .= $input[$i];
                    $i++;
                }
                if ($i < $len) {
                    $word .= $input[$i];
                    $i++;
                }
                continue;
            }

            if ($ch === '"') {
                $word .= $ch;
                $i++;
                while ($i < $len && $input[$i] !== '"') {
                    if ($input[$i] === '\\' && $i + 1 < $len) {
                        $word .= $input[$i] . $input[$i + 1];
                        $i += 2;
                    } else {
                        $word .= $input[$i];
                        $i++;
                    }
                }
                if ($i < $len) {
                    $word .= $input[$i];
                    $i++;
                }
                continue;
            }

            if ($ch === '$' && $i + 1 < $len && $input[$i + 1] === '(') {
                // Command substitution $(...)
                $word .= '$(';
                $i += 2;
                $depth = 1;
                while ($i < $len && $depth > 0) {
                    if ($input[$i] === '(') {
                        $depth++;
                    } elseif ($input[$i] === ')') {
                        $depth--;
                        if ($depth === 0) {
                            break;
                        }
                    } elseif ($input[$i] === "'") {
                        $word .= $input[$i];
                        $i++;
                        while ($i < $len && $input[$i] !== "'") {
                            $word .= $input[$i];
                            $i++;
                        }
                        if ($i < $len) {
                            $word .= $input[$i];
                            $i++;
                        }
                        continue;
                    } elseif ($input[$i] === '"') {
                        $word .= $input[$i];
                        $i++;
                        while ($i < $len && $input[$i] !== '"') {
                            if ($input[$i] === '\\' && $i + 1 < $len) {
                                $word .= $input[$i] . $input[$i + 1];
                                $i += 2;
                            } else {
                                $word .= $input[$i];
                                $i++;
                            }
                        }
                        if ($i < $len) {
                            $word .= $input[$i];
                            $i++;
                        }
                        continue;
                    }
                    $word .= $input[$i];
                    $i++;
                }
                $word .= ')';
                if ($i < $len) {
                    $i++; // skip closing )
                }
                continue;
            }

            if ($ch === '$' && $i + 1 < $len && $input[$i + 1] === '{') {
                // Parameter expansion ${...}
                $word .= '${';
                $i += 2;
                $depth = 1;
                while ($i < $len && $depth > 0) {
                    if ($input[$i] === '{') {
                        $depth++;
                    } elseif ($input[$i] === '}') {
                        $depth--;
                        if ($depth === 0) {
                            break;
                        }
                    }
                    $word .= $input[$i];
                    $i++;
                }
                $word .= '}';
                if ($i < $len) {
                    $i++; // skip closing }
                }
                continue;
            }

            if ($ch === '`') {
                // Backtick command substitution
                $word .= '`';
                $i++;
                while ($i < $len && $input[$i] !== '`') {
                    $word .= $input[$i];
                    $i++;
                }
                $word .= '`';
                if ($i < $len) {
                    $i++;
                }
                continue;
            }

            if ($ch === '\\') {
                // Escape next character
                if ($i + 1 < $len) {
                    $word .= $input[$i + 1];
                    $i += 2;
                } else {
                    $i++;
                }
                continue;
            }

            // Break on meta-characters
            if (
                $ch === ' ' || $ch === "\t" || $ch === "\n" ||
                $ch === '|' || $ch === ';' || $ch === '&' ||
                $ch === '>' || $ch === '<' || $ch === '(' || $ch === ')' ||
                $ch === '#'
            ) {
                break;
            }

            $word .= $ch;
            $i++;
        }

        if ($word !== '') {
            $tokens[] = ['type' => TOKEN_WORD, 'value' => $word];
        }
    }

    $tokens[] = ['type' => TOKEN_EOF, 'value' => ''];
    return $tokens;
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

class Parser
{
    /** @var list<array{type: string, value: string}> */
    private array $tokens;
    private int $pos;
    private int $depth;

    /**
     * @param list<array{type: string, value: string}> $tokens
     */
    public function __construct(array $tokens)
    {
        $this->tokens = $tokens;
        $this->pos = 0;
        $this->depth = 0;
    }

    private function peek(): array
    {
        return $this->tokens[$this->pos] ?? ['type' => TOKEN_EOF, 'value' => ''];
    }

    private function advance(): array
    {
        $t = $this->tokens[$this->pos];
        $this->pos++;
        return $t;
    }

    private function expect(string $type): array
    {
        $t = $this->peek();
        if ($t['type'] !== $type) {
            throw new \RuntimeException("Expected {$type} but got {$t['type']} ({$t['value']})");
        }
        return $this->advance();
    }

    private function expectKeyword(string $kw): void
    {
        $t = $this->peek();
        if ($t['type'] !== TOKEN_WORD || $t['value'] !== $kw) {
            throw new \RuntimeException("Expected '{$kw}' but got '{$t['value']}'");
        }
        $this->advance();
    }

    private function atEnd(): bool
    {
        return $this->peek()['type'] === TOKEN_EOF;
    }

    private function skipNewlines(): void
    {
        while ($this->peek()['type'] === TOKEN_NEWLINE) {
            $this->advance();
        }
    }

    private function skipSemiNewlines(): void
    {
        while ($this->peek()['type'] === TOKEN_NEWLINE || $this->peek()['type'] === TOKEN_SEMI) {
            $this->advance();
        }
    }

    private function isCommandTerminator(): bool
    {
        $t = $this->peek();
        return (
            $t['type'] === TOKEN_EOF ||
            $t['type'] === TOKEN_SEMI ||
            $t['type'] === TOKEN_DSEMI ||
            $t['type'] === TOKEN_NEWLINE ||
            $t['type'] === TOKEN_AND ||
            $t['type'] === TOKEN_OR ||
            $t['type'] === TOKEN_PIPE ||
            $t['type'] === TOKEN_RPAREN ||
            ($t['type'] === TOKEN_WORD && in_array($t['value'], ['then', 'elif', 'else', 'fi', 'do', 'done', 'esac'], true))
        );
    }

    private function isCompoundEnd(): bool
    {
        $t = $this->peek();
        return $t['type'] === TOKEN_WORD && in_array($t['value'], ['fi', 'done', 'then', 'elif', 'else', 'do', 'esac'], true);
    }

    public function parse(): array
    {
        $this->depth++;
        if ($this->depth > 50) {
            throw new \RuntimeException('Nesting depth limit exceeded');
        }
        $node = $this->parseList();
        $this->depth--;
        return $node;
    }

    public function parseList(): array
    {
        $this->skipSemiNewlines();
        if ($this->atEnd()) {
            return ['type' => 'command', 'args' => [], 'redirects' => []];
        }

        $nodes = [];
        $nodes[] = $this->parseAndOr();

        while ($this->peek()['type'] === TOKEN_SEMI || $this->peek()['type'] === TOKEN_NEWLINE) {
            $this->skipSemiNewlines();
            if ($this->atEnd() || $this->isCompoundEnd()) {
                break;
            }
            $nodes[] = $this->parseAndOr();
        }

        if (count($nodes) === 1) {
            return $nodes[0];
        }
        return ['type' => 'list', 'commands' => $nodes];
    }

    private function parseAndOr(): array
    {
        $left = $this->parsePipeline();

        while (true) {
            $t = $this->peek();
            if ($t['type'] === TOKEN_AND) {
                $this->advance();
                $this->skipNewlines();
                $right = $this->parsePipeline();
                $left = ['type' => 'and', 'left' => $left, 'right' => $right];
            } elseif ($t['type'] === TOKEN_OR) {
                $this->advance();
                $this->skipNewlines();
                $right = $this->parsePipeline();
                $left = ['type' => 'or', 'left' => $left, 'right' => $right];
            } else {
                break;
            }
        }
        return $left;
    }

    private function parsePipeline(): array
    {
        $commands = [];
        $commands[] = $this->parseCommand();

        while ($this->peek()['type'] === TOKEN_PIPE) {
            $this->advance();
            $this->skipNewlines();
            $commands[] = $this->parseCommand();
        }

        if (count($commands) === 1) {
            return $commands[0];
        }
        return ['type' => 'pipeline', 'commands' => $commands];
    }

    private function parseCommand(): array
    {
        $t = $this->peek();

        if ($t['type'] === TOKEN_WORD) {
            if ($t['value'] === 'if') {
                return $this->parseIf();
            }
            if ($t['value'] === 'for') {
                return $this->parseFor();
            }
            if ($t['value'] === 'while') {
                return $this->parseWhile();
            }
            if ($t['value'] === 'case') {
                return $this->parseCase();
            }
        }

        return $this->parseSimpleCommand();
    }

    private function parseSimpleCommand(): array
    {
        $args = [];
        $redirects = [];

        while (!$this->isCommandTerminator()) {
            $t = $this->peek();

            if ($t['type'] === TOKEN_REDIRECT_APPEND) {
                $this->advance();
                $target = $this->expect(TOKEN_WORD);
                $redirects[] = ['mode' => 'append', 'target' => $target['value']];
            } elseif ($t['type'] === TOKEN_REDIRECT_OUT) {
                $this->advance();
                $target = $this->expect(TOKEN_WORD);
                $redirects[] = ['mode' => 'write', 'target' => $target['value']];
            } elseif ($t['type'] === TOKEN_WORD) {
                $args[] = $t['value'];
                $this->advance();
            } else {
                break;
            }
        }

        // Check for assignment: first arg matches VAR=value pattern
        if (count($args) >= 1 && count($redirects) === 0) {
            $assignIdx = 0;
            // Handle `export VAR=value`
            if ($args[0] === 'export' && count($args) >= 2) {
                $assignIdx = 1;
            }
            if (isset($args[$assignIdx]) && preg_match('/^([A-Za-z_]\w*)=(.*)$/s', $args[$assignIdx], $m)) {
                if ($assignIdx === 0 && count($args) === 1) {
                    return ['type' => 'assignment', 'name' => $m[1], 'value' => $m[2]];
                }
                if ($assignIdx === 1 && count($args) === 2) {
                    return ['type' => 'assignment', 'name' => $m[1], 'value' => $m[2]];
                }
            }
        }

        return ['type' => 'command', 'args' => $args, 'redirects' => $redirects];
    }

    private function parseIf(): array
    {
        $this->expectKeyword('if');
        $this->skipSemiNewlines();
        $clauses = [];

        $condition = $this->parseList();
        $this->skipSemiNewlines();
        $this->expectKeyword('then');
        $this->skipSemiNewlines();
        $body = $this->parseList();
        $clauses[] = ['condition' => $condition, 'body' => $body];

        while ($this->peek()['type'] === TOKEN_WORD && $this->peek()['value'] === 'elif') {
            $this->advance();
            $this->skipSemiNewlines();
            $elifCond = $this->parseList();
            $this->skipSemiNewlines();
            $this->expectKeyword('then');
            $this->skipSemiNewlines();
            $elifBody = $this->parseList();
            $clauses[] = ['condition' => $elifCond, 'body' => $elifBody];
        }

        $elseBody = null;
        if ($this->peek()['type'] === TOKEN_WORD && $this->peek()['value'] === 'else') {
            $this->advance();
            $this->skipSemiNewlines();
            $elseBody = $this->parseList();
        }

        $this->skipSemiNewlines();
        $this->expectKeyword('fi');

        return ['type' => 'if', 'clauses' => $clauses, 'elseBody' => $elseBody];
    }

    private function parseFor(): array
    {
        $this->expectKeyword('for');
        $varToken = $this->expect(TOKEN_WORD);
        $variable = $varToken['value'];

        $this->skipSemiNewlines();
        $words = [];
        if ($this->peek()['type'] === TOKEN_WORD && $this->peek()['value'] === 'in') {
            $this->advance();
            while ($this->peek()['type'] === TOKEN_WORD && !$this->isCompoundEnd()) {
                $words[] = $this->advance()['value'];
            }
        }

        $this->skipSemiNewlines();
        $this->expectKeyword('do');
        $this->skipSemiNewlines();
        $body = $this->parseList();
        $this->skipSemiNewlines();
        $this->expectKeyword('done');

        return ['type' => 'for', 'variable' => $variable, 'words' => $words, 'body' => $body];
    }

    private function parseWhile(): array
    {
        $this->expectKeyword('while');
        $this->skipSemiNewlines();
        $condition = $this->parseList();
        $this->skipSemiNewlines();
        $this->expectKeyword('do');
        $this->skipSemiNewlines();
        $body = $this->parseList();
        $this->skipSemiNewlines();
        $this->expectKeyword('done');

        return ['type' => 'while', 'condition' => $condition, 'body' => $body];
    }

    private function parseCase(): array
    {
        $this->expectKeyword('case');
        $wordToken = $this->expect(TOKEN_WORD);
        $word = $wordToken['value'];
        $this->skipSemiNewlines();
        $this->expectKeyword('in');
        $this->skipSemiNewlines();

        $clauses = [];

        while (!($this->peek()['type'] === TOKEN_WORD && $this->peek()['value'] === 'esac') && !$this->atEnd()) {
            // Parse patterns: pattern1 | pattern2 )
            $patterns = [];
            // Skip optional leading (
            if ($this->peek()['type'] === TOKEN_LPAREN) {
                $this->advance();
            }

            $patterns[] = $this->expect(TOKEN_WORD)['value'];
            while ($this->peek()['type'] === TOKEN_PIPE) {
                $this->advance();
                $patterns[] = $this->expect(TOKEN_WORD)['value'];
            }
            // Expect )
            if ($this->peek()['type'] === TOKEN_RPAREN) {
                $this->advance();
            } else {
                throw new \RuntimeException("Expected ')' in case clause but got '{$this->peek()['value']}'");
            }
            $this->skipSemiNewlines();

            // Parse body until ;;
            $body = $this->parseList();
            $clauses[] = ['patterns' => $patterns, 'body' => $body];

            // Expect ;;
            if ($this->peek()['type'] === TOKEN_DSEMI) {
                $this->advance();
            }
            $this->skipSemiNewlines();
        }

        $this->expectKeyword('esac');
        return ['type' => 'case', 'word' => $word, 'clauses' => $clauses];
    }
}

// ---------------------------------------------------------------------------
// Shell
// ---------------------------------------------------------------------------

const MAX_VAR_SIZE = 64 * 1024;
const MAX_EXPANSIONS = 1_000;

/**
 * Virtual shell interpreter over a VirtualFS.
 * Tokenizer + recursive-descent parser + AST evaluator.
 */
class Shell
{
    /** @var array<string, \Closure> */
    private array $builtins;

    private int $iterationCounter = 0;
    private int $cmdSubDepth = 0;
    private int $expansionCount = 0;

    /**
     * @param list<string>|null $allowedCommands
     */
    public function __construct(
        public VirtualFS $fs,
        public string $cwd = '/',
        public array $env = [],
        private ?array $allowedCommands = null,
        private int $maxOutput = 16_000,
        private int $maxIterations = 10_000,
    ) {
        $all = [
            'cat' => $this->cmdCat(...),
            'echo' => $this->cmdEcho(...),
            'find' => $this->cmdFind(...),
            'grep' => $this->cmdGrep(...),
            'head' => $this->cmdHead(...),
            'ls' => $this->cmdLs(...),
            'pwd' => $this->cmdPwd(...),
            'sort' => $this->cmdSort(...),
            'tail' => $this->cmdTail(...),
            'tee' => $this->cmdTee(...),
            'touch' => $this->cmdTouch(...),
            'tree' => $this->cmdTree(...),
            'uniq' => $this->cmdUniq(...),
            'wc' => $this->cmdWc(...),
            'mkdir' => $this->cmdMkdir(...),
            'cp' => $this->cmdCp(...),
            'rm' => $this->cmdRm(...),
            'stat' => $this->cmdStat(...),
            'cut' => $this->cmdCut(...),
            'tr' => $this->cmdTr(...),
            'sed' => $this->cmdSed(...),
            'jq' => $this->cmdJq(...),
            'cd' => $this->cmdCd(...),
            'test' => $this->cmdTest(...),
            '[' => $this->cmdBracket(...),
            '[[' => $this->cmdDoubleBracket(...),
            'printf' => $this->cmdPrintf(...),
            'export' => $this->cmdExport(...),
            'true' => $this->cmdTrue(...),
            'false' => $this->cmdFalse(...),
        ];

        if ($allowedCommands !== null) {
            $this->builtins = array_intersect_key(
                $all,
                array_flip($allowedCommands)
            );
        } else {
            $this->builtins = $all;
        }
    }

    public function cloneShell(): self
    {
        return new self(
            fs: $this->fs->cloneFs(),
            cwd: $this->cwd,
            env: $this->env,
            allowedCommands: $this->allowedCommands,
            maxOutput: $this->maxOutput,
            maxIterations: $this->maxIterations,
        );
    }

    private function resolve(string $path): string
    {
        if ($path !== '' && $path[0] === '/') {
            return self::normPath($path);
        }
        return self::normPath($this->cwd . '/' . $path);
    }

    private static function normPath(string $path): string
    {
        if ($path === '' || $path[0] !== '/') {
            $path = '/' . $path;
        }
        $parts = explode('/', $path);
        $normalized = [];
        foreach ($parts as $part) {
            if ($part === '' || $part === '.') {
                continue;
            }
            if ($part === '..') {
                array_pop($normalized);
            } else {
                $normalized[] = $part;
            }
        }
        return '/' . implode('/', $normalized);
    }

    public function exec(string $command): ExecResult
    {
        $command = trim($command);
        if ($command === '') {
            return new ExecResult();
        }

        try {
            $tokens = tokenize($command);
            $parser = new Parser($tokens);
            $ast = $parser->parse();
        } catch (\Throwable $e) {
            return new ExecResult(stderr: "parse error: {$e->getMessage()}\n", exitCode: 2);
        }

        $this->iterationCounter = 0;
        $this->expansionCount = 0;

        try {
            $result = $this->evaluate($ast, '');
        } catch (\Throwable $e) {
            return new ExecResult(stderr: "{$e->getMessage()}\n", exitCode: 1);
        }

        // Truncate
        if (strlen($result->stdout) > $this->maxOutput) {
            $total = strlen($result->stdout);
            return new ExecResult(
                stdout: substr($result->stdout, 0, $this->maxOutput)
                    . "\n... [truncated, {$total} total chars]",
                stderr: $result->stderr,
                exitCode: $result->exitCode,
            );
        }

        return $result;
    }

    // -----------------------------------------------------------------------
    // AST evaluator
    // -----------------------------------------------------------------------

    private function evaluate(array $node, string $stdin): ExecResult
    {
        return match ($node['type']) {
            'command' => $this->evalCommand($node, $stdin),
            'pipeline' => $this->evalPipeline($node, $stdin),
            'list' => $this->evalList($node, $stdin),
            'and' => $this->evalAnd($node, $stdin),
            'or' => $this->evalOr($node, $stdin),
            'if' => $this->evalIf($node, $stdin),
            'for' => $this->evalFor($node, $stdin),
            'while' => $this->evalWhile($node, $stdin),
            'assignment' => $this->evalAssignment($node),
            'case' => $this->evalCase($node, $stdin),
            default => new ExecResult(),
        };
    }

    private function evalCommand(array $node, string $stdin): ExecResult
    {
        if (empty($node['args']) && empty($node['redirects'])) {
            return new ExecResult();
        }

        $expanded = $this->expandArgs($node['args']);
        if (empty($expanded)) {
            return new ExecResult();
        }

        $cmdName = $expanded[0];
        $args = array_slice($expanded, 1);

        $handler = $this->builtins[$cmdName] ?? null;
        if ($handler === null) {
            $this->env['?'] = '127';
            return new ExecResult(stderr: "{$cmdName}: command not found\n", exitCode: 127);
        }

        $result = $handler($args, $stdin);
        $this->env['?'] = (string) $result->exitCode;

        foreach ($node['redirects'] as $redir) {
            $target = $this->expandWord($redir['target']);
            $path = $this->resolve($target);
            if ($redir['mode'] === 'append' && $this->fs->exists($path)) {
                $existing = $this->fs->readText($path);
                $this->fs->write($path, $existing . $result->stdout);
            } else {
                $this->fs->write($path, $result->stdout);
            }
            $result = new ExecResult(stderr: $result->stderr, exitCode: $result->exitCode);
        }

        return $result;
    }

    private function evalPipeline(array $node, string $stdin): ExecResult
    {
        $currentStdin = $stdin;
        $lastResult = new ExecResult();

        foreach ($node['commands'] as $cmd) {
            $lastResult = $this->evaluate($cmd, $currentStdin);
            $currentStdin = $lastResult->stdout;
            if ($lastResult->exitCode !== 0) {
                break;
            }
        }

        $this->env['?'] = (string) $lastResult->exitCode;
        return $lastResult;
    }

    private function evalList(array $node, string $stdin): ExecResult
    {
        $results = [];
        foreach ($node['commands'] as $cmd) {
            $r = $this->evaluate($cmd, $stdin);
            $results[] = $r;
        }
        return new ExecResult(
            stdout: implode('', array_map(fn(ExecResult $r) => $r->stdout, $results)),
            stderr: implode('', array_map(fn(ExecResult $r) => $r->stderr, $results)),
            exitCode: !empty($results) ? end($results)->exitCode : 0,
        );
    }

    private function evalAnd(array $node, string $stdin): ExecResult
    {
        $left = $this->evaluate($node['left'], $stdin);
        if ($left->exitCode !== 0) {
            return $left;
        }
        $right = $this->evaluate($node['right'], $stdin);
        return new ExecResult(
            stdout: $left->stdout . $right->stdout,
            stderr: $left->stderr . $right->stderr,
            exitCode: $right->exitCode,
        );
    }

    private function evalOr(array $node, string $stdin): ExecResult
    {
        $left = $this->evaluate($node['left'], $stdin);
        if ($left->exitCode === 0) {
            return $left;
        }
        $right = $this->evaluate($node['right'], $stdin);
        return new ExecResult(
            stdout: $left->stdout . $right->stdout,
            stderr: $left->stderr . $right->stderr,
            exitCode: $right->exitCode,
        );
    }

    private function evalIf(array $node, string $stdin): ExecResult
    {
        foreach ($node['clauses'] as $clause) {
            $condResult = $this->evaluate($clause['condition'], $stdin);
            if ($condResult->exitCode === 0) {
                $bodyResult = $this->evaluate($clause['body'], $stdin);
                return new ExecResult(
                    stdout: $condResult->stdout . $bodyResult->stdout,
                    stderr: $condResult->stderr . $bodyResult->stderr,
                    exitCode: $bodyResult->exitCode,
                );
            }
        }
        if ($node['elseBody'] !== null) {
            return $this->evaluate($node['elseBody'], $stdin);
        }
        return new ExecResult();
    }

    private function evalFor(array $node, string $stdin): ExecResult
    {
        $words = [];
        foreach ($node['words'] as $w) {
            $expanded = $this->expandWord($w);
            $split = preg_split('/\s+/', $expanded, -1, PREG_SPLIT_NO_EMPTY);
            foreach ($split as $s) {
                $words[] = $s;
            }
        }

        $stdout = '';
        $stderr = '';
        $exitCode = 0;

        foreach ($words as $word) {
            $this->iterationCounter++;
            if ($this->iterationCounter > $this->maxIterations) {
                return new ExecResult(
                    stdout: $stdout,
                    stderr: $stderr . "Maximum iteration limit exceeded\n",
                    exitCode: 1,
                );
            }
            $this->env[$node['variable']] = $word;
            $r = $this->evaluate($node['body'], $stdin);
            $stdout .= $r->stdout;
            $stderr .= $r->stderr;
            $exitCode = $r->exitCode;
        }

        return new ExecResult(stdout: $stdout, stderr: $stderr, exitCode: $exitCode);
    }

    private function evalWhile(array $node, string $stdin): ExecResult
    {
        $stdout = '';
        $stderr = '';
        $exitCode = 0;

        while (true) {
            $this->iterationCounter++;
            if ($this->iterationCounter > $this->maxIterations) {
                return new ExecResult(
                    stdout: $stdout,
                    stderr: $stderr . "Maximum iteration limit exceeded\n",
                    exitCode: 1,
                );
            }
            $condResult = $this->evaluate($node['condition'], $stdin);
            if ($condResult->exitCode !== 0) {
                break;
            }
            $bodyResult = $this->evaluate($node['body'], $stdin);
            $stdout .= $bodyResult->stdout;
            $stderr .= $bodyResult->stderr;
            $exitCode = $bodyResult->exitCode;
        }

        return new ExecResult(stdout: $stdout, stderr: $stderr, exitCode: $exitCode);
    }

    private function evalAssignment(array $node): ExecResult
    {
        $value = $this->expandWord($node['value']);
        if (strlen($value) > MAX_VAR_SIZE) {
            $value = substr($value, 0, MAX_VAR_SIZE);
        }
        $this->env[$node['name']] = $value;
        return new ExecResult();
    }

    private function evalCase(array $node, string $stdin): ExecResult
    {
        $word = $this->expandWord($node['word']);
        foreach ($node['clauses'] as $clause) {
            foreach ($clause['patterns'] as $pattern) {
                $expanded = $this->expandWord($pattern);
                if ($expanded === '*' || $this->globMatch($word, $expanded)) {
                    return $this->evaluate($clause['body'], $stdin);
                }
            }
        }
        return new ExecResult();
    }

    private function globMatch(string $str, string $pattern): bool
    {
        $regex = $this->globToRegex($pattern);
        return (bool) preg_match($regex, $str);
    }

    private function evalArithmetic(string $expr): int
    {
        // Expand variables
        $expanded = preg_replace_callback('/\$\{?(\w+)\}?/', function ($m) {
            return $this->env[$m[1]] ?? '0';
        }, $expr);
        $expanded = preg_replace_callback('/[A-Za-z_]\w*/', function ($m) {
            return $this->env[$m[0]] ?? '0';
        }, $expanded);

        return $this->parseArithExpr(trim($expanded));
    }

    private function parseArithExpr(string $expr): int
    {
        $tokens = $this->tokenizeArith($expr);
        $pos = 0;

        $peek = function () use (&$tokens, &$pos): string {
            return $tokens[$pos] ?? '';
        };
        $advance = function () use (&$tokens, &$pos): string {
            $t = $tokens[$pos] ?? '';
            $pos++;
            return $t;
        };

        $parseExpr = null;
        $parseTernary = null;
        $parseOr = null;
        $parseAnd = null;
        $parseBitOr = null;
        $parseBitXor = null;
        $parseBitAnd = null;
        $parseEquality = null;
        $parseRelational = null;
        $parseShift = null;
        $parseAdd = null;
        $parseMul = null;
        $parseUnary = null;
        $parsePrimary = null;

        $parseExpr = function () use (&$parseTernary): int {
            return $parseTernary();
        };

        $parseTernary = function () use (&$parseOr, &$parseExpr, $peek, $advance): int {
            $val = $parseOr();
            if ($peek() === '?') {
                $advance();
                $truthy = $parseExpr();
                if ($peek() === ':') {
                    $advance();
                }
                $falsy = $parseExpr();
                return $val !== 0 ? $truthy : $falsy;
            }
            return $val;
        };

        $parseOr = function () use (&$parseAnd, $peek, $advance): int {
            $val = $parseAnd();
            while ($peek() === '||') {
                $advance();
                $val = ($val !== 0 || $parseAnd() !== 0) ? 1 : 0;
            }
            return $val;
        };

        $parseAnd = function () use (&$parseBitOr, $peek, $advance): int {
            $val = $parseBitOr();
            while ($peek() === '&&') {
                $advance();
                $val = ($val !== 0 && $parseBitOr() !== 0) ? 1 : 0;
            }
            return $val;
        };

        $parseBitOr = function () use (&$parseBitXor, $peek, $advance): int {
            $val = $parseBitXor();
            while ($peek() === '|') {
                $advance();
                $val = $val | $parseBitXor();
            }
            return $val;
        };

        $parseBitXor = function () use (&$parseBitAnd, $peek, $advance): int {
            $val = $parseBitAnd();
            while ($peek() === '^') {
                $advance();
                $val = $val ^ $parseBitAnd();
            }
            return $val;
        };

        $parseBitAnd = function () use (&$parseEquality, $peek, $advance): int {
            $val = $parseEquality();
            while ($peek() === '&') {
                $advance();
                $val = $val & $parseEquality();
            }
            return $val;
        };

        $parseEquality = function () use (&$parseRelational, $peek, $advance): int {
            $val = $parseRelational();
            while ($peek() === '==' || $peek() === '!=') {
                $op = $advance();
                $right = $parseRelational();
                $val = $op === '==' ? ($val === $right ? 1 : 0) : ($val !== $right ? 1 : 0);
            }
            return $val;
        };

        $parseRelational = function () use (&$parseShift, $peek, $advance): int {
            $val = $parseShift();
            while (in_array($peek(), ['<', '>', '<=', '>='], true)) {
                $op = $advance();
                $right = $parseShift();
                if ($op === '<') { $val = $val < $right ? 1 : 0; }
                elseif ($op === '>') { $val = $val > $right ? 1 : 0; }
                elseif ($op === '<=') { $val = $val <= $right ? 1 : 0; }
                else { $val = $val >= $right ? 1 : 0; }
            }
            return $val;
        };

        $parseShift = function () use (&$parseAdd, $peek, $advance): int {
            $val = $parseAdd();
            while ($peek() === '<<' || $peek() === '>>') {
                $op = $advance();
                $right = $parseAdd();
                $val = $op === '<<' ? $val << $right : $val >> $right;
            }
            return $val;
        };

        $parseAdd = function () use (&$parseMul, $peek, $advance): int {
            $val = $parseMul();
            while ($peek() === '+' || $peek() === '-') {
                $op = $advance();
                $right = $parseMul();
                $val = $op === '+' ? $val + $right : $val - $right;
            }
            return $val;
        };

        $parseMul = function () use (&$parseUnary, $peek, $advance): int {
            $val = $parseUnary();
            while ($peek() === '*' || $peek() === '/' || $peek() === '%') {
                $op = $advance();
                $right = $parseUnary();
                if ($op === '*') { $val = $val * $right; }
                elseif ($op === '/') {
                    if ($right === 0) { throw new \RuntimeException('division by zero'); }
                    $val = intdiv($val, $right);
                } else {
                    if ($right === 0) { throw new \RuntimeException('division by zero'); }
                    $val = $val % $right;
                }
            }
            return $val;
        };

        $parseUnary = function () use (&$parseUnary, &$parsePrimary, $peek, $advance): int {
            if ($peek() === '-') { $advance(); return -$parseUnary(); }
            if ($peek() === '+') { $advance(); return $parseUnary(); }
            if ($peek() === '!') { $advance(); return $parseUnary() === 0 ? 1 : 0; }
            if ($peek() === '~') { $advance(); return ~$parseUnary(); }
            return $parsePrimary();
        };

        $parsePrimary = function () use (&$parseExpr, $peek, $advance): int {
            if ($peek() === '(') {
                $advance();
                $val = $parseExpr();
                if ($peek() === ')') { $advance(); }
                return $val;
            }
            $tok = $advance();
            $n = (int) $tok;
            return is_numeric($tok) ? $n : 0;
        };

        return $parseExpr();
    }

    /**
     * @return list<string>
     */
    private function tokenizeArith(string $expr): array
    {
        $tokens = [];
        $i = 0;
        $len = strlen($expr);
        while ($i < $len) {
            if ($expr[$i] === ' ' || $expr[$i] === "\t") { $i++; continue; }
            $two = substr($expr, $i, 2);
            if (in_array($two, ['||', '&&', '==', '!=', '<=', '>=', '<<', '>>'], true)) {
                $tokens[] = $two;
                $i += 2;
                continue;
            }
            if (strpos('+-*/%()^&|<>!~?:', $expr[$i]) !== false) {
                $tokens[] = $expr[$i];
                $i++;
                continue;
            }
            if (ctype_digit($expr[$i])) {
                $num = '';
                while ($i < $len && ctype_digit($expr[$i])) { $num .= $expr[$i]; $i++; }
                $tokens[] = $num;
                continue;
            }
            $i++;
        }
        return $tokens;
    }

    // -----------------------------------------------------------------------
    // Expansion
    // -----------------------------------------------------------------------

    /**
     * @param list<string> $args
     * @return list<string>
     */
    private function expandArgs(array $args): array
    {
        $result = [];
        foreach ($args as $arg) {
            $result[] = $this->expandWord($arg);
        }
        return $result;
    }

    private function expandWord(string $s): string
    {
        $result = '';
        $i = 0;
        $len = strlen($s);

        while ($i < $len) {
            $c = $s[$i];

            if ($c === "'") {
                // Single quotes: no expansion, strip quotes
                $i++;
                while ($i < $len && $s[$i] !== "'") {
                    $result .= $s[$i];
                    $i++;
                }
                if ($i < $len) {
                    $i++; // skip closing '
                }
                continue;
            }

            if ($c === '"') {
                // Double quotes: expand variables/command subs, strip quotes
                $i++;
                while ($i < $len && $s[$i] !== '"') {
                    if ($s[$i] === '\\' && $i + 1 < $len && in_array($s[$i + 1], ['"', '$', '\\', '`'], true)) {
                        $result .= $s[$i + 1];
                        $i += 2;
                    } elseif ($s[$i] === '$') {
                        [$val, $consumed] = $this->expandDollar($s, $i);
                        $result .= $val;
                        $i += $consumed;
                    } elseif ($s[$i] === '`') {
                        [$val, $consumed] = $this->expandBacktick($s, $i);
                        $result .= $val;
                        $i += $consumed;
                    } else {
                        $result .= $s[$i];
                        $i++;
                    }
                }
                if ($i < $len) {
                    $i++; // skip closing "
                }
                continue;
            }

            if ($c === '$') {
                [$val, $consumed] = $this->expandDollar($s, $i);
                $result .= $val;
                $i += $consumed;
                continue;
            }

            if ($c === '`') {
                [$val, $consumed] = $this->expandBacktick($s, $i);
                $result .= $val;
                $i += $consumed;
                continue;
            }

            $result .= $c;
            $i++;
        }

        return $result;
    }

    /**
     * @return array{string, int}
     */
    private function trackExpansion(): void
    {
        $this->expansionCount++;
        if ($this->expansionCount > MAX_EXPANSIONS) {
            throw new \RuntimeException('Maximum expansion limit exceeded');
        }
    }

    private function expandDollar(string $s, int $i): array
    {
        $this->trackExpansion();
        $len = strlen($s);

        // $((...)) arithmetic expansion
        if ($i + 2 < $len && $s[$i + 1] === '(' && $s[$i + 2] === '(') {
            $depth = 2;
            $j = $i + 3;
            while ($j < $len && $depth > 0) {
                if ($s[$j] === '(') { $depth++; }
                elseif ($s[$j] === ')') { $depth--; }
                $j++;
            }
            $inner = substr($s, $i + 3, $j - 2 - ($i + 3));
            $val = (string) $this->evalArithmetic($inner);
            return [$val, $j - $i];
        }

        // $(...) command substitution
        if ($i + 1 < $len && $s[$i + 1] === '(') {
            $depth = 1;
            $j = $i + 2;
            while ($j < $len && $depth > 0) {
                if ($s[$j] === '(') {
                    $depth++;
                } elseif ($s[$j] === ')') {
                    $depth--;
                }
                $j++;
            }
            $inner = substr($s, $i + 2, $j - 1 - ($i + 2));
            $val = $this->commandSubstitution($inner);
            return [$val, $j - $i];
        }

        // ${...} parameter expansion
        if ($i + 1 < $len && $s[$i + 1] === '{') {
            $depth = 1;
            $j = $i + 2;
            while ($j < $len && $depth > 0) {
                if ($s[$j] === '{') {
                    $depth++;
                } elseif ($s[$j] === '}') {
                    $depth--;
                }
                $j++;
            }
            $inner = substr($s, $i + 2, $j - 1 - ($i + 2));
            $val = $this->expandBraceParam($inner);
            return [$val, $j - $i];
        }

        // $? special
        if ($i + 1 < $len && $s[$i + 1] === '?') {
            return [$this->env['?'] ?? '0', 2];
        }

        // $VAR
        $j = $i + 1;
        while ($j < $len && preg_match('/\w/', $s[$j])) {
            $j++;
        }
        if ($j === $i + 1) {
            return ['$', 1];
        }
        $name = substr($s, $i + 1, $j - ($i + 1));
        return [$this->env[$name] ?? '', $j - $i];
    }

    /**
     * @return array{string, int}
     */
    private function expandBacktick(string $s, int $i): array
    {
        $len = strlen($s);
        $j = $i + 1;
        while ($j < $len && $s[$j] !== '`') {
            $j++;
        }
        $inner = substr($s, $i + 1, $j - ($i + 1));
        $val = $this->commandSubstitution($inner);
        return [$val, $j - $i + 1];
    }

    private function commandSubstitution(string $cmd): string
    {
        if ($this->cmdSubDepth >= 10) {
            throw new \RuntimeException('Command substitution recursion depth exceeded');
        }
        $this->cmdSubDepth++;
        try {
            $saved = $this->iterationCounter;
            $result = $this->exec($cmd);
            $this->iterationCounter = $saved;
            $out = $result->stdout;
            // Strip trailing newline
            if (str_ends_with($out, "\n")) {
                $out = substr($out, 0, -1);
            }
            return $out;
        } finally {
            $this->cmdSubDepth--;
        }
    }

    private function expandBraceParam(string $expr): string
    {
        // ${#var} -- string length
        if (str_starts_with($expr, '#')) {
            $name = substr($expr, 1);
            return (string) strlen($this->env[$name] ?? '');
        }

        // ${var:offset:length} -- substring
        if (preg_match('/^(\w+):(-?\d+)(?::(\d+))?$/', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $offset = (int) $m[2];
            if ($offset < 0) {
                $offset = max(0, strlen($val) + $offset);
            }
            if (isset($m[3]) && $m[3] !== '') {
                $length = (int) $m[3];
                return substr($val, $offset, $length);
            }
            return substr($val, $offset);
        }

        // ${var:-default}
        if (preg_match('/^(\w+):-(.*)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? null;
            return ($val !== null && $val !== '') ? $val : $this->expandWord($m[2]);
        }

        // ${var:=default}
        if (preg_match('/^(\w+):=(.*)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? null;
            if ($val !== null && $val !== '') {
                return $val;
            }
            $expanded = $this->expandWord($m[2]);
            $this->env[$m[1]] = $expanded;
            return $expanded;
        }

        // ${var//pattern/replacement} -- global substitution
        if (preg_match('/^(\w+)\/\/([^\/]*)\/(.*)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            $repl = $m[3];
            if ($pat === '') {
                return $val;
            }
            return str_replace($pat, $repl, $val);
        }

        // ${var/pattern/replacement} -- first substitution
        if (preg_match('/^(\w+)\/([^\/]*)\/(.*)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            $repl = $m[3];
            if ($pat === '') {
                return $val;
            }
            $pos = strpos($val, $pat);
            if ($pos === false) {
                return $val;
            }
            return substr($val, 0, $pos) . $repl . substr($val, $pos + strlen($pat));
        }

        // ${var%%suffix} -- greedy suffix removal
        if (preg_match('/^(\w+)%%(.+)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            return $this->removePattern($val, $pat, 'suffix', true);
        }

        // ${var%suffix} -- shortest suffix removal
        if (preg_match('/^(\w+)%(.+)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            return $this->removePattern($val, $pat, 'suffix', false);
        }

        // ${var##prefix} -- greedy prefix removal
        if (preg_match('/^(\w+)##(.+)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            return $this->removePattern($val, $pat, 'prefix', true);
        }

        // ${var#prefix} -- shortest prefix removal
        if (preg_match('/^(\w+)#(.+)$/s', $expr, $m)) {
            $val = $this->env[$m[1]] ?? '';
            $pat = $m[2];
            return $this->removePattern($val, $pat, 'prefix', false);
        }

        // Simple ${var}
        if (preg_match('/^(\w+)$/', $expr, $m)) {
            return $this->env[$m[1]] ?? '';
        }

        return '';
    }

    private function removePattern(string $val, string $pattern, string $side, bool $greedy): string
    {
        $regex = $this->globToRegex($pattern);

        if ($side === 'prefix') {
            if ($greedy) {
                for ($i = strlen($val); $i >= 0; $i--) {
                    if (preg_match($regex, substr($val, 0, $i))) {
                        return substr($val, $i);
                    }
                }
            } else {
                for ($i = 0; $i <= strlen($val); $i++) {
                    if (preg_match($regex, substr($val, 0, $i))) {
                        return substr($val, $i);
                    }
                }
            }
        } else {
            if ($greedy) {
                for ($i = 0; $i <= strlen($val); $i++) {
                    if (preg_match($regex, substr($val, $i))) {
                        return substr($val, 0, $i);
                    }
                }
            } else {
                for ($i = strlen($val); $i >= 0; $i--) {
                    if (preg_match($regex, substr($val, $i))) {
                        return substr($val, 0, $i);
                    }
                }
            }
        }

        return $val;
    }

    private function globToRegex(string $pattern): string
    {
        $reg = '^';
        for ($i = 0; $i < strlen($pattern); $i++) {
            $c = $pattern[$i];
            if ($c === '*') {
                $reg .= '.*';
            } elseif ($c === '?') {
                $reg .= '.';
            } else {
                $reg .= preg_quote($c, '/');
            }
        }
        $reg .= '$';
        return '/' . $reg . '/';
    }

    // -----------------------------------------------------------------------
    // Builtins: test, [, printf, export, true, false
    // -----------------------------------------------------------------------

    private function cmdTest(array $args, string $stdin): ExecResult
    {
        return new ExecResult(exitCode: $this->evalTest($args) ? 0 : 1);
    }

    private function cmdBracket(array $args, string $stdin): ExecResult
    {
        if (empty($args) || end($args) !== ']') {
            return new ExecResult(stderr: "[: missing ']'\n", exitCode: 2);
        }
        return new ExecResult(exitCode: $this->evalTest(array_slice($args, 0, -1)) ? 0 : 1);
    }

    private function cmdDoubleBracket(array $args, string $stdin): ExecResult
    {
        if (empty($args) || end($args) !== ']]') {
            return new ExecResult(stderr: "[[: missing ']]'\n", exitCode: 2);
        }
        return new ExecResult(exitCode: $this->evalTest(array_slice($args, 0, -1)) ? 0 : 1);
    }

    private function evalTest(array $args): bool
    {
        if (empty($args)) {
            return false;
        }

        // Negation
        if ($args[0] === '!') {
            return !$this->evalTest(array_slice($args, 1));
        }

        // Unary file/string tests
        if (count($args) === 2) {
            $op = $args[0];
            $operand = $args[1];
            if ($op === '-f') {
                $p = $this->resolve($operand);
                return $this->fs->exists($p) && !$this->fs->isDir($p);
            }
            if ($op === '-d') {
                $p = $this->resolve($operand);
                return $this->fs->isDir($p);
            }
            if ($op === '-e') {
                $p = $this->resolve($operand);
                return $this->fs->exists($p) || $this->fs->isDir($p);
            }
            if ($op === '-z') {
                return strlen($operand) === 0;
            }
            if ($op === '-n') {
                return strlen($operand) > 0;
            }
        }

        // Single arg: true if non-empty
        if (count($args) === 1) {
            return strlen($args[0]) > 0;
        }

        // Binary operations
        if (count($args) === 3) {
            [$left, $op, $right] = $args;
            if ($op === '=') {
                return $left === $right;
            }
            if ($op === '!=') {
                return $left !== $right;
            }
            if ($op === '-eq') {
                return (int) $left === (int) $right;
            }
            if ($op === '-ne') {
                return (int) $left !== (int) $right;
            }
            if ($op === '-lt') {
                return (int) $left < (int) $right;
            }
            if ($op === '-gt') {
                return (int) $left > (int) $right;
            }
            if ($op === '-le') {
                return (int) $left <= (int) $right;
            }
            if ($op === '-ge') {
                return (int) $left >= (int) $right;
            }
        }

        return false;
    }

    private function cmdPrintf(array $args, string $stdin): ExecResult
    {
        if (empty($args)) {
            return new ExecResult();
        }
        $format = $args[0];
        $fmtArgs = array_slice($args, 1);
        $argIdx = 0;
        $result = '';
        $i = 0;
        $len = strlen($format);

        while ($i < $len) {
            if ($format[$i] === '\\') {
                $i++;
                if ($i < $len) {
                    if ($format[$i] === 'n') {
                        $result .= "\n";
                        $i++;
                    } elseif ($format[$i] === 't') {
                        $result .= "\t";
                        $i++;
                    } elseif ($format[$i] === '\\') {
                        $result .= '\\';
                        $i++;
                    } else {
                        $result .= $format[$i];
                        $i++;
                    }
                }
                continue;
            }

            if ($format[$i] === '%' && $i + 1 < $len) {
                $i++;
                if ($format[$i] === '%') {
                    $result .= '%';
                    $i++;
                    continue;
                }
                // Parse format spec
                $spec = '';
                while ($i < $len && preg_match('/[\d.\-]/', $format[$i])) {
                    $spec .= $format[$i];
                    $i++;
                }
                if ($i < $len) {
                    $type = $format[$i];
                    $i++;
                    $arg = $fmtArgs[$argIdx] ?? '';
                    $argIdx++;

                    if ($type === 's') {
                        $result .= $arg;
                    } elseif ($type === 'd') {
                        $result .= (string) ((int) $arg);
                    } elseif ($type === 'f') {
                        $num = (float) $arg ?: 0.0;
                        if (preg_match('/\.(\d+)/', $spec, $precMatch)) {
                            $prec = (int) $precMatch[1];
                        } else {
                            $prec = 6;
                        }
                        $result .= number_format($num, $prec, '.', '');
                    } else {
                        $result .= $arg;
                    }
                }
                continue;
            }

            $result .= $format[$i];
            $i++;
        }

        return new ExecResult(stdout: $result);
    }

    private function cmdExport(array $args, string $stdin): ExecResult
    {
        foreach ($args as $arg) {
            if (preg_match('/^([A-Za-z_]\w*)=(.*)$/s', $arg, $m)) {
                $this->env[$m[1]] = $this->expandWord($m[2]);
            }
        }
        return new ExecResult();
    }

    private function cmdTrue(array $args, string $stdin): ExecResult
    {
        return new ExecResult();
    }

    private function cmdFalse(array $args, string $stdin): ExecResult
    {
        return new ExecResult(exitCode: 1);
    }

    // -- Built-in commands (original 23) ------------------------------------

    /**
     * @param list<string> $args
     */
    private function cmdCat(array $args, string $stdin): ExecResult
    {
        if (empty($args)) {
            return new ExecResult(stdout: $stdin);
        }
        $out = [];
        foreach ($args as $path) {
            try {
                $out[] = $this->fs->readText($this->resolve($path));
            } catch (\RuntimeException $e) {
                return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
            }
        }
        return new ExecResult(stdout: implode('', $out));
    }

    private function cmdEcho(array $args, string $stdin): ExecResult
    {
        return new ExecResult(stdout: implode(' ', $args) . "\n");
    }

    private function cmdPwd(array $args, string $stdin): ExecResult
    {
        return new ExecResult(stdout: $this->cwd . "\n");
    }

    private function cmdCd(array $args, string $stdin): ExecResult
    {
        $target = $args[0] ?? '/';
        $resolved = $this->resolve($target);
        if (!$this->fs->isDir($resolved) && $resolved !== '/') {
            return new ExecResult(stderr: "cd: {$target}: No such directory\n", exitCode: 1);
        }
        $this->cwd = $resolved;
        return new ExecResult();
    }

    private function cmdLs(array $args, string $stdin): ExecResult
    {
        $longFormat = in_array('-l', $args, true);
        $paths = array_filter($args, fn(string $a) => !str_starts_with($a, '-'));
        $target = !empty($paths) ? reset($paths) : $this->cwd;
        $resolved = $this->resolve($target);

        try {
            $entries = $this->fs->listdir($resolved);
        } catch (\RuntimeException) {
            return new ExecResult(stderr: "ls: {$target}: No such directory\n", exitCode: 1);
        }

        if ($longFormat) {
            $lines = [];
            foreach ($entries as $entry) {
                $full = self::normPath($resolved . '/' . $entry);
                $s = $this->fs->stat($full);
                if ($s['type'] === 'directory') {
                    $lines[] = "drwxr-xr-x  -  {$entry}/";
                } else {
                    $size = str_pad((string)$s['size'], 8, ' ', STR_PAD_LEFT);
                    $lines[] = "-rw-r--r--  {$size}  {$entry}";
                }
            }
            return new ExecResult(stdout: !empty($lines) ? implode("\n", $lines) . "\n" : '');
        }
        return new ExecResult(stdout: !empty($entries) ? implode("\n", $entries) . "\n" : '');
    }

    private function cmdFind(array $args, string $stdin): ExecResult
    {
        $root = '.';
        $nameFilter = null;
        $typeFilter = null;

        $i = 0;
        while ($i < count($args)) {
            if ($args[$i] === '-name' && $i + 1 < count($args)) {
                $nameFilter = $args[$i + 1];
                $i += 2;
            } elseif ($args[$i] === '-type' && $i + 1 < count($args)) {
                $typeFilter = $args[$i + 1];
                $i += 2;
            } elseif (!str_starts_with($args[$i], '-')) {
                $root = $args[$i];
                $i++;
            } else {
                $i++;
            }
        }

        $resolved = $this->resolve($root);
        $results = $this->fs->find($resolved, $nameFilter ?? '*');

        if ($typeFilter === 'f') {
            $results = array_values(array_filter($results, fn(string $r) => !$this->fs->isDir($r)));
        } elseif ($typeFilter === 'd') {
            $results = array_values(array_filter($results, fn(string $r) => $this->fs->isDir($r)));
        }

        return new ExecResult(stdout: !empty($results) ? implode("\n", $results) . "\n" : '');
    }

    private function cmdGrep(array $args, string $stdin): ExecResult
    {
        // Expand combined flags like -rl, -rn, -rin etc.
        $flagChars = '';
        $nonFlagArgs = [];
        foreach ($args as $a) {
            if (str_starts_with($a, '-') && strlen($a) > 1 && !is_numeric(substr($a, 1))) {
                $flagChars .= substr($a, 1);
            } else {
                $nonFlagArgs[] = $a;
            }
        }

        $caseInsensitive = str_contains($flagChars, 'i');
        $countOnly = str_contains($flagChars, 'c');
        $lineNumbers = str_contains($flagChars, 'n');
        $invert = str_contains($flagChars, 'v');
        $recursive = str_contains($flagChars, 'r');
        $filenames = str_contains($flagChars, 'l');

        $args = $nonFlagArgs;

        if (empty($args)) {
            return new ExecResult(stderr: "grep: missing pattern\n", exitCode: 2);
        }

        $pattern = $args[0];
        $targets = array_slice($args, 1);
        $flags = $caseInsensitive ? 'i' : '';

        // Validate regex
        $regex = '/' . str_replace('/', '\/', $pattern) . '/' . $flags;
        if (@preg_match($regex, '') === false) {
            return new ExecResult(stderr: "grep: invalid pattern: {$pattern}\n", exitCode: 2);
        }

        $grepText = function (string $text, string $label = '') use ($regex, $invert, $lineNumbers): array {
            $matches = [];
            $lines = explode("\n", $text);
            // Remove trailing empty element from explode
            if (end($lines) === '' && count($lines) > 1) {
                array_pop($lines);
            }
            foreach ($lines as $i => $line) {
                $match = (bool)preg_match($regex, $line);
                if ($invert) {
                    $match = !$match;
                }
                if ($match) {
                    $prefix = $label !== '' ? "{$label}:" : '';
                    $num = $lineNumbers ? ($i + 1) . ':' : '';
                    $matches[] = "{$prefix}{$num}{$line}";
                }
            }
            return $matches;
        };

        $allMatches = [];
        $matchedFiles = [];

        if (empty($targets) && $stdin !== '') {
            $allMatches = $grepText($stdin);
        } elseif ($recursive && !empty($targets)) {
            foreach ($targets as $target) {
                $resolved = $this->resolve($target);
                foreach ($this->fs->find($resolved) as $fpath) {
                    try {
                        $text = $this->fs->readText($fpath);
                        $m = $grepText($text, $fpath);
                        if (!empty($m)) {
                            $matchedFiles[] = $fpath;
                            array_push($allMatches, ...$m);
                        }
                    } catch (\RuntimeException) {
                        // skip
                    }
                }
            }
        } else {
            foreach ($targets as $target) {
                try {
                    $text = $this->fs->readText($this->resolve($target));
                    $label = count($targets) > 1 ? $target : '';
                    $m = $grepText($text, $label);
                    if (!empty($m)) {
                        $matchedFiles[] = $target;
                        array_push($allMatches, ...$m);
                    }
                } catch (\RuntimeException) {
                    return new ExecResult(stderr: "grep: {$target}: No such file\n", exitCode: 2);
                }
            }
        }

        if ($filenames) {
            return new ExecResult(
                stdout: !empty($matchedFiles) ? implode("\n", $matchedFiles) . "\n" : '',
                exitCode: !empty($matchedFiles) ? 0 : 1,
            );
        }

        if ($countOnly) {
            return new ExecResult(
                stdout: count($allMatches) . "\n",
                exitCode: !empty($allMatches) ? 0 : 1,
            );
        }

        return new ExecResult(
            stdout: !empty($allMatches) ? implode("\n", $allMatches) . "\n" : '',
            exitCode: !empty($allMatches) ? 0 : 1,
        );
    }

    private function cmdHead(array $args, string $stdin): ExecResult
    {
        $n = 10;
        $files = [];
        $i = 0;
        while ($i < count($args)) {
            if ($args[$i] === '-n' && $i + 1 < count($args)) {
                $n = (int)$args[$i + 1];
                $i += 2;
            } elseif (str_starts_with($args[$i], '-') && ctype_digit(substr($args[$i], 1))) {
                $n = (int)substr($args[$i], 1);
                $i++;
            } else {
                $files[] = $args[$i];
                $i++;
            }
        }

        if (empty($files)) {
            $lines = array_slice(explode("\n", rtrim($stdin, "\n")), 0, $n);
            return new ExecResult(stdout: !empty($lines) && $lines !== [''] ? implode("\n", $lines) . "\n" : '');
        }

        try {
            $text = $this->fs->readText($this->resolve($files[0]));
        } catch (\RuntimeException $e) {
            return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
        }
        $lines = array_slice(explode("\n", rtrim($text, "\n")), 0, $n);
        return new ExecResult(stdout: !empty($lines) && $lines !== [''] ? implode("\n", $lines) . "\n" : '');
    }

    private function cmdTail(array $args, string $stdin): ExecResult
    {
        $n = 10;
        $files = [];
        $i = 0;
        while ($i < count($args)) {
            if ($args[$i] === '-n' && $i + 1 < count($args)) {
                $n = (int)$args[$i + 1];
                $i += 2;
            } elseif (str_starts_with($args[$i], '-') && ctype_digit(substr($args[$i], 1))) {
                $n = (int)substr($args[$i], 1);
                $i++;
            } else {
                $files[] = $args[$i];
                $i++;
            }
        }

        if (empty($files)) {
            $allLines = explode("\n", rtrim($stdin, "\n"));
            $lines = array_slice($allLines, -$n);
            return new ExecResult(stdout: !empty($lines) && $lines !== [''] ? implode("\n", $lines) . "\n" : '');
        }

        try {
            $text = $this->fs->readText($this->resolve($files[0]));
        } catch (\RuntimeException $e) {
            return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
        }
        $allLines = explode("\n", rtrim($text, "\n"));
        $lines = array_slice($allLines, -$n);
        return new ExecResult(stdout: !empty($lines) && $lines !== [''] ? implode("\n", $lines) . "\n" : '');
    }

    private function cmdWc(array $args, string $stdin): ExecResult
    {
        $linesOnly = in_array('-l', $args, true);
        $wordsOnly = in_array('-w', $args, true);
        $charsOnly = in_array('-c', $args, true);
        $files = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));

        if (empty($files)) {
            $text = $stdin;
        } else {
            try {
                $text = $this->fs->readText($this->resolve($files[0]));
            } catch (\RuntimeException $e) {
                return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
            }
        }

        $lc = substr_count($text, "\n");
        $wc = count(preg_split('/\s+/', $text, -1, PREG_SPLIT_NO_EMPTY));
        $cc = strlen($text);

        if ($linesOnly) {
            return new ExecResult(stdout: "{$lc}\n");
        }
        if ($wordsOnly) {
            return new ExecResult(stdout: "{$wc}\n");
        }
        if ($charsOnly) {
            return new ExecResult(stdout: "{$cc}\n");
        }

        $label = !empty($files) ? " {$files[0]}" : '';
        return new ExecResult(stdout: "  {$lc}  {$wc}  {$cc}{$label}\n");
    }

    private function cmdSort(array $args, string $stdin): ExecResult
    {
        $reverse = in_array('-r', $args, true);
        $numeric = in_array('-n', $args, true);
        $unique = in_array('-u', $args, true);
        $files = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));

        $text = empty($files) ? $stdin : $this->fs->readText($this->resolve($files[0]));
        $lines = explode("\n", rtrim($text, "\n"));
        if ($lines === ['']) {
            $lines = [];
        }

        if ($numeric) {
            usort($lines, function (string $a, string $b) use ($reverse) {
                $va = preg_match('/^-?\d+\.?\d*/', $a, $ma) ? (float)$ma[0] : 0.0;
                $vb = preg_match('/^-?\d+\.?\d*/', $b, $mb) ? (float)$mb[0] : 0.0;
                $cmp = $va <=> $vb;
                return $reverse ? -$cmp : $cmp;
            });
        } else {
            sort($lines);
            if ($reverse) {
                $lines = array_reverse($lines);
            }
        }

        if ($unique) {
            $lines = array_values(array_unique($lines));
        }

        return new ExecResult(stdout: !empty($lines) ? implode("\n", $lines) . "\n" : '');
    }

    private function cmdUniq(array $args, string $stdin): ExecResult
    {
        $count = in_array('-c', $args, true);
        $lines = explode("\n", rtrim($stdin, "\n"));
        if ($lines === ['']) {
            return new ExecResult();
        }

        $result = [];
        $prev = null;
        $cnt = 0;

        foreach ($lines as $line) {
            if ($line === $prev) {
                $cnt++;
            } else {
                if ($prev !== null) {
                    $result[] = $count ? "  {$cnt} {$prev}" : $prev;
                }
                $prev = $line;
                $cnt = 1;
            }
        }
        if ($prev !== null) {
            $result[] = $count ? "  {$cnt} {$prev}" : $prev;
        }

        return new ExecResult(stdout: !empty($result) ? implode("\n", $result) . "\n" : '');
    }

    private function cmdCut(array $args, string $stdin): ExecResult
    {
        $delimiter = "\t";
        $fields = [];
        $i = 0;
        while ($i < count($args)) {
            if ($args[$i] === '-d' && $i + 1 < count($args)) {
                $delimiter = $args[$i + 1];
                $i += 2;
            } elseif ($args[$i] === '-f' && $i + 1 < count($args)) {
                foreach (explode(',', $args[$i + 1]) as $part) {
                    if (str_contains($part, '-')) {
                        [$start, $end] = explode('-', $part, 2);
                        $s = $start !== '' ? (int)$start : 1;
                        $e = $end !== '' ? (int)$end : 100;
                        for ($f = $s; $f <= $e; $f++) {
                            $fields[] = $f;
                        }
                    } else {
                        $fields[] = (int)$part;
                    }
                }
                $i += 2;
            } else {
                $i++;
            }
        }

        $lines = explode("\n", rtrim($stdin, "\n"));
        $out = [];
        foreach ($lines as $line) {
            $parts = explode($delimiter, $line);
            $selected = [];
            foreach ($fields as $f) {
                if ($f > 0 && $f <= count($parts)) {
                    $selected[] = $parts[$f - 1];
                }
            }
            $out[] = implode($delimiter, $selected);
        }
        return new ExecResult(stdout: !empty($out) ? implode("\n", $out) . "\n" : '');
    }

    private function cmdTr(array $args, string $stdin): ExecResult
    {
        $delete = in_array('-d', $args, true);
        $args = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));

        if ($delete && !empty($args)) {
            $chars = str_split($args[0]);
            $result = '';
            for ($i = 0; $i < strlen($stdin); $i++) {
                if (!in_array($stdin[$i], $chars, true)) {
                    $result .= $stdin[$i];
                }
            }
            return new ExecResult(stdout: $result);
        }

        if (count($args) >= 2) {
            $set1 = $args[0];
            $set2 = $args[1];
            // Pad set2 to match set1 length
            $set2 = str_pad($set2, strlen($set1), substr($set2, -1));
            return new ExecResult(stdout: strtr($stdin, $set1, $set2));
        }

        return new ExecResult(stdout: $stdin);
    }

    private function cmdSed(array $args, string $stdin): ExecResult
    {
        $files = [];
        $expr = null;
        $i = 0;
        while ($i < count($args)) {
            if ($args[$i] === '-e' && $i + 1 < count($args)) {
                $expr = $args[$i + 1];
                $i += 2;
            } elseif (!str_starts_with($args[$i], '-')) {
                if ($expr === null) {
                    $expr = $args[$i];
                } else {
                    $files[] = $args[$i];
                }
                $i++;
            } else {
                $i++;
            }
        }

        if ($expr === null) {
            return new ExecResult(stdout: $stdin);
        }

        $text = empty($files) ? $stdin : $this->fs->readText($this->resolve($files[0]));

        if (!preg_match('/^s(.)(.*)\\1(.*)\\1(\w*)$/', $expr, $m)) {
            return new ExecResult(stdout: $text);
        }

        $pat = $m[2];
        $repl = $m[3];
        $flagsStr = $m[4];
        $limit = str_contains($flagsStr, 'g') ? -1 : 1;
        $regex = '/' . str_replace('/', '\/', $pat) . '/';
        $result = preg_replace($regex, $repl, $text, $limit);
        return new ExecResult(stdout: $result ?? $text);
    }

    private function cmdTee(array $args, string $stdin): ExecResult
    {
        $append = in_array('-a', $args, true);
        $files = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));
        foreach ($files as $f) {
            $path = $this->resolve($f);
            if ($append && $this->fs->exists($path)) {
                $this->fs->write($path, $this->fs->readText($path) . $stdin);
            } else {
                $this->fs->write($path, $stdin);
            }
        }
        return new ExecResult(stdout: $stdin);
    }

    private function cmdTouch(array $args, string $stdin): ExecResult
    {
        foreach ($args as $f) {
            $path = $this->resolve($f);
            if (!$this->fs->exists($path)) {
                $this->fs->write($path, '');
            }
        }
        return new ExecResult();
    }

    private function cmdMkdir(array $args, string $stdin): ExecResult
    {
        foreach ($args as $a) {
            if (str_starts_with($a, '-')) {
                continue;
            }
            $path = $this->resolve($a);
            $this->fs->write($path . '/.keep', '');
        }
        return new ExecResult();
    }

    private function cmdCp(array $args, string $stdin): ExecResult
    {
        $args = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));
        if (count($args) < 2) {
            return new ExecResult(stderr: "cp: missing operand\n", exitCode: 1);
        }
        $src = $this->resolve($args[0]);
        $dst = $this->resolve($args[1]);
        try {
            $this->fs->write($dst, $this->fs->read($src));
        } catch (\RuntimeException $e) {
            return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
        }
        return new ExecResult();
    }

    private function cmdRm(array $args, string $stdin): ExecResult
    {
        $files = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));
        foreach ($files as $f) {
            try {
                $this->fs->remove($this->resolve($f));
            } catch (\RuntimeException) {
                // ignore
            }
        }
        return new ExecResult();
    }

    private function cmdStat(array $args, string $stdin): ExecResult
    {
        foreach ($args as $f) {
            if (str_starts_with($f, '-')) {
                continue;
            }
            try {
                $s = $this->fs->stat($this->resolve($f));
                return new ExecResult(stdout: json_encode($s, JSON_PRETTY_PRINT) . "\n");
            } catch (\RuntimeException $e) {
                return new ExecResult(stderr: $e->getMessage() . "\n", exitCode: 1);
            }
        }
        return new ExecResult();
    }

    private function cmdTree(array $args, string $stdin): ExecResult
    {
        $target = (!empty($args) && !str_starts_with($args[0], '-')) ? $args[0] : $this->cwd;
        $resolved = $this->resolve($target);
        $lines = [$resolved];
        $this->treeRecurse($resolved, '', $lines);
        return new ExecResult(stdout: implode("\n", $lines) . "\n");
    }

    /**
     * @param list<string> $lines
     */
    private function treeRecurse(string $path, string $prefix, array &$lines): void
    {
        $entries = $this->fs->listdir($path);
        $count = count($entries);
        foreach ($entries as $i => $entry) {
            $isLast = ($i === $count - 1);
            $connector = $isLast ? '└── ' : '├── ';
            $full = self::normPath($path . '/' . $entry);
            $isDir = $this->fs->isDir($full);
            $lines[] = "{$prefix}{$connector}{$entry}" . ($isDir ? '/' : '');
            if ($isDir) {
                $extension = $isLast ? '    ' : '│   ';
                $this->treeRecurse($full, $prefix . $extension, $lines);
            }
        }
    }

    private function cmdJq(array $args, string $stdin): ExecResult
    {
        $raw = in_array('-r', $args, true);
        $args = array_values(array_filter($args, fn(string $a) => !str_starts_with($a, '-')));
        $query = $args[0] ?? '.';
        $files = array_slice($args, 1);

        $text = empty($files) ? $stdin : $this->fs->readText($this->resolve($files[0]));

        $data = json_decode($text, true);
        if (json_last_error() !== JSON_ERROR_NONE) {
            return new ExecResult(stderr: 'jq: parse error: ' . json_last_error_msg() . "\n", exitCode: 2);
        }

        try {
            $result = self::jqQuery($data, $query);
        } catch (\Throwable $e) {
            return new ExecResult(stderr: "jq: error: {$e->getMessage()}\n", exitCode: 5);
        }

        if (is_array($result) && str_ends_with($query, '[]') && array_is_list($result)) {
            $parts = [];
            foreach ($result as $item) {
                $parts[] = ($raw && is_string($item)) ? $item : json_encode($item);
            }
            return new ExecResult(stdout: implode("\n", $parts) . "\n");
        }

        if ($raw && is_string($result)) {
            return new ExecResult(stdout: $result . "\n");
        }
        return new ExecResult(stdout: json_encode($result, JSON_PRETTY_PRINT) . "\n");
    }

    private static function jqQuery(mixed $data, string $query): mixed
    {
        if ($query === '.') {
            return $data;
        }

        preg_match_all('/\.\w+|\[\d+\]|\[\]/', $query, $matches);
        $parts = $matches[0];
        $current = $data;

        foreach ($parts as $part) {
            if ($part === '[]') {
                if (!is_array($current) || !array_is_list($current)) {
                    throw new \RuntimeException('Cannot iterate over non-array');
                }
                return $current;
            } elseif (str_starts_with($part, '[')) {
                $idx = (int)substr($part, 1, -1);
                if (!is_array($current) || !isset($current[$idx])) {
                    throw new \RuntimeException("Index {$idx} out of range");
                }
                $current = $current[$idx];
            } elseif (str_starts_with($part, '.')) {
                $key = substr($part, 1);
                if (!is_array($current) || !array_key_exists($key, $current)) {
                    throw new \RuntimeException("Key '{$key}' not found");
                }
                $current = $current[$key];
            }
        }

        return $current;
    }
}

/**
 * Global registry of named Shell instances. get() returns clones.
 */
class ShellRegistry
{
    /** @var array<string, Shell> */
    private static array $shells = [];

    public static function register(string $name, Shell $shell): void
    {
        self::$shells[$name] = $shell;
    }

    public static function get(string $name): Shell
    {
        if (!isset(self::$shells[$name])) {
            throw new \RuntimeException("Shell '{$name}' not registered");
        }
        return self::$shells[$name]->cloneShell();
    }

    public static function has(string $name): bool
    {
        return isset(self::$shells[$name]);
    }

    public static function remove(string $name): void
    {
        unset(self::$shells[$name]);
    }

    public static function reset(): void
    {
        self::$shells = [];
    }
}
