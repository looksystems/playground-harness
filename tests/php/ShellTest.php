<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\ExecResult;
use AgentHarness\Shell;
use AgentHarness\ShellRegistry;
use AgentHarness\VirtualFS;
use PHPUnit\Framework\TestCase;

class ShellTest extends TestCase
{
    private function makeShell(?VirtualFS $fs = null): Shell
    {
        $fs ??= new VirtualFS([
            '/home/user/hello.txt' => "Hello World\n",
            '/home/user/nums.txt' => "3\n1\n2\n1\n",
            '/home/user/data.json' => '{"name":"test","items":[1,2,3]}',
            '/home/user/src/main.py' => "import os\nprint('hello')\n",
            '/home/user/src/lib/util.py' => "def helper():\n    pass\n",
        ]);
        return new Shell(fs: $fs, cwd: '/home/user');
    }

    // -- Basic execution --

    public function testEmptyCommand(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('');
        $this->assertSame('', $r->stdout);
        $this->assertSame(0, $r->exitCode);
    }

    public function testUnknownCommand(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('nonexistent');
        $this->assertSame(127, $r->exitCode);
        $this->assertStringContainsString('command not found', $r->stderr);
    }

    // -- echo --

    public function testEcho(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo hello world');
        $this->assertSame("hello world\n", $r->stdout);
    }

    // -- pwd / cd --

    public function testPwd(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('pwd');
        $this->assertSame("/home/user\n", $r->stdout);
    }

    public function testCd(): void
    {
        $sh = $this->makeShell();
        $sh->exec('cd src');
        $r = $sh->exec('pwd');
        $this->assertSame("/home/user/src\n", $r->stdout);
    }

    public function testCdNonexistent(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cd nope');
        $this->assertSame(1, $r->exitCode);
    }

    // -- cat --

    public function testCat(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat hello.txt');
        $this->assertSame("Hello World\n", $r->stdout);
    }

    public function testCatNonexistent(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat nope.txt');
        $this->assertSame(1, $r->exitCode);
    }

    public function testCatStdin(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo hello | cat');
        $this->assertSame("hello\n", $r->stdout);
    }

    // -- ls --

    public function testLs(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('ls');
        $this->assertStringContainsString('hello.txt', $r->stdout);
        $this->assertStringContainsString('src', $r->stdout);
    }

    public function testLsLong(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('ls -l');
        $this->assertStringContainsString('-rw-r--r--', $r->stdout);
        $this->assertStringContainsString('drwxr-xr-x', $r->stdout);
    }

    // -- find --

    public function testFind(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('find . -name "*.py"');
        $this->assertStringContainsString('main.py', $r->stdout);
        $this->assertStringContainsString('util.py', $r->stdout);
    }

    public function testFindTypeF(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('find . -type f -name "*.py"');
        $this->assertStringContainsString('main.py', $r->stdout);
    }

    // -- grep --

    public function testGrep(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep World hello.txt');
        $this->assertStringContainsString('Hello World', $r->stdout);
    }

    public function testGrepNoMatch(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep Nope hello.txt');
        $this->assertSame(1, $r->exitCode);
    }

    public function testGrepCaseInsensitive(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep -i world hello.txt');
        $this->assertStringContainsString('Hello World', $r->stdout);
    }

    public function testGrepLineNumbers(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep -n Hello hello.txt');
        $this->assertStringContainsString('1:', $r->stdout);
    }

    public function testGrepCount(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep -c Hello hello.txt');
        $this->assertSame("1\n", $r->stdout);
    }

    public function testGrepRecursive(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep -r hello src');
        $this->assertStringContainsString('hello', $r->stdout);
    }

    public function testGrepInvert(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo -e "aaa\nbbb\nccc" | grep -v bbb');
        // echo doesn't interpret -e, it just prints it
        $r2 = $sh->exec('echo "line1" | grep -v nomatch');
        $this->assertSame(0, $r2->exitCode);
    }

    public function testGrepFilenames(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('grep -rl hello src');
        $this->assertStringContainsString('main.py', $r->stdout);
    }

    public function testGrepStdin(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello world" | grep hello');
        $this->assertStringContainsString('hello', $r->stdout);
    }

    // -- head / tail --

    public function testHead(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('head -n 1 nums.txt');
        $this->assertSame("3\n", $r->stdout);
    }

    public function testTail(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('tail -n 2 nums.txt');
        $lines = array_filter(explode("\n", $r->stdout), fn($l) => $l !== '');
        $this->assertCount(2, $lines);
    }

    public function testHeadStdin(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat nums.txt | head -n 2');
        $lines = array_filter(explode("\n", $r->stdout), fn($l) => $l !== '');
        $this->assertCount(2, $lines);
    }

    // -- wc --

    public function testWcLines(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('wc -l nums.txt');
        $this->assertSame("4\n", $r->stdout);
    }

    public function testWcWords(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('wc -w hello.txt');
        $this->assertSame("2\n", $r->stdout);
    }

    public function testWcFull(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('wc hello.txt');
        $this->assertStringContainsString('hello.txt', $r->stdout);
    }

    // -- sort --

    public function testSort(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort nums.txt');
        $this->assertSame("1\n1\n2\n3\n", $r->stdout);
    }

    public function testSortReverse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort -r nums.txt');
        $this->assertSame("3\n2\n1\n1\n", $r->stdout);
    }

    public function testSortNumeric(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort -n nums.txt');
        $this->assertSame("1\n1\n2\n3\n", $r->stdout);
    }

    public function testSortUnique(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort -u nums.txt');
        $this->assertStringNotContainsString("1\n1", $r->stdout);
    }

    // -- uniq --

    public function testUniq(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort nums.txt | uniq');
        $lines = array_filter(explode("\n", $r->stdout), fn($l) => $l !== '');
        $this->assertCount(3, $lines);
    }

    public function testUniqCount(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('sort nums.txt | uniq -c');
        $this->assertStringContainsString('2 1', $r->stdout);
    }

    // -- cut --

    public function testCut(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "a:b:c" | cut -d : -f 2');
        $this->assertSame("b\n", $r->stdout);
    }

    // -- tr --

    public function testTrTranslate(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello" | tr lo LO');
        $this->assertStringContainsString('heLLO', $r->stdout);
    }

    public function testTrDelete(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello" | tr -d l');
        $this->assertStringContainsString('heo', $r->stdout);
    }

    // -- sed --

    public function testSed(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello world" | sed "s/world/php/"');
        $this->assertStringContainsString('hello php', $r->stdout);
    }

    public function testSedGlobal(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "aaa" | sed "s/a/b/g"');
        $this->assertStringContainsString('bbb', $r->stdout);
    }

    // -- touch / mkdir --

    public function testTouch(): void
    {
        $sh = $this->makeShell();
        $sh->exec('touch newfile.txt');
        $this->assertTrue($sh->fs->exists('/home/user/newfile.txt'));
    }

    public function testMkdir(): void
    {
        $sh = $this->makeShell();
        $sh->exec('mkdir newdir');
        $this->assertTrue($sh->fs->isDir('/home/user/newdir'));
    }

    // -- cp / rm --

    public function testCp(): void
    {
        $sh = $this->makeShell();
        $sh->exec('cp hello.txt copy.txt');
        $this->assertSame("Hello World\n", $sh->fs->read('/home/user/copy.txt'));
    }

    public function testRm(): void
    {
        $sh = $this->makeShell();
        $sh->exec('rm hello.txt');
        $this->assertFalse($sh->fs->exists('/home/user/hello.txt'));
    }

    // -- stat --

    public function testStat(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('stat hello.txt');
        $decoded = json_decode($r->stdout, true);
        $this->assertSame('file', $decoded['type']);
    }

    // -- tree --

    public function testTree(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('tree src');
        $this->assertStringContainsString('main.py', $r->stdout);
        $this->assertStringContainsString('lib', $r->stdout);
    }

    // -- tee --

    public function testTee(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello" | tee output.txt');
        $this->assertSame("hello\n", $r->stdout);
        $this->assertSame("hello\n", $sh->fs->read('/home/user/output.txt'));
    }

    // -- jq --

    public function testJqIdentity(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat data.json | jq .');
        $decoded = json_decode($r->stdout, true);
        $this->assertSame('test', $decoded['name']);
    }

    public function testJqField(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat data.json | jq -r .name');
        $this->assertSame("test\n", $r->stdout);
    }

    public function testJqArray(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat data.json | jq ".items[]"');
        $this->assertStringContainsString('1', $r->stdout);
        $this->assertStringContainsString('2', $r->stdout);
    }

    public function testJqIndex(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat data.json | jq ".items[0]"');
        $this->assertStringContainsString('1', $r->stdout);
    }

    public function testJqInvalidJson(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "not json" | jq .');
        $this->assertSame(2, $r->exitCode);
    }

    // -- Pipes --

    public function testPipe(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat nums.txt | sort | head -n 2');
        $lines = array_filter(explode("\n", $r->stdout), fn($l) => $l !== '');
        $this->assertCount(2, $lines);
        $this->assertSame('1', $lines[0]);
    }

    // -- Redirects --

    public function testRedirect(): void
    {
        $sh = $this->makeShell();
        $sh->exec('echo "test" > output.txt');
        $this->assertSame("test\n", $sh->fs->read('/home/user/output.txt'));
    }

    public function testRedirectAppend(): void
    {
        $sh = $this->makeShell();
        $sh->exec('echo "line1" > output.txt');
        $sh->exec('echo "line2" >> output.txt');
        $content = $sh->fs->read('/home/user/output.txt');
        $this->assertStringContainsString('line1', $content);
        $this->assertStringContainsString('line2', $content);
    }

    // -- Semicolons --

    public function testSemicolon(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "a"; echo "b"');
        $this->assertSame("a\nb\n", $r->stdout);
    }

    // -- Env vars --

    public function testEnvVars(): void
    {
        $sh = $this->makeShell();
        $sh->env['NAME'] = 'World';
        $r = $sh->exec('echo Hello $NAME');
        $this->assertSame("Hello World\n", $r->stdout);
    }

    public function testEnvVarsBraces(): void
    {
        $sh = $this->makeShell();
        $sh->env['X'] = 'val';
        $r = $sh->exec('echo ${X}');
        $this->assertSame("val\n", $r->stdout);
    }

    // -- Allowed commands --

    public function testAllowedCommands(): void
    {
        $sh = new Shell(fs: new VirtualFS(), allowedCommands: ['echo', 'pwd']);
        $r = $sh->exec('echo hi');
        $this->assertSame("hi\n", $r->stdout);
        $r = $sh->exec('cat /etc/passwd');
        $this->assertSame(127, $r->exitCode);
    }

    // -- Truncation --

    public function testTruncation(): void
    {
        $fs = new VirtualFS();
        $fs->write('/big.txt', str_repeat('x', 20000));
        $sh = new Shell(fs: $fs, maxOutput: 100);
        $r = $sh->exec('cat /big.txt');
        $this->assertStringContainsString('truncated', $r->stdout);
    }

    // -- Clone --

    public function testClone(): void
    {
        $sh = $this->makeShell();
        $clone = $sh->cloneShell();
        $clone->exec('touch cloned.txt');
        $this->assertFalse($sh->fs->exists('/home/user/cloned.txt'));
        $this->assertTrue($clone->fs->exists('/home/user/cloned.txt'));
    }

    // -- ShellRegistry --

    protected function tearDown(): void
    {
        ShellRegistry::reset();
    }

    public function testRegistryRegisterAndGet(): void
    {
        $sh = $this->makeShell();
        ShellRegistry::register('test', $sh);
        $this->assertTrue(ShellRegistry::has('test'));
        $clone = ShellRegistry::get('test');
        $clone->exec('touch new.txt');
        // Original should be unmodified
        $this->assertFalse($sh->fs->exists('/home/user/new.txt'));
    }

    public function testRegistryGetReturnsClone(): void
    {
        ShellRegistry::register('test', $this->makeShell());
        $a = ShellRegistry::get('test');
        $b = ShellRegistry::get('test');
        $a->exec('touch a.txt');
        $this->assertFalse($b->fs->exists('/home/user/a.txt'));
    }

    public function testRegistryGetNonexistent(): void
    {
        $this->expectException(\RuntimeException::class);
        ShellRegistry::get('nope');
    }

    public function testRegistryRemove(): void
    {
        ShellRegistry::register('test', $this->makeShell());
        ShellRegistry::remove('test');
        $this->assertFalse(ShellRegistry::has('test'));
    }

    public function testRegistryReset(): void
    {
        ShellRegistry::register('a', $this->makeShell());
        ShellRegistry::register('b', $this->makeShell());
        ShellRegistry::reset();
        $this->assertFalse(ShellRegistry::has('a'));
        $this->assertFalse(ShellRegistry::has('b'));
    }

    // -- Quoted arguments --

    public function testQuotedArgs(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo "hello world"');
        $this->assertSame("hello world\n", $r->stdout);
    }

    public function testSingleQuotedArgs(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("echo 'hello world'");
        $this->assertSame("hello world\n", $r->stdout);
    }
}
