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

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
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

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
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

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
        assert "---event" in result
        assert "unknown_thing" in result

    def test_malformed_yaml_passes_as_text(self):
        event_type = EventType(name="test", description="test", schema={})
        parser = EventStreamParser(event_types=[event_type])
        text = "Before.\n---event\n: this is not valid yaml [\n---\nAfter."

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
        assert "Before." in result
        assert "After." in result

    def test_incomplete_event_at_end_of_stream(self):
        event_type = EventType(name="test", description="test", schema={})
        parser = EventStreamParser(event_types=[event_type])
        text = "Before.\n---event\ntype: test\ndata:\n  x: 1"

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
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

        async def run():
            return await collect_text(parser, token_stream(text))

        result = asyncio.run(run())
        assert len(events) == 2
        assert events[0].data["data"]["msg"] == "first"
        assert events[1].data["data"]["msg"] == "second"
