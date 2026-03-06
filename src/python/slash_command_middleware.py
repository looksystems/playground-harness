from __future__ import annotations

from typing import Any

from src.python.has_middleware import BaseMiddleware


class SlashCommandMiddleware(BaseMiddleware):
    async def pre(self, messages: list[dict], context: Any) -> list[dict]:
        if not messages:
            return messages
        last = messages[-1]
        if last.get("role") != "user":
            return messages
        content = last.get("content", "")
        if not isinstance(content, str) or not content.startswith("/"):
            return messages
        agent = context.agent
        if not hasattr(agent, "intercept_slash_command"):
            return messages
        result = agent.intercept_slash_command(content)
        if result is None:
            return messages
        name, args = result
        output = agent.execute_slash_command(name, args)
        messages = messages.copy()
        messages[-1] = {**last, "content": f"[Slash command /{name} result]: {output}"}
        return messages
