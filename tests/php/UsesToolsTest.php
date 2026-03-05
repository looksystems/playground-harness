<?php

declare(strict_types=1);

namespace AgentHarness\Tests;

use AgentHarness\ToolDef;
use AgentHarness\UsesTools;
use PHPUnit\Framework\TestCase;

class ToolUser
{
    use UsesTools;
}

class UsesToolsTest extends TestCase
{
    public function testRegisterTool(): void
    {
        $obj = new ToolUser();
        $tool = ToolDef::make('add', 'Add two numbers', [
            'type' => 'object',
            'properties' => [
                'a' => ['type' => 'integer'],
                'b' => ['type' => 'integer'],
            ],
            'required' => ['a', 'b'],
        ], fn(array $args) => $args['a'] + $args['b']);
        $obj->registerTool($tool);
        $schema = $obj->toolsSchema();
        $this->assertCount(1, $schema);
        $this->assertSame('add', $schema[0]['function']['name']);
    }

    public function testRegisterToolDef(): void
    {
        $obj = new ToolUser();
        $td = new ToolDef(
            name: 'custom',
            description: 'A custom tool',
            parameters: ['type' => 'object', 'properties' => ['x' => ['type' => 'integer']]],
            execute: fn(array $args) => $args['x'] * 2,
        );
        $obj->registerTool($td);
        $schema = $obj->toolsSchema();
        $this->assertSame('custom', $schema[0]['function']['name']);
    }

    public function testToolsSchema(): void
    {
        $obj = new ToolUser();
        $tool = ToolDef::make('add', 'Add two numbers', [
            'type' => 'object',
            'properties' => [
                'a' => ['type' => 'integer'],
                'b' => ['type' => 'integer'],
            ],
        ], fn(array $args) => $args['a'] + $args['b']);
        $obj->registerTool($tool);
        $schema = $obj->toolsSchema();
        $this->assertCount(1, $schema);
        $this->assertSame('function', $schema[0]['type']);
        $this->assertSame('add', $schema[0]['function']['name']);
        $this->assertArrayHasKey('a', $schema[0]['function']['parameters']['properties']);
    }

    public function testExecuteTool(): void
    {
        $obj = new ToolUser();
        $tool = ToolDef::make('add', 'Add two numbers', [
            'type' => 'object',
            'properties' => [
                'a' => ['type' => 'integer'],
                'b' => ['type' => 'integer'],
            ],
        ], fn(array $args) => $args['a'] + $args['b']);
        $obj->registerTool($tool);
        $result = $obj->executeTool('add', ['a' => 3, 'b' => 4]);
        $this->assertStringContainsString('7', $result);
    }

    public function testExecuteToolSync(): void
    {
        $obj = new ToolUser();
        $tool = ToolDef::make('multiply', 'Multiply two numbers', [
            'type' => 'object',
            'properties' => [
                'a' => ['type' => 'integer'],
                'b' => ['type' => 'integer'],
            ],
        ], fn(array $args) => $args['a'] * $args['b']);
        $obj->registerTool($tool);
        $result = $obj->executeTool('multiply', ['a' => 3, 'b' => 4]);
        $this->assertStringContainsString('12', $result);
    }

    public function testExecuteUnknownTool(): void
    {
        $obj = new ToolUser();
        $result = $obj->executeTool('nonexistent', []);
        $lower = strtolower($result);
        $this->assertTrue(
            str_contains($lower, 'error') || str_contains($lower, 'unknown'),
            "Expected error or unknown in result, got: {$result}"
        );
    }

    public function testToolDefMake(): void
    {
        $tool = ToolDef::make(
            'greet',
            'Greet someone',
            ['type' => 'object', 'properties' => ['name' => ['type' => 'string']]],
            fn(array $args) => "Hello, {$args['name']}!"
        );
        $this->assertSame('greet', $tool->name);
        $this->assertSame('Greet someone', $tool->description);
        $result = $tool->call(['name' => 'World']);
        $this->assertSame('Hello, World!', $result);
    }
}
