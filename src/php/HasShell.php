<?php

declare(strict_types=1);

namespace AgentHarness;

trait HasShell
{
    private ?ShellDriverInterface $shell = null;

    /**
     * Initialize the HasShell trait.
     *
     * @param string|Shell|ShellDriverInterface|null $shell Registry name, Shell instance, driver, or null for default
     * @param string $cwd Working directory
     * @param array<string, string> $env Environment variables
     * @param list<string>|null $allowedCommands Restrict available commands
     * @param bool $registerTool Whether to auto-register the exec tool (requires UsesTools)
     * @param string|null $driver Named driver from ShellDriverFactory
     */
    public function initHasShell(
        string|Shell|ShellDriverInterface|null $shell = null,
        string $cwd = '/home/user',
        array $env = [],
        ?array $allowedCommands = null,
        bool $registerTool = true,
        ?string $driver = null,
    ): void {
        if (is_string($shell)) {
            $this->shell = BuiltinShellDriver::fromShell(ShellRegistry::get($shell));
        } elseif ($shell instanceof ShellDriverInterface) {
            $this->shell = $shell;
        } elseif ($shell instanceof Shell) {
            $this->shell = BuiltinShellDriver::fromShell($shell);
        } else {
            $this->shell = ShellDriverFactory::create($driver, [
                'cwd' => $cwd,
                'env' => $env,
                'allowedCommands' => $allowedCommands,
            ]);
        }

        if (method_exists($this, 'emit')) {
            $self = $this;
            $this->shell->setOnNotFound(function (string $cmdName) use ($self) {
                $self->emit(HookEvent::ShellNotFound, $cmdName);
            });
        }

        // Auto-register exec tool if UsesTools trait is composed (opt-out via registerTool: false)
        if ($registerTool && method_exists($this, 'registerTool')) {
            $this->registerShellTool();
        }
    }

    private function ensureHasShell(): void
    {
        if ($this->shell === null) {
            $this->initHasShell();
        }
    }

    private function registerShellTool(): void
    {
        $self = $this;
        $tool = ToolDef::make(
            name: 'exec',
            description: 'Execute a bash command in the virtual filesystem. '
                . 'Supports: ls, cat, grep, find, head, tail, wc, sort, uniq, '
                . 'cut, sed, jq, tree, cp, rm, mkdir, touch, tee, cd, pwd, tr, echo, stat, '
                . 'test, [, printf, true, false, export. '
                . 'Pipes (|), redirects (>, >>), semicolons (;), && and || operators, '
                . 'variable assignment (VAR=value), command substitution ($() and backticks), '
                . 'parameter expansion (${var:-default}, ${var:=default}, ${#var}, ${var:offset:length}, '
                . '${var//pat/repl}, ${var%%suffix}, ${var%suffix}, ${var##prefix}, ${var#prefix}), '
                . 'if/then/elif/else/fi, for/in/do/done, while/do/done, case/in/esac, '
                . '$((expr)) arithmetic, and $? exit code tracking. '
                . 'Custom commands registered via registerCommand() are also available.',
            parameters: [
                'type' => 'object',
                'properties' => [
                    'command' => [
                        'type' => 'string',
                        'description' => 'The shell command to execute',
                    ],
                ],
                'required' => ['command'],
            ],
            execute: function (array $args) use ($self): string {
                $result = $self->execCommand($args['command']);
                $parts = [];
                if ($result->stdout !== '') {
                    $parts[] = $result->stdout;
                }
                if ($result->stderr !== '') {
                    $parts[] = "[stderr] {$result->stderr}";
                }
                if ($result->exitCode !== 0) {
                    $parts[] = "[exit code: {$result->exitCode}]";
                }
                return implode('', $parts) ?: '(no output)';
            },
        );
        $this->registerTool($tool);
    }

    public function shell(): ShellDriverInterface
    {
        $this->ensureHasShell();
        return $this->shell;
    }

    public function fs(): FilesystemDriver
    {
        return $this->shell()->fs();
    }

    public function execCommand(string $command): ExecResult
    {
        Helpers::tryEmit($this,HookEvent::ShellCall, $command);
        $oldCwd = $this->shell()->cwd();
        $result = $this->shell()->exec($command);
        Helpers::tryEmit($this,HookEvent::ShellResult, $command, $result);
        if ($this->shell()->cwd() !== $oldCwd) {
            Helpers::tryEmit($this,HookEvent::ShellCwd, $oldCwd, $this->shell()->cwd());
        }
        return $result;
    }

    public function registerCommand(string $name, \Closure $handler): static
    {
        $this->shell()->registerCommand($name, $handler);
        Helpers::tryEmit($this, HookEvent::CommandRegister, $name);
        return $this;
    }

    public function unregisterCommand(string $name): static
    {
        $this->shell()->unregisterCommand($name);
        Helpers::tryEmit($this, HookEvent::CommandUnregister, $name);
        return $this;
    }
}
