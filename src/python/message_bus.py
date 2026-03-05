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
