"""
agent_harness.py — A lightweight, async-native, single-file LLM agent harness.

Built on litellm for multi-provider support. Features:
  - Decorator-based tool registration with auto-schema generation
  - Async middleware pipeline (pre/post processing of messages and responses)
  - Async hook system for lifecycle events
  - Streaming support
  - Parallel tool execution
  - Configurable retry with exponential backoff

Usage:
    import asyncio
    from agent_harness import Agent, tool

    @tool(description="Add two numbers")
    async def add(a: int, b: int) -> int:
        return a + b

    async def main():
        agent = Agent(model="anthropic/claude-sonnet-4-20250514")
        agent.register_tool(add)
        result = await agent.run("What is 2 + 3?")
        print(result)

    asyncio.run(main())
"""

from __future__ import annotations

import asyncio
import inspect
import json
import logging
import traceback
from copy import deepcopy
from dataclasses import dataclass, field
from enum import Enum
from typing import (
    Any,
    Callable,
    Protocol,
    TypeVar,
    get_type_hints,
    runtime_checkable,
)

import litellm

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
log = logging.getLogger("agent_harness")

# ---------------------------------------------------------------------------
# Type helpers
# ---------------------------------------------------------------------------
PYTHON_TYPE_TO_JSON: dict[type, str] = {
    str: "string",
    int: "integer",
    float: "number",
    bool: "boolean",
    list: "array",
    dict: "object",
}

F = TypeVar("F", bound=Callable[..., Any])


# ---------------------------------------------------------------------------
# Hook events
# ---------------------------------------------------------------------------
class HookEvent(str, Enum):
    """Lifecycle events the agent emits."""

    RUN_START = "run_start"          # (messages,)
    RUN_END = "run_end"              # (messages, final_text)
    LLM_REQUEST = "llm_request"      # (messages, tools_schema)
    LLM_RESPONSE = "llm_response"    # (response,)
    TOOL_CALL = "tool_call"          # (tool_name, arguments)
    TOOL_RESULT = "tool_result"      # (tool_name, result)
    TOOL_ERROR = "tool_error"        # (tool_name, error)
    RETRY = "retry"                  # (attempt, error)
    TOKEN_STREAM = "token_stream"    # (token_str,)
    ERROR = "error"                  # (error,)


# ---------------------------------------------------------------------------
# Middleware protocol
# ---------------------------------------------------------------------------
@runtime_checkable
class Middleware(Protocol):
    """
    Middleware contract. Implement any subset of these async methods.
    - pre:  transform the messages list before each LLM call
    - post: transform the assistant message after each LLM call
    """

    async def pre(self, messages: list[dict], context: RunContext) -> list[dict]: ...
    async def post(self, message: dict, context: RunContext) -> dict: ...


class BaseMiddleware:
    """Convenience base — override only what you need."""

    async def pre(self, messages: list[dict], context: RunContext) -> list[dict]:
        return messages

    async def post(self, message: dict, context: RunContext) -> dict:
        return message


# ---------------------------------------------------------------------------
# Run context (passed through middleware and hooks)
# ---------------------------------------------------------------------------
@dataclass
class RunContext:
    """Mutable bag of state for the current run, accessible to middleware & hooks."""

    agent: Agent
    turn: int = 0
    metadata: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Tool registry helpers
# ---------------------------------------------------------------------------
@dataclass
class ToolDef:
    """Internal representation of a registered tool."""

    name: str
    description: str
    function: Callable[..., Any]  # sync or async
    parameters: dict[str, Any]   # JSON Schema for the parameters


def _build_param_schema(fn: Callable) -> dict[str, Any]:
    """Derive a JSON Schema 'parameters' object from a function's type hints."""
    hints = get_type_hints(fn)
    sig = inspect.signature(fn)
    properties: dict[str, Any] = {}
    required: list[str] = []

    for name, param in sig.parameters.items():
        json_type = PYTHON_TYPE_TO_JSON.get(hints.get(name, str), "string")
        prop: dict[str, Any] = {"type": json_type}

        if param.default is inspect.Parameter.empty:
            required.append(name)
        else:
            prop["default"] = param.default

        properties[name] = prop

    return {
        "type": "object",
        "properties": properties,
        "required": required,
    }


def tool(
    description: str = "",
    name: str | None = None,
    schema_override: dict | None = None,
) -> Callable[[F], F]:
    """
    Decorator that marks a function as an agent tool.
    Works with both sync and async functions.

        @tool(description="Search the web")
        async def web_search(query: str) -> str: ...

    The decorated function gains a `_tool_def` attribute used during registration.
    You can pass `schema_override` to supply your own JSON Schema for parameters.
    """

    def decorator(fn: F) -> F:
        fn._tool_def = ToolDef(  # type: ignore[attr-defined]
            name=name or fn.__name__,
            description=description or fn.__doc__ or "",
            function=fn,
            parameters=schema_override or _build_param_schema(fn),
        )
        return fn

    return decorator


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

async def _call_fn(fn: Callable, *args: Any, **kwargs: Any) -> Any:
    """Call a sync or async function uniformly, always returning an awaitable."""
    result = fn(*args, **kwargs)
    if inspect.isawaitable(result):
        return await result
    return result


def _safe_json_loads(s: str) -> dict:
    try:
        return json.loads(s)
    except (json.JSONDecodeError, TypeError):
        return {}


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------
class Agent:
    """
    Lightweight async agent loop.

    Parameters
    ----------
    model : str
        A litellm model string, e.g. "anthropic/claude-sonnet-4-20250514",
        "openai/gpt-4o", "ollama/llama3", etc.
    system : str | None
        System prompt.
    max_turns : int
        Safety cap on tool-use loops per run.
    max_retries : int
        Retries on transient LLM errors.
    stream : bool
        If True, stream tokens and emit TOKEN_STREAM hooks.
    parallel_tool_calls : bool
        If True, execute multiple tool calls concurrently via asyncio.gather.
    litellm_kwargs : dict
        Extra kwargs forwarded to litellm.acompletion() (temperature, etc.)
    """

    def __init__(
        self,
        model: str = "anthropic/claude-sonnet-4-20250514",
        system: str | None = None,
        max_turns: int = 20,
        max_retries: int = 2,
        stream: bool = False,
        parallel_tool_calls: bool = True,
        **litellm_kwargs: Any,
    ):
        self.model = model
        self.system = system
        self.max_turns = max_turns
        self.max_retries = max_retries
        self.stream = stream
        self.parallel_tool_calls = parallel_tool_calls
        self.litellm_kwargs = litellm_kwargs

        self._tools: dict[str, ToolDef] = {}
        self._middleware: list[BaseMiddleware | Middleware] = []
        self._hooks: dict[HookEvent, list[Callable]] = {e: [] for e in HookEvent}

    # -- Registration -------------------------------------------------------

    def register_tool(self, fn_or_def: Callable | ToolDef) -> None:
        """Register a @tool-decorated function or a raw ToolDef."""
        if isinstance(fn_or_def, ToolDef):
            td = fn_or_def
        elif hasattr(fn_or_def, "_tool_def"):
            td = fn_or_def._tool_def  # type: ignore[attr-defined]
        else:
            raise ValueError(
                "Pass a @tool-decorated function or a ToolDef instance."
            )
        self._tools[td.name] = td
        log.info("Registered tool: %s", td.name)

    def use(self, mw: BaseMiddleware | Middleware) -> None:
        """Add middleware to the pipeline (order matters — FIFO)."""
        self._middleware.append(mw)

    def on(self, event: HookEvent, callback: Callable) -> None:
        """Subscribe to a lifecycle event. Callback can be sync or async."""
        self._hooks[event].append(callback)

    # -- Schema generation --------------------------------------------------

    def _tools_schema(self) -> list[dict]:
        """Build the litellm/OpenAI-compatible tools array."""
        return [
            {
                "type": "function",
                "function": {
                    "name": td.name,
                    "description": td.description,
                    "parameters": td.parameters,
                },
            }
            for td in self._tools.values()
        ]

    # -- Hook dispatch ------------------------------------------------------

    async def _emit(self, event: HookEvent, *args: Any) -> None:
        """Fire all hooks for an event concurrently."""
        if not self._hooks[event]:
            return
        results = await asyncio.gather(
            *(_call_fn(cb, *args) for cb in self._hooks[event]),
            return_exceptions=True,
        )
        for r in results:
            if isinstance(r, Exception):
                log.warning("Hook %s raised: %s", event, r)

    # -- Middleware pipeline -------------------------------------------------

    async def _run_pre(self, messages: list[dict], ctx: RunContext) -> list[dict]:
        for mw in self._middleware:
            if hasattr(mw, "pre"):
                messages = await _call_fn(mw.pre, messages, ctx)
        return messages

    async def _run_post(self, message: dict, ctx: RunContext) -> dict:
        for mw in self._middleware:
            if hasattr(mw, "post"):
                message = await _call_fn(mw.post, message, ctx)
        return message

    # -- Tool execution -----------------------------------------------------

    async def _execute_tool(self, name: str, arguments: dict) -> str:
        """Run a tool and return a JSON-serialised result string."""
        td = self._tools.get(name)
        if td is None:
            err = f"Unknown tool: {name}"
            await self._emit(HookEvent.TOOL_ERROR, name, err)
            return json.dumps({"error": err})

        await self._emit(HookEvent.TOOL_CALL, name, arguments)
        try:
            result = await _call_fn(td.function, **arguments)
            await self._emit(HookEvent.TOOL_RESULT, name, result)
            return result if isinstance(result, str) else json.dumps(result)
        except Exception as exc:
            await self._emit(HookEvent.TOOL_ERROR, name, exc)
            return json.dumps({"error": str(exc)})

    # -- LLM call (with retries + optional streaming) -----------------------

    async def _call_llm(self, messages: list[dict], ctx: RunContext) -> dict:
        """Make one LLM call, returning the assistant message dict."""
        tools = self._tools_schema() or None
        await self._emit(HookEvent.LLM_REQUEST, messages, tools)

        last_err: Exception | None = None
        for attempt in range(1, self.max_retries + 2):
            try:
                if self.stream:
                    return await self._call_llm_stream(messages, tools, ctx)

                resp = await litellm.acompletion(
                    model=self.model,
                    messages=messages,
                    tools=tools,
                    **self.litellm_kwargs,
                )
                msg = resp.choices[0].message.model_dump()  # type: ignore[union-attr]
                await self._emit(HookEvent.LLM_RESPONSE, msg)
                return msg

            except Exception as exc:
                last_err = exc
                if attempt <= self.max_retries:
                    await self._emit(HookEvent.RETRY, attempt, exc)
                    log.warning("Retry %d/%d: %s", attempt, self.max_retries, exc)
                    await asyncio.sleep(min(2**attempt, 10))

        raise RuntimeError(
            f"LLM call failed after {self.max_retries + 1} attempts: {last_err}"
        )

    async def _call_llm_stream(
        self, messages: list[dict], tools: list | None, ctx: RunContext
    ) -> dict:
        """Stream tokens, accumulate, and return the full message dict."""
        resp = await litellm.acompletion(
            model=self.model,
            messages=messages,
            tools=tools,
            stream=True,
            **self.litellm_kwargs,
        )

        content_parts: list[str] = []
        tool_calls_accum: dict[int, dict] = {}

        async for chunk in resp:
            delta = chunk.choices[0].delta  # type: ignore[union-attr]

            if delta.content:
                content_parts.append(delta.content)
                await self._emit(HookEvent.TOKEN_STREAM, delta.content)

            if delta.tool_calls:
                for tc in delta.tool_calls:
                    idx = tc.index
                    if idx not in tool_calls_accum:
                        tool_calls_accum[idx] = {
                            "id": tc.id or "",
                            "type": "function",
                            "function": {"name": "", "arguments": ""},
                        }
                    entry = tool_calls_accum[idx]
                    if tc.id:
                        entry["id"] = tc.id
                    if tc.function:
                        if tc.function.name:
                            entry["function"]["name"] += tc.function.name
                        if tc.function.arguments:
                            entry["function"]["arguments"] += tc.function.arguments

        msg: dict[str, Any] = {
            "role": "assistant",
            "content": "".join(content_parts) or None,
        }
        if tool_calls_accum:
            msg["tool_calls"] = [tool_calls_accum[i] for i in sorted(tool_calls_accum)]

        await self._emit(HookEvent.LLM_RESPONSE, msg)
        return msg

    # -- Main loop ----------------------------------------------------------

    async def run(
        self,
        prompt: str,
        messages: list[dict] | None = None,
        context_meta: dict[str, Any] | None = None,
    ) -> str:
        """
        Run the agent to completion and return the final text response.

        Parameters
        ----------
        prompt : str
            The user message.
        messages : list[dict] | None
            Optional conversation history to continue from.
        context_meta : dict | None
            Arbitrary metadata attached to the RunContext for middleware/hooks.
        """
        ctx = RunContext(agent=self, metadata=context_meta or {})

        msgs: list[dict] = []
        if self.system:
            msgs.append({"role": "system", "content": self.system})
        if messages:
            msgs.extend(deepcopy(messages))
        msgs.append({"role": "user", "content": prompt})

        await self._emit(HookEvent.RUN_START, msgs)

        for turn in range(self.max_turns):
            ctx.turn = turn

            # Middleware pre-processing
            call_msgs = await self._run_pre(deepcopy(msgs), ctx)

            # LLM call
            try:
                assistant_msg = await self._call_llm(call_msgs, ctx)
            except Exception as exc:
                await self._emit(HookEvent.ERROR, exc)
                raise

            # Middleware post-processing
            assistant_msg = await self._run_post(assistant_msg, ctx)
            msgs.append(assistant_msg)

            # If no tool calls, we're done
            tool_calls = assistant_msg.get("tool_calls")
            if not tool_calls:
                final = assistant_msg.get("content", "") or ""
                await self._emit(HookEvent.RUN_END, msgs, final)
                return final

            # Execute tool calls — parallel or sequential
            if self.parallel_tool_calls and len(tool_calls) > 1:
                results = await asyncio.gather(
                    *(
                        self._execute_tool(
                            tc["function"]["name"],
                            _safe_json_loads(tc["function"]["arguments"]),
                        )
                        for tc in tool_calls
                    )
                )
                for tc, result_str in zip(tool_calls, results):
                    msgs.append({
                        "role": "tool",
                        "tool_call_id": tc["id"],
                        "content": result_str,
                    })
            else:
                for tc in tool_calls:
                    result_str = await self._execute_tool(
                        tc["function"]["name"],
                        _safe_json_loads(tc["function"]["arguments"]),
                    )
                    msgs.append({
                        "role": "tool",
                        "tool_call_id": tc["id"],
                        "content": result_str,
                    })

        raise RuntimeError(f"Agent exceeded max_turns ({self.max_turns})")


# ===========================================================================
# Built-in middleware (import and .use() as needed)
# ===========================================================================


class TruncationMiddleware(BaseMiddleware):
    """Keep the message list under a budget by dropping old turns."""

    def __init__(self, max_messages: int = 40):
        self.max_messages = max_messages

    async def pre(self, messages: list[dict], context: RunContext) -> list[dict]:
        if len(messages) <= self.max_messages:
            return messages
        system = [m for m in messages if m["role"] == "system"]
        rest = [m for m in messages if m["role"] != "system"]
        return system + rest[-(self.max_messages - len(system)) :]


class LoggingMiddleware(BaseMiddleware):
    """Log every LLM request/response for debugging."""

    async def pre(self, messages: list[dict], context: RunContext) -> list[dict]:
        log.debug("[turn %d] Sending %d messages", context.turn, len(messages))
        return messages

    async def post(self, message: dict, context: RunContext) -> dict:
        tc = message.get("tool_calls")
        if tc:
            names = [t["function"]["name"] for t in tc]
            log.debug("[turn %d] Tool calls: %s", context.turn, names)
        else:
            snippet = (message.get("content") or "")[:120]
            log.debug("[turn %d] Response: %s...", context.turn, snippet)
        return message


class CostTrackingMiddleware(BaseMiddleware):
    """Accumulate token usage in context.metadata["usage"]."""

    async def post(self, message: dict, context: RunContext) -> dict:
        usage = context.metadata.setdefault(
            "usage", {"prompt_tokens": 0, "completion_tokens": 0}
        )
        if "_usage" in message:
            u = message["_usage"]
            usage["prompt_tokens"] += u.get("prompt_tokens", 0)
            usage["completion_tokens"] += u.get("completion_tokens", 0)
        return message


# ===========================================================================
# Demo
# ===========================================================================
if __name__ == "__main__":
    import os

    logging.basicConfig(level=logging.DEBUG)

    # --- Tools (async and sync both work) ---

    @tool(description="Add two numbers together")
    async def add(a: int, b: int) -> int:
        return a + b

    @tool(description="Get the current weather for a city (stub)")
    async def get_weather(city: str) -> dict:
        await asyncio.sleep(0.1)  # simulate latency
        return {"city": city, "temp_f": 72, "condition": "sunny"}

    @tool(description="Multiply two numbers")
    def multiply(a: int, b: int) -> int:
        """Plain sync tool — also works."""
        return a * b

    # --- Custom middleware ---

    class InjectTimestamp(BaseMiddleware):
        async def pre(self, messages, context):
            from datetime import datetime, timezone

            ts = datetime.now(timezone.utc).isoformat()
            for m in messages:
                if m["role"] == "system":
                    m["content"] += f"\n\nCurrent time: {ts}"
                    break
            return messages

    # --- Agent setup ---

    agent = Agent(
        model=os.getenv("AGENT_MODEL", "anthropic/claude-sonnet-4-20250514"),
        system="You are a helpful assistant. Use your tools when appropriate.",
        max_turns=10,
        stream=False,
        parallel_tool_calls=True,
        temperature=0.3,
    )

    agent.register_tool(add)
    agent.register_tool(get_weather)
    agent.register_tool(multiply)

    agent.use(LoggingMiddleware())
    agent.use(TruncationMiddleware(max_messages=30))
    agent.use(InjectTimestamp())

    # Hooks — sync and async both work
    agent.on(HookEvent.TOOL_CALL, lambda name, args: print(f"  🔧 {name}({args})"))
    agent.on(HookEvent.TOOL_RESULT, lambda name, res: print(f"  ✅ {name} → {res}"))

    async def on_stream(tok: str) -> None:
        print(tok, end="", flush=True)

    agent.on(HookEvent.TOKEN_STREAM, on_stream)

    async def main() -> None:
        result = await agent.run(
            "What's the weather in Tokyo, what's 17 + 38, and what's 6 * 7?"
        )
        print("\n---\n", result)

    asyncio.run(main())
