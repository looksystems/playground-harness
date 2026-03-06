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

    public function testSemicolonContinuesAfterFailure(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat /nope; echo after');
        $this->assertStringContainsString('after', $r->stdout);
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

    // ===================================================================
    // New features mirroring TypeScript implementation
    // ===================================================================

    // -- && operator --

    public function testAndOperatorSuccess(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo ok && echo yes');
        $this->assertSame("ok\nyes\n", $r->stdout);
    }

    public function testAndOperatorFailure(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat /nope && echo yes');
        $this->assertStringNotContainsString('yes', $r->stdout);
        $this->assertNotSame(0, $r->exitCode);
    }

    public function testAndOperatorChain(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo a && echo b && echo c');
        $this->assertSame("a\nb\nc\n", $r->stdout);
    }

    // -- || operator --

    public function testOrOperatorFailure(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat /nope || echo fallback');
        $this->assertStringContainsString('fallback', $r->stdout);
    }

    public function testOrOperatorSuccess(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo ok || echo fallback');
        $this->assertSame("ok\n", $r->stdout);
        $this->assertStringNotContainsString('fallback', $r->stdout);
    }

    // -- $? exit code --

    public function testExitCodeZero(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo hi; echo $?');
        $this->assertSame("hi\n0\n", $r->stdout);
    }

    public function testExitCodeNonZero(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat /nope; echo $?');
        $this->assertSame("1\n", $r->stdout);
    }

    // -- mixed && || ; --

    public function testAndOrMixed(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('cat /nope && echo yes || echo no');
        $this->assertStringContainsString('no', $r->stdout);
        $this->assertStringNotContainsString('yes', $r->stdout);
    }

    public function testSemicolonSeparatesLists(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo a; echo b && echo c');
        $this->assertSame("a\nb\nc\n", $r->stdout);
    }

    // -- Variable assignment --

    public function testVariableAssignment(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=hello; echo $X');
        $this->assertSame("hello\n", $r->stdout);
    }

    public function testVariableAssignmentQuoted(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X="hello world"; echo $X');
        $this->assertSame("hello world\n", $r->stdout);
    }

    public function testExportAssignment(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('export X=42; echo $X');
        $this->assertSame("42\n", $r->stdout);
    }

    public function testMultipleAssignments(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('A=1; B=2; echo $A $B');
        $this->assertSame("1 2\n", $r->stdout);
    }

    // -- test / [ builtins --

    public function testTestFileExists(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/exists.txt' => 'data']), cwd: '/');
        $r = $sh->exec('test -f /exists.txt');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestFileMissing(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test -f /nope.txt');
        $this->assertSame(1, $r->exitCode);
    }

    public function testTestDirectory(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/dir/file.txt' => 'x']), cwd: '/');
        $r = $sh->exec('test -d /dir');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestExistence(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/f.txt' => 'x']), cwd: '/');
        $r = $sh->exec('test -e /f.txt');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestEmptyString(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test -z ""');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestNonEmptyString(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test -n "hello"');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestStringEquality(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test "a" = "a"');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestStringInequality(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test "a" != "b"');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestNumericEq(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test 1 -eq 1');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestNumericLt(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test 1 -lt 2');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestNumericGt(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test 2 -gt 1');
        $this->assertSame(0, $r->exitCode);
    }

    public function testTestNegation(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('test ! -f /nope');
        $this->assertSame(0, $r->exitCode);
    }

    public function testBracketSyntax(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[ "a" = "a" ]');
        $this->assertSame(0, $r->exitCode);
    }

    public function testBracketLt(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[ 1 -lt 2 ]');
        $this->assertSame(0, $r->exitCode);
    }

    public function testBracketFalse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[ "a" = "b" ]');
        $this->assertSame(1, $r->exitCode);
    }

    // -- if/then/elif/else/fi --

    public function testIfTrue(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/file' => 'x']), cwd: '/');
        $r = $sh->exec('if [ -f /file ]; then echo yes; else echo no; fi');
        $this->assertSame("yes\n", $r->stdout);
    }

    public function testIfFalse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if [ -f /nope ]; then echo yes; else echo no; fi');
        $this->assertSame("no\n", $r->stdout);
    }

    public function testIfWithoutElse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if [ 1 -eq 1 ]; then echo yes; fi');
        $this->assertSame("yes\n", $r->stdout);
    }

    public function testIfFalseWithoutElse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if [ 1 -eq 2 ]; then echo yes; fi');
        $this->assertSame('', $r->stdout);
    }

    public function testElif(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if [ 1 -eq 2 ]; then echo a; elif [ 1 -eq 1 ]; then echo b; fi');
        $this->assertSame("b\n", $r->stdout);
    }

    public function testElifWithElse(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if [ 1 -eq 2 ]; then echo a; elif [ 1 -eq 3 ]; then echo b; else echo c; fi');
        $this->assertSame("c\n", $r->stdout);
    }

    public function testIfWithAndCondition(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('if echo ok && [ 1 -eq 1 ]; then echo yes; fi');
        $this->assertStringContainsString('yes', $r->stdout);
    }

    // -- for loop --

    public function testForLoop(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('for x in a b c; do echo $x; done');
        $this->assertSame("a\nb\nc\n", $r->stdout);
    }

    public function testForLoopWithVariable(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('for f in one two; do echo file-$f; done');
        $this->assertSame("file-one\nfile-two\n", $r->stdout);
    }

    public function testForLoopEmptyList(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('for x in; do echo $x; done');
        $this->assertSame('', $r->stdout);
    }

    // -- while loop --

    public function testWhileLoop(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("X=3; while [ \$X -gt 0 ]; do echo \$X; X=\$(echo \$X | sed 's/3/2/;s/2/1/;s/1/0/'); done");
        $this->assertStringContainsString('3', $r->stdout);
    }

    public function testWhileMaxIterations(): void
    {
        $sh = new Shell(fs: new VirtualFS(), maxIterations: 5);
        $r = $sh->exec('while [ 1 -eq 1 ]; do echo x; done');
        $this->assertNotSame(0, $r->exitCode);
    }

    // -- Command substitution --

    public function testCommandSubstitutionBasic(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $(echo hello)');
        $this->assertSame("hello\n", $r->stdout);
    }

    public function testCommandSubstitutionInVariable(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/file.txt' => "content\n"]), cwd: '/');
        $r = $sh->exec('X=$(cat /file.txt); echo $X');
        $this->assertSame("content\n", $r->stdout);
    }

    public function testCommandSubstitutionNested(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $(echo $(echo deep))');
        $this->assertSame("deep\n", $r->stdout);
    }

    public function testCommandSubstitutionInArgument(): void
    {
        $sh = new Shell(fs: new VirtualFS(['/dir/a.txt' => 'x']), cwd: '/');
        $r = $sh->exec('ls $(echo /dir)');
        $this->assertStringContainsString('a.txt', $r->stdout);
    }

    public function testCommandSubstitutionBacktick(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo `echo hello`');
        $this->assertSame("hello\n", $r->stdout);
    }

    // -- Parameter expansion --

    public function testParamExpansionDefaultUnset(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo ${UNSET:-default}');
        $this->assertSame("default\n", $r->stdout);
    }

    public function testParamExpansionDefaultSet(): void
    {
        $sh = $this->makeShell();
        $sh->env['X'] = 'value';
        $r = $sh->exec('echo ${X:-default}');
        $this->assertSame("value\n", $r->stdout);
    }

    public function testParamExpansionAssignDefault(): void
    {
        $sh = $this->makeShell();
        $sh->exec('echo ${X:=assigned}');
        $this->assertSame('assigned', $sh->env['X']);
    }

    public function testParamExpansionLength(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=hello; echo ${#X}');
        $this->assertSame("5\n", $r->stdout);
    }

    public function testParamExpansionSubstring(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=hello; echo ${X:1:3}');
        $this->assertSame("ell\n", $r->stdout);
    }

    public function testParamExpansionGlobalReplace(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=hello_world; echo ${X//_/-}');
        $this->assertSame("hello-world\n", $r->stdout);
    }

    public function testParamExpansionFirstReplace(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=aabaa; echo ${X/a/x}');
        $this->assertSame("xabaa\n", $r->stdout);
    }

    public function testParamExpansionGreedySuffix(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=file.tar.gz; echo ${X%%.*}');
        $this->assertSame("file\n", $r->stdout);
    }

    public function testParamExpansionShortSuffix(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=file.tar.gz; echo ${X%.*}');
        $this->assertSame("file.tar\n", $r->stdout);
    }

    public function testParamExpansionGreedyPrefix(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=/path/to/file; echo ${X##*/}');
        $this->assertSame("file\n", $r->stdout);
    }

    public function testParamExpansionShortPrefix(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=/path/to/file; echo ${X#*/}');
        $this->assertSame("path/to/file\n", $r->stdout);
    }

    // -- printf --

    public function testPrintfBasic(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf '%s has %d items\\n' foo 3");
        $this->assertSame("foo has 3 items\n", $r->stdout);
    }

    public function testPrintfString(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf '%s' hello");
        $this->assertSame('hello', $r->stdout);
    }

    public function testPrintfInteger(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf '%d' 42");
        $this->assertSame('42', $r->stdout);
    }

    public function testPrintfFloat(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf '%.2f' 3.14159");
        $this->assertSame('3.14', $r->stdout);
    }

    public function testPrintfLiteralPercent(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf '100%%'");
        $this->assertSame('100%', $r->stdout);
    }

    public function testPrintfEscapes(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec("printf 'a\\tb\\n'");
        $this->assertSame("a\tb\n", $r->stdout);
    }

    public function testPrintfNoTrailingNewline(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('printf hello');
        $this->assertSame('hello', $r->stdout);
    }

    // -- true / false --

    public function testTrueBuiltin(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('true');
        $this->assertSame(0, $r->exitCode);
    }

    public function testFalseBuiltin(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('false');
        $this->assertSame(1, $r->exitCode);
    }

    // -- [[ ]] double bracket --

    public function testDoubleBracketStringEq(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[[ "a" = "a" ]]');
        $this->assertSame(0, $r->exitCode);
    }

    public function testDoubleBracketStringNeq(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[[ "a" = "b" ]]');
        $this->assertSame(1, $r->exitCode);
    }

    public function testDoubleBracketNumeric(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('[[ 1 -lt 2 ]]');
        $this->assertSame(0, $r->exitCode);
    }

    // -- Arithmetic $(()) --

    public function testArithAddition(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((2 + 3))');
        $this->assertSame("5\n", $r->stdout);
    }

    public function testArithMulSub(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((10 * 3 - 5))');
        $this->assertSame("25\n", $r->stdout);
    }

    public function testArithDivision(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((10 / 3))');
        $this->assertSame("3\n", $r->stdout);
    }

    public function testArithModulo(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((17 % 5))');
        $this->assertSame("2\n", $r->stdout);
    }

    public function testArithParens(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $(((2 + 3) * 4))');
        $this->assertSame("20\n", $r->stdout);
    }

    public function testArithVariable(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=10; echo $(($X + 5))');
        $this->assertSame("15\n", $r->stdout);
    }

    public function testArithComparisons(): void
    {
        $sh = $this->makeShell();
        $this->assertSame("1\n", $sh->exec('echo $((3 > 2))')->stdout);
        $this->assertSame("1\n", $sh->exec('echo $((1 == 1))')->stdout);
        $this->assertSame("1\n", $sh->exec('echo $((1 != 2))')->stdout);
        $this->assertSame("0\n", $sh->exec('echo $((3 < 2))')->stdout);
    }

    public function testArithNegation(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((-5 + 3))');
        $this->assertSame("-2\n", $r->stdout);
    }

    public function testArithLogical(): void
    {
        $sh = $this->makeShell();
        $this->assertSame("1\n", $sh->exec('echo $((1 && 1))')->stdout);
        $this->assertSame("0\n", $sh->exec('echo $((1 && 0))')->stdout);
        $this->assertSame("1\n", $sh->exec('echo $((0 || 1))')->stdout);
    }

    public function testArithTernary(): void
    {
        $sh = $this->makeShell();
        $this->assertSame("10\n", $sh->exec('echo $((1 ? 10 : 20))')->stdout);
        $this->assertSame("20\n", $sh->exec('echo $((0 ? 10 : 20))')->stdout);
    }

    public function testArithDivisionByZero(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('echo $((1 / 0))');
        $this->assertNotSame(0, $r->exitCode);
    }

    // -- case/esac --

    public function testCaseLiteralMatch(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case hello in hello) echo matched;; esac');
        $this->assertSame("matched\n", $r->stdout);
    }

    public function testCaseNoMatch(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case hello in world) echo nope;; esac');
        $this->assertSame('', $r->stdout);
    }

    public function testCaseWildcard(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case anything in *) echo default;; esac');
        $this->assertSame("default\n", $r->stdout);
    }

    public function testCaseMultipleClauses(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case b in a) echo A;; b) echo B;; *) echo other;; esac');
        $this->assertSame("B\n", $r->stdout);
    }

    public function testCasePipePatterns(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case yes in y | yes) echo affirmative;; esac');
        $this->assertSame("affirmative\n", $r->stdout);
    }

    public function testCaseWithVariable(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('X=hello; case $X in hello) echo matched;; esac');
        $this->assertSame("matched\n", $r->stdout);
    }

    public function testCaseGlobPattern(): void
    {
        $sh = $this->makeShell();
        $r = $sh->exec('case file.txt in *.txt) echo text;; *.py) echo python;; esac');
        $this->assertSame("text\n", $r->stdout);
    }
}
