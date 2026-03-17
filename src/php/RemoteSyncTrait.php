<?php

declare(strict_types=1);

namespace AgentHarness;

/**
 * Shared remote sync logic for drivers that execute commands in external processes.
 *
 * Classes using this trait must have a `$fsDriver` property of type DirtyTrackingFS.
 */
trait RemoteSyncTrait
{
    protected function buildSyncPreamble(): string
    {
        $commands = [];
        foreach ($this->fsDriver->getDirty() as $path) {
            if ($this->fsDriver->exists($path) && !$this->fsDriver->isDir($path)) {
                $content = $this->fsDriver->readText($path);
                $encoded = base64_encode($content);
                $commands[] = "mkdir -p \$(dirname '{$path}') && printf '%s' '{$encoded}' | base64 -d > '{$path}'";
            } elseif (!$this->fsDriver->exists($path)) {
                $commands[] = "rm -f '{$path}'";
            }
        }
        $this->fsDriver->clearDirty();
        return count($commands) > 0 ? implode(' && ', $commands) : '';
    }

    protected function buildSyncEpilogue(string $marker, string $root = '/'): string
    {
        return "; __exit=\$?; printf '\\n" . $marker . "\\n';"
            . " find {$root} -type f 2>/dev/null -exec sh -c"
            . " 'for f; do printf \"===FILE:%s===\\n\" \"\$f\"; base64 \"\$f\"; done' _ {} +;"
            . ' exit $__exit';
    }

    /**
     * Parse ===FILE:path=== delimited base64 output into a path->content array.
     *
     * @return array<string, string>
     */
    protected static function parseFileListing(string $syncData): array
    {
        $files = [];
        $fileMarker = '===FILE:';
        $endMarker = '===';
        $currentPath = null;
        $contentLines = [];

        foreach (explode("\n", $syncData) as $line) {
            if (str_starts_with($line, $fileMarker) && str_ends_with($line, $endMarker) && strlen($line) > strlen($fileMarker) + strlen($endMarker)) {
                if ($currentPath !== null) {
                    $encoded = implode('', $contentLines);
                    $decoded = base64_decode($encoded, true);
                    $files[$currentPath] = $decoded !== false ? $decoded : $encoded;
                }
                $currentPath = substr($line, strlen($fileMarker), -(strlen($endMarker)));
                $contentLines = [];
            } elseif ($currentPath !== null) {
                $contentLines[] = $line;
            }
        }
        if ($currentPath !== null) {
            $encoded = implode('', $contentLines);
            $decoded = base64_decode($encoded, true);
            $files[$currentPath] = $decoded !== false ? $decoded : $encoded;
        }

        return $files;
    }

    /**
     * Split marker-delimited output into user stdout and file listing.
     *
     * @return array{stdout: string, files: array<string, string>|null}
     */
    protected function parseSyncOutput(string $raw, string $marker): array
    {
        $markerPos = strpos($raw, "\n{$marker}\n");
        if ($markerPos === false) {
            return ['stdout' => $raw, 'files' => null];
        }
        $stdout = substr($raw, 0, $markerPos);
        $syncData = substr($raw, $markerPos + strlen($marker) + 2);
        $files = self::parseFileListing($syncData);
        return ['stdout' => $stdout, 'files' => $files];
    }

    /**
     * Diff and apply remote file state to local VFS.
     *
     * @param array<string, string> $files
     */
    protected function applySyncBack(array $files): void
    {
        $vfsFiles = [];
        foreach ($this->fsDriver->find('/', '*') as $path) {
            if (!$this->fsDriver->isDir($path)) {
                $vfsFiles[$path] = true;
            }
        }

        foreach ($files as $path => $content) {
            if (!isset($vfsFiles[$path])) {
                $this->fsDriver->inner()->write($path, $content);
            } else {
                $existing = $this->fsDriver->readText($path);
                if ($existing !== $content) {
                    $this->fsDriver->inner()->write($path, $content);
                }
            }
        }

        foreach (array_keys($vfsFiles) as $path) {
            if (!isset($files[$path]) && $this->fsDriver->exists($path)) {
                $this->fsDriver->inner()->remove($path);
            }
        }
    }
}
