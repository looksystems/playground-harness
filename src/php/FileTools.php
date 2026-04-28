<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Built-in file tools: Read, Write, Edit, Glob, Grep.
 *
 * Mirrors Claude Code's file toolset: parameter names and observable behavior
 * match closely. All five tools delegate to the agent's FilesystemDriver, so
 * swapping the shell backend (builtin / bashkit / OpenShell) automatically
 * swaps the filesystem they see.
 */
class FileTools
{
    /** @var array<string, list<string>> */
    private const TYPE_MAP = [
        'py'   => ['.py'],
        'ts'   => ['.ts', '.tsx'],
        'js'   => ['.js', '.jsx', '.mjs', '.cjs'],
        'php'  => ['.php'],
        'go'   => ['.go'],
        'rs'   => ['.rs'],
        'java' => ['.java'],
        'rb'   => ['.rb'],
        'md'   => ['.md', '.markdown'],
        'txt'  => ['.txt'],
        'json' => ['.json'],
        'yaml' => ['.yaml', '.yml'],
        'sh'   => ['.sh', '.bash'],
        'html' => ['.html', '.htm'],
        'css'  => ['.css'],
        'sql'  => ['.sql'],
    ];

    /** @var array<string, true> */
    private array $readSet = [];

    public function __construct(
        private readonly object $agent,
        private readonly bool $enforceReadGate = false,
    ) {
    }

    /**
     * Register the five built-in file tools on $agent.
     * Returns the FileTools instance so callers can inspect its state.
     */
    public static function registerAll(object $agent, bool $enforceReadGate = false): self
    {
        $self = new self($agent, $enforceReadGate);
        $self->register();
        return $self;
    }

    public function register(): void
    {
        foreach ($this->buildTools() as $tool) {
            $this->agent->registerTool($tool);
        }
    }

    /** @return list<ToolDef> */
    private function buildTools(): array
    {
        return [
            ToolDef::make(
                'Read',
                'Read a file from the virtual filesystem. Returns content with `cat -n` '
                . 'style line numbers (1-indexed). Defaults to the first 2000 lines. Use '
                . '`offset` (line number to start from, 1-indexed) and `limit` to page '
                . 'through larger files. Binary files (containing null bytes in the first '
                . '8KB) are rejected with an error.',
                [
                    'type' => 'object',
                    'properties' => [
                        'file_path' => ['type' => 'string', 'description' => 'Absolute path of the file to read.'],
                        'offset' => ['type' => 'integer', 'description' => 'Line number to start from (1-indexed). Default 1.'],
                        'limit' => ['type' => 'integer', 'description' => 'Maximum number of lines to return. Default 2000.'],
                    ],
                    'required' => ['file_path'],
                ],
                fn (array $args): string => $this->readFile($args),
            ),
            ToolDef::make(
                'Write',
                "Write content to a file, creating it if it doesn't exist or overwriting if it does.",
                [
                    'type' => 'object',
                    'properties' => [
                        'file_path' => ['type' => 'string', 'description' => 'Absolute path of the file to write.'],
                        'content' => ['type' => 'string', 'description' => 'The content to write.'],
                    ],
                    'required' => ['file_path', 'content'],
                ],
                fn (array $args): string => $this->writeFile($args),
            ),
            ToolDef::make(
                'Edit',
                'Replace exact strings in a file. Errors if `old_string` is not found, '
                . 'or appears more than once when `replace_all` is false (the default). '
                . 'When the read-gate is enabled at registration time, errors if the file '
                . 'has not been Read in this session.',
                [
                    'type' => 'object',
                    'properties' => [
                        'file_path' => ['type' => 'string', 'description' => 'Absolute path of the file to edit.'],
                        'old_string' => ['type' => 'string', 'description' => 'The exact string to replace.'],
                        'new_string' => ['type' => 'string', 'description' => 'The replacement string.'],
                        'replace_all' => ['type' => 'boolean', 'description' => 'Replace all occurrences. Default false.'],
                    ],
                    'required' => ['file_path', 'old_string', 'new_string'],
                ],
                fn (array $args): string => $this->editFile($args),
            ),
            ToolDef::make(
                'Glob',
                'Match files by pattern (supports `*`, `?`, and recursive `**`) and return '
                . 'absolute paths sorted by modification time, newest first. `path` defaults '
                . "to the shell's current working directory.",
                [
                    'type' => 'object',
                    'properties' => [
                        'pattern' => ['type' => 'string', 'description' => 'Glob pattern, e.g. `**/*.php`.'],
                        'path' => ['type' => 'string', 'description' => 'Directory to search in. Defaults to cwd.'],
                    ],
                    'required' => ['pattern'],
                ],
                fn (array $args): array => $this->globFiles($args),
            ),
            ToolDef::make(
                'Grep',
                'Search file contents with a regular expression (PCRE without delimiters). '
                . 'Recursively walks `path` (default cwd) and matches against each file\'s content. '
                . '`output_mode` controls return shape: `files_with_matches` (default) → list of paths; '
                . '`count` → list of {path, count}; `content` → list of {path, matches} where each match '
                . 'is {line, context: [{line, text, match}]}. Use `glob` or `type` (py, ts, js, php, go, rs, '
                . 'java, rb, md, txt, json, yaml, sh, html, css, sql) to filter the file set. Set `multiline` '
                . 'true to match across line boundaries; `line_numbers` adds line numbers to context entries; '
                . '`context_before`/`context_after` add surrounding lines.',
                [
                    'type' => 'object',
                    'properties' => [
                        'pattern' => ['type' => 'string', 'description' => 'PCRE regex (no delimiters).'],
                        'path' => ['type' => 'string', 'description' => 'Directory to search in. Defaults to cwd.'],
                        'glob' => ['type' => 'string', 'description' => 'Filter the file set by glob pattern.'],
                        'type' => ['type' => 'string', 'description' => 'Filter by built-in file type alias.'],
                        'case_insensitive' => ['type' => 'boolean', 'description' => 'Case-insensitive match. Default false.'],
                        'line_numbers' => ['type' => 'boolean', 'description' => 'Include line numbers in context entries. Default false.'],
                        'output_mode' => ['type' => 'string', 'description' => 'files_with_matches (default), count, or content.'],
                        'head_limit' => ['type' => 'integer', 'description' => 'Return only the first N results.'],
                        'multiline' => ['type' => 'boolean', 'description' => 'Allow patterns to match across line boundaries. Default false.'],
                        'context_before' => ['type' => 'integer', 'description' => 'Lines of context before each match. Default 0.'],
                        'context_after' => ['type' => 'integer', 'description' => 'Lines of context after each match. Default 0.'],
                    ],
                    'required' => ['pattern'],
                ],
                fn (array $args): array => $this->grepFiles($args),
            ),
        ];
    }

    // ---------------------------------------------------------------------
    // Tool implementations
    // ---------------------------------------------------------------------

    private function readFile(array $args): string
    {
        $path = self::norm($args['file_path']);
        $fs = $this->fs();
        if (!$fs->exists($path)) {
            throw new \RuntimeException("File does not exist: {$path}");
        }
        if ($fs->isDir($path)) {
            throw new \RuntimeException("Path is a directory, not a file: {$path}");
        }
        $content = $fs->read($path);
        if (self::isBinary($content)) {
            throw new \RuntimeException('binary file not supported by Read; use a different tool');
        }
        $this->readSet[$path] = true;
        $offset = (int) ($args['offset'] ?? 1);
        $limit = (int) ($args['limit'] ?? 2000);
        return self::formatLines($content, $offset, $limit);
    }

    private function writeFile(array $args): string
    {
        $path = self::norm($args['file_path']);
        $this->fs()->write($path, $args['content']);
        $this->readSet[$path] = true;
        return "File written: {$path}";
    }

    private function editFile(array $args): string
    {
        $path = self::norm($args['file_path']);
        $fs = $this->fs();
        if (!$fs->exists($path)) {
            throw new \RuntimeException("File does not exist: {$path}");
        }
        if ($this->enforceReadGate && !isset($this->readSet[$path])) {
            throw new \RuntimeException('File has not been read yet. Read it first before writing to it.');
        }
        $text = $fs->read($path);
        $oldStr = $args['old_string'];
        $newStr = $args['new_string'];
        $replaceAll = (bool) ($args['replace_all'] ?? false);

        if ($oldStr === '' || strpos($text, $oldStr) === false) {
            throw new \RuntimeException('String to replace not found in file');
        }
        if ($replaceAll) {
            $updated = str_replace($oldStr, $newStr, $text);
        } else {
            $count = substr_count($text, $oldStr);
            if ($count > 1) {
                throw new \RuntimeException(
                    "Found {$count} matches of the string to replace, but replace_all is false. "
                    . 'Provide a larger string with more surrounding context to make it unique, '
                    . 'or set replace_all to true.'
                );
            }
            $updated = self::replaceFirst($text, $oldStr, $newStr);
        }
        $fs->write($path, $updated);
        return "File edited: {$path}";
    }

    /** @return list<string> */
    private function globFiles(array $args): array
    {
        $root = $this->resolveRoot($args['path'] ?? null);
        $files = $this->walk($root);
        $prefix = rtrim($root, '/') . '/';
        $matched = [];
        foreach ($files as $p) {
            $rel = str_starts_with($p, $prefix) ? substr($p, strlen($prefix)) : $p;
            if (self::matchesGlob($rel, $args['pattern'])) {
                $matched[] = $p;
            }
        }
        $fs = $this->fs();
        $mtimes = [];
        foreach ($matched as $p) {
            try {
                $mtimes[$p] = $fs->stat($p)['mtime'] ?? 0;
            } catch (\Throwable) {
                $mtimes[$p] = 0;
            }
        }
        usort($matched, fn (string $a, string $b): int => $mtimes[$b] <=> $mtimes[$a]);
        return $matched;
    }

    /** @return list<mixed> */
    private function grepFiles(array $args): array
    {
        $root = $this->resolveRoot($args['path'] ?? null);
        $pattern = $args['pattern'];
        $caseInsensitive = (bool) ($args['case_insensitive'] ?? false);
        $multiline = (bool) ($args['multiline'] ?? false);
        $delim = '~';
        $modifiers = ($caseInsensitive ? 'i' : '') . ($multiline ? 'sm' : '');
        $regex = $delim . str_replace($delim, '\\' . $delim, $pattern) . $delim . $modifiers;
        $lineModifiers = $caseInsensitive ? 'i' : '';
        $lineRegex = $delim . str_replace($delim, '\\' . $delim, $pattern) . $delim . $lineModifiers;

        $files = $this->walk($root);
        $prefix = rtrim($root, '/') . '/';

        if (!empty($args['glob'])) {
            $files = array_values(array_filter($files, function (string $p) use ($prefix, $args): bool {
                $rel = str_starts_with($p, $prefix) ? substr($p, strlen($prefix)) : $p;
                return self::matchesGlob($rel, $args['glob']);
            }));
        }
        if (!empty($args['type'])) {
            $exts = self::TYPE_MAP[$args['type']] ?? null;
            if ($exts === null) {
                throw new \RuntimeException("Unknown file type: {$args['type']}");
            }
            $files = array_values(array_filter($files, function (string $p) use ($exts): bool {
                foreach ($exts as $e) {
                    if (str_ends_with($p, $e)) {
                        return true;
                    }
                }
                return false;
            }));
        }

        $mode = $args['output_mode'] ?? 'files_with_matches';
        $headLimit = isset($args['head_limit']) ? (int) $args['head_limit'] : null;

        if ($mode === 'files_with_matches') {
            $matched = [];
            foreach ($files as $p) {
                try {
                    $text = $this->fs()->read($p);
                } catch (\Throwable) {
                    continue;
                }
                $hit = false;
                if ($multiline) {
                    $hit = (bool) preg_match($regex, $text);
                } else {
                    foreach (explode("\n", $text) as $line) {
                        if (preg_match($lineRegex, $line)) {
                            $hit = true;
                            break;
                        }
                    }
                }
                if ($hit) {
                    $matched[] = $p;
                }
            }
            return $headLimit !== null ? array_slice($matched, 0, $headLimit) : $matched;
        }

        if ($mode === 'count') {
            $counts = [];
            foreach ($files as $p) {
                try {
                    $text = $this->fs()->read($p);
                } catch (\Throwable) {
                    continue;
                }
                if ($multiline) {
                    $n = preg_match_all($regex, $text);
                } else {
                    $n = 0;
                    foreach (explode("\n", $text) as $line) {
                        if (preg_match($lineRegex, $line)) {
                            $n++;
                        }
                    }
                }
                if ($n > 0) {
                    $counts[] = ['path' => $p, 'count' => $n];
                }
            }
            return $headLimit !== null ? array_slice($counts, 0, $headLimit) : $counts;
        }

        if ($mode === 'content') {
            $results = [];
            $ctxBefore = (int) ($args['context_before'] ?? 0);
            $ctxAfter = (int) ($args['context_after'] ?? 0);
            $lineNumbers = (bool) ($args['line_numbers'] ?? false);
            foreach ($files as $p) {
                try {
                    $text = $this->fs()->read($p);
                } catch (\Throwable) {
                    continue;
                }
                $lines = explode("\n", $text);
                $hits = [];
                foreach ($lines as $idx => $line) {
                    if (!preg_match($lineRegex, $line)) {
                        continue;
                    }
                    $start = max(0, $idx - $ctxBefore);
                    $end = min(count($lines), $idx + $ctxAfter + 1);
                    $context = [];
                    for ($j = $start; $j < $end; $j++) {
                        $entry = ['text' => $lines[$j], 'match' => $j === $idx];
                        if ($lineNumbers) {
                            $entry['line'] = $j + 1;
                        }
                        $context[] = $entry;
                    }
                    $hits[] = ['line' => $idx + 1, 'context' => $context];
                }
                if (!empty($hits)) {
                    $results[] = ['path' => $p, 'matches' => $hits];
                }
                if ($headLimit !== null && count($results) >= $headLimit) {
                    break;
                }
            }
            return $results;
        }

        throw new \RuntimeException("Unknown output_mode: {$mode}");
    }

    // ---------------------------------------------------------------------
    // Helpers
    // ---------------------------------------------------------------------

    /** @return array<string, true> */
    public function readSet(): array
    {
        return $this->readSet;
    }

    private function fs(): FilesystemDriver
    {
        /** @var FilesystemDriver $fs */
        $fs = $this->agent->shell()->fs();
        return $fs;
    }

    private function resolveRoot(?string $path): string
    {
        if ($path === null) {
            return self::norm($this->agent->shell()->cwd());
        }
        if (str_starts_with($path, '/')) {
            return self::norm($path);
        }
        return self::norm(rtrim($this->agent->shell()->cwd(), '/') . '/' . $path);
    }

    /** @return list<string> */
    private function walk(string $root): array
    {
        $root = self::norm($root);
        $fs = $this->fs();
        if (!$fs->exists($root)) {
            return [];
        }
        if (!$fs->isDir($root)) {
            return [$root];
        }
        $out = [];
        $stack = [$root];
        while (!empty($stack)) {
            $d = array_pop($stack);
            foreach ($fs->listdir($d) as $entry) {
                if (str_starts_with($entry, '.')) {
                    continue;
                }
                $child = rtrim($d, '/') . '/' . $entry;
                if ($fs->isDir($child)) {
                    $stack[] = $child;
                } else {
                    $out[] = $child;
                }
            }
        }
        return $out;
    }

    private static function norm(string $path): string
    {
        if ($path === '' || $path[0] !== '/') {
            $path = '/' . $path;
        }
        $parts = explode('/', $path);
        $out = [];
        foreach ($parts as $seg) {
            if ($seg === '' || $seg === '.') {
                continue;
            }
            if ($seg === '..') {
                array_pop($out);
                continue;
            }
            $out[] = $seg;
        }
        return '/' . implode('/', $out);
    }

    private static function isBinary(string $content): bool
    {
        $sample = strlen($content) > 8192 ? substr($content, 0, 8192) : $content;
        return strpos($sample, "\x00") !== false;
    }

    private static function formatLines(string $text, int $offset, int $limit): string
    {
        $lines = explode("\n", $text);
        if (!empty($lines) && $lines[count($lines) - 1] === '') {
            array_pop($lines);
        }
        $start = max(1, $offset);
        $end = min(count($lines), $start + $limit - 1);
        $out = [];
        for ($i = $start; $i <= $end; $i++) {
            $out[] = sprintf("%6d\t%s", $i, $lines[$i - 1]);
        }
        return implode("\n", $out);
    }

    private static function replaceFirst(string $haystack, string $needle, string $replacement): string
    {
        $pos = strpos($haystack, $needle);
        if ($pos === false) {
            return $haystack;
        }
        return substr($haystack, 0, $pos) . $replacement . substr($haystack, $pos + strlen($needle));
    }

    private static function matchesGlob(string $relPath, string $pattern): bool
    {
        $regex = self::globToRegex($pattern);
        return (bool) preg_match($regex, $relPath);
    }

    private static function globToRegex(string $pattern): string
    {
        $out = '';
        $i = 0;
        $len = strlen($pattern);
        while ($i < $len) {
            $c = $pattern[$i];
            if ($c === '*' && $i + 1 < $len && $pattern[$i + 1] === '*') {
                $out .= '.*';
                $i += 2;
                if ($i < $len && $pattern[$i] === '/') {
                    $i++;
                }
            } elseif ($c === '*') {
                $out .= '[^/]*';
                $i++;
            } elseif ($c === '?') {
                $out .= '[^/]';
                $i++;
            } else {
                $out .= preg_quote($c, '~');
                $i++;
            }
        }
        return '~^' . $out . '$~';
    }
}
