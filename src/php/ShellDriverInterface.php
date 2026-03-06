<?php

declare(strict_types=1);

namespace AgentHarness;

interface ShellDriverInterface
{
    public function fs(): FilesystemDriver;
    public function cwd(): string;
    /** @return array<string, string> */
    public function env(): array;
    public function exec(string $command): ExecResult;
    public function registerCommand(string $name, \Closure $handler): void;
    public function unregisterCommand(string $name): void;
    public function cloneDriver(): ShellDriverInterface;
    public function setOnNotFound(?\Closure $callback): void;
    public function getOnNotFound(): ?\Closure;
}
