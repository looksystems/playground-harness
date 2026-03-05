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
