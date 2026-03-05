import asyncio
import pytest
from src.python.uses_tools import UsesTools, tool, ToolDef


class ToolUser(UsesTools):
    pass


@tool(description="Add two numbers")
async def add(a: int, b: int) -> int:
    return a + b


@tool(description="Multiply two numbers")
def multiply(a: int, b: int) -> int:
    return a * b


class TestUsesTools:
    def test_register_decorated_tool(self):
        obj = ToolUser()
        obj.register_tool(add)
        assert "add" in obj._tools

    def test_register_tooldef(self):
        obj = ToolUser()
        td = ToolDef(
            name="custom",
            description="A custom tool",
            function=lambda args: args["x"] * 2,
            parameters={"type": "object", "properties": {"x": {"type": "integer"}}},
        )
        obj.register_tool(td)
        assert "custom" in obj._tools

    def test_tools_schema(self):
        obj = ToolUser()
        obj.register_tool(add)
        schema = obj._tools_schema()
        assert len(schema) == 1
        assert schema[0]["type"] == "function"
        assert schema[0]["function"]["name"] == "add"
        assert "a" in schema[0]["function"]["parameters"]["properties"]

    def test_execute_tool_async(self):
        obj = ToolUser()
        obj.register_tool(add)
        result = asyncio.run(obj._execute_tool("add", {"a": 3, "b": 4}))
        assert "7" in result

    def test_execute_tool_sync(self):
        obj = ToolUser()
        obj.register_tool(multiply)
        result = asyncio.run(obj._execute_tool("multiply", {"a": 3, "b": 4}))
        assert "12" in result

    def test_execute_unknown_tool(self):
        obj = ToolUser()
        result = asyncio.run(obj._execute_tool("nonexistent", {}))
        assert "error" in result.lower() or "unknown" in result.lower()

    def test_auto_schema_from_type_hints(self):
        obj = ToolUser()
        obj.register_tool(add)
        schema = obj._tools_schema()
        props = schema[0]["function"]["parameters"]["properties"]
        assert props["a"]["type"] == "integer"
        assert props["b"]["type"] == "integer"
