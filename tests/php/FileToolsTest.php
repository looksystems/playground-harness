<?php

declare(strict_types=1);

namespace Tests\Php;

use AgentHarness\FileTools;
use AgentHarness\HasShell;
use AgentHarness\UsesTools;
use PHPUnit\Framework\TestCase;

final class _FileToolsAgent
{
    use HasShell;
    use UsesTools;

    public function __construct()
    {
        $this->initHasShell(cwd: '/work');
    }
}

final class FileToolsTest extends TestCase
{
    private function makeAgent(): _FileToolsAgent
    {
        return new _FileToolsAgent();
    }

    private function call(_FileToolsAgent $agent, string $name, array $args): mixed
    {
        return json_decode($agent->executeTool($name, $args), true);
    }

    // ---------- Read ----------

    public function testReadBasic(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.txt', "hello\nworld");
        FileTools::registerAll($a);
        $out = $this->call($a, 'Read', ['file_path' => '/work/a.txt']);
        $this->assertStringContainsString("     1\thello", $out);
        $this->assertStringContainsString("     2\tworld", $out);
    }

    public function testReadOffsetLimit(): void
    {
        $a = $this->makeAgent();
        $lines = [];
        for ($i = 1; $i <= 10; $i++) {
            $lines[] = "line{$i}";
        }
        $a->fs()->write('/work/big.txt', implode("\n", $lines));
        FileTools::registerAll($a);
        $out = $this->call($a, 'Read', ['file_path' => '/work/big.txt', 'offset' => 4, 'limit' => 2]);
        $this->assertStringContainsString("     4\tline4", $out);
        $this->assertStringContainsString("     5\tline5", $out);
        $this->assertStringNotContainsString('line3', $out);
        $this->assertStringNotContainsString('line6', $out);
    }

    public function testReadMissing(): void
    {
        $a = $this->makeAgent();
        FileTools::registerAll($a);
        $out = $this->call($a, 'Read', ['file_path' => '/nope.txt']);
        $this->assertArrayHasKey('error', $out);
        $this->assertStringContainsString('does not exist', $out['error']);
    }

    public function testReadDirectory(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/dir/a.txt', 'x');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Read', ['file_path' => '/work/dir']);
        $this->assertStringContainsString('directory', $out['error']);
    }

    public function testReadBinary(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/blob.bin', "text\x00stuff");
        FileTools::registerAll($a);
        $out = $this->call($a, 'Read', ['file_path' => '/work/blob.bin']);
        $this->assertStringContainsString('binary', $out['error']);
    }

    // ---------- Write ----------

    public function testWriteCreates(): void
    {
        $a = $this->makeAgent();
        FileTools::registerAll($a);
        $out = $this->call($a, 'Write', ['file_path' => '/work/new.txt', 'content' => 'hi']);
        $this->assertStringContainsString('written', $out);
        $this->assertSame('hi', $a->fs()->read('/work/new.txt'));
    }

    public function testWriteOverwrites(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/x.txt', 'old');
        FileTools::registerAll($a);
        $this->call($a, 'Write', ['file_path' => '/work/x.txt', 'content' => 'new']);
        $this->assertSame('new', $a->fs()->read('/work/x.txt'));
    }

    // ---------- Edit ----------

    public function testEditUnique(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'alpha beta gamma');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'beta', 'new_string' => 'BETA',
        ]);
        $this->assertStringContainsString('edited', $out);
        $this->assertSame('alpha BETA gamma', $a->fs()->read('/work/c.txt'));
    }

    public function testEditMissingString(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'hello');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'zzz', 'new_string' => 'qqq',
        ]);
        $this->assertStringContainsString('not found', $out['error']);
    }

    public function testEditMultipleNoReplaceAll(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'x x x');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'x', 'new_string' => 'y',
        ]);
        $this->assertStringContainsString('Found 3 matches', $out['error']);
    }

    public function testEditReplaceAll(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'x x x');
        FileTools::registerAll($a);
        $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'x', 'new_string' => 'y', 'replace_all' => true,
        ]);
        $this->assertSame('y y y', $a->fs()->read('/work/c.txt'));
    }

    public function testEditReadGateOffByDefault(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'hello');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'hello', 'new_string' => 'bye',
        ]);
        $this->assertStringContainsString('edited', $out);
    }

    public function testEditReadGateBlocks(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'hello');
        FileTools::registerAll($a, enforceReadGate: true);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'hello', 'new_string' => 'bye',
        ]);
        $this->assertStringContainsString('not been read', $out['error']);
    }

    public function testEditReadGatePassesAfterRead(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/c.txt', 'hello');
        FileTools::registerAll($a, enforceReadGate: true);
        $this->call($a, 'Read', ['file_path' => '/work/c.txt']);
        $out = $this->call($a, 'Edit', [
            'file_path' => '/work/c.txt', 'old_string' => 'hello', 'new_string' => 'bye',
        ]);
        $this->assertStringContainsString('edited', $out);
        $this->assertSame('bye', $a->fs()->read('/work/c.txt'));
    }

    // ---------- Glob ----------

    public function testGlobBasic(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'x');
        $a->fs()->write('/work/b.php', 'y');
        $a->fs()->write('/work/c.txt', 'z');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Glob', ['pattern' => '*.php']);
        sort($out);
        $this->assertSame(['/work/a.php', '/work/b.php'], $out);
    }

    public function testGlobRecursive(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'x');
        $a->fs()->write('/work/sub/b.php', 'y');
        $a->fs()->write('/work/sub/deeper/c.php', 'z');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Glob', ['pattern' => '**/*.php']);
        sort($out);
        $this->assertSame(['/work/a.php', '/work/sub/b.php', '/work/sub/deeper/c.php'], $out);
    }

    public function testGlobMtimeSort(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/old.php', 'x');
        $a->fs()->write('/work/middle.php', 'y');
        $a->fs()->write('/work/new.php', 'z');
        $a->fs()->write('/work/new.php', 'z2'); // bump mtime
        FileTools::registerAll($a);
        $out = $this->call($a, 'Glob', ['pattern' => '*.php']);
        $this->assertSame('/work/new.php', $out[0]);
    }

    public function testGlobPathDefaultsToCwd(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'x');
        $a->fs()->write('/elsewhere/b.php', 'y');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Glob', ['pattern' => '*.php']);
        $this->assertSame(['/work/a.php'], $out);
    }

    public function testGlobExplicitPath(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'x');
        $a->fs()->write('/elsewhere/b.php', 'y');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Glob', ['pattern' => '*.php', 'path' => '/elsewhere']);
        $this->assertSame(['/elsewhere/b.php'], $out);
    }

    // ---------- Grep ----------

    public function testGrepFilesWithMatches(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', "<?php\necho 'hi';");
        $a->fs()->write('/work/b.php', "echo 'hi';");
        $a->fs()->write('/work/c.php', 'x = 1');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => '^echo']);
        sort($out);
        $this->assertSame(['/work/a.php', '/work/b.php'], $out);
    }

    public function testGrepCount(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', "echo\necho\necho");
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'echo', 'output_mode' => 'count']);
        $this->assertSame([['path' => '/work/a.php', 'count' => 3]], $out);
    }

    public function testGrepContent(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', "alpha\nbeta TARGET here\ngamma");
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', [
            'pattern' => 'TARGET',
            'output_mode' => 'content',
            'line_numbers' => true,
            'context_before' => 1,
            'context_after' => 1,
        ]);
        $this->assertSame('/work/a.php', $out[0]['path']);
        $this->assertSame(2, $out[0]['matches'][0]['line']);
        $this->assertCount(3, $out[0]['matches'][0]['context']);
        $this->assertTrue($out[0]['matches'][0]['context'][1]['match']);
        $this->assertSame('beta TARGET here', $out[0]['matches'][0]['context'][1]['text']);
    }

    public function testGrepGlobFilter(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'echo');
        $a->fs()->write('/work/a.txt', 'echo');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'echo', 'glob' => '*.php']);
        $this->assertSame(['/work/a.php'], $out);
    }

    public function testGrepTypeFilter(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.php', 'echo');
        $a->fs()->write('/work/a.go', 'echo');
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'echo', 'type' => 'go']);
        $this->assertSame(['/work/a.go'], $out);
    }

    public function testGrepCaseInsensitive(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.txt', 'Hello WORLD');
        FileTools::registerAll($a);
        $this->assertSame([], $this->call($a, 'Grep', ['pattern' => 'world']));
        $this->assertSame(['/work/a.txt'], $this->call($a, 'Grep', ['pattern' => 'world', 'case_insensitive' => true]));
    }

    public function testGrepMultiline(): void
    {
        $a = $this->makeAgent();
        $a->fs()->write('/work/a.txt', "alpha\nbeta\ngamma");
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'alpha.*gamma', 'multiline' => true]);
        $this->assertSame(['/work/a.txt'], $out);
    }

    public function testGrepHeadLimit(): void
    {
        $a = $this->makeAgent();
        for ($i = 0; $i < 5; $i++) {
            $a->fs()->write("/work/f{$i}.txt", 'match');
        }
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'match', 'head_limit' => 2]);
        $this->assertCount(2, $out);
    }

    public function testGrepUnknownType(): void
    {
        $a = $this->makeAgent();
        FileTools::registerAll($a);
        $out = $this->call($a, 'Grep', ['pattern' => 'x', 'type' => 'bogus']);
        $this->assertStringContainsString('Unknown file type', $out['error']);
    }
}
