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


StructuredEvent = EventType


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
        """Wrap a token stream, extracting events and yielding clean text."""
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
                        handled = await self._finalize_event(event_lines)
                        if not handled:
                            # Unrecognized or malformed — pass through as text
                            yield EVENT_START_DELIMITER + "\n"
                            for el in event_lines:
                                yield el + "\n"
                            yield EVENT_END_DELIMITER + "\n"
                        state = _ParserState.TEXT
                        event_lines = []
                    else:
                        event_lines.append(line)
                        parsed = self._try_detect_streaming(event_lines)
                        if parsed is not None:
                            _event_type_name, _pre_stream_data, stream_queue = parsed
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

        # Handle remaining content at end of stream
        if line_buffer:
            if state == _ParserState.TEXT:
                yield line_buffer
            elif state == _ParserState.EVENT_BODY:
                # Incomplete event -- dump as text
                yield EVENT_START_DELIMITER + "\n"
                yield "\n".join(event_lines) + "\n"
                if line_buffer.strip():
                    yield line_buffer
            elif state == _ParserState.STREAMING:
                if stream_queue is not None:
                    if line_buffer.strip():
                        await stream_queue.put(line_buffer)
                    await stream_queue.put(None)
        elif state == _ParserState.EVENT_BODY:
            # Incomplete event with no trailing content
            yield EVENT_START_DELIMITER + "\n"
            yield "\n".join(event_lines)
        elif state == _ParserState.STREAMING:
            if stream_queue is not None:
                await stream_queue.put(None)

    def _try_detect_streaming(
        self, lines: list[str]
    ) -> tuple[str, dict, asyncio.Queue] | None:
        """Check if accumulated lines form a streaming event. If so, fire it early."""
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

        # Check if streaming field is present in parsed data
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

                async def stream_iter(
                    q: asyncio.Queue[str | None], initial: str
                ) -> AsyncIterator[str]:
                    if initial:
                        yield initial
                    while True:
                        item = await q.get()
                        if item is None:
                            break
                        yield item

                event = ParsedEvent(
                    type=event_name,
                    data=data,
                    stream=stream_iter(queue, initial_value),
                )
                self._fire_event(event)
                return event_name, data, queue

        return None

    async def _finalize_event(self, lines: list[str]) -> bool:
        """Parse a complete buffered event and fire it. Returns True if handled."""
        raw = "\n".join(lines)
        try:
            data = yaml.safe_load(raw)
        except yaml.YAMLError as e:
            logger.warning("Malformed event YAML: %s", e)
            return False

        if not isinstance(data, dict) or "type" not in data:
            logger.warning("Event missing 'type' field")
            return False

        event_name = data["type"]
        if event_name not in self._event_types:
            return False

        event = ParsedEvent(type=event_name, data=data, raw=raw)
        self._fire_event(event)
        return True
