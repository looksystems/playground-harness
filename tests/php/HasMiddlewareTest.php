<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\BaseMiddleware;
use AgentHarness\HasMiddleware;
use AgentHarness\Middleware;
use PHPUnit\Framework\TestCase;

class MiddlewareUser
{
    use HasMiddleware;
}

class UppercaseMiddleware extends BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        return array_map(function (array $m) {
            if (isset($m['content'])) {
                $m['content'] = strtoupper($m['content']);
            }
            return $m;
        }, $messages);
    }

    public function post(array $message, mixed $context): array
    {
        if (isset($message['content'])) {
            $message['content'] = strtoupper($message['content']);
        }
        return $message;
    }
}

class PrefixMiddleware extends BaseMiddleware
{
    public function pre(array $messages, mixed $context): array
    {
        return array_merge([['role' => 'system', 'content' => 'PREFIX']], $messages);
    }
}

class HasMiddlewareTest extends TestCase
{
    public function testUseAddsMiddleware(): void
    {
        $obj = new MiddlewareUser();
        $mw = new UppercaseMiddleware();
        $obj->use($mw);
        // We verify by running pre and checking it works
        $result = $obj->runPre([['role' => 'user', 'content' => 'hello']], null);
        $this->assertSame('HELLO', $result[0]['content']);
    }

    public function testRunPreTransformsMessages(): void
    {
        $obj = new MiddlewareUser();
        $obj->use(new UppercaseMiddleware());
        $messages = [['role' => 'user', 'content' => 'hello']];
        $result = $obj->runPre($messages, null);
        $this->assertSame('HELLO', $result[0]['content']);
    }

    public function testRunPostTransformsMessage(): void
    {
        $obj = new MiddlewareUser();
        $obj->use(new UppercaseMiddleware());
        $msg = ['role' => 'assistant', 'content' => 'hello'];
        $result = $obj->runPost($msg, null);
        $this->assertSame('HELLO', $result['content']);
    }

    public function testMiddlewareRunsInOrder(): void
    {
        $obj = new MiddlewareUser();
        $obj->use(new UppercaseMiddleware());
        $obj->use(new PrefixMiddleware());
        $messages = [['role' => 'user', 'content' => 'hello']];
        $result = $obj->runPre($messages, null);
        $this->assertSame('PREFIX', $result[0]['content']);
        $this->assertSame('HELLO', $result[1]['content']);
    }

    public function testNoMiddleware(): void
    {
        $obj = new MiddlewareUser();
        $messages = [['role' => 'user', 'content' => 'hello']];
        $result = $obj->runPre($messages, null);
        $this->assertSame($messages, $result);
    }
}
