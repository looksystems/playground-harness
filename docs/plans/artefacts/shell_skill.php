<?php

/**
 * shell_skill.php — A virtual filesystem + shell skill for agent_harness.php
 *
 * Inspired by vercel-labs/just-bash: instead of many specialized tools,
 * give the agent a single `exec` tool over an in-memory filesystem.
 *
 * Usage:
 *   require_once 'shell_skill.php';
 *   use AgentHarness\Shell\ShellSkill;
 *
 *   $skill = new ShellSkill();
 *   $skill->write('/data/users.json', json_encode($users));
 *   $skill->mountDir('/docs', ['schema.md' => '...', 'api.md' => '...']);
 *
 *   $manager->mount($skill);
 *   $result = $agent->run('How many users are in the data file?');
 */

declare(strict_types=1);

namespace AgentHarness\Shell;

use AgentHarness\{ToolDef, RunContext};
use AgentHarness\Skills\{Skill, SkillContext};

// ---------------------------------------------------------------------------
// Virtual filesystem
// ---------------------------------------------------------------------------

class VirtualFS
{
    /** @var array<string, string> */
    private array $files = [];

    /** @var array<string, callable(): string> */
    private array $lazy = [];

    public function __construct(array $files = [])
    {
        foreach ($files as $path => $content) {
            $this->write($path, $content);
        }
    }

    private function norm(string $path): string
    {
        $path = '/' . ltrim($path, '/');
        // Resolve . and ..
        $parts = explode('/', $path);
        $resolved = [];
        foreach ($parts as $p) {
            if ($p === '' || $p === '.') continue;
            if ($p === '..') { array_pop($resolved); continue; }
            $resolved[] = $p;
        }
        return '/' . implode('/', $resolved);
    }

    public function write(string $path, string $content): void
    {
        $this->files[$this->norm($path)] = $content;
    }

    public function writeLazy(string $path, callable $provider): void
    {
        $this->lazy[$this->norm($path)] = $provider;
    }

    public function read(string $path): string
    {
        $path = $this->norm($path);
        if (isset($this->lazy[$path])) {
            $this->files[$path] = ($this->lazy[$path])();
            unset($this->lazy[$path]);
        }
        if (!isset($this->files[$path])) {
            throw new \RuntimeException("{$path}: No such file");
        }
        return $this->files[$path];
    }

    public function exists(string $path): bool
    {
        $path = $this->norm($path);
        return isset($this->files[$path]) || isset($this->lazy[$path]) || $this->isDir($path);
    }

    public function remove(string $path): void
    {
        $path = $this->norm($path);
        unset($this->files[$path], $this->lazy[$path]);
    }

    /** @return list<string> */
    private function allPaths(): array
    {
        return array_unique(array_merge(array_keys($this->files), array_keys($this->lazy)));
    }

    public function isDir(string $path): bool
    {
        $prefix = rtrim($this->norm($path), '/') . '/';
        foreach ($this->allPaths() as $p) {
            if (str_starts_with($p, $prefix)) return true;
        }
        return false;
    }

    /** @return list<string> */
    public function listdir(string $path = '/'): array
    {
        $prefix = rtrim($this->norm($path), '/') . '/';
        if ($prefix === '//') $prefix = '/';
        $entries = [];
        foreach ($this->allPaths() as $p) {
            if (str_starts_with($p, $prefix) && $p !== $prefix) {
                $rest = substr($p, strlen($prefix));
                $entry = explode('/', $rest)[0];
                $entries[$entry] = true;
            }
        }
        $keys = array_keys($entries);
        sort($keys);
        return $keys;
    }

    /** @return list<string> */
    public function find(string $root = '/', string $pattern = '*'): array
    {
        $root = rtrim($this->norm($root), '/');
        $results = [];
        foreach ($this->allPaths() as $p) {
            if (!str_starts_with($p, $root)) continue;
            $basename = basename($p);
            if (fnmatch($pattern, $basename)) {
                $results[] = $p;
            }
        }
        sort($results);
        return $results;
    }

    public function stat(string $path): array
    {
        $path = $this->norm($path);
        if ($this->isDir($path)) {
            return ['path' => $path, 'type' => 'directory'];
        }
        $content = $this->read($path);
        return ['path' => $path, 'type' => 'file', 'size' => strlen($content)];
    }
}

// ---------------------------------------------------------------------------
// Shell interpreter
// ---------------------------------------------------------------------------

class ExecResult
{
    public function __construct(
        public string $stdout = '',
        public string $stderr = '',
        public int    $exitCode = 0,
    ) {}
}

class Shell
{
    private const MAX_OUTPUT = 16_000;

    /** @var array<string, callable(list<string>, string): ExecResult> */
    private array $builtins;

    public function __construct(
        public VirtualFS $fs,
        public string    $cwd = '/',
        public array     $env = [],
        ?array           $allowedCommands = null,
    ) {
        $all = [
            'cat' => $this->cat(...), 'echo' => $this->echoCmd(...),
            'pwd' => $this->pwd(...), 'cd' => $this->cd(...),
            'ls' => $this->ls(...), 'find' => $this->findCmd(...),
            'grep' => $this->grep(...), 'head' => $this->head(...),
            'tail' => $this->tail(...), 'wc' => $this->wc(...),
            'sort' => $this->sortCmd(...), 'uniq' => $this->uniq(...),
            'cut' => $this->cut(...), 'tr' => $this->tr(...),
            'sed' => $this->sed(...), 'jq' => $this->jq(...),
            'tree' => $this->tree(...), 'tee' => $this->tee(...),
            'touch' => $this->touch(...), 'mkdir' => $this->mkdirCmd(...),
            'cp' => $this->cp(...), 'rm' => $this->rm(...),
            'stat' => $this->statCmd(...),
        ];

        $this->builtins = $allowedCommands !== null
            ? array_intersect_key($all, array_flip($allowedCommands))
            : $all;
    }

    private function resolve(string $path): string
    {
        if (str_starts_with($path, '/')) {
            return $this->normPath($path);
        }
        return $this->normPath($this->cwd . '/' . $path);
    }

    private function normPath(string $p): string
    {
        $parts = explode('/', $p);
        $resolved = [];
        foreach ($parts as $s) {
            if ($s === '' || $s === '.') continue;
            if ($s === '..') { array_pop($resolved); continue; }
            $resolved[] = $s;
        }
        return '/' . implode('/', $resolved);
    }

    public function exec(string $command): ExecResult
    {
        $command = trim($command);
        if ($command === '') return new ExecResult();

        // Handle ; chaining
        $segments = $this->splitUnquoted($command, ';');
        if (count($segments) > 1) {
            $stdout = $stderr = '';
            $exitCode = 0;
            foreach ($segments as $seg) {
                $r = $this->exec(trim($seg));
                $stdout .= $r->stdout;
                $stderr .= $r->stderr;
                $exitCode = $r->exitCode;
            }
            return new ExecResult($stdout, $stderr, $exitCode);
        }

        // Handle pipes
        $segments = $this->splitUnquoted($command, '|');
        $stdin = '';
        $result = new ExecResult();

        foreach ($segments as $seg) {
            $result = $this->execSingle(trim($seg), $stdin);
            $stdin = $result->stdout;
            if ($result->exitCode !== 0) break;
        }

        if (strlen($result->stdout) > self::MAX_OUTPUT) {
            $len = strlen($result->stdout);
            $result->stdout = substr($result->stdout, 0, self::MAX_OUTPUT)
                . "\n... [truncated, {$len} total chars]";
        }

        return $result;
    }

    private function execSingle(string $command, string $stdin = ''): ExecResult
    {
        // Basic redirect
        $redirectPath = null;
        $append = false;

        foreach (['>>', '>'] as $op) {
            $idx = strpos($command, $op);
            if ($idx !== false) {
                $rest = trim(substr($command, $idx + strlen($op)));
                $redirectPath = explode(' ', $rest)[0];
                $command = trim(substr($command, 0, $idx));
                $append = $op === '>>';
                break;
            }
        }

        $parts = $this->shellSplit($command);
        if (empty($parts)) return new ExecResult();

        $cmdName = array_shift($parts);
        $handler = $this->builtins[$cmdName] ?? null;

        if ($handler === null) {
            return new ExecResult('', "{$cmdName}: command not found\n", 127);
        }

        $result = $handler($parts, $stdin);

        if ($redirectPath !== null) {
            $path = $this->resolve($redirectPath);
            if ($append && $this->fs->exists($path)) {
                $this->fs->write($path, $this->fs->read($path) . $result->stdout);
            } else {
                $this->fs->write($path, $result->stdout);
            }
            $result = new ExecResult('', $result->stderr, $result->exitCode);
        }

        return $result;
    }

    /** @return list<string> */
    private function shellSplit(string $s): array
    {
        $parts = [];
        $current = '';
        $inSingle = $inDouble = false;

        for ($i = 0; $i < strlen($s); $i++) {
            $c = $s[$i];
            if ($c === "'" && !$inDouble) { $inSingle = !$inSingle; continue; }
            if ($c === '"' && !$inSingle) { $inDouble = !$inDouble; continue; }
            if ($c === ' ' && !$inSingle && !$inDouble) {
                if ($current !== '') { $parts[] = $current; $current = ''; }
                continue;
            }
            $current .= $c;
        }
        if ($current !== '') $parts[] = $current;
        return $parts;
    }

    /** @return list<string> */
    private function splitUnquoted(string $s, string $sep): array
    {
        $parts = [];
        $current = '';
        $inSingle = $inDouble = false;
        $sepLen = strlen($sep);

        for ($i = 0; $i < strlen($s); $i++) {
            $c = $s[$i];
            if ($c === "'" && !$inDouble) { $inSingle = !$inSingle; $current .= $c; }
            elseif ($c === '"' && !$inSingle) { $inDouble = !$inDouble; $current .= $c; }
            elseif (substr($s, $i, $sepLen) === $sep && !$inSingle && !$inDouble) {
                $parts[] = $current;
                $current = '';
                $i += $sepLen - 1;
            } else {
                $current .= $c;
            }
        }
        $parts[] = $current;
        return $parts;
    }

    /** @return list<string> */
    private function lines(string $text): array
    {
        if ($text === '') return [];
        $lines = explode("\n", $text);
        if (end($lines) === '') array_pop($lines);
        return $lines;
    }

    // -- Commands ----------------------------------------------------------

    private function ok(string $stdout): ExecResult { return new ExecResult($stdout); }
    private function err(string $stderr, int $code = 1): ExecResult { return new ExecResult('', $stderr, $code); }

    private function cat(array $args, string $stdin): ExecResult
    {
        if (empty($args)) return $this->ok($stdin);
        $out = '';
        foreach ($args as $a) {
            try { $out .= $this->fs->read($this->resolve($a)); }
            catch (\Throwable $e) { return $this->err($e->getMessage() . "\n"); }
        }
        return $this->ok($out);
    }

    private function echoCmd(array $args, string $stdin): ExecResult
    {
        return $this->ok(implode(' ', $args) . "\n");
    }

    private function pwd(array $args, string $stdin): ExecResult
    {
        return $this->ok($this->cwd . "\n");
    }

    private function cd(array $args, string $stdin): ExecResult
    {
        $target = $args[0] ?? '/';
        $resolved = $this->resolve($target);
        if (!$this->fs->isDir($resolved) && $resolved !== '/') {
            return $this->err("cd: {$target}: No such directory\n");
        }
        $this->cwd = $resolved;
        return $this->ok('');
    }

    private function ls(array $args, string $stdin): ExecResult
    {
        $long = in_array('-l', $args);
        $paths = array_filter($args, fn($a) => !str_starts_with($a, '-'));
        $target = $paths[0] ?? $this->cwd;
        $entries = $this->fs->listdir($this->resolve($target));

        if ($long) {
            $lines = array_map(function ($e) use ($target) {
                $full = $this->normPath($this->resolve($target) . '/' . $e);
                $s = $this->fs->stat($full);
                return $s['type'] === 'directory'
                    ? "drwxr-xr-x  -  {$e}/"
                    : sprintf("-rw-r--r--  %8s  %s", $s['size'] ?? 0, $e);
            }, $entries);
            return $this->ok(implode("\n", $lines) . "\n");
        }
        return $this->ok(implode("\n", $entries) . ($entries ? "\n" : ''));
    }

    private function findCmd(array $args, string $stdin): ExecResult
    {
        $root = '.';
        $nameFilter = '*';
        for ($i = 0; $i < count($args); $i++) {
            if ($args[$i] === '-name' && isset($args[$i + 1])) { $nameFilter = $args[++$i]; }
            elseif (!str_starts_with($args[$i], '-')) { $root = $args[$i]; }
        }
        $results = $this->fs->find($this->resolve($root), $nameFilter);
        return $this->ok(implode("\n", $results) . ($results ? "\n" : ''));
    }

    private function grep(array $args, string $stdin): ExecResult
    {
        $flags = [];
        $positional = [];
        foreach ($args as $a) {
            if (str_starts_with($a, '-')) {
                foreach (str_split(substr($a, 1)) as $f) $flags[$f] = true;
            } else {
                $positional[] = $a;
            }
        }

        if (empty($positional)) return $this->err("grep: missing pattern\n", 2);

        $pattern = $positional[0];
        $files = array_slice($positional, 1);
        $iFlag = isset($flags['i']) ? 'i' : '';

        $grepText = function (string $text, string $label = '') use ($pattern, $iFlag, $flags): array {
            $matches = [];
            foreach ($this->lines($text) as $i => $line) {
                $match = (bool) preg_match("/{$pattern}/{$iFlag}", $line);
                if (isset($flags['v'])) $match = !$match;
                if ($match) {
                    $prefix = $label ? "{$label}:" : '';
                    $num = isset($flags['n']) ? ($i + 1) . ':' : '';
                    $matches[] = "{$prefix}{$num}{$line}";
                }
            }
            return $matches;
        };

        $allMatches = [];
        if (empty($files)) {
            $allMatches = $grepText($stdin);
        } else {
            foreach ($files as $f) {
                try {
                    $text = $this->fs->read($this->resolve($f));
                    $label = count($files) > 1 ? $f : '';
                    $allMatches = array_merge($allMatches, $grepText($text, $label));
                } catch (\Throwable $e) {
                    return $this->err("grep: {$f}: No such file\n", 2);
                }
            }
        }

        if (isset($flags['c'])) {
            $c = count($allMatches);
            return new ExecResult("{$c}\n", '', $c ? 0 : 1);
        }
        $out = $allMatches ? implode("\n", $allMatches) . "\n" : '';
        return new ExecResult($out, '', $allMatches ? 0 : 1);
    }

    private function head(array $args, string $stdin): ExecResult
    {
        $n = 10; $files = [];
        for ($i = 0; $i < count($args); $i++) {
            if ($args[$i] === '-n' && isset($args[$i + 1])) { $n = (int)$args[++$i]; }
            else { $files[] = $args[$i]; }
        }
        $text = $files ? $this->fs->read($this->resolve($files[0])) : $stdin;
        $lines = array_slice($this->lines($text), 0, $n);
        return $this->ok(implode("\n", $lines) . ($lines ? "\n" : ''));
    }

    private function tail(array $args, string $stdin): ExecResult
    {
        $n = 10; $files = [];
        for ($i = 0; $i < count($args); $i++) {
            if ($args[$i] === '-n' && isset($args[$i + 1])) { $n = (int)$args[++$i]; }
            else { $files[] = $args[$i]; }
        }
        $text = $files ? $this->fs->read($this->resolve($files[0])) : $stdin;
        $lines = array_slice($this->lines($text), -$n);
        return $this->ok(implode("\n", $lines) . ($lines ? "\n" : ''));
    }

    private function wc(array $args, string $stdin): ExecResult
    {
        $flags = [];
        $files = [];
        foreach ($args as $a) {
            if (str_starts_with($a, '-')) { foreach (str_split(substr($a, 1)) as $f) $flags[$f] = true; }
            else $files[] = $a;
        }
        $text = $files ? $this->fs->read($this->resolve($files[0])) : $stdin;
        $lc = substr_count($text, "\n");
        $wc = count(preg_split('/\s+/', $text, -1, PREG_SPLIT_NO_EMPTY));
        $cc = strlen($text);
        if (isset($flags['l'])) return $this->ok("{$lc}\n");
        if (isset($flags['w'])) return $this->ok("{$wc}\n");
        if (isset($flags['c'])) return $this->ok("{$cc}\n");
        $label = $files ? ' ' . $files[0] : '';
        return $this->ok("  {$lc}  {$wc}  {$cc}{$label}\n");
    }

    private function sortCmd(array $args, string $stdin): ExecResult
    {
        $flags = [];
        $files = [];
        foreach ($args as $a) {
            if (str_starts_with($a, '-')) { foreach (str_split(substr($a, 1)) as $f) $flags[$f] = true; }
            else $files[] = $a;
        }
        $text = $files ? $this->fs->read($this->resolve($files[0])) : $stdin;
        $lines = $this->lines($text);
        if (isset($flags['n'])) usort($lines, fn($a, $b) => floatval($a) <=> floatval($b));
        else sort($lines);
        if (isset($flags['r'])) $lines = array_reverse($lines);
        if (isset($flags['u'])) $lines = array_values(array_unique($lines));
        return $this->ok(implode("\n", $lines) . "\n");
    }

    private function uniq(array $args, string $stdin): ExecResult
    {
        $count = in_array('-c', $args);
        $lines = $this->lines($stdin);
        $result = [];
        $prev = null; $cnt = 0;
        foreach ($lines as $line) {
            if ($line === $prev) { $cnt++; }
            else {
                if ($prev !== null) $result[] = $count ? "  {$cnt} {$prev}" : $prev;
                $prev = $line; $cnt = 1;
            }
        }
        if ($prev !== null) $result[] = $count ? "  {$cnt} {$prev}" : $prev;
        return $this->ok(implode("\n", $result) . ($result ? "\n" : ''));
    }

    private function cut(array $args, string $stdin): ExecResult
    {
        $delim = "\t"; $fields = [];
        for ($i = 0; $i < count($args); $i++) {
            if ($args[$i] === '-d' && isset($args[$i + 1])) { $delim = $args[++$i]; }
            elseif ($args[$i] === '-f' && isset($args[$i + 1])) {
                $fields = array_map('intval', explode(',', $args[++$i]));
            }
        }
        $out = array_map(function ($line) use ($delim, $fields) {
            $parts = explode($delim, $line);
            return implode($delim, array_map(fn($f) => $parts[$f - 1] ?? '', $fields));
        }, $this->lines($stdin));
        return $this->ok(implode("\n", $out) . "\n");
    }

    private function tr(array $args, string $stdin): ExecResult
    {
        $delete = in_array('-d', $args);
        $positional = array_values(array_filter($args, fn($a) => !str_starts_with($a, '-')));
        if ($delete && isset($positional[0])) {
            $chars = str_split($positional[0]);
            return $this->ok(str_replace($chars, '', $stdin));
        }
        if (count($positional) >= 2) {
            return $this->ok(strtr($stdin, $positional[0], $positional[1]));
        }
        return $this->ok($stdin);
    }

    private function sed(array $args, string $stdin): ExecResult
    {
        $positional = array_values(array_filter($args, fn($a) => !str_starts_with($a, '-')));
        $expr = $positional[0] ?? null;
        if (!$expr) return $this->ok($stdin);
        if (preg_match('/^s(.)(.+?)\1(.*?)\1(\w*)$/', $expr, $m)) {
            [, , $pat, $repl, $flags] = $m;
            $limit = str_contains($flags, 'g') ? -1 : 1;
            return $this->ok(preg_replace("/{$pat}/", $repl, $stdin, $limit));
        }
        return $this->ok($stdin);
    }

    private function jq(array $args, string $stdin): ExecResult
    {
        $raw = in_array('-r', $args);
        $positional = array_values(array_filter($args, fn($a) => !str_starts_with($a, '-')));
        $query = $positional[0] ?? '.';
        $files = array_slice($positional, 1);
        $text = $files ? $this->fs->read($this->resolve($files[0])) : $stdin;

        $data = json_decode($text, true);
        if ($data === null && $text !== 'null') {
            return $this->err("jq: parse error\n", 2);
        }

        try {
            $result = $this->jqQuery($data, $query);
        } catch (\Throwable $e) {
            return $this->err("jq: {$e->getMessage()}\n", 5);
        }

        if (is_array($result) && !$this->isAssoc($result) && str_ends_with($query, '[]')) {
            $parts = array_map(fn($item) =>
                $raw && is_string($item) ? $item : json_encode($item),
                $result
            );
            return $this->ok(implode("\n", $parts) . "\n");
        }

        if ($raw && is_string($result)) return $this->ok($result . "\n");
        return $this->ok(json_encode($result, JSON_PRETTY_PRINT) . "\n");
    }

    private function jqQuery(mixed $data, string $query): mixed
    {
        if ($query === '.') return $data;
        preg_match_all('/\.\w+|\[\d+\]|\[\]/', $query, $matches);
        $current = $data;
        foreach ($matches[0] as $part) {
            if ($part === '[]') return is_array($current) ? $current : [];
            if (str_starts_with($part, '[')) $current = $current[(int)substr($part, 1, -1)];
            else $current = $current[substr($part, 1)] ?? null;
        }
        return $current;
    }

    private function isAssoc(array $arr): bool
    {
        return array_keys($arr) !== range(0, count($arr) - 1);
    }

    private function tree(array $args, string $stdin): ExecResult
    {
        $target = null;
        foreach ($args as $a) { if (!str_starts_with($a, '-')) { $target = $a; break; } }
        $resolved = $this->resolve($target ?? $this->cwd);
        $lines = [$resolved];
        $this->treeRecurse($resolved, '', $lines);
        return $this->ok(implode("\n", $lines) . "\n");
    }

    private function treeRecurse(string $path, string $prefix, array &$lines): void
    {
        $entries = $this->fs->listdir($path);
        foreach ($entries as $i => $entry) {
            $isLast = $i === count($entries) - 1;
            $full = $this->normPath($path . '/' . $entry);
            $isDir = $this->fs->isDir($full);
            $connector = $isLast ? '└── ' : '├── ';
            $lines[] = "{$prefix}{$connector}{$entry}" . ($isDir ? '/' : '');
            if ($isDir) {
                $ext = $isLast ? '    ' : '│   ';
                $this->treeRecurse($full, $prefix . $ext, $lines);
            }
        }
    }

    private function tee(array $args, string $stdin): ExecResult
    {
        $files = array_filter($args, fn($a) => !str_starts_with($a, '-'));
        foreach ($files as $f) $this->fs->write($this->resolve($f), $stdin);
        return $this->ok($stdin);
    }

    private function touch(array $args, string $stdin): ExecResult
    {
        foreach ($args as $f) {
            $p = $this->resolve($f);
            if (!$this->fs->exists($p)) $this->fs->write($p, '');
        }
        return $this->ok('');
    }

    private function mkdirCmd(array $args, string $stdin): ExecResult
    {
        foreach ($args as $a) {
            if (str_starts_with($a, '-')) continue;
            $this->fs->write($this->resolve($a) . '/.keep', '');
        }
        return $this->ok('');
    }

    private function cp(array $args, string $stdin): ExecResult
    {
        $paths = array_values(array_filter($args, fn($a) => !str_starts_with($a, '-')));
        if (count($paths) < 2) return $this->err("cp: missing operand\n");
        try {
            $this->fs->write($this->resolve($paths[1]), $this->fs->read($this->resolve($paths[0])));
        } catch (\Throwable $e) { return $this->err($e->getMessage() . "\n"); }
        return $this->ok('');
    }

    private function rm(array $args, string $stdin): ExecResult
    {
        foreach ($args as $f) {
            if (str_starts_with($f, '-')) continue;
            $this->fs->remove($this->resolve($f));
        }
        return $this->ok('');
    }

    private function statCmd(array $args, string $stdin): ExecResult
    {
        foreach ($args as $f) {
            if (str_starts_with($f, '-')) continue;
            try {
                return $this->ok(json_encode($this->fs->stat($this->resolve($f)), JSON_PRETTY_PRINT) . "\n");
            } catch (\Throwable $e) { return $this->err($e->getMessage() . "\n"); }
        }
        return $this->ok('');
    }
}

// ---------------------------------------------------------------------------
// ShellSkill — plugs into agent harness
// ---------------------------------------------------------------------------

class ShellSkill extends Skill
{
    public string $name = 'shell';
    public string $description = 'Execute bash commands over a virtual filesystem';
    public string $instructions =
        'You have access to a virtual filesystem via the `exec` tool. '
        . 'Use standard Unix commands: ls, cat, grep, find, head, tail, wc, '
        . 'sort, uniq, cut, sed, jq, tree. Pipes (|) and redirects (>, >>) work. '
        . 'Use `tree /` to see the full file layout.';

    public VirtualFS $fs;
    private ?Shell $shell = null;

    public function __construct(
        array  $files = [],
        private readonly string $initCwd = '/home/user',
        private readonly array  $initEnv = [],
        private readonly ?array $allowedCommands = null,
    ) {
        parent::__construct();
        $this->fs = new VirtualFS($files);
    }

    // -- Convenience methods -----------------------------------------------

    public function write(string $path, string $content): void { $this->fs->write($path, $content); }
    public function writeLazy(string $path, callable $provider): void { $this->fs->writeLazy($path, $provider); }

    public function mountDir(string $prefix, array $files): void
    {
        foreach ($files as $name => $content) {
            $this->fs->write("{$prefix}/{$name}", $content);
        }
    }

    public function mountJson(string $path, mixed $data): void
    {
        $this->fs->write($path, json_encode($data, JSON_PRETTY_PRINT));
    }

    // -- Skill lifecycle ---------------------------------------------------

    public function setup(SkillContext $ctx): void
    {
        $this->shell = new Shell(
            $this->fs,
            $this->initCwd,
            $this->initEnv,
            $this->allowedCommands,
        );
    }

    public function tools(): array
    {
        $shell = &$this->shell;

        return [
            ToolDef::make(
                name: 'exec',
                description:
                    'Execute a bash command. Supports ls, cat, grep, find, head, tail, '
                    . 'wc, sort, uniq, cut, sed, jq, tree, cp, rm, mkdir, touch, tee, '
                    . 'cd, pwd, tr, echo, stat. Pipes (|) and redirects (>, >>) work.',
                parameters: [
                    'type' => 'object',
                    'properties' => [
                        'command' => ['type' => 'string', 'description' => 'The bash command to run'],
                    ],
                    'required' => ['command'],
                ],
                execute: function (array $args) use (&$shell): string {
                    $result = $shell->exec($args['command']);
                    $parts = [];
                    if ($result->stdout !== '') $parts[] = $result->stdout;
                    if ($result->stderr !== '') $parts[] = "[stderr] {$result->stderr}";
                    if ($result->exitCode !== 0) $parts[] = "[exit code: {$result->exitCode}]";
                    return implode('', $parts) ?: '(no output)';
                },
            ),
        ];
    }
}

// ===========================================================================
// Demo
// ===========================================================================

if (php_sapi_name() === 'cli' && realpath($argv[0] ?? '') === realpath(__FILE__)) {

    $skill = new ShellSkill();
    $skill->write('/data/users.json', json_encode([
        ['name' => 'Alice', 'role' => 'admin', 'active' => true],
        ['name' => 'Bob', 'role' => 'user', 'active' => true],
        ['name' => 'Charlie', 'role' => 'user', 'active' => false],
        ['name' => 'Diana', 'role' => 'admin', 'active' => true],
    ], JSON_PRETTY_PRINT));
    $skill->write('/data/config.yaml', "database:\n  host: localhost\n  port: 5432\n  name: mydb\n");
    $skill->write('/docs/README.md', "# My Project\n\nThis is a test project.\n\n## Features\n- Fast\n- Reliable\n");

    // Direct shell test (no LLM needed)
    $fs = $skill->fs;
    $shell = new Shell($fs);

    $tests = [
        'tree /',
        'cat /data/users.json | jq ".[]"',
        'grep admin /data/users.json',
        'grep -c active /data/users.json',
        'cat /data/config.yaml | grep port',
        'find / -name "*.json"',
        'wc -l /docs/README.md',
        'head -3 /docs/README.md',
        'echo hello > /tmp/test.txt; cat /tmp/test.txt',
    ];

    foreach ($tests as $cmd) {
        echo "=== {$cmd} ===\n";
        $r = $shell->exec($cmd);
        echo $r->stdout;
        if ($r->stderr) echo "[stderr] {$r->stderr}";
        echo "\n";
    }
}
