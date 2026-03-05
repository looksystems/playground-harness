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
