# Streaming Events & Trait Decomposition Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Decompose the monolithic agent into trait-based mixins and add a streaming event system with inline YAML parsing, message bus, and per-run event control.

**Architecture:** BaseAgent provides the thin message loop. Concerns (middleware, hooks, tools, skills, events) are mixins composed via multiple inheritance (Python), mixins (TS), or traits (PHP). The event system uses a standalone EventStreamParser that detects `---event`/`---` delimiters in the LLM text stream, parses YAML, and routes events through a standalone MessageBus.

**Tech Stack:** Python (litellm, PyYAML), TypeScript (OpenAI SDK), PHP (Guzzle). pytest/vitest/PHPUnit for testing.

**Scope:** Python reference implementation first. TypeScript and PHP ports follow the same structure — tasks are provided for all three but Python is the critical path.

---

## Phase 1: Python — Trait Decomposition

### Task 1: Set up project structure and test infrastructure

**Files:**
- Create: `src/python/base_agent.py`
- Create: `src/python/__init__.py`
- Create: `tests/python/conftest.py`
- Create: `tests/python/__init__.py`
- Create: `pyproject.toml`

**Step 1: Create directory structure**

```bash
mkdir -p src/python tests/python
```

**Step 2: Create pyproject.toml**

```toml
[project]
name = "agent-harness"
version = "0.1.0"
requires-python = ">=3.11"
dependencies = ["litellm", "pyyaml"]

[project.optional-dependencies]
dev = ["pytest", "pytest-asyncio"]

[tool.pytest.ini_options]
asyncio_mode = "auto"
testpaths = ["tests"]
```

**Step 3: Create empty init files**

```python
# src/python/__init__.py
# tests/python/__init__.py
# tests/python/conftest.py
```

**Step 4: Verify pytest runs**

Run: `pip install -e ".[dev]" && pytest --co`
Expected: no errors, 0 tests collected

**Step 5: Commit**

```bash
git add src/ tests/ pyproject.toml
git commit -m "chore: set up project structure and test infrastructure"
```

---

### Task 2: Extract HasHooks mixin

**Files:**
- Create: `src/python/has_hooks.py`
- Create: `tests/python/test_has_hooks.py`
- Reference: `examples/agent_harness.py:73-85` (HookEvent enum), `examples/agent_harness.py:277-279` (on method), `examples/agent_harness.py:299-309` (_emit method)

**Step 1: Write the failing tests**

```python
# tests/python/test_has_hooks.py
import asyncio
import pytest
from src.python.has_hooks import HasHooks, HookEvent


class HookUser(HasHooks):
    pass


class TestHasHooks:
    def test_subscribe_and_emit(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.RUN_START, lambda: received.append("started"))
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["started"]

    def test_emit_with_args(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.TOOL_CALL, lambda name, args: received.append((name, args)))
        asyncio.run(obj._emit(HookEvent.TOOL_CALL, "add", {"a": 1}))
        assert received == [("add", {"a": 1})]

    def test_multiple_hooks_same_event(self):
        obj = HookUser()
        received = []
        obj.on(HookEvent.RUN_END, lambda: received.append("a"))
        obj.on(HookEvent.RUN_END, lambda: received.append("b"))
        asyncio.run(obj._emit(HookEvent.RUN_END))
        assert set(received) == {"a", "b"}

    def test_async_hook(self):
        obj = HookUser()
        received = []

        async def hook():
            received.append("async")

        obj.on(HookEvent.RUN_START, hook)
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["async"]

    def test_hook_error_does_not_propagate(self):
        obj = HookUser()
        received = []

        def bad_hook():
            raise ValueError("boom")

        obj.on(HookEvent.RUN_START, bad_hook)
        obj.on(HookEvent.RUN_START, lambda: received.append("ok"))
        asyncio.run(obj._emit(HookEvent.RUN_START))
        assert received == ["ok"]

    def test_no_hooks_registered(self):
        obj = HookUser()
        asyncio.run(obj._emit(HookEvent.RUN_START))  # should not raise
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_has_hooks.py -v`
Expected: FAIL — ImportError

**Step 3: Implement HasHooks**

```python
# src/python/has_hooks.py
from __future__ import annotations

import asyncio
import inspect
import logging
from collections import defaultdict
from enum import Enum
from typing import Any, Callable

logger = logging.getLogger(__name__)


class HookEvent(Enum):
    RUN_START = "run_start"
    RUN_END = "run_end"
    LLM_REQUEST = "llm_request"
    LLM_RESPONSE = "llm_response"
    TOOL_CALL = "tool_call"
    TOOL_RESULT = "tool_result"
    TOOL_ERROR = "tool_error"
    RETRY = "retry"
    TOKEN_STREAM = "token_stream"
    ERROR = "error"


async def _call_fn(fn: Callable, *args: Any, **kwargs: Any) -> Any:
    result = fn(*args, **kwargs)
    if inspect.isawaitable(result):
        return await result
    return result


class HasHooks:
    def __init_has_hooks__(self) -> None:
        self._hooks: dict[HookEvent, list[Callable]] = defaultdict(list)

    def on(self, event: HookEvent, callback: Callable) -> None:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        self._hooks[event].append(callback)

    async def _emit(self, event: HookEvent, *args: Any) -> None:
        if not hasattr(self, "_hooks"):
            self.__init_has_hooks__()
        callbacks = self._hooks.get(event, [])
        if not callbacks:
            return
        results = await asyncio.gather(
            *[_call_fn(cb, *args) for cb in callbacks],
            return_exceptions=True,
        )
        for r in results:
            if isinstance(r, Exception):
                logger.warning("Hook %s error: %s", event.value, r)
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_has_hooks.py -v`
Expected: all 6 tests PASS

**Step 5: Commit**

```bash
git add src/python/has_hooks.py tests/python/test_has_hooks.py
git commit -m "feat: extract HasHooks mixin with concurrent dispatch"
```

---

### Task 3: Extract HasMiddleware mixin

**Files:**
- Create: `src/python/has_middleware.py`
- Create: `tests/python/test_has_middleware.py`
- Reference: `examples/agent_harness.py:91-110` (Middleware protocol, BaseMiddleware), `examples/agent_harness.py:273-275` (use), `examples/agent_harness.py:313-323` (_run_pre, _run_post)

**Step 1: Write the failing tests**

```python
# tests/python/test_has_middleware.py
import asyncio
import pytest
from src.python.has_middleware import HasMiddleware, BaseMiddleware


class MiddlewareUser(HasMiddleware):
    pass


class UppercaseMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        return [
            {**m, "content": m.get("content", "").upper()} if m.get("content") else m
            for m in messages
        ]

    async def post(self, message, context):
        if message.get("content"):
            message = {**message, "content": message["content"].upper()}
        return message


class PrefixMiddleware(BaseMiddleware):
    async def pre(self, messages, context):
        return [{"role": "system", "content": "PREFIX"}] + messages


class TestHasMiddleware:
    def test_use_adds_middleware(self):
        obj = MiddlewareUser()
        mw = UppercaseMiddleware()
        obj.use(mw)
        assert len(obj._middleware) == 1

    def test_run_pre_transforms_messages(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result[0]["content"] == "HELLO"

    def test_run_post_transforms_message(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        msg = {"role": "assistant", "content": "hello"}
        result = asyncio.run(obj._run_post(msg, None))
        assert result["content"] == "HELLO"

    def test_middleware_runs_in_order(self):
        obj = MiddlewareUser()
        obj.use(UppercaseMiddleware())
        obj.use(PrefixMiddleware())
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result[0]["content"] == "PREFIX"
        assert result[1]["content"] == "HELLO"

    def test_no_middleware(self):
        obj = MiddlewareUser()
        messages = [{"role": "user", "content": "hello"}]
        result = asyncio.run(obj._run_pre(messages, None))
        assert result == messages
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_has_middleware.py -v`
Expected: FAIL — ImportError

**Step 3: Implement HasMiddleware**

```python
# src/python/has_middleware.py
from __future__ import annotations

import inspect
from typing import Any, Protocol, runtime_checkable


async def _call_fn(fn, *args, **kwargs):
    result = fn(*args, **kwargs)
    if inspect.isawaitable(result):
        return await result
    return result


@runtime_checkable
class Middleware(Protocol):
    async def pre(self, messages: list[dict], context: Any) -> list[dict]: ...
    async def post(self, message: dict, context: Any) -> dict: ...


class BaseMiddleware:
    async def pre(self, messages: list[dict], context: Any) -> list[dict]:
        return messages

    async def post(self, message: dict, context: Any) -> dict:
        return message


class HasMiddleware:
    def __init_has_middleware__(self) -> None:
        self._middleware: list[BaseMiddleware] = []

    def use(self, middleware: BaseMiddleware) -> None:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        self._middleware.append(middleware)

    async def _run_pre(self, messages: list[dict], context: Any) -> list[dict]:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            messages = await _call_fn(mw.pre, messages, context)
        return messages

    async def _run_post(self, message: dict, context: Any) -> dict:
        if not hasattr(self, "_middleware"):
            self.__init_has_middleware__()
        for mw in self._middleware:
            message = await _call_fn(mw.post, message, context)
        return message
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_has_middleware.py -v`
Expected: all 5 tests PASS

**Step 5: Commit**

```bash
git add src/python/has_middleware.py tests/python/test_has_middleware.py
git commit -m "feat: extract HasMiddleware mixin with ordered pipeline"
```

---

### Task 4: Extract UsesTools mixin

**Files:**
- Create: `src/python/uses_tools.py`
- Create: `tests/python/test_uses_tools.py`
- Reference: `examples/agent_harness.py:128-135` (ToolDef), `examples/agent_harness.py:138-188` (tool decorator, _build_param_schema), `examples/agent_harness.py:260-271` (register_tool), `examples/agent_harness.py:283-295` (_tools_schema), `examples/agent_harness.py:327-342` (_execute_tool)

**Step 1: Write the failing tests**

```python
# tests/python/test_uses_tools.py
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
        assert '"7"' in result or "7" in result

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
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_uses_tools.py -v`
Expected: FAIL — ImportError

**Step 3: Implement UsesTools**

Extract from `examples/agent_harness.py` lines 58-67 (type map), 128-207 (ToolDef, tool decorator, _build_param_schema, _call_fn, _safe_json_loads), and 260-342 (register_tool, _tools_schema, _execute_tool). Adapt to mixin style:

```python
# src/python/uses_tools.py
from __future__ import annotations

import inspect
import json
import logging
from dataclasses import dataclass
from typing import Any, Callable, TypeVar, get_type_hints

logger = logging.getLogger(__name__)

PYTHON_TYPE_TO_JSON = {
    str: "string",
    int: "integer",
    float: "number",
    bool: "boolean",
    list: "array",
    dict: "object",
}

F = TypeVar("F", bound=Callable)


@dataclass
class ToolDef:
    name: str
    description: str
    function: Callable
    parameters: dict[str, Any]


def _build_param_schema(fn: Callable) -> dict[str, Any]:
    hints = get_type_hints(fn)
    props: dict[str, Any] = {}
    required: list[str] = []
    sig = inspect.signature(fn)
    for pname, param in sig.parameters.items():
        if pname in ("self", "cls"):
            continue
        ptype = hints.get(pname, str)
        props[pname] = {"type": PYTHON_TYPE_TO_JSON.get(ptype, "string")}
        if param.default is inspect.Parameter.empty:
            required.append(pname)
    schema: dict[str, Any] = {"type": "object", "properties": props}
    if required:
        schema["required"] = required
    return schema


def tool(
    description: str,
    name: str | None = None,
    schema_override: dict | None = None,
) -> Callable[[F], F]:
    def decorator(fn: F) -> F:
        fn._tool_meta = {  # type: ignore[attr-defined]
            "name": name or fn.__name__,
            "description": description,
            "schema": schema_override,
        }
        return fn
    return decorator


async def _call_fn(fn: Callable, *args: Any, **kwargs: Any) -> Any:
    result = fn(*args, **kwargs)
    if inspect.isawaitable(result):
        return await result
    return result


def _safe_json_loads(s: str) -> dict:
    try:
        return json.loads(s)
    except (json.JSONDecodeError, TypeError):
        return {}


class UsesTools:
    def __init_uses_tools__(self) -> None:
        self._tools: dict[str, ToolDef] = {}

    def register_tool(self, fn_or_def: Callable | ToolDef) -> None:
        if not hasattr(self, "_tools"):
            self.__init_uses_tools__()
        if isinstance(fn_or_def, ToolDef):
            self._tools[fn_or_def.name] = fn_or_def
            return
        meta = getattr(fn_or_def, "_tool_meta", None)
        if meta is None:
            raise ValueError(f"{fn_or_def} is not decorated with @tool")
        td = ToolDef(
            name=meta["name"],
            description=meta["description"],
            function=fn_or_def,
            parameters=meta["schema"] or _build_param_schema(fn_or_def),
        )
        self._tools[td.name] = td

    def _tools_schema(self) -> list[dict[str, Any]]:
        if not hasattr(self, "_tools"):
            self.__init_uses_tools__()
        return [
            {
                "type": "function",
                "function": {
                    "name": t.name,
                    "description": t.description,
                    "parameters": t.parameters,
                },
            }
            for t in self._tools.values()
        ]

    async def _execute_tool(self, name: str, arguments: dict[str, Any]) -> str:
        if not hasattr(self, "_tools"):
            self.__init_uses_tools__()
        td = self._tools.get(name)
        if td is None:
            return json.dumps({"error": f"Unknown tool: {name}"})
        try:
            result = await _call_fn(td.function, **arguments)
            return json.dumps(result, default=str)
        except Exception as e:
            logger.warning("Tool %s error: %s", name, e)
            return json.dumps({"error": str(e)})
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_uses_tools.py -v`
Expected: all 7 tests PASS

**Step 5: Commit**

```bash
git add src/python/uses_tools.py tests/python/test_uses_tools.py
git commit -m "feat: extract UsesTools mixin with tool registration and dispatch"
```

---

### Task 5: Implement BaseAgent

**Files:**
- Create: `src/python/base_agent.py`
- Create: `tests/python/test_base_agent.py`
- Reference: `examples/agent_harness.py:213-512` (Agent class)

**Step 1: Write the failing tests**

```python
# tests/python/test_base_agent.py
import asyncio
import pytest
from unittest.mock import AsyncMock, patch, MagicMock
from src.python.base_agent import BaseAgent, RunContext


class TestBaseAgent:
    def test_init_defaults(self):
        agent = BaseAgent(model="gpt-4")
        assert agent.model == "gpt-4"
        assert agent.max_turns == 20
        assert agent.max_retries == 2
        assert agent.stream is True

    def test_init_custom(self):
        agent = BaseAgent(
            model="claude-3-opus",
            system="You are helpful.",
            max_turns=5,
            max_retries=0,
            stream=False,
        )
        assert agent.model == "claude-3-opus"
        assert agent.system == "You are helpful."
        assert agent.max_turns == 5

    def test_build_system_prompt(self):
        agent = BaseAgent(model="gpt-4", system="Be helpful.")
        result = asyncio.run(agent._build_system_prompt("Be helpful.", None))
        assert result == "Be helpful."

    def test_run_context_creation(self):
        agent = BaseAgent(model="gpt-4")
        ctx = RunContext(agent=agent, turn=0, metadata={})
        assert ctx.agent is agent
        assert ctx.turn == 0
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_base_agent.py -v`
Expected: FAIL — ImportError

**Step 3: Implement BaseAgent**

```python
# src/python/base_agent.py
from __future__ import annotations

import asyncio
import json
import logging
from copy import deepcopy
from dataclasses import dataclass, field
from typing import Any

import litellm

logger = logging.getLogger(__name__)


@dataclass
class RunContext:
    agent: Any
    turn: int = 0
    metadata: dict[str, Any] = field(default_factory=dict)


class BaseAgent:
    def __init__(
        self,
        model: str,
        system: str | None = None,
        max_turns: int = 20,
        max_retries: int = 2,
        stream: bool = True,
        **litellm_kwargs: Any,
    ) -> None:
        self.model = model
        self.system = system
        self.max_turns = max_turns
        self.max_retries = max_retries
        self.stream = stream
        self.litellm_kwargs = litellm_kwargs

    async def _build_system_prompt(self, base_prompt: str | None, context: Any) -> str | None:
        return base_prompt

    async def _on_run_start(self, context: RunContext) -> None:
        pass

    async def _on_run_end(self, context: RunContext) -> None:
        pass

    async def _handle_stream(self, stream: Any) -> dict[str, Any]:
        content_parts: list[str] = []
        tool_calls_map: dict[int, dict] = {}

        async for chunk in stream:
            delta = chunk.choices[0].delta if chunk.choices else None
            if delta is None:
                continue
            if delta.content:
                content_parts.append(delta.content)
            if delta.tool_calls:
                for tc in delta.tool_calls:
                    idx = tc.index
                    if idx not in tool_calls_map:
                        tool_calls_map[idx] = {
                            "id": tc.id or "",
                            "type": "function",
                            "function": {"name": "", "arguments": ""},
                        }
                    entry = tool_calls_map[idx]
                    if tc.id:
                        entry["id"] = tc.id
                    if tc.function:
                        if tc.function.name:
                            entry["function"]["name"] += tc.function.name
                        if tc.function.arguments:
                            entry["function"]["arguments"] += tc.function.arguments

        message: dict[str, Any] = {"role": "assistant"}
        if content_parts:
            message["content"] = "".join(content_parts)
        if tool_calls_map:
            message["tool_calls"] = [tool_calls_map[i] for i in sorted(tool_calls_map)]
        return message

    async def _handle_response(self, response: dict[str, Any], context: RunContext) -> dict[str, Any] | None:
        return response

    async def _call_llm(self, messages: list[dict], tools_schema: list[dict] | None = None) -> dict[str, Any]:
        for attempt in range(self.max_retries + 1):
            try:
                kwargs: dict[str, Any] = {
                    "model": self.model,
                    "messages": messages,
                    **self.litellm_kwargs,
                }
                if tools_schema:
                    kwargs["tools"] = tools_schema

                if self.stream:
                    kwargs["stream"] = True
                    resp = await litellm.acompletion(**kwargs)
                    return await self._handle_stream(resp)
                else:
                    resp = await litellm.acompletion(**kwargs)
                    msg = resp.choices[0].message
                    return {"role": "assistant", "content": msg.content, "tool_calls": getattr(msg, "tool_calls", None)}
            except Exception as e:
                if attempt < self.max_retries:
                    delay = min(2 ** attempt, 10)
                    logger.warning("LLM call failed (attempt %d): %s", attempt + 1, e)
                    await asyncio.sleep(delay)
                else:
                    raise RuntimeError(f"LLM call failed after {self.max_retries + 1} attempts: {e}") from e
        raise RuntimeError("Unreachable")

    async def run(self, messages: list[dict], **kwargs: Any) -> str:
        messages = deepcopy(messages)
        system_prompt = await self._build_system_prompt(self.system, kwargs)

        if system_prompt:
            if messages and messages[0].get("role") == "system":
                messages[0]["content"] = system_prompt
            else:
                messages.insert(0, {"role": "system", "content": system_prompt})

        context = RunContext(agent=self, turn=0, metadata={})
        await self._on_run_start(context)

        for turn in range(self.max_turns):
            context.turn = turn
            assistant_msg = await self._call_llm(messages)
            result = await self._handle_response(assistant_msg, context)

            if result is None:
                messages.append(assistant_msg)
                content = assistant_msg.get("content", "")
                await self._on_run_end(context)
                return content or ""

            messages.append(result)
            if not result.get("tool_calls"):
                await self._on_run_end(context)
                return result.get("content", "")

        await self._on_run_end(context)
        return messages[-1].get("content", "")
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_base_agent.py -v`
Expected: all 4 tests PASS

**Step 5: Commit**

```bash
git add src/python/base_agent.py tests/python/test_base_agent.py
git commit -m "feat: implement BaseAgent with thin message loop and extension points"
```

---

### Task 6: Implement StandardAgent composition

**Files:**
- Create: `src/python/standard_agent.py`
- Create: `tests/python/test_standard_agent.py`

**Step 1: Write the failing tests**

```python
# tests/python/test_standard_agent.py
import asyncio
import pytest
from src.python.standard_agent import StandardAgent
from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import tool


@tool(description="Add two numbers")
async def add(a: int, b: int) -> int:
    return a + b


class TestStandardAgent:
    def test_has_all_capabilities(self):
        agent = StandardAgent(model="gpt-4")
        assert hasattr(agent, "on")
        assert hasattr(agent, "use")
        assert hasattr(agent, "register_tool")
        assert hasattr(agent, "_emit")
        assert hasattr(agent, "_run_pre")
        assert hasattr(agent, "_tools_schema")

    def test_register_tool_and_hook(self):
        agent = StandardAgent(model="gpt-4")
        agent.register_tool(add)
        received = []
        agent.on(HookEvent.RUN_START, lambda: received.append("start"))
        assert "add" in agent._tools
        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert received == ["start"]

    def test_middleware_integration(self):
        agent = StandardAgent(model="gpt-4")

        class TestMW(BaseMiddleware):
            async def pre(self, messages, context):
                return messages + [{"role": "system", "content": "injected"}]

        agent.use(TestMW())
        messages = [{"role": "user", "content": "hi"}]
        result = asyncio.run(agent._run_pre(messages, None))
        assert len(result) == 2
        assert result[-1]["content"] == "injected"
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_standard_agent.py -v`
Expected: FAIL — ImportError

**Step 3: Implement StandardAgent**

```python
# src/python/standard_agent.py
from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.has_middleware import HasMiddleware
from src.python.uses_tools import UsesTools


class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools):
    pass
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_standard_agent.py -v`
Expected: all 3 tests PASS

**Step 5: Commit**

```bash
git add src/python/standard_agent.py tests/python/test_standard_agent.py
git commit -m "feat: compose StandardAgent from BaseAgent + mixins"
```

---

## Phase 2: Python — Event System

### Task 7: Implement MessageBus

**Files:**
- Create: `src/python/message_bus.py`
- Create: `tests/python/test_message_bus.py`

**Step 1: Write the failing tests**

```python
# tests/python/test_message_bus.py
import asyncio
import pytest
from src.python.message_bus import MessageBus, ParsedEvent


class TestMessageBus:
    def test_subscribe_and_publish(self):
        bus = MessageBus()
        received = []

        async def handler(event, bus):
            received.append(event.type)

        bus.subscribe("greeting", handler)
        event = ParsedEvent(type="greeting", data={"msg": "hi"})
        asyncio.run(bus.publish(event))
        assert received == ["greeting"]

    def test_wildcard_subscriber(self):
        bus = MessageBus()
        received = []

        async def handler(event, bus):
            received.append(event.type)

        bus.subscribe("*", handler)
        asyncio.run(bus.publish(ParsedEvent(type="a", data={})))
        asyncio.run(bus.publish(ParsedEvent(type="b", data={})))
        assert received == ["a", "b"]

    def test_multiple_handlers(self):
        bus = MessageBus()
        received = []

        async def h1(event, bus):
            received.append("h1")

        async def h2(event, bus):
            received.append("h2")

        bus.subscribe("test", h1)
        bus.subscribe("test", h2)
        asyncio.run(bus.publish(ParsedEvent(type="test", data={})))
        assert set(received) == {"h1", "h2"}

    def test_handler_can_publish(self):
        bus = MessageBus()
        received = []

        async def chain_handler(event, bus):
            received.append(event.type)
            if event.type == "first":
                await bus.publish(ParsedEvent(type="second", data={}))

        bus.subscribe("first", chain_handler)
        bus.subscribe("second", chain_handler)
        asyncio.run(bus.publish(ParsedEvent(type="first", data={})))
        assert received == ["first", "second"]

    def test_cycle_detection(self):
        bus = MessageBus(max_depth=3)
        call_count = 0

        async def recursive_handler(event, bus):
            nonlocal call_count
            call_count += 1
            await bus.publish(ParsedEvent(type="loop", data={}))

        bus.subscribe("loop", recursive_handler)
        asyncio.run(bus.publish(ParsedEvent(type="loop", data={})))
        assert call_count <= 3

    def test_handler_error_does_not_propagate(self):
        bus = MessageBus()
        received = []

        async def bad_handler(event, bus):
            raise ValueError("boom")

        async def good_handler(event, bus):
            received.append("ok")

        bus.subscribe("test", bad_handler)
        bus.subscribe("test", good_handler)
        asyncio.run(bus.publish(ParsedEvent(type="test", data={})))
        assert received == ["ok"]

    def test_no_subscribers(self):
        bus = MessageBus()
        asyncio.run(bus.publish(ParsedEvent(type="orphan", data={})))
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_message_bus.py -v`
Expected: FAIL — ImportError

**Step 3: Implement MessageBus**

```python
# src/python/message_bus.py
from __future__ import annotations

import asyncio
import logging
from collections import defaultdict
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Callable

logger = logging.getLogger(__name__)


@dataclass
class ParsedEvent:
    type: str
    data: dict[str, Any]
    stream: AsyncIterator[str] | None = None
    raw: str | None = None


class MessageBus:
    def __init__(self, max_depth: int = 10) -> None:
        self._handlers: dict[str, list[Callable]] = defaultdict(list)
        self._max_depth = max_depth
        self._depth = 0

    def subscribe(self, event_type: str, handler: Callable) -> None:
        self._handlers[event_type].append(handler)

    async def publish(self, event: ParsedEvent) -> None:
        if self._depth >= self._max_depth:
            logger.warning(
                "Max publish depth %d reached, dropping event: %s",
                self._max_depth,
                event.type,
            )
            return

        handlers = list(self._handlers.get(event.type, []))
        handlers.extend(self._handlers.get("*", []))

        if not handlers:
            return

        self._depth += 1
        try:
            results = await asyncio.gather(
                *[h(event, self) for h in handlers],
                return_exceptions=True,
            )
            for r in results:
                if isinstance(r, Exception):
                    logger.warning("Handler error for %s: %s", event.type, r)
        finally:
            self._depth -= 1
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_message_bus.py -v`
Expected: all 7 tests PASS

**Step 5: Commit**

```bash
git add src/python/message_bus.py tests/python/test_message_bus.py
git commit -m "feat: implement MessageBus with pub/sub and cycle detection"
```

---

### Task 8: Implement EventStreamParser

**Files:**
- Create: `src/python/event_stream_parser.py`
- Create: `tests/python/test_event_stream_parser.py`

**Step 1: Write the failing tests**

```python
# tests/python/test_event_stream_parser.py
import asyncio
import pytest
from src.python.event_stream_parser import EventStreamParser, EventType, StreamConfig


async def token_stream(text: str):
    for char in text:
        yield char


async def collect_text(parser, stream):
    chunks = []
    async for chunk in parser.wrap(stream):
        chunks.append(chunk)
    return "".join(chunks)


async def collect_stream_field(stream):
    parts = []
    async for token in stream:
        parts.append(token)
    return "".join(parts)


class TestEventStreamParser:
    def test_plain_text_passes_through(self):
        parser = EventStreamParser(event_types=[])
        text = "Hello world, no events here."
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert result == text

    def test_buffered_event_extraction(self):
        event_type = EventType(
            name="log_entry",
            description="A log entry",
            schema={"data": {"level": "string", "message": "string"}},
        )
        parser = EventStreamParser(event_types=[event_type])
        events = []
        parser.on_event(lambda e: events.append(e))

        text = "Before.\n---event\ntype: log_entry\ndata:\n  level: info\n  message: something happened\n---\nAfter."
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert "Before." in result
        assert "After." in result
        assert "---event" not in result
        assert len(events) == 1
        assert events[0].type == "log_entry"
        assert events[0].data["data"]["level"] == "info"

    def test_streaming_event(self):
        event_type = EventType(
            name="user_response",
            description="Response to user",
            schema={"data": {"message": "string"}},
            streaming=StreamConfig(mode="streaming", stream_fields=["data.message"]),
        )
        parser = EventStreamParser(event_types=[event_type])
        events = []
        parser.on_event(lambda e: events.append(e))

        text = "Hi.\n---event\ntype: user_response\ndata:\n  message: Hello there friend\n---\nDone."

        async def run():
            result = await collect_text(parser, token_stream(text))
            assert len(events) == 1
            assert events[0].stream is not None
            streamed = await collect_stream_field(events[0].stream)
            assert "Hello there friend" in streamed
            return result

        result = asyncio.run(run())
        assert "Hi." in result
        assert "Done." in result

    def test_unrecognized_event_passes_as_text(self):
        parser = EventStreamParser(event_types=[])
        text = "Before.\n---event\ntype: unknown_thing\ndata:\n  x: 1\n---\nAfter."
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert "---event" in result
        assert "unknown_thing" in result

    def test_malformed_yaml_passes_as_text(self):
        event_type = EventType(name="test", description="test", schema={})
        parser = EventStreamParser(event_types=[event_type])
        text = "Before.\n---event\n: this is not valid yaml [\n---\nAfter."
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert "Before." in result
        assert "After." in result

    def test_incomplete_event_at_end_of_stream(self):
        event_type = EventType(name="test", description="test", schema={})
        parser = EventStreamParser(event_types=[event_type])
        text = "Before.\n---event\ntype: test\ndata:\n  x: 1"
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert "Before." in result
        assert "---event" in result

    def test_multiple_events(self):
        event_type = EventType(
            name="log",
            description="A log",
            schema={"data": {"msg": "string"}},
        )
        parser = EventStreamParser(event_types=[event_type])
        events = []
        parser.on_event(lambda e: events.append(e))

        text = "A\n---event\ntype: log\ndata:\n  msg: first\n---\nB\n---event\ntype: log\ndata:\n  msg: second\n---\nC"
        result = asyncio.run(collect_text(parser, token_stream(text)))
        assert len(events) == 2
        assert events[0].data["data"]["msg"] == "first"
        assert events[1].data["data"]["msg"] == "second"
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_event_stream_parser.py -v`
Expected: FAIL — ImportError

**Step 3: Implement EventStreamParser**

```python
# src/python/event_stream_parser.py
from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from enum import Enum, auto
from typing import Any, AsyncIterator, Callable

import yaml

from src.python.message_bus import ParsedEvent

logger = logging.getLogger(__name__)

EVENT_START_DELIMITER = "---event"
EVENT_END_DELIMITER = "---"


@dataclass
class StreamConfig:
    mode: str = "buffered"
    stream_fields: list[str] = field(default_factory=list)


@dataclass
class EventType:
    name: str
    description: str
    schema: dict[str, Any]
    instructions: str | None = None
    streaming: StreamConfig = field(default_factory=StreamConfig)


class _ParserState(Enum):
    TEXT = auto()
    EVENT_BODY = auto()
    STREAMING = auto()


class EventStreamParser:
    def __init__(self, event_types: list[EventType]) -> None:
        self._event_types = {et.name: et for et in event_types}
        self._callbacks: list[Callable] = []

    def on_event(self, callback: Callable) -> None:
        self._callbacks.append(callback)

    def _fire_event(self, event: ParsedEvent) -> None:
        for cb in self._callbacks:
            try:
                cb(event)
            except Exception as e:
                logger.warning("Event callback error: %s", e)

    async def wrap(self, token_stream: AsyncIterator[str]) -> AsyncIterator[str]:
        state = _ParserState.TEXT
        line_buffer = ""
        event_lines: list[str] = []
        stream_queue: asyncio.Queue[str | None] | None = None

        async for token in token_stream:
            line_buffer += token

            while "\n" in line_buffer:
                line, line_buffer = line_buffer.split("\n", 1)

                if state == _ParserState.TEXT:
                    if line.strip() == EVENT_START_DELIMITER:
                        state = _ParserState.EVENT_BODY
                        event_lines = []
                    else:
                        yield line + "\n"

                elif state == _ParserState.EVENT_BODY:
                    if line.strip() == EVENT_END_DELIMITER:
                        await self._finalize_event(event_lines)
                        state = _ParserState.TEXT
                        event_lines = []
                    else:
                        event_lines.append(line)
                        parsed = self._try_detect_streaming(event_lines)
                        if parsed is not None:
                            event_type_name, pre_stream_data, stream_queue = parsed
                            state = _ParserState.STREAMING

                elif state == _ParserState.STREAMING:
                    if line.strip() == EVENT_END_DELIMITER:
                        assert stream_queue is not None
                        await stream_queue.put(None)
                        stream_queue = None
                        state = _ParserState.TEXT
                    else:
                        assert stream_queue is not None
                        await stream_queue.put(line + "\n")

        if line_buffer:
            if state == _ParserState.TEXT:
                yield line_buffer
            elif state == _ParserState.EVENT_BODY:
                yield EVENT_START_DELIMITER + "\n"
                yield "\n".join(event_lines) + "\n"
                if line_buffer.strip():
                    yield line_buffer
            elif state == _ParserState.STREAMING:
                if stream_queue is not None:
                    if line_buffer.strip():
                        await stream_queue.put(line_buffer)
                    await stream_queue.put(None)

    def _try_detect_streaming(
        self, lines: list[str]
    ) -> tuple[str, dict, asyncio.Queue] | None:
        try:
            raw = "\n".join(lines)
            data = yaml.safe_load(raw)
            if not isinstance(data, dict) or "type" not in data:
                return None
        except yaml.YAMLError:
            return None

        event_name = data["type"]
        et = self._event_types.get(event_name)
        if et is None or et.streaming.mode != "streaming":
            return None

        for sf in et.streaming.stream_fields:
            parts = sf.split(".")
            obj = data
            for part in parts[:-1]:
                if isinstance(obj, dict) and part in obj:
                    obj = obj[part]
                else:
                    return None
            last_key = parts[-1]
            if isinstance(obj, dict) and last_key in obj:
                queue: asyncio.Queue[str | None] = asyncio.Queue()
                initial_value = str(obj[last_key])
                asyncio.get_event_loop().call_soon(
                    lambda v=initial_value: queue.put_nowait(v)
                )

                async def stream_iter(q: asyncio.Queue[str | None]) -> AsyncIterator[str]:
                    while True:
                        item = await q.get()
                        if item is None:
                            break
                        yield item

                event = ParsedEvent(
                    type=event_name,
                    data=data,
                    stream=stream_iter(queue),
                )
                self._fire_event(event)
                return event_name, data, queue

        return None

    async def _finalize_event(self, lines: list[str]) -> None:
        raw = "\n".join(lines)
        try:
            data = yaml.safe_load(raw)
        except yaml.YAMLError as e:
            logger.warning("Malformed event YAML: %s", e)
            return

        if not isinstance(data, dict) or "type" not in data:
            logger.warning("Event missing 'type' field")
            return

        event_name = data["type"]
        if event_name not in self._event_types:
            return

        event = ParsedEvent(type=event_name, data=data, raw=raw)
        self._fire_event(event)
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_event_stream_parser.py -v`
Expected: all 7 tests PASS

**Step 5: Commit**

```bash
git add src/python/event_stream_parser.py tests/python/test_event_stream_parser.py
git commit -m "feat: implement EventStreamParser with YAML parsing and streaming field support"
```

---

### Task 9: Implement EmitsEvents mixin

**Files:**
- Create: `src/python/emits_events.py`
- Create: `tests/python/test_emits_events.py`

**Step 1: Write the failing tests**

```python
# tests/python/test_emits_events.py
import asyncio
import pytest
from src.python.emits_events import EmitsEvents, EventType, StreamConfig
from src.python.message_bus import ParsedEvent


class EventEmitter(EmitsEvents):
    pass


class TestEmitsEvents:
    def test_register_event(self):
        obj = EventEmitter()
        et = EventType(name="test", description="a test", schema={})
        obj.register_event(et)
        assert "test" in obj._event_registry

    def test_default_events(self):
        obj = EventEmitter()
        obj.default_events = ["test"]
        et = EventType(name="test", description="a test", schema={})
        obj.register_event(et)
        active = obj._resolve_active_events()
        assert len(active) == 1
        assert active[0].name == "test"

    def test_override_events_per_run(self):
        obj = EventEmitter()
        et1 = EventType(name="a", description="", schema={})
        et2 = EventType(name="b", description="", schema={})
        obj.register_event(et1)
        obj.register_event(et2)
        obj.default_events = ["a", "b"]
        active = obj._resolve_active_events(events=["a"])
        assert len(active) == 1
        assert active[0].name == "a"

    def test_adhoc_event(self):
        obj = EventEmitter()
        obj.default_events = []
        adhoc = EventType(name="adhoc", description="inline", schema={})
        active = obj._resolve_active_events(events=[adhoc])
        assert len(active) == 1
        assert active[0].name == "adhoc"

    def test_mixed_registered_and_adhoc(self):
        obj = EventEmitter()
        registered = EventType(name="reg", description="", schema={})
        obj.register_event(registered)
        adhoc = EventType(name="adhoc", description="", schema={})
        active = obj._resolve_active_events(events=["reg", adhoc])
        assert len(active) == 2

    def test_bus_exists(self):
        obj = EventEmitter()
        assert obj.bus is not None

    def test_build_event_prompt(self):
        obj = EventEmitter()
        et = EventType(
            name="user_response",
            description="Send a message to the user",
            schema={"data": {"message": "string"}},
            instructions="Always use this for replies.",
        )
        obj.register_event(et)
        prompt = obj._build_event_prompt([et])
        assert "user_response" in prompt
        assert "---event" in prompt
        assert "Always use this for replies." in prompt
```

**Step 2: Run tests to verify they fail**

Run: `pytest tests/python/test_emits_events.py -v`
Expected: FAIL — ImportError

**Step 3: Implement EmitsEvents**

```python
# src/python/emits_events.py
from __future__ import annotations

from typing import Any

from src.python.event_stream_parser import EventStreamParser, EventType, StreamConfig
from src.python.message_bus import MessageBus


class EmitsEvents:
    def __init_emits_events__(self) -> None:
        self._event_registry: dict[str, EventType] = {}
        self.default_events: list[str] = []
        self.bus: MessageBus = MessageBus()

    def _ensure_events_init(self) -> None:
        if not hasattr(self, "_event_registry"):
            self.__init_emits_events__()

    def register_event(self, event_type: EventType) -> None:
        self._ensure_events_init()
        self._event_registry[event_type.name] = event_type

    def _resolve_active_events(
        self, events: list[str | EventType] | None = None
    ) -> list[EventType]:
        self._ensure_events_init()
        if events is None:
            return [
                self._event_registry[name]
                for name in self.default_events
                if name in self._event_registry
            ]
        result: list[EventType] = []
        for item in events:
            if isinstance(item, str):
                if item in self._event_registry:
                    result.append(self._event_registry[item])
            elif isinstance(item, EventType):
                result.append(item)
        return result

    def _build_event_prompt(self, event_types: list[EventType]) -> str:
        if not event_types:
            return ""
        sections: list[str] = []
        sections.append("# Event Emission")
        sections.append("")
        sections.append("You can emit structured events inline in your response using the following format:")
        sections.append("")

        for et in event_types:
            sections.append(f"## Event: {et.name}")
            sections.append(f"Description: {et.description}")
            sections.append("Format:")
            sections.append("```")
            sections.append("---event")
            sections.append(f"type: {et.name}")
            if et.schema:
                for key, val in et.schema.items():
                    if isinstance(val, dict):
                        sections.append(f"{key}:")
                        for k, v in val.items():
                            sections.append(f"  {k}: <{v}>")
                    else:
                        sections.append(f"{key}: <{val}>")
            sections.append("---")
            sections.append("```")
            if et.instructions:
                sections.append(et.instructions)
            sections.append("")

        return "\n".join(sections)

    @property
    def bus(self) -> MessageBus:
        self._ensure_events_init()
        return self._bus

    @bus.setter
    def bus(self, value: MessageBus) -> None:
        self._bus = value
```

**Step 4: Run tests to verify they pass**

Run: `pytest tests/python/test_emits_events.py -v`
Expected: all 7 tests PASS

**Step 5: Commit**

```bash
git add src/python/emits_events.py tests/python/test_emits_events.py
git commit -m "feat: implement EmitsEvents mixin with event registry and prompt generation"
```

---

### Task 10: Integration test — full StandardAgent with events

**Files:**
- Modify: `src/python/standard_agent.py`
- Create: `tests/python/test_integration.py`

**Step 1: Write the failing test**

```python
# tests/python/test_integration.py
import asyncio
import pytest
from unittest.mock import AsyncMock, patch
from src.python.standard_agent import StandardAgent
from src.python.has_hooks import HookEvent
from src.python.has_middleware import BaseMiddleware
from src.python.uses_tools import tool
from src.python.emits_events import EventType, StreamConfig
from src.python.message_bus import ParsedEvent


class TestIntegration:
    def test_standard_agent_with_events(self):
        agent = StandardAgent(model="gpt-4")

        agent.register_event(EventType(
            name="user_response",
            description="Respond to user",
            schema={"data": {"message": "string"}},
        ))
        agent.default_events = ["user_response"]

        hook_log = []
        agent.on(HookEvent.RUN_START, lambda: hook_log.append("start"))

        @tool(description="Add numbers")
        async def add(a: int, b: int) -> int:
            return a + b

        agent.register_tool(add)

        assert "user_response" in agent._event_registry
        assert "add" in agent._tools
        assert len(agent._tools_schema()) == 1

        bus_events = []
        agent.bus.subscribe("user_response", lambda e, b: bus_events.append(e))

        active = agent._resolve_active_events()
        assert len(active) == 1

        prompt = agent._build_event_prompt(active)
        assert "user_response" in prompt

        asyncio.run(agent._emit(HookEvent.RUN_START))
        assert hook_log == ["start"]
```

**Step 2: Run test to verify it fails**

Run: `pytest tests/python/test_integration.py -v`
Expected: FAIL — StandardAgent doesn't include EmitsEvents

**Step 3: Update StandardAgent**

```python
# src/python/standard_agent.py
from src.python.base_agent import BaseAgent
from src.python.has_hooks import HasHooks
from src.python.has_middleware import HasMiddleware
from src.python.uses_tools import UsesTools
from src.python.emits_events import EmitsEvents


class StandardAgent(BaseAgent, HasMiddleware, HasHooks, UsesTools, EmitsEvents):
    pass
```

**Step 4: Run test to verify it passes**

Run: `pytest tests/python/test_integration.py -v`
Expected: PASS

**Step 5: Run all tests**

Run: `pytest tests/ -v`
Expected: all tests PASS

**Step 6: Commit**

```bash
git add src/python/standard_agent.py tests/python/test_integration.py
git commit -m "feat: add EmitsEvents to StandardAgent with integration test"
```

---

## Phase 3: TypeScript — Trait Decomposition & Events

### Task 11: TypeScript trait decomposition

**Files:**
- Create: `src/typescript/has-hooks.ts`
- Create: `src/typescript/has-middleware.ts`
- Create: `src/typescript/uses-tools.ts`
- Create: `src/typescript/base-agent.ts`
- Create: `src/typescript/standard-agent.ts`
- Create: `src/typescript/message-bus.ts`
- Create: `src/typescript/event-stream-parser.ts`
- Create: `src/typescript/emits-events.ts`
- Create: `tests/typescript/` with test files

Follow the same patterns as Python but using TypeScript idioms:
- Mixin pattern via `type Constructor<T> = new (...args: any[]) => T` and function-based mixins
- `AsyncIterable` for streaming fields
- `Promise.allSettled()` for concurrent hook/handler dispatch
- `for await...of` for stream consumption
- vitest for testing

The contracts are identical — same HookEvent enum values, same Middleware interface, same ToolDef structure, same EventType/StreamConfig/ParsedEvent shapes, same MessageBus API.

Reference: `examples/agent_harness.ts` for existing patterns. Port each Python mixin 1:1.

**Commit after each mixin + tests pass.**

---

## Phase 4: PHP — Trait Decomposition & Events

### Task 12: PHP trait decomposition

**Files:**
- Create: `src/php/HasHooks.php`
- Create: `src/php/HasMiddleware.php`
- Create: `src/php/UsesTools.php`
- Create: `src/php/BaseAgent.php`
- Create: `src/php/StandardAgent.php`
- Create: `src/php/MessageBus.php`
- Create: `src/php/EventStreamParser.php`
- Create: `src/php/EmitsEvents.php`
- Create: `tests/php/` with test files

Follow the same patterns using PHP idioms:
- Native `trait` keyword for mixins
- `Generator` for streaming fields (pull-based, `foreach`)
- `HookEvent` enum (PHP 8.1+)
- PHPUnit for testing
- Guzzle `StreamInterface` for SSE reading

The contracts are identical. PHP streaming uses Generators instead of async iterators, matching the ecosystem convention.

Reference: `examples/agent_harness.php` for existing patterns. Port each Python mixin 1:1.

**Commit after each trait + tests pass.**

---

## Summary

| Phase | Tasks | What |
|-------|-------|------|
| 1 | Tasks 1-6 | Python trait decomposition: HasHooks, HasMiddleware, UsesTools, BaseAgent, StandardAgent |
| 2 | Tasks 7-10 | Python event system: MessageBus, EventStreamParser, EmitsEvents, integration test |
| 3 | Task 11 | TypeScript port of all traits + events |
| 4 | Task 12 | PHP port of all traits + events |

Total: ~12 tasks, Python is the critical path (Tasks 1-10), TS and PHP are ports.
