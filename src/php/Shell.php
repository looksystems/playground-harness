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

/**
 * Minimal shell interpreter over a VirtualFS.
 * Supports pipes, redirects, and core Unix commands.
 */
class Shell
{
    /** @var array<string, \Closure> */
    private array $builtins;

    /**
     * @param set<string>|null $allowedCommands
     */
    public function __construct(
        public VirtualFS $fs,
        public string $cwd = '/',
        public array $env = [],
        private ?array $allowedCommands = null,
        private int $maxOutput = 16_000,
        private int $maxIterations = 10_000,
    ) {
        $this->builtins = [
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
        ];

        if ($allowedCommands !== null) {
            $this->builtins = array_intersect_key(
                $this->builtins,
                array_flip($allowedCommands)
            );
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

        // Handle command chaining with ;
        if (str_contains($command, ';')) {
            $pos = strpos($command, ';');
            if ($pos !== false && !self::inQuotes($command, $pos)) {
                $results = [];
                foreach (self::splitOn($command, ';') as $part) {
                    $part = trim($part);
                    if ($part === '') {
                        continue;
                    }
                    $r = $this->exec($part);
                    $results[] = $r;
                    if ($r->exitCode !== 0) {
                        break;
                    }
                }
                return new ExecResult(
                    stdout: implode('', array_map(fn(ExecResult $r) => $r->stdout, $results)),
                    stderr: implode('', array_map(fn(ExecResult $r) => $r->stderr, $results)),
                    exitCode: !empty($results) ? end($results)->exitCode : 0,
                );
            }
        }

        // Handle pipes
        $segments = self::splitOn($command, '|');
        $stdin = '';
        $lastResult = new ExecResult();

        foreach ($segments as $seg) {
            $lastResult = $this->execSingle(trim($seg), $stdin);
            $stdin = $lastResult->stdout;
            if ($lastResult->exitCode !== 0) {
                break;
            }
        }

        // Truncate
        if (strlen($lastResult->stdout) > $this->maxOutput) {
            $total = strlen($lastResult->stdout);
            return new ExecResult(
                stdout: substr($lastResult->stdout, 0, $this->maxOutput)
                    . "\n... [truncated, {$total} total chars]",
                stderr: $lastResult->stderr,
                exitCode: $lastResult->exitCode,
            );
        }

        return $lastResult;
    }

    private function execSingle(string $command, string $stdin = ''): ExecResult
    {
        $append = false;
        $redirectPath = null;

        foreach (['>>', '>'] as $op) {
            $pos = strpos($command, $op);
            if ($pos !== false && !self::inQuotes($command, $pos)) {
                $rest = trim(substr($command, $pos + strlen($op)));
                $parts = preg_split('/\s+/', $rest, 2);
                $redirectPath = $parts[0] ?? '';
                $command = trim(substr($command, 0, $pos));
                $append = ($op === '>>');
                break;
            }
        }

        $parts = self::shellSplit($command);
        if (empty($parts)) {
            return new ExecResult();
        }

        $parts = array_map(fn(string $p) => $this->expandVars($p), $parts);
        $cmdName = $parts[0];
        $args = array_slice($parts, 1);

        $handler = $this->builtins[$cmdName] ?? null;
        if ($handler === null) {
            return new ExecResult(stderr: "{$cmdName}: command not found\n", exitCode: 127);
        }

        $result = $handler($args, $stdin);

        if ($redirectPath !== null && $redirectPath !== '') {
            $path = $this->resolve($redirectPath);
            if ($append && $this->fs->exists($path)) {
                $existing = $this->fs->readText($path);
                $this->fs->write($path, $existing . $result->stdout);
            } else {
                $this->fs->write($path, $result->stdout);
            }
            $result = new ExecResult(stderr: $result->stderr, exitCode: $result->exitCode);
        }

        return $result;
    }

    private function expandVars(string $s): string
    {
        return preg_replace_callback('/\$\{(\w+)\}|\$(\w+)/', function (array $m) {
            $name = $m[1] !== '' ? $m[1] : $m[2];
            return $this->env[$name] ?? '';
        }, $s);
    }

    private static function inQuotes(string $s, int $pos): bool
    {
        $inSingle = false;
        $inDouble = false;
        for ($i = 0; $i < $pos; $i++) {
            $c = $s[$i];
            if ($c === "'" && !$inDouble) {
                $inSingle = !$inSingle;
            } elseif ($c === '"' && !$inSingle) {
                $inDouble = !$inDouble;
            }
        }
        return $inSingle || $inDouble;
    }

    /**
     * @return list<string>
     */
    private static function splitOn(string $command, string $sep): array
    {
        $parts = [];
        $current = '';
        $inSingle = false;
        $inDouble = false;
        $i = 0;
        $len = strlen($command);
        $sepLen = strlen($sep);

        while ($i < $len) {
            $c = $command[$i];
            if ($c === "'" && !$inDouble) {
                $inSingle = !$inSingle;
                $current .= $c;
            } elseif ($c === '"' && !$inSingle) {
                $inDouble = !$inDouble;
                $current .= $c;
            } elseif (substr($command, $i, $sepLen) === $sep && !$inSingle && !$inDouble) {
                $parts[] = $current;
                $current = '';
                $i += $sepLen;
                continue;
            } else {
                $current .= $c;
            }
            $i++;
        }
        $parts[] = $current;
        return $parts;
    }

    /**
     * Quote-aware argument tokenization (similar to shlex.split).
     *
     * @return list<string>
     */
    private static function shellSplit(string $s): array
    {
        $tokens = [];
        $current = '';
        $inSingle = false;
        $inDouble = false;
        $i = 0;
        $len = strlen($s);

        while ($i < $len) {
            $c = $s[$i];

            if ($inSingle) {
                if ($c === "'") {
                    $inSingle = false;
                } else {
                    $current .= $c;
                }
            } elseif ($inDouble) {
                if ($c === '"') {
                    $inDouble = false;
                } elseif ($c === '\\' && $i + 1 < $len && in_array($s[$i + 1], ['"', '\\', '$'], true)) {
                    $current .= $s[$i + 1];
                    $i++;
                } else {
                    $current .= $c;
                }
            } elseif ($c === "'") {
                $inSingle = true;
            } elseif ($c === '"') {
                $inDouble = true;
            } elseif ($c === '\\' && $i + 1 < $len) {
                $current .= $s[$i + 1];
                $i++;
            } elseif (ctype_space($c)) {
                if ($current !== '') {
                    $tokens[] = $current;
                    $current = '';
                }
            } else {
                $current .= $c;
            }

            $i++;
        }

        if ($current !== '') {
            $tokens[] = $current;
        }

        return $tokens;
    }

    // -- Built-in commands --------------------------------------------------

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
